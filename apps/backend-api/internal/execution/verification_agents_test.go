package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationReviewerEnumerationUsesFrozenIdentityAndActualStage(t *testing.T) {
	f, in := verificationFixture(t)
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.ExecutionID)).First()
	require.NoError(t, err)
	v, err := view(row)
	require.NoError(t, err)
	v.Plan.ExecutorPrincipalID = 21
	v.Plan.ModelFamily = "source-family"
	b, _ := json.Marshal(v.Plan)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(in.ExecutionID)).Updates(map[string]any{"plan_json": string(b), "plan_hash": hash(b)})
	require.NoError(t, err)
	for _, stage := range []string{"AGENT_TEST", "AGENT_ACCEPTANCE"} {
		role := "TESTER"
		if stage == "AGENT_ACCEPTANCE" {
			role = "ACCEPTOR"
		}
		f.m.agents.ActiveByRoleOn = func(_ context.Context, tx *gorm.DB, tenant guid.ID, requested string) ([]fleet.AgentIdentity, error) {
			require.Equal(t, guid.ID(7), tenant)
			require.Equal(t, role, requested)
			base := fleet.AgentIdentity{TenantID: 7, PrincipalID: 33, State: "ACTIVE", RuntimeKind: "CLAUDE_CODE", ModelID: "review-model", ModelFamily: "review-family", Roles: []string{role}}
			out := []fleet.AgentIdentity{base}
			base.PrincipalID = 22
			out = append(out, base, base)
			for _, fault := range []string{"tenant", "self", "family", "model", "inactive", "role", "runtime", "unknown_family"} {
				a := base
				a.PrincipalID = 40
				switch fault {
				case "tenant":
					a.TenantID = 8
				case "self":
					a.PrincipalID = 21
				case "family":
					a.ModelFamily = "source-family"
					a.PrincipalID = 10
				case "model":
					a.ModelID = v.Plan.Model
				case "inactive":
					a.State = "DISABLED"
				case "role":
					a.Roles = []string{"EXECUTOR"}
				case "runtime":
					a.RuntimeKind = "UNKNOWN"
				case "unknown_family":
					a.ModelFamily = ""
				}
				out = append(out, a)
			}
			return out, nil
		}
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			got, err := f.m.verificationReviewersOn(t.Context(), tx, 7, in.ExecutionID, stage)
			require.NoError(t, err)
			require.Equal(t, []guid.ID{22, 33, 10}, got)
			return nil
		}))
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.verificationReviewersOn(t.Context(), tx, 7, in.ExecutionID, "UNKNOWN")
		require.ErrorIs(t, err, ErrInvalidArgument)
		f.m.agents.ActiveByRoleOn = nil
		_, err = f.m.verificationReviewersOn(t.Context(), tx, 7, in.ExecutionID, "AGENT_TEST")
		require.ErrorIs(t, err, ErrCapabilityUnavailable)
		failure := errors.New("directory unavailable")
		f.m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) { return nil, failure }
		_, err = f.m.verificationReviewersOn(t.Context(), tx, 7, in.ExecutionID, "AGENT_TEST")
		require.ErrorIs(t, err, failure)
		v.Plan.ModelFamily = ""
		b, _ := json.Marshal(v.Plan)
		tq := query.BindExecutions(tx, f.m.names)
		_, err = tq.WithContext(t.Context()).Where(tq.ID.Eq(in.ExecutionID)).Updates(map[string]any{"plan_json": string(b), "plan_hash": hash(b)})
		require.NoError(t, err)
		got, err := f.m.verificationReviewersOn(t.Context(), tx, 7, in.ExecutionID, "AGENT_TEST")
		require.NoError(t, err)
		require.Empty(t, got)
		return nil
	}))
}

func TestVerificationSourcePrincipalCannotInventMissingOrCorruptSnapshot(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.verificationSourcePrincipalOn(t.Context(), tx, 7, 99)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = f.m.verificationSourcePrincipalOn(t.Context(), tx, 7, v.ID)
		require.ErrorIs(t, err, ErrCapabilityUnavailable)
		q := query.BindExecutions(tx, f.m.names)
		_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.PlanJSON, "corrupt")
		require.NoError(t, err)
		_, err = f.m.verificationSourcePrincipalOn(t.Context(), tx, 7, v.ID)
		require.Error(t, err)
		return nil
	}))
}

