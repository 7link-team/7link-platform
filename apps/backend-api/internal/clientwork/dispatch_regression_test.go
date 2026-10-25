package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchCandidateFailuresRemainExplicitAndNeverRecommended(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	lead, remaining, runtime := guid.ID(20), int64(0), "OTHER"
	m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
		return delivery.DispatchWorkFacts{WorkItemID: 11, WorkVersion: 1, State: "READY", Stage: "ACTIVE", Risk: "HIGH", RequiredRuntime: &runtime, RequiredTags: []string{"required"}, Policy: "AUTO", LeadAgentID: &lead, BudgetState: "EXHAUSTED", BudgetRemaining: &remaining, Held: true, RequireHumanDecision: true}, nil
	}
	m.dispatch.LoadsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]delivery.DispatchLoadFact, error) {
		return []delivery.DispatchLoadFact{{PrincipalID: 21, ActiveItems: 2, Member: false}}, nil
	}
	m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) {
		return []fleet.DispatchAgentFact{{ID: 20, PrincipalID: 21, State: "STOPPED", Roles: []string{}, MaxRisk: "LOW", MaxParallel: 1, SpawnBudgetUnverified: true}}, nil
	}
	created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "no-eligible", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "CHANNEL_REQUEST"}})
	require.NoError(t, err)
	page, err := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	view := page.Body.Data.Items[0]
	require.Equal(t, created.Body.Data.ID, view.ID)
	require.Equal(t, "NO_ELIGIBLE", view.Result)
	require.Nil(t, view.RecommendedID)
	require.ElementsMatch(t, []string{"ROLE", "STATE", "MACHINE", "CAPABILITY", "SKILL_REQUIRED", "RISK", "MEMBER", "CAPACITY", "NOT_LEAD_AUTO", "BUDGET_COST_UNAVAILABLE", "SPAWN_BUDGET"}, view.Snapshot.Candidates[0].IneligibleCodes)
	require.Subset(t, view.Snapshot.GuardReasons, []string{"G1", "G2", "G6", "CHANNEL_REQUEST", "AUTO_POLICY_UNAVAILABLE"})
	_, err = m.listDispatchHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, err)
	for _, input := range []*ObjectInput{{EntityID: "bad"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		_, err = m.getDispatchHTTP(ctx, input)
		require.Error(t, err)
	}
	_, err = (&Module{}).getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: "1"})
	require.Error(t, err)
	_, err = m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: "1", IfMatch: "bad"})
	require.Error(t, err)
	q := query.BindDispatchProposal(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(view.ID)).Update(q.ExpiresAt, now().Add(-time.Hour))
	require.NoError(t, err)
	_, err = m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "replacement", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "REDISPATCH"}})
	require.NoError(t, err)
	old, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", old.State)
	require.Nil(t, old.ActiveSlot)
	require.Equal(t, 2, old.Version)
}

func TestDispatchSourceFailuresCannotPersistPartialProposal(t *testing.T) {
	for _, scenario := range []string{"work", "fleet", "loads", "missing_load", "missing_capability", "id", "version", "invalid", "insert"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			failure := errors.New("dispatch source unavailable")
			body := DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}
			switch scenario {
			case "work":
				m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
					return delivery.DispatchWorkFacts{}, failure
				}
			case "fleet":
				m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) { return nil, failure }
			case "loads":
				m.dispatch.LoadsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]delivery.DispatchLoadFact, error) {
					return nil, failure
				}
			case "missing_load":
				m.dispatch.LoadsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]delivery.DispatchLoadFact, error) {
					return nil, nil
				}
			case "missing_capability":
				m.dispatch.WorkOn = nil
			case "id":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "version":
				body.ExpectedWorkVersion = 99
			case "invalid":
				body.Trigger = "UNKNOWN"
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_dispatch_insert BEFORE INSERT ON g_clientwork_dispatch_proposals BEGIN SELECT RAISE(ABORT, 'insert unavailable'); END").Error)
			}
			_, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "failed-create", Body: body})
			require.Error(t, err)
			count, err := query.BindDispatchProposal(db, m.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestDispatchHistoryAndHumanOverrideConstrainSubsequentRecommendation(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	original := m.dispatchFleet.ListOn
	m.dispatchFleet.ListOn = func(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]fleet.DispatchAgentFact, error) {
		rows, err := original(ctx, tx, tenant)
		second := rows[0]
		second.ID, second.PrincipalID = 19, 22
		return append(rows, second), err
	}
	for i, decision := range []string{"CONFIRM", "OVERRIDE"} {
		created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: decision + "-create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "REDISPATCH"}})
		require.NoError(t, err)
		view, err := m.getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
		require.NoError(t, err)
		if i == 0 {
			require.Equal(t, guid.ID(22), *view.Body.Data.RecommendedID)
		} else {
			require.Equal(t, guid.ID(21), *view.Body.Data.RecommendedID)
		}
		target := guid.ID(6)
		_, err = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: created.Body.Data.ID.String(), Key: decision + "-decide", IfMatch: `"1"`, Body: DispatchDecision{Decision: decision, Reason: "Human reviewed current candidates", AssigneePrincipalID: &target}})
		require.NoError(t, err)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snapshot, err := m.dispatchSnapshotOn(ctx, tx, 3, 11)
		require.NoError(t, err)
		require.Subset(t, snapshot.GuardReasons, []string{"G5", "G8"})
		require.Equal(t, guid.ID(22), snapshot.Candidates[0].PrincipalID)
		require.Equal(t, 1, snapshot.Candidates[0].Applied24H)
		require.NotNil(t, snapshot.Candidates[0].LastAppliedAt)
		require.Contains(t, snapshot.Candidates[1].IneligibleCodes, "NOT_EXCLUDED")
		return nil
	}))
}

