package notification

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmailConfiguration(t *testing.T) {
	base := EmailConfig{Enabled: true, SMTPHost: "localhost", FromAddress: "sender@example.test", MessageIDDomain: "example.test"}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m := &Module{config: Config{Email: base, EncryptionKey: key}}
	require.NoError(t, m.configureEmail())
	require.Equal(t, 587, m.config.Email.SMTPPort)
	require.Equal(t, "starttls", m.config.Email.TLSMode)
	require.Equal(t, 20, m.config.Email.PerAddressHourly)
	require.Equal(t, 500, m.config.Email.PerTenantHourly)
	require.Equal(t, "en-US", m.config.DefaultLocale)
	for _, change := range []func(*EmailConfig){func(c *EmailConfig) { c.SMTPHost = "" }, func(c *EmailConfig) { c.SMTPPort = 65536 }, func(c *EmailConfig) { c.Timeout = -time.Second }, func(c *EmailConfig) { c.PerAddressHourly = -1 }, func(c *EmailConfig) { c.PerTenantHourly = -1 }, func(c *EmailConfig) { c.TLSMode = "none" }, func(c *EmailConfig) { c.TLSMode = "unknown" }, func(c *EmailConfig) { c.FromAddress = "invalid" }, func(c *EmailConfig) { c.MessageIDDomain = "bad/domain" }, func(c *EmailConfig) { c.FromName = "bad\nheader" }} {
		c := base
		change(&c)
		m := &Module{config: Config{Email: c, EncryptionKey: key}}
		require.Error(t, m.configureEmail())
	}
	m = &Module{config: Config{Email: base, EncryptionKey: "bad"}}
	require.Error(t, m.configureEmail())
	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte(key), 0600))
	passwordFile := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(passwordFile, []byte("secret"), 0600))
	base.PasswordFile = passwordFile
	m = &Module{config: Config{Email: base, EncryptionKeyFile: keyFile}}
	require.NoError(t, m.configureEmail())
	require.Equal(t, "secret", m.config.Email.Password)
	for _, domain := range []string{"", ".example", "example.", "bad domain", strings.Repeat("a", 254)} {
		require.False(t, validDomain(domain))
	}
}
