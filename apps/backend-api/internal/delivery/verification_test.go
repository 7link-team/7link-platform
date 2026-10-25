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
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationProviderBindsExecutorOnceAndPublishesAutomation(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideVerification(s))
	require.Error(t, m.provideVerification(s))
	require.NoError(t, s.Seal())
	a, err := module.Resolve[VerificationAutomation](s)
	require.NoError(t, err)
	require.NotNil(t, a.QueueOn)
	require.NotNil(t, a.CollectOn)
	c, err := module.Resolve[VerificationExecutorConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(VerificationExecutor{}), ErrInvalidArgument)
	e := VerificationExecutor{QueueOn: func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { return 1, nil }, ReceiptOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error) {
		return VerificationReceipt{}, nil
	}}
	require.NoError(t, c.Bind(e))
	require.ErrorIs(t, c.Bind(e), ErrInvalidArgument)
}

func TestVisualVerificationQueuesFrozenCaptureAndRemainsUnverified(t *testing.T) {
	h, r, in := verificationFixture(t)
	in.Method = "VISUAL"
	in.Argv = []string{"index.html", "390", "844"}
	frozen, _ := json.Marshal([]workVerificationStep{{CriterionID: in.CriterionID, Method: in.Method, Detail: "Capture actual mobile delivery", Argv: in.Argv, TimeoutSec: in.TimeoutSec}})
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("verification_plan", string(frozen)).Error)
	queued := false
	h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, req VerificationRequest) (guid.ID, error) {
		queued = true
		require.Equal(t, "VISUAL", req.Method)
		require.Equal(t, in.Argv, req.Argv)
		require.Equal(t, strings.Repeat("a", 40), req.HeadCommit)
		return 900, nil
	}
	h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
		return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: 880, CriterionID: in.CriterionID, ResultVersion: r.Sequence, Method: "VISUAL", Verdict: "INCONCLUSIVE", Reason: "VISUAL_REVIEW_REQUIRED", ReceiptHash: strings.Repeat("b", 64), PlanHash: strings.Repeat("c", 64), HeadCommit: strings.Repeat("a", 40)}, nil
	}
	_, e := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
	})
	require.NoError(t, e)
	require.True(t, queued)
	_, e = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
	})
	require.NoError(t, e)
	q := query.BindVerificationRuns(h.db, h.m.service.names)
	runs, e := q.WithContext(t.Context()).Find()
	require.NoError(t, e)
	require.Len(t, runs, 1)
	require.Equal(t, "VISUAL", runs[0].Method)
	require.Equal(t, "INCONCLUSIVE", runs[0].Verdict)
	require.Equal(t, "VISUAL_REVIEW_REQUIRED", runs[0].Reason)
}

func TestVerificationQueueAndReceiptStorageFailuresRollBackAllFacts(t *testing.T) {
	for _, tc := range []struct{ operation, kind, table string }{
		{"queue", "provider", ""}, {"queue", "id", ""}, {"queue", "create", "runs"}, {"queue", "audit", ""}, {"queue", "outbox", ""},
		{"queue", "query", "criteria"}, {"collect", "query", "runs"}, {"collect", "update", "runs"}, {"collect", "query", "overrides"},
	} {
		t.Run(tc.operation+"_"+tc.kind+"_"+tc.table, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			if tc.operation == "collect" {
				_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
					return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
				})
				require.NoError(t, err)
			}
			before := h.snapshot()
			failure := errors.New("verification storage boundary failure")
			fired := false
			if tc.operation == "queue" {
				h.m.verificationExecutor.QueueOn = func(_ context.Context, tx *gorm.DB, _ VerificationRequest) (guid.ID, error) {
					require.NoError(t, tx.Exec("INSERT INTO provision_probe(step) VALUES ('executor_task')").Error)
					if tc.kind == "provider" {
						fired = true
						return 0, failure
					}
					return 900, nil
				}
			}
			switch tc.kind {
			case "id":
				h.m.ids = draftIDSource(func() (int64, error) { fired = true; return 0, failure })
			case "audit", "outbox":
				h.fail = tc.kind
				fired = true
			case "query", "create", "update":
				tables := map[string]string{"criteria": h.m.service.names.WorkCriteria.String(), "runs": h.m.service.names.VerificationRuns.String(), "overrides": h.m.service.names.VerificationOverrides.String()}
				hook := func(tx *gorm.DB) {
					if !fired && tx.Statement.Table == tables[tc.table] {
						fired = true
						_ = tx.AddError(failure)
					}
				}
				switch tc.kind {
				case "query":
					require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("verification_failure", hook))
				case "create":
					require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("verification_failure", hook))
				case "update":
					require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("verification_failure", hook))
				}
			}
			_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				if tc.operation == "queue" {
					return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
				}
				return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
			})
			require.True(t, fired, "failure must reach the intended storage operation")
			require.Error(t, err)
			if tc.kind != "audit" && tc.kind != "outbox" {
				require.ErrorIs(t, err, failure)
			}
			require.Equal(t, before, h.snapshot())
			runs, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Find()
			require.NoError(t, err)
			if tc.operation == "collect" {
				require.Len(t, runs, 1)
				require.Equal(t, "PENDING", runs[0].State)
				require.Empty(t, runs[0].ReceiptHash)
			} else {
				require.Empty(t, runs)
			}
		})
	}
}

