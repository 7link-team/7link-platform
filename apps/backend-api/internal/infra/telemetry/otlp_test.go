package telemetry

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOTLPModuleSupportsConfiguredTransports(t *testing.T) {
	for _, protocol := range []string{OTLPProtocolGRPC, OTLPProtocolHTTP} {
		t.Run(protocol, func(t *testing.T) {
			module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), WithConfig(Config{OTLP: OTLPConfig{
				Enabled:  true,
				Protocol: protocol,
				Endpoint: "127.0.0.1:4317",
				Insecure: true,
				Headers:  map[string]string{"authorization": "secret"},
				Timeout:  time.Millisecond,
			}}))
			require.NoError(t, module.Initialize(t.Context()))
			require.NoError(t, module.Deinitialize(context.Background()))
		})
	}
}

func TestNewOTLPModuleRejectsUnknownProtocol(t *testing.T) {
	module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), WithConfig(Config{OTLP: OTLPConfig{Enabled: true, Protocol: "zipkin"}}))
	err := module.Initialize(t.Context())
	assert.EqualError(t, err, `otel: unsupported OTLP protocol "zipkin"`)
	require.NoError(t, module.Deinitialize(t.Context()))
}

func TestOTLPValidationRunsOnlyWhenExporterIsEnabled(t *testing.T) {
	disabled := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), WithConfig(Config{OTLP: OTLPConfig{Protocol: "invalid"}}))
	require.NoError(t, disabled.Initialize(t.Context()))
	require.NoError(t, disabled.Deinitialize(t.Context()))

	enabled := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), WithConfig(Config{OTLP: OTLPConfig{
		Enabled: true, Protocol: OTLPProtocolGRPC, Timeout: -time.Second,
	}}))
	assert.EqualError(t, enabled.Initialize(t.Context()), "otel: OTLP timeout cannot be negative: -1s")
	require.NoError(t, enabled.Deinitialize(t.Context()))
}
