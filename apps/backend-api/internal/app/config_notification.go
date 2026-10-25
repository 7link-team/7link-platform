package app

import (
	"7link.com/platform/internal/notification"
	"7link.com/platform/pkg/module"
	"time"
)

type NotificationConfig struct {
	DefaultLocale          string                  `mapstructure:"default_locale" default:"en-US"`
	EncryptionKey          string                  `mapstructure:"encryption_key" secret:"true"`
	EncryptionKeyFile      string                  `mapstructure:"encryption_key_file"`
	PreviousEncryptionKeys []string                `mapstructure:"previous_encryption_keys" secret:"true"`
	Email                  NotificationEmailConfig `mapstructure:"email" group:"email"`
}
type NotificationEmailConfig struct {
	Enabled          bool          `mapstructure:"enabled" default:"false"`
	SMTPHost         string        `mapstructure:"smtp_host"`
	SMTPPort         int           `mapstructure:"smtp_port" default:"587"`
	TLSMode          string        `mapstructure:"tls_mode" default:"starttls"`
	Username         string        `mapstructure:"username"`
	Password         string        `mapstructure:"password" secret:"true"`
	PasswordFile     string        `mapstructure:"password_file"`
	FromAddress      string        `mapstructure:"from_address"`
	FromName         string        `mapstructure:"from_name"`
	MessageIDDomain  string        `mapstructure:"message_id_domain"`
	Timeout          time.Duration `mapstructure:"timeout" default:"10s"`
	PerAddressHourly int           `mapstructure:"per_address_hourly" default:"20"`
	PerTenantHourly  int           `mapstructure:"per_tenant_hourly" default:"500"`
	AllowPlaintext   bool          `mapstructure:"allow_plaintext" default:"false" description:"development-only permission for SMTP tls_mode none"`
}

func notificationConfig(c AppConfig) notification.Config {
	e := c.Notification.Email
	name := e.FromName
	if name == "" {
		name = c.Deployment.Name
	}
	return notification.Config{Config: module.Config{Enabled: c.Authn.Enabled}, DefaultLocale: c.Notification.DefaultLocale, EncryptionKey: c.Notification.EncryptionKey, EncryptionKeyFile: c.Notification.EncryptionKeyFile, PreviousEncryptionKeys: c.Notification.PreviousEncryptionKeys, Email: notification.EmailConfig{Enabled: e.Enabled, SMTPHost: e.SMTPHost, SMTPPort: e.SMTPPort, TLSMode: e.TLSMode, Username: e.Username, Password: e.Password, PasswordFile: e.PasswordFile, FromAddress: e.FromAddress, FromName: name, MessageIDDomain: e.MessageIDDomain, Timeout: e.Timeout, PerAddressHourly: e.PerAddressHourly, PerTenantHourly: e.PerTenantHourly, AllowPlaintext: e.AllowPlaintext}}
}
