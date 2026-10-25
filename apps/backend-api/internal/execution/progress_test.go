package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestProgressCountsOnlyDeclaredContentAndConsecutiveMissingInputs(t *testing.T) {
	plan := ExecutionPlan{ProgressPolicy: &templates.ProgressPolicy{NoProgressLimit: 2, DependencyMissLimit: 3, ObservablePaths: []string{"out"}, RequiredDependencyPaths: []string{"input"}}}
	sample := ProgressObservation{Observable: map[string]string{"out": strings.Repeat("a", 64)}, Dependencies: map[string]bool{"input": false}, Baseline: true}
	state, e := advanceProgress(plan, nil, sample)
	require.NoError(t, e)
	require.Zero(t, state.DependencyMissCount)
	sample.Baseline = false
	state, e = advanceProgress(plan, &state, sample)
	require.NoError(t, e)
	require.Equal(t, 1, state.NoProgressCount)
	require.Equal(t, 1, state.DependencyMissCount)
	sample.Observable["out"] = strings.Repeat("b", 64)
	sample.Dependencies["input"] = true
	state, e = advanceProgress(plan, &state, sample)
	require.NoError(t, e)
	require.Zero(t, state.NoProgressCount)
	require.Zero(t, state.DependencyMissCount)
	state, e = advanceProgress(plan, &state, sample)
	require.NoError(t, e)
	require.Equal(t, 1, state.NoProgressCount)
	state, e = advanceProgress(plan, &state, sample)
	require.NoError(t, e)
	require.Equal(t, "AUTONOMY_NO_PROGRESS", state.StopReason)
	state.StopReason = ""
	state.NoProgressCount = 0
	sample.Dependencies["input"] = false
	plan.ProgressPolicy.NoProgressLimit = 0
	for range 3 {
		state, e = advanceProgress(plan, &state, sample)
		require.NoError(t, e)
	}
	require.Equal(t, "AUTONOMY_DEPENDENCY_MISSING", state.StopReason)
	_, e = advanceProgress(plan, nil, ProgressObservation{})
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = advanceProgress(ExecutionPlan{}, nil, sample)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestChangeSimilarityExactBoundaryTruncationAndSmallUnion(t *testing.T) {
	a := ProgressObservation{Final: true, ChangedFiles: map[string]string{}}
	b := ProgressObservation{Final: true, ChangedFiles: map[string]string{}}
	for i := range 25 {
		name := fmt.Sprint(i)
		a.ChangedFiles[name] = "same"
		b.ChangedFiles[name] = "same"
	}
	b.ChangedFiles["0"] = "new"
	ratio, union, changed := changeSimilarity(a, b)
	require.Equal(t, 40000, *ratio)
	require.Equal(t, 25, union)
	require.Equal(t, 1, changed)
	b.ChangedFiles["1"] = "new"
	ratio, _, _ = changeSimilarity(a, b)
	require.Equal(t, 80000, *ratio)
	b.Truncated = true
	ratio, _, _ = changeSimilarity(a, b)
	require.Nil(t, ratio)
	a.ChangedFiles = map[string]string{"one": "one"}
	b.ChangedFiles = map[string]string{"one": "one"}
	b.Truncated = false
	ratio, _, _ = changeSimilarity(a, b)
	require.Nil(t, ratio)
}

func TestProgressReceiptReplayFencingAndOutboxRollback(t *testing.T) {
	for _, fault := range []string{"none", "outbox", "authority", "wrong_epoch"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			f.m.templateVerifiers.catalog.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (templates.VerifierBinding, bool, error) {
				return templates.VerifierBinding{TemplateVersionID: 33, DefinitionHash: "approved", ProgressPolicy: &templates.ProgressPolicy{DependencyMissLimit: 1, RequiredDependencyPaths: []string{"input"}}}, true, nil
			}
			f.m.work.ProgressStopOn = func(_ context.Context, tx *gorm.DB, _ WorkLink, _ string) error {
				return tx.Exec("INSERT INTO effects(kind) VALUES ('progress.stop')").Error
			}
			v := f.create(t)
			f.pull(t, v.ID)
			_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			sample := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PROGRESS", Progress: &ProgressObservation{Dependencies: map[string]bool{"input": false}}}
			if fault == "outbox" {
				f.pub.fail = true
			}
			if fault == "authority" {
				f.authorized = false
			}
			if fault == "wrong_epoch" {
				sample.DispatchEpoch = 2
			}
			ack, e := f.receipt(v.ID, sample)
			if fault != "none" {
				require.Error(t, e)
				stored := f.read(t, v.ID)
				require.Nil(t, stored.Progress)
				require.Equal(t, int64(1), stored.LastSeq)
				return
			}
			require.NoError(t, e)
			require.True(t, ack.PauseRequested)
			require.Equal(t, "RUNNING", ack.State)
			stored := f.read(t, v.ID)
			require.Equal(t, "AUTONOMY_DEPENDENCY_MISSING", stored.Progress.StopReason)
			ack, e = f.receipt(v.ID, sample)
			require.NoError(t, e)
			require.True(t, ack.Replayed)
			require.Equal(t, 1, f.read(t, v.ID).Progress.DependencyMissCount)
			sample.Progress.Dependencies["input"] = true
			_, e = f.receipt(v.ID, sample)
			require.ErrorIs(t, e, ErrConflict)
		})
	}
}