func verificationFixture(t *testing.T) (*resultFixture, ResultView, verificationStartBody) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	r, err := h.submit()
	require.NoError(t, err)
	input := verificationStartBody{CriterionID: *r.Evidence[0].CriterionID, Method: "COMMAND", Argv: []string{"go", "version"}, TimeoutSec: 30}
	frozen, _ := json.Marshal([]workVerificationStep{{CriterionID: input.CriterionID, Method: "COMMAND", Detail: "Observe command exit", Argv: input.Argv, TimeoutSec: input.TimeoutSec}})
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Updates(map[string]any{"source_execution_id": guid.ID(880), "head_commit": strings.Repeat("a", 40), "verification_plan": string(frozen)}).Error)
	h.m.verificationExecutor = VerificationExecutor{
		LaunchQualityOn: func(ctx context.Context, tx *gorm.DB, request QualityLaunchRequest) (guid.ID, error) {
			require.Same(t, h.tx, tx)
			require.Equal(t, guid.ID(1), authn.TenantIDFromContext(ctx))
			require.Zero(t, authn.PrincipalIDFromContext(ctx), "an internal failure must not impersonate the source initiator")
			require.Equal(t, guid.ID(880), request.ParentExecutionID)
			require.Equal(t, h.work, request.WorkItemID)
			require.Equal(t, strings.Repeat("a", 40), request.HeadCommit)
			require.Positive(t, int64(request.SourceDecisionID))
			require.Contains(t, request.Feedback, "REQUEST_CHANGES")
			a := query.BindResultActions(tx, h.m.service.names)
			action, err := a.WithContext(ctx).Where(a.ID.Eq(request.SourceDecisionID)).First()
			require.NoError(t, err)
			require.Zero(t, action.ActorID)
			require.Equal(t, "SYSTEM:verification", action.AuthoritySource)
			require.Equal(t, r.ID, action.ResultID)
			require.NoError(t, tx.Exec("INSERT INTO provision_probe(step) VALUES ('quality_child')").Error)
			return 99001, nil
		},
		QueueOn: func(_ context.Context, tx *gorm.DB, in VerificationRequest) (guid.ID, error) {
			require.Same(t, h.tx, tx)
			require.Equal(t, guid.ID(880), in.ExecutionID)
			require.Equal(t, r.ID, in.ResultID)
			require.Equal(t, r.Sequence, in.ResultVersion)
			return 900, nil
		},
		ReceiptOn: func(_ context.Context, tx *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
			require.Same(t, h.tx, tx)
			exit := 0
			return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: 880, CriterionID: input.CriterionID, ResultVersion: r.Sequence, Method: "COMMAND", Verdict: "PASSED", ReceiptHash: strings.Repeat("b", 64), PlanHash: strings.Repeat("c", 64), HeadCommit: strings.Repeat("a", 40), ExitCode: &exit}, nil
		},
	}
	return h, r, input
}