func TestCommandVerdictRejectsWrongHeadUnknownExitAndMissingOutputProof(t *testing.T) {
	for _, tc := range []struct {
		head, digest string
		code         int
		reason       string
	}{
		{"other", hash(nil), 0, "HEAD_MISMATCH"}, {"head", hash(nil), -1, "PROCESS_OUTCOME_UNKNOWN"}, {"head", "missing", 0, "OUTPUT_DIGEST_INVALID"}, {"head", hash(nil), 1, "NONZERO_EXIT"},
	} {
		verdict, reason := verificationVerdict(VerificationPlan{Method: "COMMAND", HeadCommit: "head"}, VerificationObservation{Reason: "PROCESS_EXITED", HeadCommit: tc.head, OutputSHA256: tc.digest, ExitCode: &tc.code})
		require.NotEqual(t, "PASSED", verdict)
		require.Equal(t, tc.reason, reason)
	}
}

func TestExecutionFamilySnapshotIgnoresClientAndRequiresMatchingAssignedAgent(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "different_model"}[match], func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				model := "observed-model"
				if !match {
					model = "different"
				}
				return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: model, ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
			}
			in := validCreate()
			in.Plan.ModelFamily = "posted-family"
			in.Plan.ExecutorPrincipalID = 99
			var v ExecutionView
			err := f.db.Transaction(func(tx *gorm.DB) error { var err error; v, err = f.m.CreateOn(f.human, tx, 7, in); return err })
			if match {
				require.NoError(t, err)
				require.Equal(t, guid.ID(21), v.Plan.ExecutorPrincipalID)
				require.Equal(t, "source-family", v.Plan.ModelFamily)
			} else {
				require.ErrorIs(t, err, ErrForbidden)
			}
		})
	}
}

