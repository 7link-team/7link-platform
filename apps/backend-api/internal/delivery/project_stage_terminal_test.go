package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestTerminalConnectorsBindCurrentOwnersExactlyOnce(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	m.stagePackages = ProjectStagePackages{}
	m.stageExecutions = nil
	s := module.NewServices()
	require.NoError(t, m.provideStageTerminal(s))
	require.NoError(t, module.Provide(s, governance.ProjectTerminalCleanup{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11, 11}, nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectStageTerminal(s))
	packages, e := module.Resolve[ProjectStagePackagesConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, packages.Bind(ProjectStagePackages{}), ErrInvalidArgument)
	owner := ProjectStagePackages{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) {
		return ProjectStagePackageFacts{}, nil
	}, ArchiveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }, DecisionOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, string) error { return nil }}
	require.NoError(t, packages.Bind(owner))
	require.ErrorIs(t, packages.Bind(owner), ErrInvalidArgument)
	executions, e := module.Resolve[ProjectStageExecutionsConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, executions.Bind(nil), ErrInvalidArgument)
	reader := func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.NoError(t, executions.Bind(reader))
	require.ErrorIs(t, executions.Bind(reader), ErrInvalidArgument)
	ids, e := m.stageTerminalCandidates(ctx, db, 1, 99)
	require.NoError(t, e)
	require.Contains(t, ids, guid.ID(11))
	count := 0
	for _, id := range ids {
		if id == 11 {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestTerminalSourceFailuresCannotCompleteProject(t *testing.T) {
	for _, fault := range []string{"facts", "deliveries", "work", "executions", "executions_missing", "cleanup", "archive", "missing_cleanup", "finish_storage", "candidates", "candidates_missing"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			injected := errors.New("terminal source unavailable")
			want := injected
			m.stagePackages = ProjectStagePackages{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) {
				return ProjectStagePackageFacts{}, nil
			}, ArchiveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}
			m.stageCleanup = governance.ProjectTerminalCleanup{EndOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) error { return nil }, PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }}
			m.stageExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
			m.service.channels = collab.ChannelProvisioner{ArchiveProjectChannelsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, collab.ProvisioningAudit) error { return nil }}
			switch fault {
			case "facts":
				m.stagePackages.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) {
					return ProjectStagePackageFacts{}, injected
				}
			case "deliveries", "work":
				table := m.service.names.Deliveries.String()
				if fault == "work" {
					table = m.service.names.WorkItems.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal-read-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
			case "executions":
				m.stageExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected }
			case "executions_missing":
				m.stageExecutions = nil
				want = ErrUnavailable
			case "cleanup":
				m.stageCleanup.EndOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) error { return injected }
			case "archive":
				m.stagePackages.ArchiveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return injected }
			case "missing_cleanup":
				m.stageCleanup.EndOn = nil
				want = ErrUnavailable
			case "finish_storage":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("terminal-write-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
			case "candidates":
				m.stageCleanup.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, injected }
			case "candidates_missing":
				m.stageCleanup.PrincipalsOn = nil
				want = ErrUnavailable
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				p, e := query.BindProjects(tx, m.service.names).WithContext(ctx).First()
				if e != nil {
					return e
				}
				if fault == "candidates" || fault == "candidates_missing" {
					_, e = m.stageTerminalCandidates(ctx, tx, 1, 99)
					return e
				}
				if fault == "cleanup" || fault == "archive" || fault == "missing_cleanup" || fault == "finish_storage" {
					return m.finishProjectOn(ctx, tx, p, "ROLE:owner", "Current terminal decision", nil)
				}
				_, e = m.stageTerminalCriteriaOn(ctx, tx, p, "CLOSED")
				return e
			})
			require.ErrorIs(t, e, want)
			p, e := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, 1, p.Version)
			require.Equal(t, "INTAKE", p.Stage)
		})
	}
}