func TestAutomaticVerificationCurrentSourceFailuresDoNotQueueTasks(t *testing.T) {
	for _, fault := range []string{"quality", "reviewers", "producer", "principal", "member", "policy", "hidden"} {
		t.Run(fault, func(t *testing.T) {
			h, r, _ := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
			injected := errors.New("automatic verification source unavailable")
			h.m.verificationExecutor.ReviewersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]guid.ID, error) {
				if fault == "reviewers" {
					return nil, injected
				}
				return []guid.ID{12}, nil
			}
			switch fault {
			case "quality":
				h.m.verificationExecutor.QualityPrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, injected }
			case "producer":
				h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 0, injected }
			case "principal":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "hidden":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
				}
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				w, _, reviewers, e := h.m.lockAutomaticVerificationOn(h.ctx, tx, 1, 99, h.work, r.ID)
				if fault == "hidden" {
					require.NoError(t, e)
					require.NotNil(t, w)
					require.Empty(t, reviewers["AGENT_TEST"])
					require.Empty(t, reviewers["AGENT_ACCEPTANCE"])
				}
				return e
			})
			if fault == "hidden" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, injected)
			}
			count, e := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
	h, r, _ := verificationFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
	injected := errors.New("automatic verification read unavailable")
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("automatic-verification-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("automatic-verification-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, _, _, e := h.m.lockAutomaticVerificationOn(h.ctx, tx, 1, 99, h.work, r.ID)
			return e
		})
	}
	require.NoError(t, read())
	total := reads
	require.Equal(t, 4, total)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.ErrorIs(t, read(), injected, "verification source read %d", i)
	}
}

func TestVerificationReviewerAuthorizationFailuresCannotDispatchOrMutate(t *testing.T) {
	for _, scenario := range []string{"source_capability", "foreign_result", "human_source", "source_failure", "self_review", "invalid_stage", "missing_reviewer", "principal_lock", "membership_failure", "inactive_member", "policy_failure", "project_denied", "owner_denied"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			in.Method = "AGENT_JUDGE"
			in.Stage = "AGENT_TEST"
			in.ReviewerPrincipalID = 21
			in.Argv = nil
			h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			failure := errors.New("current reviewer authority unavailable")
			want := ErrWorkForbidden
			resultID := r.ID
			switch scenario {
			case "source_capability":
				h.m.verificationExecutor.SourcePrincipalOn = nil
				want = ErrUnavailable
			case "foreign_result":
				resultID = 999
				want = ErrProjectNotFound
			case "human_source":
				require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", nil).Error)
				want = ErrResultState
			case "source_failure":
				h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 0, failure }
				want = failure
			case "self_review":
				h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 21, nil }
				want = ErrResultIndependent
			case "invalid_stage":
				in.Stage = "AUTOMATED"
				want = ErrInvalidArgument
			case "missing_reviewer":
				in.ReviewerPrincipalID = 0
				want = ErrInvalidArgument
			case "principal_lock":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
				want = failure
			case "membership_failure":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
				want = failure
			case "inactive_member":
				h.m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _, id guid.ID) (bool, error) { return id != 21, nil })
				want = ErrInvalidMember
			case "policy_failure":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
				want = failure
			case "project_denied":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{DeniedIDs: []string{"99"}}, nil
				}
			case "owner_denied":
				h.m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ string, id guid.ID) (authz.DataConstraint, error) {
					if id == 21 {
						return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
					}
					return authz.DataConstraint{AllowAll: true}, nil
				}
			}
			dispatched := 0
			h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { dispatched++; return 900, nil }
			before := h.snapshot()
			_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, resultID, 12, 11, v, in)
			})
			require.ErrorIs(t, err, want)
			require.Zero(t, dispatched)
			require.Equal(t, before, h.snapshot())
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestVerificationNoEligibleReviewerEscalatesOnceAndRollsBackFailures(t *testing.T) {
	for _, failure := range []string{"", "audit", "outbox"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "INTERNAL_QA").Error)
			now := time.Now().UTC()
			require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.VerificationRun{TenantID: 1, ID: 889, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "PASSED", ActorID: 12, CreatedAt: now, CompletedAt: &now}))
			h.m.verificationExecutor.ReviewersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]guid.ID, error) { return []guid.ID{}, nil }
			dispatched := 0
			h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { dispatched++; return 900, nil }
			h.fail = failure
			before := h.snapshot()
			apply := func() error {
				return h.db.Transaction(func(tx *gorm.DB) error {
					return h.m.queueAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, r.ID)
				})
			}
			err := apply()
			require.Zero(t, dispatched)
			if failure != "" {
				require.ErrorContains(t, err, failure)
				require.Equal(t, before, h.snapshot())
				require.Equal(t, "INTERNAL_QA", h.stored().State)
				return
			}
			require.NoError(t, err)
			work := h.stored()
			require.Equal(t, "NEEDS_HUMAN_REVIEW", work.State)
			require.True(t, work.RequireHumanDecision)
			require.Equal(t, "MANDATORY_HUMAN_GATE", *work.EscalationReason)
			after := h.snapshot()
			require.NoError(t, apply())
			require.Equal(t, after, h.snapshot(), "event replay cannot emit another escalation or version change")
		})
	}
}

