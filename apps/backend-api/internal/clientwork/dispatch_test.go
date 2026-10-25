package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestDispatchSnapshotDependencyFailuresCannotProduceAnAssignableProposal(t *testing.T) {
	for _, stage := range []string{"auto_work_missing", "queue", "history", "history_samples", "action_count"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			failure := errors.New("dispatch facts unavailable")
			if stage == "auto_work_missing" || stage == "history_samples" || stage == "action_count" {
				ctx = context.WithValue(ctx, dispatchAutoKey{}, &dispatchAutoState{principals: []guid.ID{21}})
				m.dispatch.AutoWorkOn = m.dispatch.WorkOn
			}
			switch stage {
			case "auto_work_missing":
				m.dispatch.AutoWorkOn = nil
			case "queue":
				m.dispatch.QueuesOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]delivery.DispatchQueueFact, error) {
					return nil, failure
				}
			case "history", "action_count":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_dispatch_read", func(tx *gorm.DB) {
					if (stage == "history" && tx.Statement.Table == m.names.DispatchProposal.String()) || (stage == "action_count" && tx.Statement.Table == m.names.DispatchAction.String()) {
						tx.AddError(failure)
					}
				}))
			case "history_samples":
				m.dispatch.HistorySamplesOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) (int64, error) { return 0, failure }
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.dispatchSnapshotOn(ctx, tx, 3, 11); return err })
			require.Error(t, err)
			require.NoError(t, db.Callback().Query().Remove("regression_dispatch_read"))
			var count int64
			require.NoError(t, db.Table(m.names.DispatchProposal.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestDispatchAutomaticGuardsReportCurrentWorkAndLeadWithoutAssignment(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := context.WithValue(testContext(t, 2), dispatchAutoKey{}, &dispatchAutoState{principals: []guid.ID{21}})
	execution, lead := guid.ID(77), guid.ID(20)
	m.dispatch.AutoWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
		return delivery.DispatchWorkFacts{WorkItemID: 11, WorkVersion: 1, State: "RUNNING", Stage: "PAUSED", TeamMode: "HUMAN_ONLY", Type: "TASK", Risk: "LOW", RequiredTags: []string{"go"}, Policy: "MANUAL", BudgetState: "NORMAL", CurrentExecutionID: &execution, LeadAgentID: &lead}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snapshot, err := m.dispatchSnapshotOn(ctx, tx, 3, 11)
		require.NoError(t, err)
		for _, code := range []string{"G2", "G7", "CURRENT_ASSIGNMENT_OR_EXECUTION", "QUALITY_HISTORY_UNAVAILABLE", "MANUAL_POLICY"} {
			require.Contains(t, snapshot.GuardReasons, code)
		}
		require.True(t, snapshot.RequiresHuman)
		return nil
	}))
}

func dispatchFixture(t *testing.T) (*Module, *gorm.DB, *bool) {
	t.Helper()
	m, db, _, _ := harness(t)
	changed := false
	m.dispatch = delivery.DispatchAccess{
		AuthorityOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
			return "ROLE:delivery_lead", nil
		},
		WorkOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
			v := 1
			if changed {
				v = 2
			}
			return delivery.DispatchWorkFacts{WorkItemID: 11, WorkVersion: v, State: "READY", Stage: "ACTIVE", Type: "TASK", Risk: "LOW", RequiredTags: []string{"go"}, PreferredTags: []string{}, Policy: "MANUAL", BudgetState: "NORMAL"}, nil
		},
		LoadsOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) ([]delivery.DispatchLoadFact, error) {
			out := []delivery.DispatchLoadFact{}
			for _, id := range ids {
				out = append(out, delivery.DispatchLoadFact{PrincipalID: id, Member: true})
			}
			return out, nil
		},
		ApplyOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, in delivery.DispatchAssignmentInput) (guid.ID, error) {
			return 88, tx.Exec("INSERT INTO baseline_probe VALUES (?)", in.ProposalID).Error
		},
	}
	m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) {
		return []fleet.DispatchAgentFact{{ID: 20, PrincipalID: 21, Name: "Worker", State: "ACTIVE", ProcessStatus: "RUNNING", Roles: []string{"EXECUTOR"}, Runtime: "CODEX", ModelListed: true, Tags: []string{"go"}, MaxRisk: "HIGH", MaxParallel: 2, MachineOnline: true, FreeSlots: 1}}, nil
	}
	return m, db, &changed
}

func TestDispatchReplacementConflictsWhenExpiryWins(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	in := DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}
	var first Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.createDispatchOn(ctx, tx, 3, in)
		return err
	}))
	q := query.BindDispatchProposal(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Update(q.ExpiresAt, now().Add(-time.Minute))
	require.NoError(t, err)
	before, err := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, err)
	fired := false
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dispatch_expiry_wins", func(tx *gorm.DB) {
		if fired || tx.Statement.Table != m.names.DispatchProposal.String() {
			return
		}
		fired = true
		winner := query.BindDispatchProposal(tx.Session(&gorm.Session{NewDB: true}), m.names)
		_, err := winner.WithContext(ctx).Where(winner.ID.Eq(first.ID)).UpdateSimple(winner.State.Value("EXPIRED"), winner.ActiveSlot.Null(), winner.Version.Add(1))
		_ = tx.AddError(err)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("dispatch_expiry_wins")) })
	events := 0
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { events++; return nil }
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createDispatchOn(ctx, tx, 3, in)
		return err
	})
	require.True(t, fired)
	require.ErrorIs(t, err, ErrConflict)
	require.Zero(t, events)
	after, err := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, err)
	require.Equal(t, before, after, "the losing foreground transaction rolls back")
	rows, err := q.WithContext(ctx).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1, "no replacement is created after a failed expiry CAS")
}

