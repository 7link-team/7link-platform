package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAutomationDispatchConnectorRequiresCompleteCapabilitiesAndCurrentRoster(t *testing.T) {
	for _, stage := range []string{"unsealed", "incomplete", "bind_failure", "missing_roster", "roster_failure", "run"} {
		t.Run(stage, func(t *testing.T) {
			m := &Module{}
			s := module.NewServices()
			failure := errors.New("automation binding unavailable")
			connector := delivery.AutomationDispatchConnector{
				WorkOn: func(context.Context, *gorm.DB, delivery.AutomationDispatch) (delivery.DispatchWorkFacts, error) {
					return delivery.DispatchWorkFacts{}, nil
				},
				ApplyOn: func(context.Context, *gorm.DB, delivery.AutomationAssignment, guid.ID, guid.ID) (guid.ID, error) {
					return 0, nil
				},
				Bind: func(discover func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error), run func(context.Context, *gorm.DB, delivery.AutomationDispatch) error) error {
					if stage == "bind_failure" {
						return failure
					}
					if stage == "run" {
						require.ErrorIs(t, run(t.Context(), nil, delivery.AutomationDispatch{}), ErrInvalid)
						return nil
					}
					_, err := discover(t.Context(), nil, 1)
					if stage == "missing_roster" {
						require.ErrorIs(t, err, ErrUnavailable)
					} else {
						require.ErrorIs(t, err, failure)
					}
					return nil
				},
			}
			if stage == "incomplete" {
				connector.WorkOn = nil
			}
			if stage == "roster_failure" {
				m.dispatchFleet.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, failure }
			}
			require.NoError(t, module.Provide[delivery.AutomationDispatchConnector](s, connector))
			if stage != "unsealed" {
				require.NoError(t, s.Seal())
			}
			err := m.bindDispatch(s)
			switch stage {
			case "unsealed":
				require.Error(t, err)
			case "incomplete":
				require.ErrorIs(t, err, ErrUnavailable)
			case "bind_failure":
				require.ErrorIs(t, err, failure)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestAutomationDispatchUsesRealProposalPolicyAndSystemWriter(t *testing.T) {
	for _, mode := range []string{"manual", "auto", "high_risk", "revoked", "roster_changed", "apply_failure", "later_failure", "bad_role"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := authn.WithClaims(context.Background(), &authn.Claims{TenantID: 1, EntityID: 3, SubjectType: authn.SubjectTypePrincipal})
			in := delivery.AutomationDispatch{AutomationTransition: delivery.AutomationTransition{TenantID: 1, ProjectID: 3, WorkItemID: 11, CreatorID: 2, OwnerID: 2, RuleID: 700, RunID: 701, RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{2, 21}}, Role: "EXECUTOR"}
			if mode == "bad_role" {
				in.Role = "LEAD"
			}
			if mode == "roster_changed" {
				in.LockedPrincipals = []guid.ID{2}
			}
			m.dispatch.HistorySamplesOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) (int64, error) { return 0, nil }
			m.dispatch.QueuesOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]delivery.DispatchQueueFact, error) {
				return []delivery.DispatchQueueFact{{MachineID: 1, PendingCommands: 0, ObservedAt: now()}}, nil
			}
			list := m.dispatchFleet.ListOn
			m.dispatchFleet.ListOn = func(c context.Context, tx *gorm.DB, tenant guid.ID) ([]fleet.DispatchAgentFact, error) {
				rows, err := list(c, tx, tenant)
				machine := guid.ID(1)
				rows[0].MachineID = &machine
				return rows, err
			}
			audits := 0
			m.audit = func(c context.Context, _ *gorm.DB, e audit.DomainEvent) error {
				audits++
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Zero(t, e.PrincipalID)
				require.Equal(t, in.RunAs, e.Detail["authority_source"])
				require.Equal(t, in.RunID, e.Detail["run_id"])
				return nil
			}
			calls := 0
			connector := delivery.AutomationDispatchConnector{
				WorkOn: func(c context.Context, _ *gorm.DB, change delivery.AutomationDispatch) (delivery.DispatchWorkFacts, error) {
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, in, change)
					if mode == "revoked" {
						return delivery.DispatchWorkFacts{}, delivery.ErrWorkForbidden
					}
					policy, risk := "AUTO", "LOW"
					if mode == "manual" {
						policy = "MANUAL"
					}
					if mode == "high_risk" {
						risk = "HIGH"
					}
					lead := guid.ID(30)
					return delivery.DispatchWorkFacts{WorkItemID: 11, WorkVersion: 1, State: "READY", Stage: "ACTIVE", Type: "TASK", Risk: risk, RequiredTags: []string{"go"}, PreferredTags: []string{}, Policy: policy, TeamMode: "HYBRID", BudgetState: "NORMAL", LeadAgentID: &lead}, nil
				},
				ApplyOn: func(c context.Context, tx *gorm.DB, assignment delivery.AutomationAssignment, proposal, decision guid.ID) (guid.ID, error) {
					calls++
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, in.AutomationTransition, assignment.AutomationTransition)
					require.EqualValues(t, 21, assignment.AssigneePrincipalID)
					require.Positive(t, proposal)
					require.Positive(t, decision)
					if mode == "apply_failure" {
						return 0, delivery.ErrWorkForbidden
					}
					return 88, tx.Exec("INSERT INTO baseline_probe VALUES (?)", proposal).Error
				},
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				err := m.dispatchAutomationOn(ctx, tx, in, connector)
				if err == nil && mode == "later_failure" {
					return ErrForbidden
				}
				return err
			})
			q := query.BindDispatchProposal(db, m.names)
			rows, readErr := q.WithContext(ctx).Find()
			require.NoError(t, readErr)
			if mode == "revoked" || mode == "roster_changed" || mode == "apply_failure" || mode == "later_failure" || mode == "bad_role" {
				require.Error(t, err)
				require.Empty(t, rows)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Positive(t, audits)
			view, err := dispatchView(rows[0])
			require.NoError(t, err)
			require.Len(t, view.Snapshot.Candidates, 1)
			if mode != "auto" {
				require.Equal(t, "PROPOSED", rows[0].State)
				require.Zero(t, calls)
				return
			}
			require.Equal(t, "APPLIED", rows[0].State)
			require.Equal(t, 1, calls)
			require.EqualValues(t, 0, *rows[0].DecidedBy)
			a := query.BindDispatchAction(db, m.names)
			actions, err := a.WithContext(ctx).Find()
			require.NoError(t, err)
			require.Len(t, actions, 1)
			require.Zero(t, actions[0].ActorID)
			require.Equal(t, in.RunAs, actions[0].AuthoritySource)
		})
	}
}

