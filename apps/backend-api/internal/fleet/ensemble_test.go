package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func ensembleHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *fleetPublisher, map[guid.ID]EnsembleExecution) {
	m, db, ctx, _, p := enrollmentHarness(t)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}

	facts := map[guid.ID]EnsembleExecution{11: {ID: 11, WorkItemID: 20, PrincipalID: 30, State: "SUCCEEDED", ResultState: "READY"}, 12: {ID: 12, WorkItemID: 20, PrincipalID: 31, State: "FAILED"}, 13: {ID: 13, WorkItemID: 20, PrincipalID: 32, State: "SUCCEEDED", ResultState: "READY"}}
	m.ensemble = EnsembleSources{AuthorizeOn: func(ctx context.Context, _ *gorm.DB, tenant, project, work guid.ID, _ string) error {
		if tenant != 7 || project != 9 || work != 20 || authn.PrincipalIDFromContext(ctx) != 101 {
			return ErrMachineForbidden
		}
		return nil
	}, ExecutionOn: func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) (EnsembleExecution, error) {
		f, ok := facts[id]
		if !ok || tenant != 7 || project != 9 {
			return f, ErrMachineNotFound
		}
		return f, nil
	}}
	return m, db, ctx, p, facts
}
func TestEnsembleLifecycleUsesRealFactsAndArchivesAlternatives(t *testing.T) {
	for _, action := range []string{"SELECT", "SYNTHESIZE", "ABANDON"} {
		t.Run(action, func(t *testing.T) {
			m, db, ctx, _, _ := ensembleHarness(t)
			var v EnsembleView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "compare actual alternatives"})
				return e
			}))
			require.Len(t, v.Branches, 2)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, EnsembleDecision{Action: "COMPARE", Version: 1, Reason: "freeze actual owner results"})
				return e
			}))
			require.Equal(t, "COMPARING", v.State)
			require.Len(t, v.Comparison, 2)
			in := EnsembleDecision{Action: action, Version: 2, BranchID: v.Branches[0].ID, SynthesisExecutionID: 13, Reason: "human reviewed actual alternatives"}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, in); return e }))
			require.Equal(t, 3, v.Version)
			if action == "ABANDON" {
				require.Equal(t, "ABANDONED", v.State)
				require.NotNil(t, v.Branches[0].ArchivedAt)
			} else {
				require.NotNil(t, v.SelectedBranchID)
				require.Nil(t, v.Branches[0].ArchivedAt)
				require.NotNil(t, v.Branches[1].ArchivedAt)
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, in); return e }), ErrVersionConflict)
		})
	}
}
func TestEnsembleInvalidFactsAuthorityAndRollback(t *testing.T) {
	m, db, ctx, p, facts := ensembleHarness(t)
	create := func(in EnsembleCreate) (EnsembleView, error) {
		var v EnsembleView
		e := db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.CreateEnsembleOn(ctx, tx, 7, 9, in); return e })
		return v, e
	}
	for _, in := range []EnsembleCreate{{20, []guid.ID{11, 11}, "duplicate execution denied"}, {20, []guid.ID{11}, "too few executions"}, {20, []guid.ID{11, 99}, "unknown execution denied"}} {
		_, e := create(in)
		require.Error(t, e)
	}
	facts[12] = EnsembleExecution{ID: 12, WorkItemID: 99, State: "SUCCEEDED"}
	_, e := create(EnsembleCreate{20, []guid.ID{11, 12}, "foreign work denied"})
	require.ErrorIs(t, e, ErrInvalidArgument)
	facts[12] = EnsembleExecution{ID: 12, WorkItemID: 20, State: "RUNNING"}
	p.fail = true
	_, e = create(EnsembleCreate{20, []guid.ID{11, 12}, "rollback all effects"})
	require.Error(t, e)
	q := query.BindEnsemblePlans(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	var effects int64
	require.NoError(t, db.Table("test_fleet_effects").Count(&effects).Error)
	require.Zero(t, effects)
	p.fail = false
	v, e := create(EnsembleCreate{20, []guid.ID{11, 12}, "actual running branch"})
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, EnsembleDecision{Action: "COMPARE", Version: 1, Reason: "running results not ready"})
		return e
	}), ErrVersionConflict)
	facts[12] = EnsembleExecution{ID: 12, WorkItemID: 20, State: "FAILED"}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, EnsembleDecision{Action: "SELECT", Version: 1, BranchID: v.Branches[1].ID, Reason: "cannot select failed execution"})
		return e
	}))
	m.ensemble.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrMachineForbidden }
	_, e = create(EnsembleCreate{20, []guid.ID{11, 12}, "revoked human denied"})
	require.ErrorIs(t, e, ErrMachineForbidden)
	require.NoError(t, db.Table(m.names.EnsemblePlans.String()).Where("id = ?", v.ID).First(&model.EnsemblePlan{}).Error)
}