func TestVerificationAutomationAuthorityReadFailuresCannotQueue(t *testing.T) {
	for _, scenario := range []string{"candidate_test", "candidate_accept", "source", "principal", "membership", "policy"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, _ := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
			failure := errors.New("automatic participant facts unavailable")
			fired := false
			h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) {
				if scenario == "source" {
					fired = true
					return 0, failure
				}
				return 30, nil
			}
			h.m.verificationExecutor.ReviewersOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, stage string) ([]guid.ID, error) {
				if (scenario == "candidate_test" && stage == "AGENT_TEST") || (scenario == "candidate_accept" && stage == "AGENT_ACCEPTANCE") {
					fired = true
					return nil, failure
				}
				return []guid.ID{21}, nil
			}
			h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error {
				if scenario == "principal" {
					fired = true
					return failure
				}
				return nil
			}
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
				if scenario == "membership" {
					fired = true
					return false, failure
				}
				return true, nil
			})
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "policy" {
					fired = true
					return authz.DataConstraint{}, failure
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			queued := 0
			h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { queued++; return 900, nil }
			before := h.snapshot()
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.queueAutomaticVerificationOn(context.Background(), tx, 1, 99, h.work, r.ID)
			})
			require.True(t, fired)
			require.ErrorIs(t, err, failure)
			require.Zero(t, queued)
			require.Equal(t, before, h.snapshot())
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestVerificationExactVersionReceiptAndNoSyntheticInternalQA(t *testing.T) {
	h, r, in := verificationFixture(t)
	queued, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
	})
	require.NoError(t, err)
	require.Len(t, queued.Verifications, 1)
	require.Equal(t, "PENDING", queued.Verifications[0].State)
	collected, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
	})
	require.NoError(t, err)
	require.Equal(t, "PASSED", collected.Verifications[0].Verdict)
	require.False(t, collected.Completion.L2, "deterministic command cannot fabricate Tester and Acceptor stages")
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 12)
	require.ErrorIs(t, err, ErrVerificationPending)
}

func TestVerificationRejectsChangedFrozenCommandAndUntrustedReceipt(t *testing.T) {
	for _, change := range []string{"argv", "method", "version", "head", "exit", "hash", "criterion", "audit", "outbox"} {
		t.Run(change, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			if change == "argv" {
				in.Argv = []string{"echo", "passed"}
			}
			if change == "method" {
				in.Method = "TEST"
			}
			_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
			})
			if change == "argv" || change == "method" {
				require.ErrorIs(t, err, ErrInvalidArgument)
				var n int64
				require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Count(&n).Error)
				require.Zero(t, n)
				return
			}
			require.NoError(t, err)
			original := h.m.verificationExecutor.ReceiptOn
			h.m.verificationExecutor.ReceiptOn = func(ctx context.Context, tx *gorm.DB, a, b, c, d, e guid.ID) (VerificationReceipt, error) {
				v, err := original(ctx, tx, a, b, c, d, e)
				switch change {
				case "version":
					v.ResultVersion++
				case "head":
					v.HeadCommit = strings.Repeat("d", 40)
				case "exit":
					n := 1
					v.ExitCode = &n
				case "hash":
					v.ReceiptHash = "bad"
				case "criterion":
					v.CriterionID++
				}
				return v, err
			}
			if change == "audit" || change == "outbox" {
				h.fail = change
			}
			before := h.snapshot()
			_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
			})
			require.Error(t, err)
			require.Equal(t, before, h.snapshot())
			run, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", run.State)
			require.Empty(t, run.Verdict)
		})
	}
}

