package execution

import (
	"7link.com/platform/pkg/module"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationExactBindingFencesReplayAndNoGenericEvidenceVerdict(t *testing.T) {
	f, request := verificationFixture(t)
	head := request.HeadCommit
	var task guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		task, e = f.m.queueVerificationOn(f.human, tx, request)
		return e
	}))
	read := func(result guid.ID) (VerificationReceipt, error) {
		var r VerificationReceipt
		e := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			r, e = f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, result, task)
			return e
		})
		return r, e
	}
	_, err := read(50)
	require.ErrorIs(t, err, ErrConflict)
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Len(t, plans, 1)
	planBytes, _ := json.Marshal(plans[0])
	exit := 0
	o := VerificationObservation{PlanHash: hash(planBytes), HeadCommit: head, ExitCode: &exit, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	complete := func(nonce string, o VerificationObservation) error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.completeVerificationOn(f.runner, tx, 7, task, f.fence, nonce, o) })
	}
	require.ErrorIs(t, complete("wrong", o), ErrFenced)
	require.NoError(t, complete(plans[0].Nonce, o))
	require.NoError(t, complete(plans[0].Nonce, o))
	changed := o
	changed.Reason = "TIMEOUT_OR_CANCELED"
	require.ErrorIs(t, complete(plans[0].Nonce, changed), ErrConflict)
	r, err := read(50)
	require.NoError(t, err)
	require.Equal(t, "PASSED", r.Verdict)
	require.Equal(t, 2, r.ResultVersion)
	require.Equal(t, head, r.HeadCommit)
	_, err = read(99)
	require.ErrorIs(t, err, ErrNotFound)
	// A lost runner is sealed inconclusive and never automatically rerun.
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		task, e = f.m.queueVerificationOn(f.human, tx, request)
		return e
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Len(t, plans, 1)
	require.NoError(t, f.m.expireVerificationTasks(t.Context(), f.db, 7, time.Now().UTC().Add(2*time.Hour)))
	r, err = read(50)
	require.NoError(t, err)
	require.Equal(t, "INCONCLUSIVE", r.Verdict)
	require.Equal(t, "MACHINE_UNAVAILABLE", r.Reason)
	var remaining []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		remaining, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Empty(t, remaining)
	f.sessionValid = false
	require.ErrorIs(t, complete(plans[0].Nonce, o), ErrFenced)
}

func verificationFixture(t *testing.T) (*executionFixture, VerificationRequest) {
	t.Helper()
	f := executionHarness(t)
	v := f.create(t)
	head := strings.Repeat("b", 40)
	v.Plan.Workdir = t.TempDir()
	b, _ := json.Marshal(v.Plan)
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"state": "SUCCEEDED", "plan_json": string(b), "plan_hash": hash(b)})
	require.NoError(t, err)
	require.NoError(t, query.BindEvidence(f.db, f.m.names).WithContext(t.Context()).Create(&model.Evidence{ID: 80, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "COMMIT", Content: head, ContentHash: hash([]byte(head)), CreatedAt: time.Now().UTC()}))
	return f, VerificationRequest{TenantID: 7, ProjectID: 9, WorkItemID: 3, ResultID: 50, ExecutionID: v.ID, CriterionID: 51, ResultVersion: 2, Method: "COMMAND", HeadCommit: head, Argv: []string{"git", "status", "--porcelain"}, TimeoutSec: 10}
}

func TestVerificationQueueRejectsUnsupportedAndUnboundCommandsAtomically(t *testing.T) {
	for _, fault := range []string{"invalid_result", "unsupported", "missing_report", "traversal_report", "nul_argv", "wrong_work", "wrong_head", "unavailable_source", "audit"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			switch fault {
			case "invalid_result":
				in.ResultID = 0
			case "unsupported":
				in.Method = "VISUAL"
			case "missing_report":
				in.Method = "TEST"
			case "traversal_report":
				in.Method = "TEST"
				in.ReportPath = "../report.json"
			case "nul_argv":
				in.Argv = []string{"git", "bad\x00argument"}
			case "wrong_work":
				in.WorkItemID = 99
			case "wrong_head":
				in.HeadCommit = strings.Repeat("c", 40)
			case "unavailable_source":
				in.ExecutionID = 99
			case "audit":
				f.pub.fail = true
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.queueVerificationOn(f.human, tx, in); return e })
			require.Error(t, err)
			q := query.BindVerificationTasks(f.db, f.m.names)
			count, e := q.WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestVerificationTestReportsNeverTreatMissingSkippedOrFailedReportsAsPass(t *testing.T) {
	zero, one := 0, 1
	p := VerificationPlan{Method: "TEST", HeadCommit: strings.Repeat("a", 40)}
	base := VerificationObservation{Reason: "PROCESS_EXITED", HeadCommit: p.HeadCommit, ExitCode: &zero, OutputSHA256: hash(nil), ReportSHA256: hash(nil)}
	for _, tc := range []struct {
		name                    string
		passed, failed, skipped *int
		want                    string
	}{
		{"missing", nil, nil, nil, "INCONCLUSIVE"}, {"empty", &zero, &zero, &zero, "INCONCLUSIVE"}, {"skipped", &one, &zero, &one, "INCONCLUSIVE"}, {"failed", &one, &one, &zero, "FAILED"}, {"passed", &one, &zero, &zero, "PASSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			o.Passed, o.Failed, o.Skipped = tc.passed, tc.failed, tc.skipped
			v, _ := verificationVerdict(p, o)
			require.Equal(t, tc.want, v)
		})
	}
}

func TestProvideVerificationExecutorRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideVerificationExecutor(s))
	require.ErrorIs(t, f.m.provideVerificationExecutor(s), module.ErrServiceDuplicate)
}
