package fleet

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleRuntimeHTTPReplayReauthorizesBeforeEffects(t *testing.T) {
	m, db, ctx, _ := ensembleRegressionTestEnsembleHarness(t)
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idempotency = store
	require.NoError(t, db.Exec("CREATE TABLE launch_receipt_effects(kind TEXT NOT NULL)").Error)
	allowed := true
	locked := false
	original := m.ensemble.AuthorizeOn
	m.ensemble.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, permission string) error {
		require.True(t, locked, "all branch principals must precede project/work authority, including replay")
		return original(ctx, tx, tenant, project, work, permission)
	}
	write := func(kind string) func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
		return func(_ context.Context, tx *gorm.DB, _, _, _, _ guid.ID, _ []EnsembleLaunchBranch) error {
			return tx.Exec("INSERT INTO launch_receipt_effects VALUES (?)", kind).Error
		}
	}
	require.NoError(t, m.bindEnsembleRuntime(EnsembleRuntime{
		LockOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch) error {
			locked = true
			return nil
		},
		AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, []EnsembleLaunchBranch, bool) error {
			if !allowed {
				return ErrMachineForbidden
			}
			return nil
		},
		AssignOn: write("assign"), LaunchOn: write("launch"), CancelOn: write("cancel"),
	}))
	prepare := &ensemblePrepareWire{EnsembleWireProject: EnsembleWireProject{EntityID: "99"}, IdempotencyKey: "prepare-runtime", Body: EnsemblePrepare{WorkItemID: 77, Reason: "Approve immutable runtime alternatives", Branches: []EnsembleLaunchBranch{{PrincipalID: 30, MachineID: 40, Plan: json.RawMessage(`{"runtime":"CODEX"}`)}, {PrincipalID: 31, MachineID: 41, Plan: json.RawMessage(`{"runtime":"CODEX"}`)}}}}
	prepared, err := m.prepareEnsembleWireHTTP(ctx, prepare)
	require.NoError(t, err)
	v := prepared.Body.Data
	for _, action := range []string{"REQUEST_APPROVAL", "APPROVE"} {
		locked = false
		decision, err := m.changeEnsembleHTTP(ctx, &ensembleChangeInput{EnsembleRefInput: EnsembleRefInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: 99}, PlanID: v.ID}, IdempotencyKey: action, Body: EnsembleDecision{Action: action, Version: v.Version, Reason: "Review actual approved branch snapshot"}})
		require.NoError(t, err)
		v = decision.Body.Data
	}
	launch := &ensembleLaunchWire{EnsembleWireRef: EnsembleWireRef{EnsembleWireProject: prepare.EnsembleWireProject, PlanID: v.ID.String()}, IdempotencyKey: "launch-runtime"}
	launch.Body.Version = v.Version
	locked = false
	first, err := m.launchEnsembleWireHTTP(ctx, launch)
	require.NoError(t, err)
	locked = false
	replay, err := m.launchEnsembleWireHTTP(ctx, launch)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	allowed, locked = false, false
	_, err = m.launchEnsembleWireHTTP(ctx, launch)
	require.Error(t, err, "revoked runtime authority invalidates receipt replay")
	allowed = true
	cancel := *launch
	cancel.IdempotencyKey, cancel.Body.Version = "cancel-runtime", first.Body.Data.Version
	locked = false
	_, err = m.cancelEnsembleWireHTTP(ctx, &cancel)
	require.NoError(t, err)
	locked = false
	_, err = m.cancelEnsembleWireHTTP(ctx, &cancel)
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Table("launch_receipt_effects").Count(&count).Error)
	require.Equal(t, int64(3), count, "assign, launch and cancel each run once")
	// The launch source cannot advertise a selected Claim without an owner
	// capability that binds the actual pending candidate and its evidence.
	locked = false
	compared, err := m.changeEnsembleHTTP(ctx, &ensembleChangeInput{EnsembleRefInput: EnsembleRefInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: 99}, PlanID: v.ID}, IdempotencyKey: "compare-runtime", Body: EnsembleDecision{Action: "COMPARE", Version: first.Body.Data.Version + 1, Reason: "Compare actual finished runtime alternatives"}})
	require.NoError(t, err)
	locked = false
	_, err = m.changeEnsembleHTTP(ctx, &ensembleChangeInput{EnsembleRefInput: EnsembleRefInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: 99}, PlanID: v.ID}, IdempotencyKey: "select-runtime", Body: EnsembleDecision{Action: "SELECT", Version: compared.Body.Data.Version, BranchID: compared.Body.Data.Branches[0].ID, Reason: "A selected branch must submit its real Claim"}})
	require.Error(t, err)
	var status interface{ GetStatus() int }
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
	q := query.BindEnsemblePlans(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "COMPARING", stored.State)
	require.Nil(t, stored.SelectedBranchID)
}