func TestProjectStageTerminalActualFactsAndRollback(t *testing.T) {
	for _, scenario := range []string{"accepted", "missing", "stale", "old_result", "pending", "rework", "no_acceptance", "close_open_work", "close_execution", "cleanup_rollback", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := stageHarness(t)
			claim := guid.ID(800)
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "ACCEPTANCE_PENDING").Error)
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"type": "FEATURE", "state": "ACCEPTED", "current_claim_id": claim}).Error)
			facts := ProjectStagePackageFacts{Published: true, HasAccepted: true, Outcomes: []ProjectClientOutcome{{WorkItemID: work.ID, ResultID: claim, Accepted: true}}}
			target := "DELIVERED"
			quiet := true
			cleanup := 0
			failure := errors.New("cleanup rollback")
			m.stagePackages = ProjectStagePackages{FactsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) { return facts, nil }, ArchiveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { cleanup++; return nil }}
			m.stageCleanup = governance.ProjectTerminalCleanup{EndOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) error {
				cleanup++
				return nil
			}}
			m.service.channels = collab.ChannelProvisioner{ArchiveProjectChannelsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, collab.ProvisioningAudit) error {
				cleanup++
				if scenario == "cleanup_rollback" {
					return failure
				}
				return nil
			}}
			m.stageExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return quiet, nil }
			switch scenario {
			case "missing":
				facts.Outcomes = nil
			case "stale":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("baseline_stale", true).Error)
			case "old_result":
				facts.Outcomes[0].ResultID = 799
			case "pending":
				facts.Pending = true
			case "rework":
				facts.OpenRework = true
			case "no_acceptance":
				facts.HasAccepted = false
			}
			if scenario == "closed" || scenario == "cleanup_rollback" || scenario == "close_open_work" || scenario == "close_execution" {
				target = "CLOSED"
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "DELIVERED").Error)
			}
			if scenario == "close_open_work" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "READY").Error)
			}
			if scenario == "close_execution" {
				quiet = false
			}
			var result stageReceipt
			e := db.Transaction(func(tx *gorm.DB) error {
				var e error
				result, e = m.changeProjectStageOn(ctx, tx, 1, 99, 11, 1, stageChangeBody{ToStage: target, Reason: "Explicit project lifecycle decision"}, nil)
				return e
			})
			if scenario == "accepted" || scenario == "closed" {
				require.NoError(t, e)
				require.Equal(t, target, result.Project.Stage)
				if target == "CLOSED" {
					require.Equal(t, 3, cleanup)
					require.Equal(t, "NONE", result.Project.Health)
				}
			} else {
				require.Error(t, e)
				if target == "DELIVERED" {
					var failure stageFailure
					require.ErrorAs(t, e, &failure)
					require.ErrorIs(t, e, ErrStageDelivered)
				}
				q := query.BindProjects(db, m.service.names)
				p, e := q.WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, 1, p.Version)
			}
		})
	}
}

func TestProjectStageLatestReceiptAndScopeChangeCannotBeInvented(t *testing.T) {
	m, db, ctx, work := stageHarness(t)
	claim := guid.ID(800)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"type": "FEATURE", "state": "ACCEPTED", "current_claim_id": claim}).Error)
	now := time.Now().UTC()
	facts := ProjectStagePackageFacts{HasAccepted: true, Outcomes: []ProjectClientOutcome{{WorkItemID: work.ID, ResultID: claim, Accepted: false, DecidedAt: now, ReceiptID: 2}, {WorkItemID: work.ID, ResultID: claim, Accepted: true, DecidedAt: now.Add(-time.Hour), ReceiptID: 1}}}
	m.stagePackages.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) { return facts, nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindProjects(tx, m.service.names)
		p, e := q.WithContext(ctx).First()
		require.NoError(t, e)
		criteria, e := m.stageTerminalCriteriaOn(ctx, tx, p, "DELIVERED")
		require.NoError(t, e)
		require.False(t, criteria[1].Passed)
		require.Equal(t, []guid.ID{work.ID}, criteria[1].WorkItemIDs)
		// A second accepted artifact cannot hide a rejection in the same receipt.
		facts.Outcomes = append(facts.Outcomes, ProjectClientOutcome{WorkItemID: work.ID, ResultID: claim, Accepted: true, DecidedAt: now, ReceiptID: 2})
		criteria, e = m.stageTerminalCriteriaOn(ctx, tx, p, "DELIVERED")
		require.NoError(t, e)
		require.False(t, criteria[1].Passed)
		ok, e := m.scopeChangeEffectiveOn(ctx, tx, p, &claim)
		require.NoError(t, e)
		require.False(t, ok)
		return nil
	}))
}