func TestHumanChecksKeepSourceIdentityAndNeverBecomeAutomaticVerdicts(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	checked, err := h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	require.Len(t, checked.Verifications, 1)
	require.Equal(t, "HUMAN", checked.Verifications[0].Method)
	require.Equal(t, checked.Actions[0].ID, *checked.Verifications[0].CheckID)
	require.False(t, checked.Completion.L2)
	accepted, err := h.accept(r.ID, 12)
	require.NoError(t, err)
	require.True(t, accepted.Completion.L3)
	require.False(t, accepted.Completion.L4)
}

func TestVerificationOverridePreservesVerdictAndDisclosesInHandoff(t *testing.T) {
	h, r, _ := verificationFixture(t)
	// This fixture explicitly disables Agent QA to isolate the deterministic
	// override contract; production default remains InternalQARequired=true.
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_qa_required", false).Error)
	_, err := h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	now := time.Now().UTC()
	task := guid.ID(901)
	run := &model.VerificationRun{TenantID: 1, ID: 902, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: *r.Evidence[0].CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "FAILED", Reason: "EXIT_NONZERO", ActorID: 12, TaskID: &task, ReceiptHash: strings.Repeat("a", 64), CreatedAt: now, CompletedAt: &now}
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(run))
	_, err = h.accept(r.ID, 12)
	require.ErrorIs(t, err, ErrVerificationPending)
	var locked []guid.ID
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
	h.m.scopeReauthentication = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.Equal(t, []guid.ID{11, 12}, locked, "all principal fences precede actor session step-up lock")
		require.Same(t, h.tx, tx)
		require.Equal(t, "verification_override", purpose)
		require.Equal(t, "result:"+r.ID.String(), target)
		if proof != "fresh" {
			return errors.New("step-up missing")
		}
		return nil
	}
	body := verificationOverrideBody{VerificationIDs: []guid.ID{run.ID}, EvidenceIDs: []guid.ID{r.Evidence[0].ID}, Reason: "Known external environment failure", Risk: "Release owner accepts deferred environment retest", ReauthProof: "fresh"}
	override, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.overrideVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, "session", body)
	})
	require.NoError(t, err)
	require.Len(t, override.Overrides, 1)
	require.True(t, override.Completion.HumanOverride)
	require.False(t, override.Completion.L2)
	stored, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, "FAILED", stored.Verdict)
	_, err = h.accept(r.ID, 12)
	require.NoError(t, err)
	packageView, err := h.handoff(r.ID)
	require.NoError(t, err)
	require.Contains(t, packageView.Handoffs[0].Manifest, "verification_overrides")
	require.Contains(t, packageView.Handoffs[0].Manifest, "FAILED")
	require.Contains(t, packageView.Handoffs[0].Manifest, body.Risk)
}

func TestVerificationStagesRequireEveryPredecessorCriterion(t *testing.T) {
	h, r, in := verificationFixture(t)
	row, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, r.ID)
	require.NoError(t, err)
	work, err := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.ErrorIs(t, h.m.verificationStageReadyOn(h.ctx, h.db, work, row, "AGENT_TEST"), ErrVerificationPending)
	now := time.Now().UTC()
	run := &model.VerificationRun{TenantID: 1, ID: 980, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "PASSED", ActorID: 12, CreatedAt: now, CompletedAt: &now}
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(run))
	require.NoError(t, h.m.verificationStageReadyOn(h.ctx, h.db, work, row, "AGENT_TEST"))
	require.ErrorIs(t, h.m.verificationStageReadyOn(h.ctx, h.db, work, row, "AGENT_ACCEPTANCE"), ErrVerificationPending)
	var plan []workVerificationStep
	require.NoError(t, json.Unmarshal([]byte(row.VerificationPlan), &plan))
	plan = append(plan, workVerificationStep{CriterionID: in.CriterionID + 1, Method: "COMMAND", Detail: "Second condition"})
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	row.VerificationPlan = string(raw)
	require.ErrorIs(t, h.m.verificationStageReadyOn(h.ctx, h.db, work, row, "AGENT_TEST"), ErrVerificationPending, "one criterion cannot open the whole stage")
}

