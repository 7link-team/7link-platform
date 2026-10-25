package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotificationConfigurationMapping(t *testing.T) {
	var c AppConfig
	c.Authn.Enabled = true
	c.Deployment.Name = "Example deployment"
	c.Notification.EncryptionKey = "secret"
	c.Notification.Email = NotificationEmailConfig{Enabled: true, SMTPHost: "mail.example.test", SMTPPort: 465, TLSMode: "implicit", FromAddress: "sender@example.test", MessageIDDomain: "example.test", Timeout: 3 * time.Second, PerAddressHourly: 20, PerTenantHourly: 500}
	actual := notificationConfig(c)
	require.True(t, actual.Enabled)
	require.Equal(t, c.Deployment.Name, actual.Email.FromName)
	require.Equal(t, c.Notification.EncryptionKey, actual.EncryptionKey)
	require.Equal(t, 465, actual.Email.SMTPPort)
	require.Equal(t, 3*time.Second, actual.Email.Timeout)
	c.Notification.Email.FromName = "Configured sender"
	require.Equal(t, "Configured sender", notificationConfig(c).Email.FromName)
	c.Authn.Enabled = false
	require.False(t, notificationConfig(c).Enabled)
}