func TestProjectStageCancellationCanonicalDecisionAndReplay(t *testing.T) {
	for _, scenario := range []string{"cancel", "decision_rollback", "code", "proof"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := stageHarness(t)
			allowed := true
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: allowed, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: allowed, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.scopeReauthentication = func(_ context.Context, _ *gorm.DB, _ guid.ID, cookie, proof, purpose, target string) error {
				require.Equal(t, "project_cancel", purpose)
				require.Equal(t, "project:99", target)
				if proof != "proof" || cookie != "cookie" {
					return ErrWorkForbidden
				}
				return nil
			}
			cancellation := ProjectExecutionCancellation{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11}, nil }, CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}
			m.stageCancellation = &clientWork{m: m, cancellation: &cancellation}
			m.stageCleanup = governance.ProjectTerminalCleanup{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }, EndOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) error { return nil }}
			require.NoError(t, db.Exec("CREATE TABLE stage_decision_probe(id BIGINT)").Error)
			m.stagePackages = ProjectStagePackages{ArchiveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }, DecisionOn: func(_ context.Context, tx *gorm.DB, _, _, id guid.ID, _ int, _, _ string) error {
				if scenario == "decision_rollback" {
					return errors.New("decision write failed")
				}
				return tx.Exec("INSERT INTO stage_decision_probe VALUES(?)", id).Error
			}}
			m.service.channels = collab.ChannelProvisioner{ArchiveProjectChannelsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, collab.ProvisioningAudit) error { return nil }}
			q := query.BindProjects(db, m.service.names)
			project, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			in := &stageChangeInput{EntityID: "99", IfMatch: `"1"`, IdempotencyKey: "terminal-cancel", Cookie: "cookie", Body: stageChangeBody{ToStage: "CANCELLED", Reason: "Human cancels the entire project", ConfirmCode: project.Code, ReauthProof: "proof"}} //nolint:misspell // Preserve the published domain or serialized contract spelling.
			if scenario == "code" {
				in.Body.ConfirmCode = "WRONG"
			}
			if scenario == "proof" {
				in.Body.ReauthProof = "bad"
			}
			out, e := m.changeProjectStageHTTP(ctx, in)
			if scenario != "cancel" {
				require.Error(t, e)
				p, e := q.WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, 1, p.Version)
				require.Equal(t, "INTAKE", p.Stage)
				return
			}
			require.NoError(t, e)
			require.Equal(t, "CANCELLED", out.Body.Data.Project.Stage) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			require.NotNil(t, out.Body.Data.Transition.DecisionID)
			w := query.BindWorkItems(db, m.service.names)
			item, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "CANCELLED", item.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			replay, e := m.changeProjectStageHTTP(ctx, in)
			require.NoError(t, e)
			require.Equal(t, "true", replay.Replayed)
			require.Equal(t, out.Body.Data, replay.Body.Data)
			allowed = false
			_, e = m.changeProjectStageHTTP(ctx, in)
			assertWorkStatus(t, e, 403)
		})
	}
}

func TestProjectStageReopensOnlyActualEffectivePostDeliveryScopeChange(t *testing.T) {
	m, db, ctx, requirement, body := scopeHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	delivered := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"stage": "DELIVERED", "delivered_at": delivered}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.requirementWriterOn(ctx, tx, 1, 99, 11); return e }), ErrRequirementStage, "ordinary Requirement edits remain forbidden after delivery")
	var change scopeChangeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		change, e = m.scopeChangeOn(ctx, tx, 1, 99, requirement.ID, 11, requirement.Version, "", body)
		return e
	}))
	q := query.BindProjects(db, m.service.names)
	before, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	invalid := guid.ID(123)
	e = db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, before.Version, stageChangeBody{ToStage: "ACTIVE", Reason: "Reopen for approved requirement change", ScopeChangeID: &invalid}, nil)
		return e
	})
	require.ErrorIs(t, e, ErrStageCriteria)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.changeProjectStageOn(ctx, tx, 1, 99, 11, before.Version, stageChangeBody{ToStage: "ACTIVE", Reason: "Reopen for approved requirement change", ScopeChangeID: &change.ID}, nil)
		require.NoError(t, e)
		require.Equal(t, "ACTIVE", out.Project.Stage)
		require.Equal(t, &delivered, out.Project.DeliveredAt)
		require.Equal(t, &change.ID, out.Transition.ScopeChangeID)
		current, e := query.BindProjects(tx, m.service.names).WithContext(ctx).First()
		require.NoError(t, e)
		reusable, e := m.scopeChangeEffectiveOn(ctx, tx, current, &change.ID)
		require.NoError(t, e)
		require.False(t, reusable, "a consumed scope change cannot reopen another delivery cycle")
		return nil
	}))
}