func TestVerificationRejectsStageSpoofingAndRoundExhaustion(t *testing.T) {
	for _, scenario := range []string{"stage", "reviewer", "round_limit"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			switch scenario {
			case "stage":
				in.Stage = "AGENT_TEST"
			case "reviewer":
				in.ReviewerPrincipalID = 55
			case "round_limit":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_round_count", 5).Error)
			}
			_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
			})
			if scenario == "round_limit" {
				require.ErrorIs(t, err, ErrVerificationPending)
			} else {
				require.ErrorIs(t, err, ErrInvalidArgument)
			}
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestVerificationAgentStagesPinTrustedReviewerAndRequireReceipt(t *testing.T) {
	h, r, in := verificationFixture(t)
	// Human-only criteria are checked after internal QA opens review.
	criterion, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	criterion.ID++
	criterion.Position++
	criterion.Text = "Human delivery review"
	require.NoError(t, query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).Create(criterion))
	plan, _ := json.Marshal([]workVerificationStep{{CriterionID: in.CriterionID, Method: "COMMAND", Detail: "Command", Argv: in.Argv, TimeoutSec: in.TimeoutSec}, {CriterionID: criterion.ID, Method: "HUMAN", Detail: "Human review"}})
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("verification_plan", string(plan)).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "INTERNAL_QA").Error)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
	tasks := map[guid.ID]VerificationRequest{}
	h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, req VerificationRequest) (guid.ID, error) {
		id := guid.ID(1000 + len(tasks))
		tasks[id] = req
		return id, nil
	}
	h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
		req := tasks[task]
		var exit *int
		if req.Method != "AGENT_JUDGE" {
			zero := 0
			exit = &zero
		}
		return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: req.ExecutionID, CriterionID: req.CriterionID, ResultVersion: req.ResultVersion, Method: req.Method, Stage: req.Stage, ReviewerID: req.ReviewerPrincipalID, Model: "review-model", Family: "independent-family", Verdict: "PASSED", ReceiptHash: strings.Repeat("b", 64), HeadCommit: req.HeadCommit, ExitCode: exit}, nil
	}
	command := func(body verificationStartBody) (ResultView, error) {
		return h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, body)
		})
	}
	collect := func() (ResultView, error) {
		return h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
		})
	}
	judge := verificationStartBody{CriterionID: in.CriterionID, Method: "AGENT_JUDGE", Stage: "AGENT_TEST", ReviewerPrincipalID: 21, TimeoutSec: 30}
	_, err = command(judge)
	require.ErrorIs(t, err, ErrVerificationPending)
	_, err = command(in)
	require.NoError(t, err)
	_, err = collect()
	require.NoError(t, err)
	_, err = command(judge)
	require.NoError(t, err)
	original := h.m.verificationExecutor.ReceiptOn
	h.m.verificationExecutor.ReceiptOn = func(ctx context.Context, tx *gorm.DB, a, b, c, d, e guid.ID) (VerificationReceipt, error) {
		v, err := original(ctx, tx, a, b, c, d, e)
		v.ReviewerID++
		return v, err
	}
	_, err = collect()
	require.ErrorIs(t, err, ErrInvalidArgument)
	h.m.verificationExecutor.ReceiptOn = original
	tested, err := collect()
	require.NoError(t, err)
	require.False(t, tested.Completion.L2)
	require.Equal(t, "independent-family", tested.Verifications[0].ReviewerFamily)
	require.Equal(t, guid.ID(21), *tested.Verifications[0].ReviewerID)
	judge.Stage = "AGENT_ACCEPTANCE"
	judge.ReviewerPrincipalID = 22
	_, err = command(judge)
	require.NoError(t, err)
	accepted, err := collect()
	require.NoError(t, err)
	require.True(t, accepted.Completion.L2)
	require.Equal(t, "AGENT_ACCEPTANCE", accepted.Verifications[0].Stage)
	require.Equal(t, "NEEDS_HUMAN_REVIEW", h.stored().State)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err, "Human CHECK opens only after internal Agent QA passes")
	for _, req := range tasks {
		if req.Method == "AGENT_JUDGE" {
			require.NotEmpty(t, req.CriterionText)
			require.Empty(t, req.Argv)
		}
	}
}

