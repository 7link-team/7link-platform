package notification

import (
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

type EmailConfig struct {
	Enabled                                bool
	SMTPHost                               string
	SMTPPort                               int
	TLSMode                                string
	Username, Password, PasswordFile       string
	FromAddress, FromName, MessageIDDomain string
	Timeout                                time.Duration
	PerAddressHourly, PerTenantHourly      int
	AllowPlaintext                         bool
}

func (m *Module) configureEmail() error {
	c := &m.config.Email
	if c.SMTPPort == 0 {
		c.SMTPPort = 587
	}
	if c.TLSMode == "" {
		c.TLSMode = "starttls"
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.PerAddressHourly == 0 {
		c.PerAddressHourly = 20
	}
	if c.PerTenantHourly == 0 {
		c.PerTenantHourly = 500
	}
	if m.config.DefaultLocale == "" {
		m.config.DefaultLocale = "en-US"
	}
	if !c.Enabled {
		return nil
	}
	if c.SMTPHost == "" || c.SMTPPort < 1 || c.SMTPPort > 65535 || c.Timeout <= 0 || c.PerAddressHourly < 1 || c.PerTenantHourly < 1 {
		return ErrInvalid
	}
	if c.TLSMode != "starttls" && c.TLSMode != "implicit" && c.TLSMode != "none" || c.TLSMode == "none" && !c.AllowPlaintext {
		return ErrInvalid
	}
	address, err := mail.ParseAddress(c.FromAddress)
	if err != nil || address.Address != c.FromAddress {
		return ErrInvalid
	}
	if !validDomain(c.MessageIDDomain) || strings.ContainsAny(c.FromName, "\r\n") {
		return ErrInvalid
	}
	c.Password, err = secretx.Resolve("notification.email.password", c.Password, c.PasswordFile, secretx.DefaultMaxBytes)
	if err != nil {
		return err
	}
	key, err := secretx.Resolve("notification.encryption_key", m.config.EncryptionKey, m.config.EncryptionKeyFile, secretx.DefaultMaxBytes)
	if err != nil {
		return err
	}
	m.keyring, err = keyringx.New("notification:", key, m.config.PreviousEncryptionKeys...)
	if err != nil {
		return fmt.Errorf("notification encryption configuration: %w", err)
	}
	return nil
}
func validDomain(s string) bool {
	if len(s) == 0 || len(s) > 253 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}
