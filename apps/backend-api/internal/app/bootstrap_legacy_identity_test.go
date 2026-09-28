package app

import (
	"context"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

type legacyStartupProbe struct{ initialized bool }

func (*legacyStartupProbe) ModuleName() string { return "legacy-startup-probe" }
func (probe *legacyStartupProbe) Initialize(context.Context) error {
	probe.initialized = true
	return nil
}

func TestAuthenticationStartupRequiresDisabledFederationQuarantine(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "retained"}[retained], func(t *testing.T) {
			instance := &App{config: AppConfig{
				TablePrefix: "sl_",
				Database:    map[string]Database{"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "startup.sqlite"), Writable: true}},
			}, modules: module.NewRegistry()}
			instance.config.Authn.Enabled = true
			require.NoError(t, instance.initializeRuntime(t.Context()))
			t.Cleanup(func() { _ = instance.runtime.Close() })
			probe := &legacyStartupProbe{}
			require.NoError(t, instance.modules.Register(probe))
			require.NoError(t, instance.prepareModules())
			db := instance.runtime.Database().Write()
			if retained {
				require.NoError(t, db.Exec("CREATE TABLE sl_iam_identity_links (principal_id BIGINT, binding_provenance TEXT, quarantined_at DATETIME, recovery_evidence TEXT)").Error)
				require.NoError(t, db.Exec("INSERT INTO sl_iam_identity_links VALUES (11, 'unknown', NULL, '')").Error)
				require.ErrorIs(t, instance.lifecycle(t.Context()), federation.ErrLegacyQuarantineRequired)
				require.False(t, probe.initialized, "gate must run before authentication starts")
				require.NoError(t, db.Exec("UPDATE sl_iam_identity_links SET quarantined_at = CURRENT_TIMESTAMP").Error)
				require.ErrorIs(t, instance.lifecycle(t.Context()), federation.ErrLegacyQuarantineRequired, "unknown mailbox ownership cannot become recoverable by disabling federation")
				require.False(t, probe.initialized)
				require.NoError(t, db.Exec("UPDATE sl_iam_identity_links SET recovery_evidence = 'independent ownership review'").Error)
			}
			require.NoError(t, instance.lifecycle(t.Context()))
			require.True(t, probe.initialized)
		})
	}
}