func TestVerificationAutomationQueuesOnceAndCollectsPersistedFacts(t *testing.T) {
	h, r, in := verificationFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
	queues := 0
	h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, req VerificationRequest) (guid.ID, error) {
		queues++
		require.Equal(t, r.ID, req.ExecutionID)
		require.Equal(t, in.Argv, req.Argv)
		return 901, nil
	}
	complete := false
	h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, a, b, c, d, e guid.ID) (VerificationReceipt, error) {
		if !complete {
			return VerificationReceipt{}, ErrResultState
		}
		zero := 0
		return VerificationReceipt{TaskID: e, ResultID: d, ExecutionID: r.ID, CriterionID: in.CriterionID, ResultVersion: r.Sequence, Method: "COMMAND", Verdict: "PASSED", HeadCommit: strings.Repeat("a", 40), ReceiptHash: strings.Repeat("b", 64), ExitCode: &zero}, nil
	}
	apply := func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error) error {
		return h.db.Transaction(func(tx *gorm.DB) error { return fn(context.Background(), tx, 1, 99, h.work, r.ID) })
	}
	require.NoError(t, apply(h.m.queueAutomaticVerificationOn))
	require.NoError(t, apply(h.m.queueAutomaticVerificationOn))
	require.Equal(t, 1, queues)
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn), "not-ready receipt event must not fabricate a terminal record")
	run, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", run.State)
	complete = true
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	run, err = query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Equal(t, "PASSED", run.Verdict)
	transitions, err := query.BindWorkTransitions(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	system := 0
	for _, transition := range transitions {
		if transition.AuthoritySource == "SYSTEM:verification" {
			system++
			require.Equal(t, "SYSTEM", transition.ActorKind)
			require.Nil(t, transition.ActorID)
		}
	}
	require.Equal(t, 2, system)
}

func TestVerificationAutomationIgnoresSupersededAndPausedResults(t *testing.T) {
	for _, scenario := range []string{"manual", "superseded", "paused", "closed", "baseline", "round_limit", "custom_round_limit", "transaction"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, _ := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
			switch scenario {
			case "round_limit":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_round_count", 5).Error)
			case "custom_round_limit":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"internal_round_count": 1, "budget": `{"max_internal_rounds":1}`}).Error)
			case "manual":
				require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", nil).Error)
			case "superseded":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("current_claim_id", nil).Error)
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now()).Error)
			case "closed":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "CLOSED").Error)
			case "baseline":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			}
			if scenario == "transaction" {
				require.ErrorIs(t, h.m.queueAutomaticVerificationOn(h.ctx, h.db, 1, 99, h.work, r.ID), ErrInvalidArgument)
				return
			}
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.queueAutomaticVerificationOn(h.ctx, tx, 1, 99, h.work, r.ID) }))
			var count int64
			require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestVerificationFailedRoundsStayInternalUntilExhausted(t *testing.T) {
	for _, prior := range []int{0, 4} {
		t.Run(string(rune('0'+prior)), func(t *testing.T) {
			h, r, in := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"state": "INTERNAL_QA", "internal_round_count": prior}).Error)
			_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
			})
			require.NoError(t, err)
			receipt := h.m.verificationExecutor.ReceiptOn
			h.m.verificationExecutor.ReceiptOn = func(ctx context.Context, tx *gorm.DB, a, b, c, d, e guid.ID) (VerificationReceipt, error) {
				r, err := receipt(ctx, tx, a, b, c, d, e)
				r.Verdict = "FAILED"
				r.Reason = "NONZERO_EXIT"
				one := 1
				r.ExitCode = &one
				return r, err
			}
			original := h.m.service.events
			humanRework, escalations := 0, 0
			h.m.service.events = projectPublisher(func(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
				if event.Type == "work_item.state_changed" {
					if data, ok := event.Data.(map[string]any); ok && data["to"] == "REWORK" {
						humanRework++
					}
				}
				if event.Type == "work_item.escalated" {
					escalations++
				}
				return original.Publish(ctx, tx, event)
			})
			_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
			})
			require.NoError(t, err)
			work := h.stored()
			require.Equal(t, prior+1, work.InternalRoundCount)
			var qualityPasses int64
			require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("kind=?", "INTERNAL_QA_PASSED").Count(&qualityPasses).Error)
			require.Zero(t, qualityPasses, "failed/exhausted internal round is never a successful quality gate receipt")
			require.Zero(t, humanRework)
			if prior == 4 {
				require.Equal(t, "NEEDS_HUMAN_REVIEW", work.State)
				require.True(t, work.RequireHumanDecision)
				require.Equal(t, "INTERNAL_ROUNDS_EXHAUSTED", *work.EscalationReason)
				require.Equal(t, 1, escalations)
			} else {
				require.Equal(t, "REWORK", work.State)
				require.Zero(t, escalations)
			}
			_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
				return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
			})
			require.ErrorIs(t, err, ErrVerificationPending)
			require.Equal(t, prior+1, h.stored().InternalRoundCount)
		})
	}
}