func TestProgressHumanWindowPreservesTotalsAndRequiresRunnerResumeACK(t *testing.T) {
	f := executionHarness(t)
	f.m.templateVerifiers.catalog.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (templates.VerifierBinding, bool, error) {
		return templates.VerifierBinding{TemplateVersionID: 33, DefinitionHash: "approved", ProgressPolicy: &templates.ProgressPolicy{DependencyMissLimit: 1, RequiredDependencyPaths: []string{"input"}}}, true, nil
	}
	f.m.work.ProgressStopOn = func(context.Context, *gorm.DB, WorkLink, string) error { return nil }
	v := f.create(t)
	f.pull(t, v.ID)
	_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	sample := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PROGRESS", Progress: &ProgressObservation{Dependencies: map[string]bool{"input": false}}}
	_, e = f.receipt(v.ID, sample)
	require.NoError(t, e)
	_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "PAUSED"})
	require.NoError(t, e)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.changeHoldOn(f.human, tx, 7, 9, 3, true); return e }))
	opened := f.read(t, v.ID)
	require.Equal(t, "PAUSED", opened.State)
	require.Equal(t, "RESUME_REQUESTED", opened.StateReason)
	require.Equal(t, int64(1), opened.Progress.TotalDependencyMissActions)
	require.Equal(t, 1, opened.Progress.HumanResumeCount)
	require.Empty(t, opened.Progress.StopReason)
	_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "RESUMED"})
	require.NoError(t, e)
	sample.Seq = 5
	ack, e := f.receipt(v.ID, sample)
	require.NoError(t, e)
	require.True(t, ack.PauseRequested)
	final := f.read(t, v.ID)
	require.Equal(t, int64(2), final.Progress.TotalDependencyMissActions)
	require.Equal(t, 1, final.Progress.DependencyMissCount)
}

func TestProgressSimilarityKeepsClaimAndEscalatesSameTransaction(t *testing.T) {
	f, ctx, in := qualityFixture(t)
	manifest := map[string]string{}
	for i := range 20 {
		manifest[fmt.Sprint(i)] = strings.Repeat("a", 40)
	}
	parentProgress, _ := json.Marshal(ProgressState{Observation: ProgressObservation{Final: true, HeadCommit: in.HeadCommit, ChangedFiles: manifest}})
	q := query.BindExecutions(f.db, f.m.names)
	_, e := q.WithContext(t.Context()).Where(q.ID.Eq(in.ParentExecutionID)).Updates(map[string]any{"state_reason": "CLAIM_SUBMITTED", "progress_json": string(parentProgress)})
	require.NoError(t, e)
	var child guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; child, e = f.m.launchQualityOn(ctx, tx, in); return e }))
	f.m.work.ProgressStopOn = func(_ context.Context, tx *gorm.DB, _ WorkLink, reason string) error {
		require.Equal(t, "AUTONOMY_SPINNING", reason)
		return tx.Exec("INSERT INTO effects(kind) VALUES ('spinning.stop')").Error
	}
	f.pull(t, child)
	_, e = f.receipt(child, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	manifest["0"] = strings.Repeat("b", 40)
	_, e = f.receipt(child, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "PROGRESS", Progress: &ProgressObservation{Final: true, HeadCommit: strings.Repeat("c", 40), ChangedFiles: manifest}})
	require.NoError(t, e)
	_, e = f.receipt(child, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Actual head", Content: strings.Repeat("c", 40)})
	require.NoError(t, e)
	ack, e := f.receipt(child, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: strings.Repeat("c", 40)})
	require.NoError(t, e)
	require.Equal(t, "SUCCEEDED", ack.State)
	stored := f.read(t, child)
	require.NotNil(t, stored.Claim)
	require.Equal(t, 50000, *stored.Progress.SimilarityRatioPPM)
	require.Equal(t, "AUTONOMY_SPINNING", stored.Progress.StopReason)
	var claims int64
	require.NoError(t, f.db.Table("effects").Where("kind=?", "work.SUCCEEDED").Count(&claims).Error)
	require.Equal(t, int64(1), claims)
}
