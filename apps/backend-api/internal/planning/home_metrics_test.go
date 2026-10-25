package planning

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHomeReworkP50UsesEarliestInternalOrClientRejectionAndBucketUpperBound(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	at := h.m.now().Add(-time.Hour)
	h.m.homeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (delivery.HomeProjectFacts, error) {
		return delivery.HomeProjectFacts{ProjectID: 3, VisibleWorkIDs: []guid.ID{100, 101}, ReworkTimeline: []delivery.HomeWorkTransition{
			{WorkItemID: 100, At: at.Add(-2 * time.Hour)},
			{WorkItemID: 100, At: at, Accepted: true},
			{WorkItemID: 101, At: at.Add(-5 * time.Hour)},
			{WorkItemID: 101, At: at, Accepted: true},
		}}, nil
	}
	h.m.homeReceipts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (clientwork.HomeReceiptHistory, error) {
		return clientwork.HomeReceiptHistory{Complete: true, Events: []clientwork.HomeReceiptEvent{{WorkItemID: 100, At: at.Add(-3 * time.Hour), Rejected: true}}}, nil
	}
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Equal(t, "4", *out.KPIs[3].Value, "two cycles of 3h and 5h: P50 is the 4-hour bucket")
	require.Equal(t, "4.000000", *out.KPIs[3].MeanHours)
	require.Equal(t, "8", *out.KPIs[3].P90Hours)
	require.Equal(t, "2", *out.KPIs[3].Denominator)
	h.m.homeReceipts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (clientwork.HomeReceiptHistory, error) {
		return clientwork.HomeReceiptHistory{Complete: false}, nil
	}
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Nil(t, out.KPIs[3].Value, "legacy receipt without item identity cannot silently become no client rejection")
}

func TestHomeMetricsExactDenominatorsAndUnavailableFields(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
	at := h.m.now().Add(-time.Hour)
	h.m.homeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (delivery.HomeProjectFacts, error) {
		return delivery.HomeProjectFacts{ProjectID: 3, Mode: "HYBRID", Stage: "ACTIVE", BudgetSpentMicros: "9007199254740993", ClaimedDays: 3, EvidencedDays: 2,
			FirstAcceptances: []delivery.HomeAcceptance{{WorkItemID: 100, At: at}, {WorkItemID: 101, At: at, PriorQualityRework: true}}, ActiveLoads: map[guid.ID]int{2: 1}}, nil
	}
	h.m.qualityBranchBefore = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID, before time.Time) (bool, error) {
		require.Equal(t, guid.ID(100), work)
		require.Equal(t, at, before)
		return false, nil
	}
	h.m.homeCapacity = func(context.Context, *gorm.DB, guid.ID, guid.ID) (*int, error) { v := 4; return &v, nil }
	out, err := workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Len(t, out.KPIs, 5)
	require.Equal(t, "0.500000", *out.KPIs[1].Value)
	require.Equal(t, "2", *out.KPIs[1].Denominator)
	require.Equal(t, "0.666667", *out.KPIs[4].Value)
	require.Equal(t, "3", *out.KPIs[4].Denominator)
	require.Nil(t, out.KPIs[2].Value)
	require.Equal(t, "HUMAN_ATTENTION_DURATION", out.KPIs[2].UnavailableReason)
	require.Nil(t, out.KPIs[3].Value)
	require.Equal(t, "9007199254740993", out.ProjectHealth[0].BudgetSpentMicros)
	require.Equal(t, 1, *out.Load[0].Active)
	require.Equal(t, 4, *out.Load[0].Capacity)
	// Missing source predicates never silently become a zero rejection count.
	h.m.qualityBranchBefore = nil
	out, err = workbenchRead(t, h, 2, "", "")
	require.NoError(t, err)
	require.Nil(t, out.KPIs[1].Value)
	require.Equal(t, "QUALITY_BRANCH_HISTORY", out.KPIs[1].UnavailableReason)
}

func TestHomeRatioZeroDenominatorIsUnavailableAndLargeNumeratorExact(t *testing.T) {
	empty := homeRatio("COST_PER_ACCEPTED", 0, 0)
	require.Nil(t, empty.Value)
	require.Equal(t, "NO_DENOMINATOR", empty.UnavailableReason)
	large := homeRatio("COST_PER_ACCEPTED", 9007199254740993, 3)
	require.Equal(t, "3002399751580331.000000", *large.Value)
	require.Equal(t, "9007199254740993", *large.Numerator)
}