func TestEnsembleReasonUsesUTF8CharacterBoundsAcrossCommands(t *testing.T) {
	for _, command := range []string{"create", "prepare", "decision"} {
		for _, tc := range []struct {
			name   string
			reason string
			valid  bool
		}{
			{name: "minimum", reason: strings.Repeat("界", 10), valid: true},
			{name: "maximum", reason: strings.Repeat("界", 2000), valid: true},
			{name: "too_short", reason: strings.Repeat("界", 9)},
			{name: "too_long", reason: strings.Repeat("界", 2001)},
			{name: "invalid_utf8", reason: strings.Repeat("a", 10) + string([]byte{0xff})},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				m, db, ctx, _, _ := ensembleHarness(t)
				var plan EnsembleView
				if command == "decision" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						plan, err = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Compare actual alternatives"})
						return err
					}))
				}
				if command == "prepare" {
					m.ensemble.Runtime.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error { return nil }
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					var err error
					switch command {
					case "create":
						_, err = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, tc.reason})
					case "prepare":
						_, err = m.PrepareEnsembleOn(ctx, tx, 7, 9, EnsemblePrepare{WorkItemID: 20, Reason: tc.reason, Branches: []EnsembleLaunchBranch{
							{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{"runtime":"CODEX"}`)},
							{PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{"runtime":"CODEX"}`)},
						}})
					case "decision":
						_, err = m.ChangeEnsembleOn(ctx, tx, 7, 9, plan.ID, EnsembleDecision{Action: "COMPARE", Version: plan.Version, Reason: tc.reason})
					}
					return err
				})
				if tc.valid {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrInvalidArgument)
					var count int64
					require.NoError(t, db.Table(m.names.EnsemblePlans.String()).Count(&count).Error)
					if command == "decision" {
						require.Equal(t, int64(1), count)
						var stored model.EnsemblePlan
						require.NoError(t, db.Table(m.names.EnsemblePlans.String()).Where("id=?", plan.ID).First(&stored).Error)
						require.Equal(t, plan.Version, stored.Version)
						require.Equal(t, plan.State, stored.State)
					} else {
						require.Zero(t, count, "invalid text cannot leave a partial plan")
					}
				}
			})
		}
	}
}

func TestEnsembleHistoricalAlternativesEveryStorageFailurePreservesComparison(t *testing.T) {
	for _, command := range []string{"create", "compare", "select", "synthesize", "abandon", "request", "approve"} {
		for _, kind := range []string{"query", "create", "update"} {
			total := 0
			run := func(t *testing.T, at int) {
				m, db, ctx, _, _ := ensembleHarness(t)
				var view EnsembleView
				if command != "create" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						view, e = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Review actual execution alternatives"})
						return e
					}))
				}
				if command == "select" || command == "synthesize" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						view, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, view.ID, EnsembleDecision{Action: "COMPARE", Version: view.Version, Reason: "Compare actual persisted alternatives"})
						return e
					}))
				}
				if command == "approve" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var e error
						view, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, view.ID, EnsembleDecision{Action: "REQUEST_APPROVAL", Version: view.Version, Reason: "Request current Human approval"})
						return e
					}))
				}
				pq := query.BindEnsemblePlans(db, m.names)
				bq := query.BindEnsembleBranches(db, m.names)
				before, e := pq.WithContext(ctx).Find()
				require.NoError(t, e)
				branches, e := bq.WithContext(ctx).Order(bq.ID).Find()
				require.NoError(t, e)
				count := 0
				injected := errors.New("ensemble alternatives storage unavailable")
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table != m.names.EnsemblePlans.String() && tx.Statement.Table != m.names.EnsembleBranches.String() {
						return
					}
					count++
					if at > 0 && count == at {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("ensemble_alternatives_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("ensemble_alternatives_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("ensemble_alternatives_failure", hook))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					if command == "create" {
						_, e := m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Review actual execution alternatives"})
						return e
					}
					action := strings.ToUpper(command)
					if command == "request" {
						action = "REQUEST_APPROVAL"
					}
					decision := EnsembleDecision{Action: action, Version: view.Version, Reason: "Current Human reviews actual alternatives"}
					if command == "select" {
						decision.BranchID = view.Branches[0].ID
					}
					if command == "synthesize" {
						decision.BranchID = view.Branches[0].ID
						decision.SynthesisExecutionID = 13
					}
					_, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, view.ID, decision)
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("ensemble_alternatives_failure"))
				require.NoError(t, db.Callback().Create().Remove("ensemble_alternatives_failure"))
				require.NoError(t, db.Callback().Update().Remove("ensemble_alternatives_failure"))
				if at == 0 {
					require.NoError(t, err)
					total = count
					return
				}
				require.ErrorIs(t, err, injected)
				after, e := pq.WithContext(ctx).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				afterBranches, e := bq.WithContext(ctx).Order(bq.ID).Find()
				require.NoError(t, e)
				require.Equal(t, branches, afterBranches)
			}
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { run(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, kind, at), func(t *testing.T) { run(t, at) })
			}
		}
	}
}