func TestVerificationAutomationSelectsCurrentEligibleReviewers(t *testing.T) {
	h, r, in := verificationFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", r.ID).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "INTERNAL_QA").Error)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
	h.m.verificationExecutor.ReviewersOn = func(_ context.Context, _ *gorm.DB, tenant, source guid.ID, stage string) ([]guid.ID, error) {
		require.Equal(t, r.ID, source)
		if stage == "AGENT_TEST" {
			return []guid.ID{21, 22}, nil
		}
		return []guid.ID{23}, nil
	}
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		if principal == 21 {
			return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	tasks := map[guid.ID]VerificationRequest{}
	h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, req VerificationRequest) (guid.ID, error) {
		id := guid.ID(1000 + len(tasks))
		tasks[id] = req
		return id, nil
	}
	h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
		req := tasks[task]
		var exit *int
		if req.Method != "AGENT_JUDGE" {
			zero := 0
			exit = &zero
		}
		return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: r.ID, CriterionID: in.CriterionID, ResultVersion: r.Sequence, Method: req.Method, Stage: req.Stage, ReviewerID: req.ReviewerPrincipalID, Model: "review-model", Family: "source-family", FamilyFallback: req.Method == "AGENT_JUDGE", Verdict: "PASSED", ReceiptHash: strings.Repeat("a", 64), HeadCommit: req.HeadCommit, ExitCode: exit}, nil
	}
	apply := func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error) error {
		return h.db.Transaction(func(tx *gorm.DB) error { return fn(context.Background(), tx, 1, 99, h.work, r.ID) })
	}
	require.NoError(t, apply(h.m.queueAutomaticVerificationOn))
	require.Len(t, tasks, 1)
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	require.Len(t, tasks, 2)
	require.Equal(t, "AGENT_TEST", tasks[1001].Stage)
	require.Equal(t, guid.ID(22), tasks[1001].ReviewerPrincipalID, "owner-scoped ineligible candidate must be skipped")
	require.True(t, tasks[1001].AllowFamilyFallback, "fallback may be considered only after current project eligibility selects first preferred candidate")
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	require.Len(t, tasks, 3)
	require.Equal(t, "AGENT_ACCEPTANCE", tasks[1002].Stage)
	require.Equal(t, guid.ID(23), tasks[1002].ReviewerPrincipalID)
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	require.Equal(t, "NEEDS_HUMAN_REVIEW", h.stored().State)
	require.NoError(t, apply(h.m.collectAutomaticVerificationOn))
	require.Len(t, tasks, 3, "completed event replay cannot dispatch another reviewer")
	var quality []model.ReportQualityReceipt
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("kind=?", "INTERNAL_QA_PASSED").Find(&quality).Error)
	require.Len(t, quality, 1, "whole internal gate produces one immutable first pass, including replay")
	require.Equal(t, 1, quality[0].Rounds)
}
