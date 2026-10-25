package notification

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestSMTPConsecutiveFailuresUseRealAttemptsAndResetOnSuccess(t *testing.T) {
	c, _ := smtpFixture(t, 451, 451, 451, 451, 451, 451, 451, 451, 451, 451, 250, 550)
	m := &Module{config: Config{Email: c}}
	p := emailPayload{To: []string{"reader@example.test"}, Subject: "Test", Text: "Test"}
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	value := func() float64 {
		t.Helper()
		metrics, err := registry.Gather()
		require.NoError(t, err)
		require.Len(t, metrics, 1)
		require.Equal(t, "notification_smtp_consecutive_failures", metrics[0].GetName())
		require.Len(t, metrics[0].Metric, 1)
		return metrics[0].Metric[0].GetGauge().GetValue()
	}
	require.Zero(t, value())
	for i := 1; i <= 10; i++ {
		require.Error(t, m.sendSMTP(t.Context(), p, "<attempt@example.test>"))
		require.InDelta(t, float64(i), value(), 0)
	}
	require.NoError(t, m.sendSMTP(t.Context(), p, "<success@example.test>"))
	require.Zero(t, value())
	require.Error(t, m.sendSMTP(t.Context(), p, "<retry@example.test>"))
	require.InDelta(t, float64(1), value(), 0)
}
