package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleLaunchStorageFailuresPreserveApprovedSnapshotAndBranchEffects(t *testing.T) {
	for _, command := range []string{"prepare", "launch", "cancel"} {
		for _, operation := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				m, db, ctx, _, _ := ensembleHarness(t)
				require.NoError(t, db.Exec("CREATE TABLE ensemble_runtime_effects(kind TEXT NOT NULL)").Error)
				write := func(kind string) func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
					return func(_ context.Context, tx *gorm.DB, _, _, _, _ guid.ID, _ []EnsembleLaunchBranch) error {
						return tx.Exec("INSERT INTO ensemble_runtime_effects VALUES (?)", kind).Error
					}
				}
				require.NoError(t, m.bindEnsembleRuntime(EnsembleRuntime{LockOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error { return nil }, AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch, bool) error {
					return nil
				}, AssignOn: write("assigned"), LaunchOn: write("launch_requested"), CancelOn: write("cancel_requested")}))
				input := EnsemblePrepare{WorkItemID: 20, Reason: "Approve exact proposed configurations", Branches: []EnsembleLaunchBranch{{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{"runtime":"CODEX"}`)}, {PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{"runtime":"CODEX"}`)}}}
				var plan EnsembleView
				if command != "prepare" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; plan, e = m.PrepareEnsembleOn(ctx, tx, 7, 9, input); return e }))
					for _, action := range []string{"REQUEST_APPROVAL", "APPROVE"} {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							var e error
							plan, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, plan.ID, EnsembleDecision{Action: action, Version: plan.Version, Reason: "Human checks exact branch snapshot"})
							return e
						}))
					}
					if command == "cancel" {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							var e error
							plan, e = m.launchEnsembleOn(ctx, tx, 7, 9, plan.ID, plan.Version, false)
							return e
						}))
					}
				}
				pq := query.BindEnsemblePlans(db, m.names)
				bq := query.BindEnsembleBranches(db, m.names)
				before, e := pq.WithContext(ctx).Order(pq.ID).Find()
				require.NoError(t, e)
				branches, e := bq.WithContext(ctx).Order(bq.ID).Find()
				require.NoError(t, e)
				var effectsBefore int64
				require.NoError(t, db.Table("ensemble_runtime_effects").Count(&effectsBefore).Error)
				count := 0
				injected := errors.New("ensemble storage unavailable")
				hook := func(tx *gorm.DB) {
					if !strings.Contains(tx.Statement.Table, "fleet_ensemble") {
						return
					}
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("ensemble_storage_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("ensemble_storage_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("ensemble_storage_failure", hook))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					if command == "prepare" {
						_, e := m.PrepareEnsembleOn(ctx, tx, 7, 9, input)
						return e
					}
					_, e := m.launchEnsembleOn(ctx, tx, 7, 9, plan.ID, plan.Version, command == "cancel")
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("ensemble_storage_failure"))
				require.NoError(t, db.Callback().Create().Remove("ensemble_storage_failure"))
				require.NoError(t, db.Callback().Update().Remove("ensemble_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := pq.WithContext(ctx).Order(pq.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				afterBranches, e := bq.WithContext(ctx).Order(bq.ID).Find()
				require.NoError(t, e)
				require.Equal(t, branches, afterBranches)
				var effects int64
				require.NoError(t, db.Table("ensemble_runtime_effects").Count(&effects).Error)
				require.Equal(t, effectsBefore, effects)
				return count
			}
			count := exercise(t, 0)
			for point := 1; point <= count; point++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, operation, point), func(t *testing.T) { exercise(t, point) })
			}
		}
	}
}

