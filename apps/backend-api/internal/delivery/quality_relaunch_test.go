package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func qualityFixture(t *testing.T) (*resultFixture, ResultView, *QualityLaunchRequest, *ExecutionWorkLink) {
	t.Helper()
	h, result, input := verificationFixture(t)
	approveFixtureReviewPlan(t, h)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("source_execution_id", result.ID).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"state": "INTERNAL_QA", "internal_qa_required": true}).Error)
	assignment, err := query.BindAssignments(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	expires := time.Now().UTC().Add(time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", assignment.ID).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 91, "agent_id": 93, "lease_expires_at": expires}).Error)
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 93, PrincipalID: 91, TenantID: 1, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
	}
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { return 900, nil }
	h.m.verificationExecutor.ReceiptOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error) {
		return VerificationReceipt{TaskID: 900, ResultID: result.ID, ExecutionID: result.ID, CriterionID: input.CriterionID, ResultVersion: result.Sequence, Method: "COMMAND", Verdict: "FAILED", HeadCommit: strings.Repeat("a", 40), ReceiptHash: strings.Repeat("b", 64), Reason: strings.Repeat("actual failure ", 60)}, nil
	}
	request := &QualityLaunchRequest{}
	link := &ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, AssignmentID: assignment.ID, ExecutionID: 9001}
	h.m.verificationExecutor.QualityPrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{91, 101}, nil }
	h.m.verificationExecutor.LaunchQualityOn = func(ctx context.Context, tx *gorm.DB, in QualityLaunchRequest) (guid.ID, error) {
		*request = in
		require.Zero(t, authn.PrincipalIDFromContext(ctx))
		require.Equal(t, guid.ID(1), authn.TenantIDFromContext(ctx))
		link.ParentExecutionID, link.SourceDecisionID = in.ParentExecutionID, in.SourceDecisionID
		link.SourceHeadCommit, link.SourceFeedback = in.HeadCommit, in.Feedback
		return link.ExecutionID, h.m.authorizeExecutionWorkOn(ctx, tx, *link, "CREATE")
	}
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, result.ID, 12, 11, v, input)
	})
	require.NoError(t, err)
	return h, result, request, link
}

func TestQualityFailedRoundCreatesStructuredSystemActionAndExecutableChild(t *testing.T) {
	h, result, request, link := qualityFixture(t)
	var locks []guid.ID
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locks = append(locks, id); return nil }
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
	}))
	require.Equal(t, []guid.ID{11, 91, 101}, locks)
	stored := h.stored()
	require.Equal(t, "REWORK", stored.State)
	require.Equal(t, 1, stored.InternalRoundCount)
	require.Zero(t, stored.QualityReworkCount, "internal and Human rework counters stay separate")
	require.Equal(t, link.ExecutionID, *stored.CurrentExecutionID)
	action, err := query.BindResultActions(h.db, h.m.service.names).WithContext(h.ctx).Where(query.BindResultActions(h.db, h.m.service.names).ID.Eq(request.SourceDecisionID)).First()
	require.NoError(t, err)
	require.Zero(t, action.ActorID)
	require.Equal(t, "SYSTEM:verification", action.AuthoritySource)
	var decision qualityDecision
	require.NoError(t, json.Unmarshal([]byte(action.Reason), &decision))
	require.Equal(t, "REQUEST_CHANGES", decision.Action)
	feedback, err := query.BindResultFeedback(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	var fact qualityFailure
	require.NoError(t, json.Unmarshal([]byte(feedback.Detail), &fact))
	require.Equal(t, "FUNCTIONAL_DEFECT", fact.Category)
	require.Len(t, []rune(fact.Detail), 500)
	require.Equal(t, action.ID, fact.SourceDecisionID)
	require.Contains(t, request.Feedback, fact.ReceiptHash)
	// Replay of the durable collection event creates neither another action nor child.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
	}))
	var n int64
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("kind='RETURN'").Count(&n).Error)
	require.EqualValues(t, 1, n)
	runner := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, PrincipalID: 101})
	link.ActorID = 101
	for _, op := range []string{"DISPATCH", "RECEIPT"} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(runner, tx, *link, op) }))
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.startExecutionWorkOn(runner, tx, *link) }))
	require.Equal(t, "IN_PROGRESS", h.stored().State)
}