func TestAgentVerificationPinsActualReviewerRoleAndIndependentFamily(t *testing.T) {
	for _, fault := range []string{"pass", "family_fallback", "fallback_same_model", "self", "same_family", "same_model", "wrong_role", "inactive", "unknown_reviewer", "legacy_source", "unknown_stage", "missing_criterion"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			q := query.BindExecutions(f.db, f.m.names)
			row, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.ExecutionID)).First()
			require.NoError(t, err)
			v, err := view(row)
			require.NoError(t, err)
			v.Plan.ExecutorPrincipalID = 21
			v.Plan.ModelFamily = "source-family"
			if fault == "legacy_source" {
				v.Plan.ModelFamily = ""
			}
			encoded, _ := json.Marshal(v.Plan)
			_, err = q.WithContext(t.Context()).Where(q.ID.Eq(in.ExecutionID)).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)})
			require.NoError(t, err)
			in.Method = "AGENT_JUDGE"
			in.Stage = "AGENT_TEST"
			in.CriterionText = "Exact frozen acceptance criterion"
			in.ReviewerPrincipalID = 22
			in.AllowFamilyFallback = fault == "family_fallback" || fault == "fallback_same_model"
			in.Argv = nil
			if fault == "self" {
				in.ReviewerPrincipalID = 21
			}
			if fault == "unknown_stage" {
				in.Stage = "HUMAN"
			}
			if fault == "missing_criterion" {
				in.CriterionText = ""
			}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				agent := fleet.AgentIdentity{PrincipalID: 22, State: "ACTIVE", RuntimeKind: "CLAUDE_CODE", ModelID: "judge-model", ModelFamily: "review-family", Roles: []string{"TESTER"}}
				switch fault {
				case "same_family", "family_fallback", "fallback_same_model":
					agent.ModelFamily = "source-family"
					if fault == "fallback_same_model" {
						agent.ModelID = v.Plan.Model
					}
				case "same_model":
					agent.ModelID = v.Plan.Model
				case "wrong_role":
					agent.Roles = []string{"EXECUTOR"}
				case "inactive":
					agent.State = "DISABLED"
				case "unknown_reviewer":
					return fleet.AgentIdentity{}, false, nil
				}
				return agent, true, nil
			}
			var task guid.ID
			err = f.db.Transaction(func(tx *gorm.DB) error { var e error; task, e = f.m.queueVerificationOn(f.human, tx, in); return e })
			if fault != "pass" && fault != "family_fallback" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tasks := query.BindVerificationTasks(f.db, f.m.names)
			stored, err := tasks.WithContext(t.Context()).Where(tasks.ID.Eq(task)).First()
			require.NoError(t, err)
			var p VerificationPlan
			require.NoError(t, json.Unmarshal([]byte(stored.PlanJSON), &p))
			require.Equal(t, "22", p.ReviewerID)
			if fault == "family_fallback" {
				require.Equal(t, "source-family", p.ReviewerFamily)
			} else {
				require.Equal(t, "review-family", p.ReviewerFamily)
			}
			require.Equal(t, "judge-model", p.ReviewerModel)
			require.Equal(t, "AGENT_TEST", p.Stage)
			require.Equal(t, fault == "family_fallback", p.FamilyFallback)
			require.Nil(t, p.Argv)
			var leased []VerificationPlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				leased, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
				return e
			}))
			require.Len(t, leased, 1)
			encoded, _ = json.Marshal(leased[0])
			o := VerificationObservation{PlanHash: hash(encoded), HeadCommit: in.HeadCommit, Reason: "AGENT_REPORTED", ReportSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), AgentReport: &VerificationAgentReport{CriterionID: in.CriterionID.String(), HeadCommit: in.HeadCommit, Verdict: "PASSED", Findings: []VerificationAgentFinding{}, Unverified: []string{}}}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.completeVerificationOn(f.runner, tx, 7, task, f.fence, leased[0].Nonce, o)
			}))
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				r, e := f.m.verificationReceiptOn(f.human, tx, 7, in.ProjectID, in.WorkItemID, in.ResultID, task)
				require.NoError(t, e)
				require.Equal(t, "PASSED", r.Verdict)
				require.Nil(t, r.ExitCode)
				require.Equal(t, fault == "family_fallback", r.FamilyFallback)
				require.Equal(t, in.ReviewerPrincipalID, r.ReviewerID)
				return nil
			}))
			var source guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				source, e = f.m.verificationSourcePrincipalOn(f.human, tx, 7, in.ExecutionID)
				return e
			}))
			require.Equal(t, guid.ID(21), source)
		})
	}
}

func TestAgentReportsCannotPassMissingCriterionUnverifiedOrMajorFindings(t *testing.T) {
	p := VerificationPlan{Method: "AGENT_JUDGE", CriterionID: "51", HeadCommit: "head"}
	for _, fault := range []string{"pass", "major", "unverified", "missing_findings", "missing_unverified", "wrong_criterion", "wrong_head", "invalid_verdict", "bad_severity", "runtime_incomplete"} {
		t.Run(fault, func(t *testing.T) {
			r := &VerificationAgentReport{CriterionID: "51", HeadCommit: "head", Verdict: "PASSED", Findings: []VerificationAgentFinding{}, Unverified: []string{}}
			o := VerificationObservation{HeadCommit: "head", Reason: "AGENT_REPORTED", ReportSHA256: hash(nil), AgentReport: r}
			want := "INCONCLUSIVE"
			switch fault {
			case "pass":
				want = "PASSED"
			case "major":
				r.Findings = []VerificationAgentFinding{{Severity: "MAJOR", Summary: "Observed regression"}}
				want = "FAILED"
			case "unverified":
				r.Unverified = []string{"Missing observation"}
			case "missing_findings":
				r.Findings = nil
			case "missing_unverified":
				r.Unverified = nil
			case "wrong_criterion":
				r.CriterionID = "52"
			case "wrong_head":
				r.HeadCommit = "other"
			case "invalid_verdict":
				r.Verdict = "SUCCESS"
			case "bad_severity":
				r.Findings = []VerificationAgentFinding{{Severity: "UNKNOWN", Summary: "Finding"}}
			case "runtime_incomplete":
				o.Reason = "AGENT_RUNTIME_INCOMPLETE"
			}
			verdict, _ := verificationVerdict(p, o)
			require.Equal(t, want, verdict)
		})
	}
}
