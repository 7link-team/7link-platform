package secrets

import (
	"7link.com/platform/pkg/module"
	"encoding/base64"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func testKey(c string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(c, 32)))
}
func TestModulePublishesCapabilityAndFailsClosedWithoutKeys(t *testing.T) {
	m := NewModule(Config{Config: module.Config{Enabled: true}})
	require.True(t, m.Enabled())
	require.Equal(t, Name, m.ModuleName())
	require.Error(t, m.Initialize(t.Context()))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	sealer, err := module.Resolve[Sealer](services)
	require.NoError(t, err)
	aad, err := AssociatedData(1, 2)
	require.NoError(t, err)
	_, err = sealer.Seal("test.secret", aad, []byte("secret"))
	require.ErrorIs(t, err, ErrUnavailable)
	m.config.ActiveKey = testKey("a")
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, sealer.RegisterPurpose("test.secret"))
	_, err = sealer.Seal("test.secret", aad, []byte("secret"))
	require.NoError(t, err)
	disabled := NewModule(Config{})
	require.False(t, disabled.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	for _, config := range []Config{{ActiveKey: "bad"}, {ActiveKey: testKey("a"), PreviousKeys: []string{"bad"}}, {ActiveKey: testKey("a"), ActiveKeyFile: "missing"}} {
		require.Error(t, NewModule(config).Initialize(t.Context()))
	}
}
