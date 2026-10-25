package fleet

import (
	"context"
	"testing"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ensembleRegressionTestEnsembleHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *fleetPublisher) {
	t.Helper()
	m, db, ctx, _, pub := enrollmentHarness(t)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}

	m.ensemble = EnsembleSources{
		AuthorizeOn: func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID, permission string) error {
			require.EqualValues(t, 7, tenant)
			require.EqualValues(t, 99, project)
			require.EqualValues(t, 77, work)
			require.Contains(t, []string{"work_item_assign", "work_item_read"}, permission)
			return nil
		},
		ExecutionOn: func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) (EnsembleExecution, error) {
			require.EqualValues(t, 7, tenant)
			require.EqualValues(t, 99, project)
			return EnsembleExecution{ID: id, WorkItemID: 77, PrincipalID: 101, State: "SUCCEEDED", ResultState: "SUBMITTED"}, nil
		},
	}
	return m, db, ctx, pub
}

func TestEnsembleActualAlternativesCompareSelectSynthesizeAndAbandon(t *testing.T) {
	for _, action := range []string{"SELECT", "SYNTHESIZE", "ABANDON"} {
		t.Run(action, func(t *testing.T) {
			m, db, ctx, pub := ensembleRegressionTestEnsembleHarness(t)
			var plan EnsembleView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				plan, e = m.CreateEnsembleOn(ctx, tx, 7, 99, EnsembleCreate{WorkItemID: 77, ExecutionIDs: []guid.ID{11, 12}, Reason: "Compare actual completed alternatives"})
				return e
			}))
			require.Equal(t, "DRAFT", plan.State)
			require.Len(t, plan.Branches, 2)
			change := func(in EnsembleDecision) (EnsembleView, error) {
				var out EnsembleView
				e := db.Transaction(func(tx *gorm.DB) error {
					var e error
					out, e = m.ChangeEnsembleOn(ctx, tx, 7, 99, plan.ID, in)
					return e
				})
				return out, e
			}
			compared, err := change(EnsembleDecision{Action: "COMPARE", Version: 1, Reason: "Compare immutable execution facts"})
			require.NoError(t, err)
			require.Equal(t, "COMPARING", compared.State)
			require.Len(t, compared.Comparison, 2)
			for _, fact := range compared.Comparison {
				require.Equal(t, "SUCCEEDED", fact.State)
				require.Equal(t, "SUBMITTED", fact.ResultState)
			}
			in := EnsembleDecision{Action: action, BranchID: plan.Branches[0].ID, SynthesisExecutionID: 13, Version: 2, Reason: "Choose a real execution alternative"}
			pub.fail = true
			_, err = change(in)
			require.Error(t, err)
			q := query.BindEnsemblePlans(db, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "COMPARING", row.State)
			require.Equal(t, 2, row.Version)
			pub.fail = false
			out, err := change(in)
			require.NoError(t, err)
			want := map[string]string{"SELECT": "SELECTED", "SYNTHESIZE": "SYNTHESIZED", "ABANDON": "ABANDONED"}[action]
			require.Equal(t, want, out.State)
			require.Equal(t, 3, out.Version)
			for _, branch := range out.Branches {
				if action == "ABANDON" || branch.ID != in.BranchID {
					require.NotNil(t, branch.ArchivedAt)
				} else {
					require.Nil(t, branch.ArchivedAt)
				}
			}
			if action == "SYNTHESIZE" {
				require.NotNil(t, out.SynthesisExecutionID)
				require.EqualValues(t, 13, *out.SynthesisExecutionID)
			}
			_, err = change(in)
			require.ErrorIs(t, err, ErrVersionConflict)
		})
	}
}

