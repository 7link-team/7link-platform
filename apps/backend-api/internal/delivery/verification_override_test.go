package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationOverrideBindsEveryOriginalAndCannotAuthorizeLaterFailure(t *testing.T) {
	h, r, in := verificationFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_qa_required", false).Error)
	now := time.Now().UTC()
	q := query.BindVerificationRuns(h.db, h.m.service.names)
	for _, id := range []guid.ID{902, 903} {
		require.NoError(t, q.WithContext(h.ctx).Create(&model.VerificationRun{TenantID: 1, ID: id, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "FAILED", Reason: "NONZERO_EXIT", ReceiptHash: strings.Repeat("a", 64), ActorID: 12, CreatedAt: now, CompletedAt: &now}))
	}
	h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	body := verificationOverrideBody{VerificationIDs: []guid.ID{903, 902}, EvidenceIDs: []guid.ID{r.Evidence[0].ID}, Reason: "Two independently recorded environment failures", Risk: "Require a new exception for any later failure", ReauthProof: "fresh"}
	v, err := h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
		return h.m.overrideVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, version, "cookie", body)
	})
	require.NoError(t, err)
	require.Len(t, v.Overrides, 1)
	require.Equal(t, []guid.ID{902, 903}, []guid.ID{v.Overrides[0].Original[0].ID, v.Overrides[0].Original[1].ID})
	row, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, r.ID)
	require.NoError(t, err)
	work := h.stored()
	require.NoError(t, h.m.verificationAcceptanceOn(h.ctx, h.db, work, row))
	work.BaselineVersionNo++
	allowed, err := h.m.verificationAllowedOverridesOn(h.ctx, h.db, work, row)
	require.NoError(t, err)
	require.Empty(t, allowed, "baseline changes invalidate every old exception")
	work = h.stored()
	require.NoError(t, q.WithContext(h.ctx).Create(&model.VerificationRun{TenantID: 1, ID: 904, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 2, State: "DONE", Verdict: "FAILED", Reason: "NONZERO_EXIT", ReceiptHash: strings.Repeat("b", 64), ActorID: 12, CreatedAt: now, CompletedAt: &now}))
	require.ErrorIs(t, h.m.verificationAcceptanceOn(h.ctx, h.db, work, row), ErrVerificationPending, "exception for earlier receipt IDs must not cover a later failure")
	stored, err := q.WithContext(h.ctx).Where(q.ID.Eq(guid.ID(903))).First()
	require.NoError(t, err)
	require.Equal(t, "FAILED", stored.Verdict)
	require.Equal(t, strings.Repeat("a", 64), stored.ReceiptHash)
}

func TestVerificationOverrideGuardsAreAtomicAndPreserveOriginal(t *testing.T) {
	for _, scenario := range []string{"no_proof", "no_reason", "no_risk", "self", "oauth", "agent_subject", "foreign_verification", "duplicate", "foreign_evidence", "duplicate_evidence", "stale", "tampered_tests", "denied", "audit", "outbox", "no_reauthentication", "proof_failure", "principal_failure", "wrong_state", "baseline_changed", "non_role", "approve_denied", "id_failure", "insert_failure"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			now := time.Now().UTC()
			task := guid.ID(901)
			run := &model.VerificationRun{TenantID: 1, ID: 902, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "FAILED", Reason: "EXIT_NONZERO", ActorID: 12, TaskID: &task, ReceiptHash: strings.Repeat("a", 64), CreatedAt: now, CompletedAt: &now}
			if scenario == "tampered_tests" {
				run.Reason = "FROZEN_TEST_TAMPERED"
			}
			require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(run))
			h.m.scopeReauthentication = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ string, _ string, _ string, _ string) error {
				require.Same(t, h.tx, tx)
				return tx.Exec("INSERT INTO provision_probe(step) VALUES ('proof_consumed')").Error
			}
			body := verificationOverrideBody{VerificationIDs: []guid.ID{run.ID}, EvidenceIDs: []guid.ID{r.Evidence[0].ID}, Reason: "Known integration failure", Risk: "Explicit follow-up needed", ReauthProof: "fresh"}
			actor := guid.ID(12)
			switch scenario {
			case "no_proof":
				body.ReauthProof = ""
			case "no_reason":
				body.Reason = ""
			case "no_risk":
				body.Risk = ""
			case "self":
				actor = 11
			case "foreign_verification":
				body.VerificationIDs = []guid.ID{999}
			case "duplicate":
				body.VerificationIDs = append(body.VerificationIDs, run.ID)
			case "foreign_evidence":
				body.EvidenceIDs = []guid.ID{999}
			case "duplicate_evidence":
				body.EvidenceIDs = append(body.EvidenceIDs, body.EvidenceIDs[0])
			case "no_reauthentication":
				h.m.scopeReauthentication = nil
			case "proof_failure":
				h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
					return errors.New("proof rejected")
				}
			case "principal_failure":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("principal lock failed") }
			case "wrong_state":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "IN_PROGRESS").Error)
			case "baseline_changed":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_version_no", r.Sequence+10).Error)
			case "non_role":
				h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DECISION:1"}, nil
				}
			case "approve_denied":
				h.m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: in.Code != "decision_approve", AuthoritySource: "ROLE:reviewer"}, nil
				}
			case "id_failure":
				h.m.ids = draftIDSource(func() (int64, error) { return 0, errors.New("id unavailable") })
			case "insert_failure":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_override BEFORE INSERT ON "+h.m.service.names.VerificationOverrides.String()+" BEGIN SELECT RAISE(ABORT, 'override rejected'); END").Error)
			case "stale":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "denied":
				h.deniedAuthority = true
			case "audit", "outbox":
				h.fail = scenario
			}
			before := h.snapshot()
			_, err := h.command(actor, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				if scenario == "oauth" {
					claims, _ := authn.FromContext(ctx)
					delegated := *claims
					delegated.OAuthGrantID = "delegated"
					ctx = authn.WithClaims(ctx, &delegated)
				}
				if scenario == "agent_subject" {
					claims, _ := authn.FromContext(ctx)
					agentClaims := *claims
					agentClaims.SubjectType = "agent"
					ctx = authn.WithClaims(ctx, &agentClaims)
				}
				return h.m.overrideVerificationOn(ctx, tx, 1, 99, h.work, r.ID, actor, 11, v, "cookie", body)
			})
			require.Error(t, err)
			require.Equal(t, before, h.snapshot())
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.VerificationOverrides.String()).Count(&count).Error)
			require.Zero(t, count)
			stored, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, err)
			require.Equal(t, "FAILED", stored.Verdict)
		})
	}
}
