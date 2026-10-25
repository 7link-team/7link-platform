package clientwork

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchAutoColdStartAppliesActualPolicyAndGuardedCasesStayProposed(t *testing.T) {
	for _, scenario := range []string{"apply", "risk", "history", "budget", "require_human", "queues_missing", "wrong_actor", "apply_failure", "fleet_changed"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 25)
			lead, machine := guid.ID(30), guid.ID(1)
			allowed := true
			m.dispatch.AutoLockOn = func(ctx context.Context, _ *gorm.DB, tenant, project, work guid.ID, ids []guid.ID) error {
				if !allowed || scenario == "wrong_actor" {
					return ErrForbidden
				}
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 3, project)
				require.EqualValues(t, 11, work)
				require.ElementsMatch(t, []guid.ID{21, 25}, ids)
				return nil
			}
			m.dispatch.AutoWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
				f := delivery.DispatchWorkFacts{WorkItemID: 11, WorkVersion: 1, State: "READY", Stage: "ACTIVE", Type: "TASK", Risk: "LOW", RequiredTags: []string{"go"}, PreferredTags: []string{}, Policy: "AUTO", TeamMode: "HYBRID", LeadAgentID: &lead, BudgetState: "NORMAL"}
				if scenario == "risk" {
					f.Risk = "HIGH"
				}
				if scenario == "require_human" {
					f.RequireHumanDecision = true
				}
				if scenario == "budget" {
					v := int64(100)
					f.BudgetRemaining = &v
				}
				return f, nil
			}
			m.dispatch.AutoAuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
				return "POLICY:dispatch_auto", nil
			}
			m.dispatch.HistorySamplesOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) (int64, error) {
				if scenario == "history" {
					return 1, nil
				}
				return 0, nil
			}
			m.dispatch.QueuesOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]delivery.DispatchQueueFact, error) {
				return []delivery.DispatchQueueFact{{MachineID: machine, PendingCommands: 0, ObservedAt: now()}}, nil
			}
			if scenario == "queues_missing" {
				m.dispatch.QueuesOn = nil
			}
			m.dispatchFleet.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{21, 25}, nil }
			oldList := m.dispatchFleet.ListOn
			m.dispatchFleet.ListOn = func(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]fleet.DispatchAgentFact, error) {
				facts, err := oldList(ctx, tx, tenant)
				facts[0].MachineID = &machine
				if scenario == "fleet_changed" {
					facts[0].PrincipalID = 99
				}
				return append(facts, fleet.DispatchAgentFact{ID: lead, PrincipalID: 25, State: "ACTIVE", ProcessStatus: "RUNNING", Roles: []string{"LEAD"}, MaxParallel: 1, MaxRisk: "HIGH"}), err
			}
			apply := m.dispatch.ApplyOn
			m.dispatch.ApplyOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in delivery.DispatchAssignmentInput) (guid.ID, error) {
				require.True(t, in.Auto)
				require.Positive(t, in.DecisionID)
				if scenario == "apply_failure" {
					return 0, ErrUnavailable
				}
				return apply(ctx, tx, tenant, project, in)
			}
			input := &DispatchAutoInput{EntityID: "3", Key: "auto", Body: DispatchAutoBody{WorkItemID: 11, ExpectedWorkVersion: 1}}
			out, err := m.autoDispatchHTTP(ctx, input)
			q := query.BindDispatchProposal(db, m.names)
			rows, readErr := q.WithContext(ctx).Find()
			require.NoError(t, readErr)
			if scenario == "wrong_actor" || scenario == "apply_failure" || scenario == "fleet_changed" {
				require.Error(t, err)
				require.Empty(t, rows)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 1)
			aq := query.BindDispatchAction(db, m.names)
			actions, err := aq.WithContext(ctx).Find()
			require.NoError(t, err)
			if scenario != "apply" {
				require.Equal(t, "PROPOSED", out.Body.Data.State)
				require.Empty(t, actions)
				return
			}
			require.Equal(t, "APPLIED", out.Body.Data.State)
			require.Len(t, actions, 1)
			require.Equal(t, "AUTO_APPLY", actions[0].Action)
			require.EqualValues(t, 25, actions[0].ActorID)
			require.Equal(t, "POLICY:dispatch_auto", actions[0].AuthoritySource)
			frozen, err := dispatchView(rows[0])
			require.NoError(t, err)
			require.False(t, frozen.Snapshot.RequiresHuman)
			require.EqualValues(t, 0, *frozen.Snapshot.HistorySamples90D)
			require.False(t, frozen.Snapshot.Candidates[0].HistoryAvailable)
			replay, err := m.autoDispatchHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "true", replay.Replayed)
			allowed = false
			_, err = m.autoDispatchHTTP(ctx, input)
			require.Error(t, err, "revoked current lead authority must deny replay")
		})
	}
}
