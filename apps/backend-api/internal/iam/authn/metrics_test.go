package authn

import (
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gatherNotificationMetrics(t *testing.T, m *Module) map[string]int64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	values := map[string]int64{}
	for _, family := range families {
		values[family.GetName()] = int64(family.Metric[0].GetCounter().GetValue())
	}
	return values
}

func TestNotificationMetricsWithoutService(t *testing.T) {
	assert.Equal(t, map[string]int64{
		"iam_authn_notification_poll_failures_total":      0,
		"iam_authn_notification_retryable_failures_total": 0,
		"iam_authn_notification_terminal_failures_total":  0,
	}, gatherNotificationMetrics(t, &Module{}))
}

func TestNotificationMetricsCountRetryableAndTerminalFailures(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	service.cfg.Notification.MaxAttempts = 2
	recorder.status = http.StatusServiceUnavailable
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
	instance := &Module{Service: service}

	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	values := gatherNotificationMetrics(t, instance)
	assert.EqualValues(t, 1, values["iam_authn_notification_retryable_failures_total"])
	assert.Zero(t, values["iam_authn_notification_terminal_failures_total"])

	service.now = func() time.Time { return repositoryTestNow.Add(3 * time.Second) }
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	values = gatherNotificationMetrics(t, instance)
	assert.EqualValues(t, 1, values["iam_authn_notification_retryable_failures_total"])
	assert.EqualValues(t, 1, values["iam_authn_notification_terminal_failures_total"], "exhausted attempts reach the terminal state")

	// A terminal row is never polled again, so it is counted exactly once.
	service.now = func() time.Time { return repositoryTestNow.Add(time.Hour) }
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.EqualValues(t, 1, gatherNotificationMetrics(t, instance)["iam_authn_notification_terminal_failures_total"])
	assert.Zero(t, gatherNotificationMetrics(t, instance)["iam_authn_notification_poll_failures_total"])
}

func TestNotificationMetricsCountPollFailures(t *testing.T) {
	service, harness, _ := newRecoveryService(t)
	sqlDB, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	service.deliverPendingNotificationsAndReport(t.Context())
	assert.EqualValues(t, 1, gatherNotificationMetrics(t, &Module{Service: service})["iam_authn_notification_poll_failures_total"])
}