func TestDispatchRejectedDecisionsPreserveFrozenProposal(t *testing.T) {
	for _, scenario := range []string{"empty_reason", "missing", "version", "invalid", "early_expire", "no_target", "missing_apply", "missing_candidate", "update"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
			require.NoError(t, err)
			q := query.BindDispatchProposal(db, m.names)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
			require.NoError(t, err)
			id, version := before.ID, 1
			body := DispatchDecision{Decision: "CONFIRM", Reason: "Human reviewed input"}
			switch scenario {
			case "empty_reason":
				body.Reason = ""
			case "missing":
				id = 999
			case "version":
				version = 99
			case "invalid":
				body.Decision = "UNKNOWN"
			case "early_expire":
				body.Decision = "EXPIRE"
			case "no_target":
				body.Decision = "OVERRIDE"
			case "missing_apply":
				m.dispatch.ApplyOn = nil
			case "missing_candidate":
				m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) { return nil, nil }
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_dispatch_update BEFORE UPDATE ON g_clientwork_dispatch_proposals BEGIN SELECT RAISE(ABORT, 'update unavailable'); END").Error)
			}
			err = db.Transaction(func(tx *gorm.DB) error { _, e := m.decideDispatchOn(ctx, tx, 3, id, version, body); return e })
			require.Error(t, err)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(before.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after)
			var count int64
			require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestDispatchTieOrderIsDeterministicAndPrefersEligibleCandidates(t *testing.T) {
	at := now()
	later := at.Add(time.Hour)
	base := DispatchCandidate{DispatchAgentFact: fleet.DispatchAgentFact{ID: 1}, Score: 100, Components: DispatchComponents{FPABP: 100}, ActiveItems: 1}
	for _, scenario := range []string{"eligible", "fewer_failures", "score", "quality", "load", "never_assigned", "older_assignment", "id"} {
		t.Run(scenario, func(t *testing.T) {
			a, b := base, base
			b.ID = 2
			switch scenario {
			case "eligible":
				b.IneligibleCodes = []string{"STATE"}
			case "fewer_failures":
				a.IneligibleCodes = []string{"STATE"}
				b.IneligibleCodes = []string{"STATE", "MACHINE"}
			case "score":
				b.Score = 99
			case "quality":
				b.Components.FPABP = 99
			case "load":
				b.ActiveItems = 2
			case "never_assigned":
				b.LastAppliedAt = &at
			case "older_assignment":
				a.LastAppliedAt = &at
				b.LastAppliedAt = &later
			}
			require.True(t, dispatchCandidateLess(a, b), scenario)
			require.False(t, dispatchCandidateLess(b, a), scenario)
			require.False(t, dispatchCandidateLess(a, a), "ordering must be irreflexive")
		})
	}
}

func TestDispatchCorruptSnapshotFailsListWithoutPartialData(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, err)
	q := query.BindDispatchProposal(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).Update(q.Snapshot, "{")
	require.NoError(t, err)
	page, err := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, err)
	require.Nil(t, page)
	view, err := m.getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.Error(t, err)
	require.Nil(t, view)
}