func TestEnsembleLaunchRequiresExactApprovalAndRollsBackEveryBranch(t *testing.T) {
	m, db, ctx, _, _ := ensembleHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE heavy_effects(execution_id BIGINT NOT NULL, kind TEXT NOT NULL)").Error)
	fail := false
	lock := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error { return nil }
	authority := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch, bool) error {
		return nil
	}
	write := func(kind string) func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
		return func(_ context.Context, tx *gorm.DB, _, _, _, _ guid.ID, b []EnsembleLaunchBranch) error {
			for i, r := range b {
				if e := tx.Exec("INSERT INTO heavy_effects VALUES (?,?)", r.ExecutionID, kind).Error; e != nil {
					return e
				}
				if fail && kind == "launch" && i == 1 {
					return errors.New("second branch budget rejected")
				}
			}
			return nil
		}
	}
	require.NoError(t, m.bindEnsembleRuntime(EnsembleRuntime{LockOn: lock, AuthorizeOn: authority, AssignOn: write("assign"), LaunchOn: write("launch"), CancelOn: write("cancel")}))
	in := EnsemblePrepare{WorkItemID: 20, Reason: "Human approves frozen branch configurations", Branches: []EnsembleLaunchBranch{{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{"runtime":"CODEX"}`), AssignmentID: 1, ExecutionID: 2}, {PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{"runtime":"CODEX"}`)}}}
	var v EnsembleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.PrepareEnsembleOn(ctx, tx, 7, 9, in); return e }))
	require.Len(t, v.ProposedBranches, 2)
	require.Empty(t, v.Branches)
	require.NotEqual(t, guid.ID(1), v.ProposedBranches[0].AssignmentID)
	require.NotEqual(t, guid.ID(2), v.ProposedBranches[0].ExecutionID)
	launch := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, e := m.launchEnsembleOn(ctx, tx, 7, 9, v.ID, v.Version, false); return e })
	}
	require.ErrorIs(t, launch(), ErrVersionConflict)
	for _, action := range []string{"REQUEST_APPROVAL", "APPROVE"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			v, e = m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, EnsembleDecision{Action: action, Version: v.Version, Reason: "Human reviews immutable proposed branches"})
			return e
		}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.ChangeEnsembleOn(ctx, tx, 7, 9, v.ID, EnsembleDecision{Action: "COMPARE", Version: v.Version, Reason: "Unstarted branches cannot be compared"})
		return e
	}), ErrVersionConflict)
	fail = true
	require.Error(t, launch())
	var count int64
	require.NoError(t, db.Table("heavy_effects").Count(&count).Error)
	require.Zero(t, count)
	q := query.BindEnsembleBranches(db, m.names)
	count, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	fail = false
	require.NoError(t, launch())
	require.ErrorIs(t, launch(), ErrVersionConflict)
	require.NoError(t, db.Table("heavy_effects").Count(&count).Error)
	require.Equal(t, int64(4), count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		b := v.ProposedBranches[0]
		require.NoError(t, m.checkEnsembleBranchOn(ctx, tx, 7, 9, 20, v.ID, b.AssignmentID, b.ExecutionID))
		require.ErrorIs(t, m.checkEnsembleBranchOn(ctx, tx, 7, 9, 20, v.ID, b.AssignmentID, v.ProposedBranches[1].ExecutionID), ErrMachineForbidden)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.launchEnsembleOn(ctx, tx, 7, 9, v.ID, v.Version+1, true); return e }))
	require.NoError(t, db.Table("heavy_effects").Where("kind = ?", "cancel").Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func TestEnsemblePrepareEveryIdentifierAndCurrentSourceFailureLeavesNoProposal(t *testing.T) {
	for _, fault := range []string{"id_first", "id_second", "id_plan", "zero_first", "zero_second", "zero_plan", "missing_lock", "missing_ids", "lock", "authority", "invalid_work", "invalid_branch", "duplicate_principal", "invalid_plan"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := ensembleHarness(t)
			injected := errors.New("ensemble proposal source unavailable")
			m.ensemble.Runtime.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error { return nil }
			in := EnsemblePrepare{WorkItemID: 20, Reason: "Propose bounded actual branch configurations", Branches: []EnsembleLaunchBranch{{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{}`)}, {PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{}`)}}}
			switch fault {
			case "missing_lock":
				m.ensemble.Runtime.LockOn = nil
			case "missing_ids":
				m.ids = nil
			case "lock":
				m.ensemble.Runtime.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
					return injected
				}
			case "authority":
				m.ensemble.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return injected }
			case "invalid_work":
				in.WorkItemID = 0
			case "invalid_branch":
				in.Branches[0].MachineID = 0
			case "duplicate_principal":
				in.Branches[1].PrincipalID = in.Branches[0].PrincipalID
			case "invalid_plan":
				in.Branches[0].Plan = json.RawMessage(`{`)
			default:
				index := map[string]int{"id_first": 1, "id_second": 2, "id_plan": 5, "zero_first": 1, "zero_second": 2, "zero_plan": 5}[fault]
				calls := 0
				m.ids = gatewayTestIDs(func() (int64, error) {
					calls++
					if calls == index {
						if strings.HasPrefix(fault, "zero") {
							return 0, nil
						}
						return 0, injected
					}
					return int64(1000 + calls), nil
				})
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.PrepareEnsembleOn(ctx, tx, 7, 9, in); return e }))
			var proposals, branches int64
			require.NoError(t, db.Table(m.names.EnsemblePlans.String()).Count(&proposals).Error)
			require.NoError(t, db.Table(m.names.EnsembleBranches.String()).Count(&branches).Error)
			require.Zero(t, proposals)
			require.Zero(t, branches)
		})
	}
}

func TestEnsembleRuntimeRejectsEveryReplacementOwnerBeforePreparingProposal(t *testing.T) {
	for _, owner := range []string{"lock", "authorize", "assign", "launch", "cancel"} {
		t.Run(owner, func(t *testing.T) {
			m, db, ctx, _, _ := ensembleHarness(t)
			lock := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error { return nil }
			authority := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch, bool) error {
				return nil
			}
			command := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
				return nil
			}
			require.NoError(t, m.bindEnsembleRuntime(EnsembleRuntime{LockOn: lock, AuthorizeOn: authority, AssignOn: command, LaunchOn: command, CancelOn: command}))
			replacement := EnsembleRuntime{}
			switch owner {
			case "lock":
				replacement.LockOn = lock
			case "authorize":
				replacement.AuthorizeOn = authority
			case "assign":
				replacement.AssignOn = command
			case "launch":
				replacement.LaunchOn = command
			case "cancel":
				replacement.CancelOn = command
			}
			require.ErrorIs(t, m.bindEnsembleRuntime(replacement), ErrInvalidArgument)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				view, e := m.PrepareEnsembleOn(ctx, tx, 7, 9, EnsemblePrepare{WorkItemID: 20, Reason: "Bounded approved source ownership", Branches: []EnsembleLaunchBranch{{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{}`)}, {PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{}`)}}})
				require.NoError(t, e)
				require.Len(t, view.ProposedBranches, 2)
				require.Equal(t, "DRAFT", view.State)
				return e
			}))
		})
	}
}