func TestDispatchImmutableDecisionsBindInputsAndRollbackAsOneTransaction(t *testing.T) {
	for _, scenario := range []string{"confirm", "override", "dismiss", "expire", "decision_write", "authority", "empty_authority", "authority_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "action-create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
			require.NoError(t, err)
			id := created.Body.Data.ID
			body := DispatchDecision{Decision: "CONFIRM", Reason: "Observed inputs reviewed"}
			success := true
			switch scenario {
			case "override":
				target := guid.ID(22)
				body.Decision, body.AssigneePrincipalID = "OVERRIDE", &target
			case "dismiss":
				body.Decision = "DISMISS"
			case "expire":
				body.Decision = "EXPIRE"
				q := query.BindDispatchProposal(db, m.names)
				_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.ExpiresAt, now().Add(-time.Minute))
				require.NoError(t, err)
			case "decision_write":
				success = false
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_dispatch_action BEFORE INSERT ON g_clientwork_dispatch_actions BEGIN SELECT RAISE(ABORT, 'decision unavailable'); END").Error)
			case "authority", "empty_authority", "authority_unavailable":
				success = false
				m.dispatch.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
					if scenario == "authority" {
						return "", ErrForbidden
					}
					return "", nil
				}
				if scenario == "authority_unavailable" {
					m.dispatch.AuthorityOn = nil
				}
			}
			input := &EditInput[DispatchDecision]{EntityID: "3", ID: id.String(), IfMatch: `"1"`, Key: "action-decide", Body: body}
			_, err = m.decideDispatchHTTP(ctx, input)
			aq := query.BindDispatchAction(db, m.names)
			rows, readErr := aq.WithContext(ctx).Find()
			require.NoError(t, readErr)
			cq := query.BindCommandDecision(db, m.names)
			canonical, readErr := cq.WithContext(ctx).Find()
			require.NoError(t, readErr)
			var applied int64
			require.NoError(t, db.Table("baseline_probe").Count(&applied).Error)
			if !success {
				require.Error(t, err)
				require.Empty(t, rows)
				require.Empty(t, canonical)
				require.Zero(t, applied, "assignment must roll back when immutable evidence fails")
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, body.Decision, rows[0].Action)
			require.Equal(t, 1, rows[0].ProposalVersion)
			require.EqualValues(t, 2, rows[0].ActorID)
			require.Equal(t, "ROLE:delivery_lead", rows[0].AuthoritySource)
			view, err := m.getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
			require.NoError(t, err)
			require.NotNil(t, view.Body.Data.Decision)
			require.Equal(t, view.Body.Data.InputsDigest, rows[0].InputsDigest)
			if body.Decision == "CONFIRM" || body.Decision == "OVERRIDE" {
				require.Len(t, canonical, 1)
				require.Equal(t, rows[0].ID, canonical[0].ID)
				require.EqualValues(t, 1, applied)
			} else {
				require.Empty(t, canonical)
				require.Zero(t, applied)
			}
			replayed, err := m.decideDispatchHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "true", replayed.Replayed)
			m.dispatch.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
				return "", ErrForbidden
			}
			_, err = m.decideDispatchHTTP(ctx, input)
			require.Error(t, err, "replay must check current decision authority")
			count, err := aq.WithContext(ctx).Count()
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestDispatchProposalSnapshotApplyReplayAndCurrentVersion(t *testing.T) {
	m, db, changed := dispatchFixture(t)
	ctx := testContext(t, 2)
	input := &CreateInput[DispatchBody]{EntityID: "3", Key: "dispatch", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}}
	receipt, e := m.createDispatchHTTP(ctx, input)
	require.NoError(t, e)
	replay, e := m.createDispatchHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	view, e := m.getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: receipt.Body.Data.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "RECOMMENDED", view.Body.Data.Result)
	require.EqualValues(t, 21, *view.Body.Data.RecommendedID)
	require.Len(t, view.Body.Data.InputsDigest, 64)
	require.True(t, view.Body.Data.Snapshot.RequiresHuman)
	require.False(t, view.Body.Data.Snapshot.Candidates[0].HistoryAvailable)
	decision := &EditInput[DispatchDecision]{EntityID: "3", ID: receipt.Body.Data.ID.String(), Key: "confirm", IfMatch: `"1"`, Body: DispatchDecision{Decision: "CONFIRM", Reason: "Checked candidate"}}
	*changed = true
	_, e = m.decideDispatchHTTP(ctx, decision)
	require.Error(t, e)
	*changed = false
	applied, e := m.decideDispatchHTTP(ctx, decision)
	require.NoError(t, e)
	require.Equal(t, "APPLIED", applied.Body.Data.State)
	replay, e = m.decideDispatchHTTP(ctx, decision)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	var n int64
	require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
	require.EqualValues(t, 1, n)
	_, e = m.getDispatchHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: decision.ID})
	require.Error(t, e)
}
func TestDispatchExpiryAndApplicationRollback(t *testing.T) {
	for _, scenario := range []string{"expiry", "audit", "assign", "eligibility", "dismiss", "override"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
			require.NoError(t, e)
			id := created.Body.Data.ID
			if scenario == "expiry" {
				q := query.BindDispatchProposal(db, m.names)
				_, e = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.ExpiresAt, now().Add(-time.Second))
				require.NoError(t, e)
			}
			if scenario == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			if scenario == "assign" {
				m.dispatch.ApplyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.DispatchAssignmentInput) (guid.ID, error) {
					return 0, errors.New("assign unavailable")
				}
			}
			if scenario == "eligibility" {
				m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) {
					return []fleet.DispatchAgentFact{{ID: 20, PrincipalID: 21, State: "STOPPED"}}, nil
				}
			}
			decision := DispatchDecision{Decision: "CONFIRM", Reason: "Reviewed"}
			if scenario == "dismiss" {
				decision.Decision = "DISMISS"
			}
			if scenario == "override" {
				target := guid.ID(6)
				decision.Decision = "OVERRIDE"
				decision.AssigneePrincipalID = &target
			}
			_, e = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: id.String(), Key: "decide", IfMatch: `"1"`, Body: decision})
			q := query.BindDispatchProposal(db, m.names)
			row, re := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
			require.NoError(t, re)
			if scenario == "dismiss" || scenario == "override" {
				require.NoError(t, e)
				require.Nil(t, row.ActiveSlot)
				require.Equal(t, 2, row.Version)
			} else {
				require.Error(t, e)
				require.Equal(t, "PROPOSED", row.State)
				require.Equal(t, 1, row.Version)
				var n int64
				require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
				require.Zero(t, n)
			}
			if scenario == "expiry" {
				_, e = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: id.String(), Key: "expire", IfMatch: `"1"`, Body: DispatchDecision{Decision: "EXPIRE", Reason: "TTL reached"}})
				require.NoError(t, e)
			}
		})
	}
}
func TestDispatchScoringIntegerBoundariesAndStableTies(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5} {
		c := DispatchCandidate{DispatchAgentFact: fleet.DispatchAgentFact{ID: 1, Tags: []string{"a"}, MaxParallel: 5}, ActiveItems: n, Applied24H: n}
		dispatchScore(&c, delivery.DispatchWorkFacts{RequiredTags: []string{"a", "b", "c"}})
		require.Equal(t, 3333, c.Components.SkillBP)
		require.Equal(t, 10000-2000*n, c.Components.LoadBP)
		require.Equal(t, 10000-2000*n, c.Components.DiversityBP)
		require.LessOrEqual(t, c.Score, 1000000)
		require.GreaterOrEqual(t, c.Score, 0)
	}
	a := DispatchCandidate{DispatchAgentFact: fleet.DispatchAgentFact{ID: 1}, Score: 900000}
	b := a
	b.ID = 2
	require.True(t, dispatchCandidateLess(a, b))
	require.False(t, dispatchCandidateLess(b, a))
	b.IneligibleCodes = []string{"STATE"}
	require.True(t, dispatchCandidateLess(a, b))
}