func TestEnsembleSourceFencesAndInvalidInputLeaveNoPlans(t *testing.T) {
	for _, fault := range []string{"duplicate", "invalid id", "reason", "no dependency", "authority", "execution", "foreign work", "id source", "zero id", "outbox", "plan insert", "branch insert"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, pub := ensembleRegressionTestEnsembleHarness(t)
			in := EnsembleCreate{WorkItemID: 77, ExecutionIDs: []guid.ID{11, 12}, Reason: "Compare actual completed alternatives"}
			switch fault {
			case "duplicate":
				in.ExecutionIDs = []guid.ID{11, 11}
			case "invalid id":
				in.ExecutionIDs = []guid.ID{0, 12}
			case "reason":
				in.Reason = "short"
			case "no dependency":
				m.ensemble.ExecutionOn = nil
			case "authority":
				m.ensemble.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrMachineForbidden }
			case "execution":
				m.ensemble.ExecutionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error) {
					return EnsembleExecution{}, ErrUnavailable
				}
			case "foreign work":
				m.ensemble.ExecutionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error) {
					return EnsembleExecution{WorkItemID: 88}, nil
				}
			case "id source":
				m.ids = ensembleIDs{err: ErrUnavailable}
			case "zero id":
				m.ids = ensembleIDs{}
			case "outbox":
				pub.fail = true
			case "plan insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_ensemble BEFORE INSERT ON "+m.names.EnsemblePlans.String()+" BEGIN SELECT RAISE(ABORT,'plan insert failed'); END").Error)
			case "branch insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_ensemble BEFORE INSERT ON "+m.names.EnsembleBranches.String()+" BEGIN SELECT RAISE(ABORT,'branch insert failed'); END").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.CreateEnsembleOn(ctx, tx, 7, 99, in); return e })
			require.Error(t, err)
			for _, name := range []string{m.names.EnsemblePlans.String(), m.names.EnsembleBranches.String(), "test_fleet_effects"} {
				var n int64
				require.NoError(t, db.Table(name).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

type ensembleIDs struct{ err error }

func TestEnsembleRejectedDecisionsPreservePlanAndEvents(t *testing.T) {
	for _, fault := range []string{"reason", "action", "branch", "execution source", "foreign comparison", "failed selection", "synthesis duplicate", "synthesis source", "synthesis foreign"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := ensembleRegressionTestEnsembleHarness(t)
			var plan EnsembleView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				plan, err = m.CreateEnsembleOn(ctx, tx, 7, 99, EnsembleCreate{WorkItemID: 77, ExecutionIDs: []guid.ID{11, 12}, Reason: "Compare actual completed alternatives"})
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				plan, err = m.ChangeEnsembleOn(ctx, tx, 7, 99, plan.ID, EnsembleDecision{Version: 1, Action: "COMPARE", Reason: "Freeze actual completed results"})
				return err
			}))
			in := EnsembleDecision{Version: 2, Action: "SELECT", BranchID: plan.Branches[0].ID, Reason: "Choose actual completed alternative"}
			original := m.ensemble.ExecutionOn
			switch fault {
			case "reason":
				in.Reason = "short"
			case "action":
				in.Action = "UNKNOWN"
			case "branch":
				in.BranchID = 999
			case "execution source":
				in.Action = "COMPARE"
				m.ensemble.ExecutionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error) {
					return EnsembleExecution{}, ErrUnavailable
				}
			case "foreign comparison", "failed selection":
				if fault == "foreign comparison" {
					in.Action = "COMPARE"
				}
				m.ensemble.ExecutionOn = func(c context.Context, tx *gorm.DB, tenant, project, id guid.ID) (EnsembleExecution, error) {
					v, err := original(c, tx, tenant, project, id)
					if fault == "foreign comparison" {
						v.WorkItemID = 88
					} else {
						v.State = "FAILED"
					}
					return v, err
				}
			case "synthesis duplicate":
				in.Action, in.SynthesisExecutionID = "SYNTHESIZE", 11
			case "synthesis source", "synthesis foreign":
				in.Action, in.SynthesisExecutionID = "SYNTHESIZE", 13
				m.ensemble.ExecutionOn = func(c context.Context, tx *gorm.DB, tenant, project, id guid.ID) (EnsembleExecution, error) {
					v, err := original(c, tx, tenant, project, id)
					if id == 13 {
						if fault == "synthesis source" {
							return v, ErrUnavailable
						}
						v.WorkItemID = 88
					}
					return v, err
				}
			}
			var before int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&before).Error)
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.ChangeEnsembleOn(ctx, tx, 7, 99, plan.ID, in)
				return err
			}))
			q := query.BindEnsemblePlans(db, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).First()
			require.NoError(t, err)
			require.Equal(t, 2, row.Version)
			require.Equal(t, "COMPARING", row.State)
			require.Nil(t, row.SelectedBranchID)
			var after int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func (s ensembleIDs) Next() (int64, error) { return 0, s.err }

func TestEnsembleOwnerConnectorsBindOnce(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideEnsembles(s))
	require.Error(t, m.provideEnsembles(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[EnsembleConnector](s)
	require.NoError(t, err)
	require.Error(t, c.BindWork(nil))
	require.Error(t, c.BindExecution(nil))
	work := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }
	execution := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (EnsembleExecution, error) {
		return EnsembleExecution{}, nil
	}
	require.NoError(t, c.BindWork(work))
	require.NoError(t, c.BindExecution(execution))
	require.Error(t, c.BindWork(work))
	require.Error(t, c.BindExecution(execution))
}