func TestQualityLaunchFailureRollsBackVerdictActionFeedbackAndTransition(t *testing.T) {
	for _, fault := range []string{"missing", "queue_full", "zero_id", "feedback_storage", "action_storage", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			h, result, _, _ := qualityFixture(t)
			failure := errors.New("actual launch unavailable")
			switch fault {
			case "missing":
				h.m.verificationExecutor.LaunchQualityOn = nil
			case "queue_full":
				h.m.verificationExecutor.LaunchQualityOn = func(context.Context, *gorm.DB, QualityLaunchRequest) (guid.ID, error) { return 0, failure }
			case "zero_id":
				h.m.verificationExecutor.LaunchQualityOn = func(context.Context, *gorm.DB, QualityLaunchRequest) (guid.ID, error) { return 0, nil }
			case "audit", "outbox":
				h.fail = fault
			case "feedback_storage":
				require.NoError(t, h.db.Exec("CREATE TRIGGER fail_feedback BEFORE INSERT ON "+h.m.service.names.ResultFeedback.String()+" BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			case "action_storage":
				require.NoError(t, h.db.Exec("CREATE TRIGGER fail_action BEFORE INSERT ON "+h.m.service.names.ResultActions.String()+" BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			}
			before := h.snapshot()
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
			}))
			require.Equal(t, before, h.snapshot())
			var n int64
			require.NoError(t, h.db.Table(h.m.service.names.ResultFeedback.String()).Count(&n).Error)
			require.Zero(t, n)
			run, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", run.State)
			require.Empty(t, run.ReceiptHash)
		})
	}
}

func TestQualitySystemSourceAndCurrentAuthorityCannotBeSpoofed(t *testing.T) {
	for _, fault := range []string{"action", "parent", "actor", "head", "feedback", "human_holder", "membership", "role", "paused", "stale", "superseded", "project_hold"} {
		t.Run(fault, func(t *testing.T) {
			h, result, _, link := qualityFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
			}))
			ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1})
			switch fault {
			case "action":
				link.SourceDecisionID++
			case "parent":
				link.ParentExecutionID++
			case "actor":
				link.ActorID = 11
				ctx = h.ctx
			case "head":
				link.SourceHeadCommit = strings.Repeat("c", 40)
			case "feedback":
				link.SourceFeedback = "forged pass"
			case "human_holder":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("id=?", link.AssignmentID).Updates(map[string]any{"assignee_kind": "HUMAN", "agent_id": nil}).Error)
			case "membership":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			case "role":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{ID: 93, State: "ACTIVE", Roles: []string{"REVIEWER"}}, true, nil
				}
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now().UTC()).Error)
			case "stale":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "superseded":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("current_claim_id", nil).Error)
			case "project_hold":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", `[{"kind":"MANUAL"}]`).Error)
			}
			before := h.snapshot()
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(ctx, tx, *link, "CREATE") }))
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestQualityFailedRoundLimitEscalatesWithoutChild(t *testing.T) {
	h, result, request, _ := qualityFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_round_count", 4).Error)
	require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("tenant_id=?", 1).Update("round", 5).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
	}))
	require.Zero(t, request.SourceDecisionID)
	require.Equal(t, "NEEDS_HUMAN_REVIEW", h.stored().State)
	require.Equal(t, 5, h.stored().InternalRoundCount)
	require.True(t, h.stored().RequireHumanDecision)
	var actions []model.ResultAction
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Find(&actions).Error)
	require.Empty(t, actions)
}