func TestDispatchDomainFailuresDoNotCreateDecisionOrApplyAssignment(t *testing.T) {
	for _, fault := range []string{"authority_unavailable", "authority_empty", "ids", "auto_apply", "snapshot", "canonical"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "decision-source", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
			require.NoError(t, err)
			id := created.Body.Data.ID
			in := DispatchDecision{Decision: "CONFIRM", Reason: "Current authority and inputs are required"}
			switch fault {
			case "authority_unavailable":
				m.dispatch.AuthorityOn = nil
			case "authority_empty":
				m.dispatch.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) { return "", nil }
			case "ids":
				m.ids = nil
			case "auto_apply":
				in.Decision = "AUTO_APPLY"
			case "snapshot":
				m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
					return delivery.DispatchWorkFacts{}, ErrUnavailable
				}
			case "canonical":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("canonical_dispatch_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.CommandDecision.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err = db.Transaction(func(tx *gorm.DB) error { _, e := m.decideDispatchOn(ctx, tx, 3, id, 1, in); return e })
			require.Error(t, err)
			require.NoError(t, db.Callback().Create().Remove("canonical_dispatch_failure"))
			q := query.BindDispatchProposal(db, m.names)
			row, e := q.WithContext(ctx).Where(q.ID.Eq(id)).Take()
			require.NoError(t, e)
			require.Equal(t, "PROPOSED", row.State)
			require.Equal(t, 1, row.Version)
			for _, table := range []string{m.names.CommandDecision.String(), m.names.DispatchAction.String(), "baseline_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}