func TestAutomationTestRunOwnerBindsThroughPublishedConnector(t *testing.T) {
	m, _, _, _ := harness(t)
	empty := module.NewServices()
	require.NoError(t, empty.Seal())
	require.NoError(t, m.bindAutomation(empty))
	for _, valid := range []bool{false, true} {
		services := module.NewServices()
		connector := delivery.AutomationTestRunWorkConnector{}
		bound := false
		if valid {
			connector.Bind = func(resolve delivery.AutomationTriggerWorkOn) error { bound = resolve != nil; return nil }
		}
		require.NoError(t, module.Provide[delivery.AutomationTestRunWorkConnector](services, connector))
		require.NoError(t, services.Seal())
		err := m.bindAutomation(services)
		if valid {
			require.NoError(t, err)
			require.True(t, bound)
		} else {
			require.ErrorIs(t, err, ErrUnavailable)
		}
	}
}

func TestAutomationTestRunTargetUsesFrozenCaseAndScopedSource(t *testing.T) {
	for _, mode := range []string{"stored", "edited_case", "foreign_tenant", "foreign_project", "missing_run", "missing_plan", "foreign_plan", "wrong_version", "empty_snapshot", "duplicate_case", "unfrozen_plan", "malformed_snapshot", "wrong_kind", "unrelated", "no_transaction"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			var item, plan, run Receipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				item, err = m.writeCaseOn(ctx, tx, 3, 0, 0, TestCaseBody{Title: "Source case", Steps: "Exercise", Expected: "Actual result", WorkItemID: 11})
				if err != nil {
					return err
				}
				plan, err = m.createPlanOn(ctx, tx, 3, TestPlanBody{Title: "Frozen source", CaseIDs: []guid.ID{item.ID}})
				if err != nil {
					return err
				}
				run, err = m.recordRunOn(ctx, tx, 3, plan.ID, plan.Version, TestRunBody{CaseID: item.ID, CaseVersion: item.Version, Result: "FAILED", Evidence: "Recorded observation"})
				return err
			}))
			source := events.RecordedEvent{TenantID: 1, ProjectID: 3, SubjectID: run.ID, SubjectKind: "clientwork", Type: "clientwork.test_run_recorded", Data: json.RawMessage(`{"work_item_id":"999"}`)}
			switch mode {
			case "edited_case":
				require.NoError(t, db.Table(m.names.TestCase.String()).Where("id = ?", item.ID).Updates(map[string]any{"work_item_id": 999, "version": 2}).Error)
			case "foreign_tenant":
				source.TenantID++
			case "foreign_project":
				source.ProjectID++
			case "missing_run":
				source.SubjectID++
			case "missing_plan":
				require.NoError(t, db.Table(m.names.TestRun.String()).Where("id = ?", run.ID).Update("plan_id", 999).Error)
			case "foreign_plan":
				require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id = ?", plan.ID).Update("project_id", 999).Error)
			case "wrong_version":
				require.NoError(t, db.Table(m.names.TestRun.String()).Where("id = ?", run.ID).Update("case_version", 2).Error)
			case "empty_snapshot":
				require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id = ?", plan.ID).Update("cases", "[]").Error)
			case "duplicate_case":
				p := query.BindTestPlan(db, m.names)
				row, err := p.WithContext(ctx).Where(p.ID.Eq(plan.ID)).First()
				require.NoError(t, err)
				view, err := planView(row)
				require.NoError(t, err)
				encoded, err := json.Marshal(append(view.Cases, view.Cases[0]))
				require.NoError(t, err)
				require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id = ?", plan.ID).Update("cases", string(encoded)).Error)
			case "unfrozen_plan":
				require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id = ?", plan.ID).Update("state", "DRAFT").Error)
			case "malformed_snapshot":
				require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id = ?", plan.ID).Update("cases", "{").Error)
			case "wrong_kind":
				source.SubjectKind = "work_item"
			case "unrelated":
				source.Type = "clientwork.test_case_written"
			}
			var target guid.ID
			resolve := func(tx *gorm.DB) error {
				var err error
				target, err = m.automationTestRunWorkOn(ctx, tx, source)
				return err
			}
			var err error
			if mode == "no_transaction" {
				err = resolve(db)
			} else {
				err = db.Transaction(resolve)
			}
			switch mode {
			case "stored", "edited_case":
				require.NoError(t, err)
				require.Equal(t, guid.ID(11), target)
			case "wrong_kind", "unrelated":
				require.NoError(t, err)
				require.Zero(t, target)
			default:
				require.Error(t, err)
				require.Zero(t, target)
			}
		})
	}
}

func TestSourceDispositionHTTPReplaysAndRechecksProjectAuthority(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	in := &EditInput[intake.SourceDisposition]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "decline-source-once", Body: intake.SourceDisposition{Action: "DECLINE", ReasonCode: "OTHER", Reason: "Human declines the requested source", ClientMessage: "This request cannot be accepted"}}
	first, e := m.disposeHTTP(testContext(t, 2), in, "decline")
	require.NoError(t, e)
	require.Equal(t, "DECLINED", first.Body.Data.State)
	replay, e := m.disposeHTTP(testContext(t, 2), in, "decline")
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	projects.deny = true
	_, e = m.disposeHTTP(testContext(t, 2), in, "decline")
	require.Error(t, e)
	projects.deny = false
	source, e := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, e)
	require.Equal(t, "private original source", *source.Body.Data.RawContent)
	require.Equal(t, in.Body.ClientMessage, *source.Body.Data.ClientMessage)
	var n int64
	require.NoError(t, db.Table("g_intake_records").Where("id=50 AND version=2").Count(&n).Error)
	require.EqualValues(t, 1, n)
}