func TestQualitySourceCorruptionAndReadFailuresCannotAuthorizeChild(t *testing.T) {
	for _, fault := range []string{"malformed_decision", "changed_action", "no_failed_receipt", "missing_receipt_hash", "execute_policy", "action_read", "result_read", "runs_read", "assignment_read"} {
		t.Run(fault, func(t *testing.T) {
			h, result, request, link := qualityFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
			}))
			failure := errors.New("canonical source storage unavailable")
			target := ""
			switch fault {
			case "malformed_decision":
				require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("id=?", request.SourceDecisionID).Update("reason", `{"action":3}`).Error)
			case "changed_action":
				require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("id=?", request.SourceDecisionID).Update("authority_source", "permission:decision_request_changes").Error)
			case "no_failed_receipt":
				require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("tenant_id=?", 1).Update("verdict", "PASSED").Error)
			case "missing_receipt_hash":
				require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("tenant_id=?", 1).Update("receipt_hash", "").Error)
			case "execute_policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "action_read":
				target = h.m.service.names.ResultActions.String()
			case "result_read":
				target = h.m.service.names.ResultVersions.String()
			case "runs_read":
				target = h.m.service.names.VerificationRuns.String()
			case "assignment_read":
				target = h.m.service.names.Assignments.String()
			}
			before := h.snapshot()
			if target != "" {
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("quality_source_failure", func(db *gorm.DB) {
					if db.Statement.Table == target {
						db.AddError(failure)
					}
				}))
			}
			ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1})
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(ctx, tx, *link, "CREATE") })
			if target != "" {
				require.NoError(t, h.db.Callback().Query().Remove("quality_source_failure"))
			}
			require.Error(t, err)
			if target != "" || fault == "execute_policy" {
				require.ErrorIs(t, err, failure)
			}
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestQualityPreflightFailsBeforeTakingBusinessLocks(t *testing.T) {
	for _, fault := range []string{"principal_source", "principal_lock", "assignment_fence", "execute_fence", "human_result", "missing_result"} {
		t.Run(fault, func(t *testing.T) {
			h, result, _, _ := qualityFixture(t)
			failure := errors.New("participant fencing failed")
			switch fault {
			case "principal_source":
				h.m.verificationExecutor.QualityPrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
			case "principal_lock":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "execute_fence":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "assignment_fence":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("quality_fence_failure", func(db *gorm.DB) {
					if db.Statement.Table == h.m.service.names.Assignments.String() {
						db.AddError(failure)
					}
				}))
				defer func() { require.NoError(t, h.db.Callback().Query().Remove("quality_fence_failure")) }()
			case "human_result":
				require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("source_execution_id", nil).Error)
			case "missing_result":
				result.ID++
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.prelockQualityOn(h.ctx, tx, 1, 99, h.work, result.ID, 12, 11) })
			if fault == "human_result" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestQualityRecoveryRequiresCurrentHumanStopScopeAndCanonicalSystemSource(t *testing.T) {
	for _, scenario := range []string{"active", "inactive_human", "wrong_human", "human_lookup", "revoked_member", "membership_lookup", "stop_denied", "stop_owner_wrong", "stop_owner_granted", "stop_owner_read", "stop_policy_failure", "changed_source", "executor_scope_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			h, result, request, link := qualityFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
			}))
			link.ActorID = 12
			ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
			failure := errors.New("current recovery authority unavailable")
			priorHuman := h.m.principals.ActiveHumanByID
			h.m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if id == 12 {
					switch scenario {
					case "inactive_human":
						return identity.Principal{ID: 12}, false, nil
					case "wrong_human":
						return identity.Principal{ID: 13}, true, nil
					case "human_lookup":
						return identity.Principal{}, false, failure
					}
				}
				return priorHuman(ctx, tx, id)
			}
			h.m.members = memberCheck(func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (bool, error) {
				if id == 12 {
					if scenario == "revoked_member" {
						return false, nil
					}
					if scenario == "membership_lookup" {
						return false, failure
					}
				}
				return tenant == 1, nil
			})
			h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				if permission == "execution_stop" {
					require.Equal(t, guid.ID(12), actor)
					switch scenario {
					case "stop_denied":
						return authz.DataConstraint{}, nil
					case "stop_owner_wrong":
						return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
					case "stop_owner_granted", "stop_owner_read":
						return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
					case "stop_policy_failure":
						return authz.DataConstraint{}, failure
					}
				}
				if permission == "work_item_execute" && scenario == "executor_scope_revoked" {
					return authz.DataConstraint{}, nil
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			if scenario == "changed_source" {
				require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("id=?", request.SourceDecisionID).Update("authority_source", "permission:decision_request_changes").Error)
			}
			before := h.snapshot()
			if scenario == "stop_owner_read" {
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("recovery_owner_failure", func(db *gorm.DB) {
					if db.Statement.Table == h.m.service.names.WorkItems.String() {
						_ = db.AddError(failure)
					}
				}))
			}
			e := h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(ctx, tx, *link, "RECOVER") })
			if scenario == "stop_owner_read" {
				require.NoError(t, h.db.Callback().Query().Remove("recovery_owner_failure"))
			}
			if scenario == "active" || scenario == "stop_owner_granted" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
				if scenario == "human_lookup" || scenario == "membership_lookup" || scenario == "stop_policy_failure" || scenario == "stop_owner_read" {
					require.ErrorIs(t, e, failure)
				}
			}
			require.Equal(t, before, h.snapshot(), "authority check must not change execution, verification, or source decision facts")
		})
	}
}

func TestQualityCollectionRollsBackEveryFailedStorageStage(t *testing.T) {
	h, result, _, _ := qualityFixture(t)
	before := h.snapshot()
	failure := errors.New("quality persistence unavailable")
	rollback := errors.New("rollback successful quality collection probe")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("quality-collection-write", probe))
	require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("quality-collection-write", probe))
	run := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			e := h.m.collectAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, result.ID)
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 2)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "quality persistence stage %d", i)
	}
	require.NoError(t, h.db.Callback().Create().Remove("quality-collection-write"))
	require.NoError(t, h.db.Callback().Update().Remove("quality-collection-write"))
	require.Equal(t, before, h.snapshot())
}
