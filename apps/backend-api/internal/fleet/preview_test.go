package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestPreviewConfigRequiresIndependentRegistrableDomain(t *testing.T) {
	require.NoError(t, (PreviewConfig{}).Validate())
	require.ErrorContains(t, (PreviewConfig{TrustedProxies: []string{"127.0.0.1/32"}}).Validate(), "unsupported")
	c := PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.com"}
	require.NoError(t, c.Validate())
	for _, console := range []string{"https://console.example.net", "http://console.example.com", "https://console.example.com/path", "https://user@console.example.com", "https://console.example.com?redirect=1"} {
		c.ConsoleOrigin = console
		require.Error(t, c.Validate(), console)
	}
	c.ConsoleOrigin = "https://console.example.com"
	for _, domain := range []string{"127.0.0.1", "-bad.example.net", "bad-.example.net", "bad..example.net", "example.net:443", "https://example.net"} {
		c.Domain = domain
		require.Error(t, c.Validate(), domain)
	}
	c.Domain = "preview.example.net"
	c.Mode = "PATH"
	label := "abcdefghijklmnopqrstuvwxyz"
	require.Equal(t, "https://preview.example.net/p/"+label+"/", c.URL(label, "/"))
	require.Empty(t, c.URL("invalid", "/"))
}

func TestPreviewConfigurationBoundsAndRuntimeCapabilityBindings(t *testing.T) {
	base := PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.org"}
	for _, mutate := range []func(*PreviewConfig){func(c *PreviewConfig) { c.Domain = strings.Repeat("a", 254) }, func(c *PreviewConfig) { c.Domain = "com" }, func(c *PreviewConfig) { c.Mode = "INVALID" }, func(c *PreviewConfig) { c.MaxSessionMinutes = 1441 }, func(c *PreviewConfig) { c.TLS.CertFile = "missing" }, func(c *PreviewConfig) { c.ConsoleOrigin = "https://localhost" }} {
		c := base
		mutate(&c)
		require.Error(t, c.Validate())
	}
	require.Equal(t, "https://abcdefghijklmnopqrstuvwxyz.preview.example.net/", base.URL("abcdefghijklmnopqrstuvwxyz", ""))
	require.Empty(t, base.URL("abcdefghijklmnopqrstuvwxy1", "/"))
	m, db, ctx, _ := machineHarness(t)
	m.config.Preview = base
	s := module.NewServices()
	require.NoError(t, m.providePreview(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[PreviewAccessResolverConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(PreviewAccessResolver{}), ErrInvalidArgument)
	runtime, e := module.Resolve[*PreviewRuntime](s)
	require.NoError(t, e)
	require.Equal(t, base, runtime.Config())
	require.False(t, runtime.Available(7, 1))
	_, e = runtime.Ports(ctx, PreviewGrant{})
	require.ErrorIs(t, e, ErrUnavailable)
	require.NoError(t, runtime.ResetSession(ctx, 1, "cleanup"))
	resolver := PreviewAccessResolver{Authorize: func(context.Context, string, string, string) (PreviewGrant, error) {
		return PreviewGrant{}, ErrMachineForbidden
	}, ExchangeCode: func(context.Context, string, string) (string, string, time.Time, error) {
		return "", "", time.Time{}, ErrMachineForbidden
	}, Touch: func(context.Context, guid.ID) error { return nil }, Revalidate: func(context.Context, PreviewGrant) error { return ErrMachineForbidden }}
	require.NoError(t, connector.Bind(resolver))
	require.ErrorIs(t, connector.Bind(resolver), ErrInvalidArgument)
	require.NoError(t, m.initializePreview(ctx))
	require.False(t, runtime.Available(7, 1))
	require.NoError(t, runtime.ResetSession(ctx, 1, "cleanup"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, e := runtime.MachineOn(ctx, tx, 7, 1)
		require.NoError(t, e)
		require.False(t, facts.Enabled)
		require.Equal(t, "OFFLINE", facts.State)
		return nil
	}))
}
