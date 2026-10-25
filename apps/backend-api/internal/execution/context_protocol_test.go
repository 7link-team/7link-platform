package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionEvidenceContextRollbackRefreshReplayAndEndActivity(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	head := int64(1)
	f.m.commentContext.CheckOn = func(_ context.Context, _ *gorm.DB, tenant, work guid.ID, seen *int64, required bool) error {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 3, work)
		if seen == nil && required {
			return delivery.ErrWorkSeenSeqRequired
		}
		if seen != nil && *seen < head {
			return &delivery.WorkWriteInterrupted{State: "interrupted", Stream: "work_item:3", SeenSeq: *seen, CurrentSeq: head, NewerEvents: []delivery.WorkActivityView{{Seq: 1, Kind: "COMMENT_ADDED"}}}
		}
		return nil
	}
	stale := int64(0)
	in := ReceiptInput{SeenSeq: &stale, DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Pinned head", Content: strings.Repeat("c", 40)}
	var before int64
	require.NoError(t, f.db.Table("effects").Count(&before).Error)
	_, err = f.receipt(v.ID, in)
	var interrupted *delivery.WorkWriteInterrupted
	require.ErrorAs(t, err, &interrupted)
	require.Equal(t, "COMMENT_ADDED", interrupted.NewerEvents[0].Kind)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, f.db.Table("effects").Count(&count).Error)
	require.Equal(t, before, count)
	require.EqualValues(t, 1, f.read(t, v.ID).LastSeq)
	in.SeenSeq = &head
	_, err = f.receipt(v.ID, in)
	require.NoError(t, err)
	newer := int64(2)
	in.SeenSeq = &newer
	replay, err := f.receipt(v.ID, in)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "EVIDENCE_ADDED").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: in.Content})
	require.NoError(t, err)
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "EXECUTION_ENDED").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: in.Content})
	require.NoError(t, err)
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "EXECUTION_ENDED").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAgentCommentContextRequiredRefreshSameKeyAndCurrentAuthority(t *testing.T) {
	f := verifierRegistryFixture(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Source", Upload: &ArtifactUpload{Name: "source.go", Data: []byte("package source\n"), Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	evidence, err := q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID)).First()
	require.NoError(t, err)
	f.m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 220, TenantID: 7, PrincipalID: 22, State: "ACTIVE"}, true, nil
	}}
	f.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, EntityID: 9, PrincipalID: 22, SubjectType: authn.SubjectTypePrincipal})
	fresh := int64(1)
	f.m.commentContext.CheckOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, seen *int64, required bool) error {
		require.True(t, required)
		if seen == nil {
			return delivery.ErrWorkSeenSeqRequired
		}
		if *seen < fresh {
			return &delivery.WorkWriteInterrupted{State: "interrupted", Stream: "work_item:3", CurrentSeq: fresh}
		}
		return nil
	}
	in := &artifactCommentInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, IdempotencyKey: "same-comment", Body: ArtifactCommentCreate{EvidenceID: evidence.ID, StartLine: 1, EndLine: 1, Body: "Read this updated requirement"}}
	_, err = f.m.createArtifactCommentHTTP(ctx, in)
	require.Error(t, err)
	stale := int64(0)
	in.Body.SeenSeq = &stale
	_, err = f.m.createArtifactCommentHTTP(ctx, in)
	require.Error(t, err)
	raw, marshalErr := respx.MarshalEnvelope(ctx, err)
	require.NoError(t, marshalErr)
	require.Contains(t, string(raw), `"code":130474`)
	require.Contains(t, string(raw), `"operation":"artifact_comment.create"`)
	require.Contains(t, string(raw), `"operation":"inbox_events.list"`)
	require.Contains(t, string(raw), "RETRY_WITH_SAME_IDEMPOTENCY_KEY")
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Count(&count).Error)
	require.Zero(t, count)
	in.Body.SeenSeq = &fresh
	created, err := f.m.createArtifactCommentHTTP(ctx, in)
	require.NoError(t, err)
	fresh++
	replay, err := f.m.createArtifactCommentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data.ID, replay.Body.Data.ID)
	f.authorized = false
	_, err = f.m.createArtifactCommentHTTP(ctx, in)
	require.Error(t, err)
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestExecutionActivityFailureRollsBackTerminalMutation(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	failure := errors.New("activity storage unavailable")
	var effectsBefore int64
	require.NoError(t, f.db.Table("effects").Count(&effectsBefore).Error)
	f.m.commentActivities.RecordOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.WorkActivityInput) (int64, error) {
		return 0, failure
	}
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "FAILED"})
	require.ErrorIs(t, err, failure)
	require.Equal(t, "RUNNING", f.read(t, v.ID).State)
	require.EqualValues(t, 1, f.read(t, v.ID).LastSeq)
	var effectsAfter, receiptsAfter, activeSlots int64
	require.NoError(t, f.db.Table("effects").Count(&effectsAfter).Error)
	require.Equal(t, effectsBefore, effectsAfter, "terminal work effects, audit and outbox must all roll back")
	require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Where("execution_id = ? AND seq = ?", v.ID, 2).Count(&receiptsAfter).Error)
	require.Zero(t, receiptsAfter, "failed end hook cannot consume the receipt sequence")
	require.NoError(t, f.db.Table("slots").Where("execution_id = ? AND active = ?", v.ID, 1).Count(&activeSlots).Error)
	require.EqualValues(t, 1, activeSlots, "failed end hook must preserve the occupied runtime slot")
}

func TestExecutionContextAdhocExemptionAndMissingCapability(t *testing.T) {
	m := &Module{}
	row := &model.Execution{TenantID: 7}
	require.NoError(t, m.checkExecutionContextOn(t.Context(), nil, row, nil, true))
	row.WorkItemID = 3
	require.ErrorIs(t, m.checkExecutionContextOn(t.Context(), nil, row, nil, true), ErrCapabilityUnavailable)
	raw, err := json.Marshal(ArtifactCommentCreate{SeenSeq: new(int64)})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"seen_seq":0`)
}

func TestAgentCommentAndEvidenceAdhocExecutionDoNotRequireWorkCursor(t *testing.T) {
	f, agent, v := manualEvidenceFixture(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Source", Upload: &ArtifactUpload{Name: "source.go", Data: []byte("package example\n"), Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	source, err := q.WithContext(agent).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
	require.NoError(t, err)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Update("work_item_id", 0).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.CreateEvidenceOn(agent, tx, 7, v.ID, EvidenceCreate{EvidenceType: "OBSERVATION", Title: "Adhoc observation", ScopeText: "Scratch screen", Text: "No work context is linked"})
		return e
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.CreateArtifactCommentOn(agent, tx, 7, v.ID, ArtifactCommentCreate{EvidenceID: source.ID, StartLine: 1, EndLine: 1, Body: "Adhoc source comment"})
		return e
	}))
}
