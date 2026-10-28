package authn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configx "7link.com/platform/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistrationDefaultsOpenAndPreservesExplicitDisabledConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, file string
		args       []string
		enabled    bool
	}{
		{name: "default", enabled: true},
		{name: "flag", args: []string{"--registration.enabled=false"}},
		{name: "file", file: "registration:\n  enabled: false\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cfg Config
			flags := configx.New()
			flags.UseConfigFileArgDefault()
			args := test.args
			if test.file != "" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte(test.file), 0600))
				args = append(args, "--config", path)
			}
			if len(args) == 0 {
				args = []string{"--enabled=false"}
			}
			require.NoError(t, flags.Parse(&cfg, args...))
			assert.Equal(t, test.enabled, cfg.Registration.Enabled)
		})
	}
	t.Run("environment", func(t *testing.T) {
		t.Setenv("TEST_REGISTRATION_ENABLED", "false")
		flags := configx.New()
		flags.UseEnvPrefix("TEST")
		flags.UseEnvKeyReplacer(strings.NewReplacer(".", "_"))
		var cfg Config
		require.NoError(t, flags.Parse(&cfg, "--enabled=false"))
		assert.False(t, cfg.Registration.Enabled)
	})
}