func TestHomeMetricsBindVisibleHistoryAndNeverTurnFailedSourcesIntoRatios(t *testing.T) {
	for _, fault := range []string{"success", "facts", "delegations", "receipts", "receipt_forbidden", "quality", "quality_forbidden", "identity", "capacity", "bad_cost", "negative_cost"} {
		t.Run(fault, func(t *testing.T) {
			h := setup(t)
			at := h.m.now().Add(-time.Hour)
			h.m.homeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (delivery.HomeProjectFacts, error) {
				if fault == "facts" {
					return delivery.HomeProjectFacts{}, ErrUnavailable
				}
				return delivery.HomeProjectFacts{VisibleWorkIDs: []guid.ID{100}, FirstAcceptances: []delivery.HomeAcceptance{{WorkItemID: 100, At: at}}, ActiveLoads: map[guid.ID]int{2: 1, 3: 2}}, nil
			}
			h.m.homeDelegations = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) ([]governance.AppliedDelegationFact, error) {
				if fault == "delegations" {
					return nil, ErrUnavailable
				}
				return []governance.AppliedDelegationFact{{WorkItemID: 100, ActorID: 3, At: at}, {WorkItemID: 999, At: at}}, nil
			}
			h.m.homeReceipts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (clientwork.HomeReceiptHistory, error) {
				if fault == "receipts" {
					return clientwork.HomeReceiptHistory{}, ErrUnavailable
				}
				if fault == "receipt_forbidden" {
					return clientwork.HomeReceiptHistory{}, clientwork.ErrForbidden
				}
				return clientwork.HomeReceiptHistory{Complete: true, Events: []clientwork.HomeReceiptEvent{{WorkItemID: 999, Rejected: true, At: at}, {PackageID: 50, At: at}}}, nil
			}
			h.m.qualityBranchBefore = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
				if fault == "quality" {
					return false, ErrUnavailable
				}
				if fault == "quality_forbidden" {
					return false, execution.ErrForbidden
				}
				return false, nil
			}
			h.m.principals.ActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if fault == "identity" {
					return identity.Principal{}, false, ErrUnavailable
				}
				return identity.Principal{ID: id, DisplayName: "Current member"}, true, nil
			}
			h.m.homeCapacity = func(context.Context, *gorm.DB, guid.ID, guid.ID) (*int, error) {
				if fault == "capacity" {
					return nil, ErrUnavailable
				}
				v := 4
				return &v, nil
			}
			cost := "12"
			if fault == "bad_cost" {
				cost = "corrupt"
			}
			if fault == "negative_cost" {
				cost = "-1"
			}
			out := WorkbenchSummary{From: at.Add(-time.Hour), To: h.m.now(), ExecutionCostMicros: &cost, Activity: []WorkbenchActivity{{Kind: "WORK_ACCEPTED"}, {Kind: "MESSAGE"}}}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.homeMetricsOn(claims(t, 2), tx, 1, []guid.ID{3}, &out) })
			if fault == "success" || fault == "receipt_forbidden" || fault == "quality_forbidden" {
				require.NoError(t, err)
				require.Len(t, out.Load, 2)
				require.Equal(t, guid.ID(2), out.Load[0].PrincipalID)
				require.Equal(t, "Current member", out.Load[1].DisplayName)
				require.Equal(t, "12.000000", *out.KPIs[0].Value)
				require.NotContains(t, out.Activity, WorkbenchActivity{Kind: "WORK_ACCEPTED"})
				var delegated int
				for _, a := range out.Activity {
					if a.Kind == "DELEGATED_ACCEPTANCE" {
						delegated++
						require.Equal(t, guid.ID(100), a.ObjectID)
					}
				}
				require.Equal(t, 1, delegated)
				if fault == "receipt_forbidden" {
					require.Nil(t, out.KPIs[3].Value)
				}
				if fault == "quality_forbidden" {
					require.Nil(t, out.KPIs[1].Value)
				}
			} else {
				require.ErrorIs(t, err, ErrUnavailable)
			}
		})
	}
}
