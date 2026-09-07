package provisioning

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"7link.com/platform/pkg/module"
)

// provisioningKeyDomain separates this module's derived subkeys from every
// other module's. It is the literal prefix the derivation already used before
// the key ring existed, so every stored bearer token keeps opening.
const provisioningKeyDomain = "provisioning:key:"

// Config controls outbound SCIM provisioning. The encryption key protects the
// bearer tokens stored for SCIM targets; resolve it from a secret manager via
// encryption_key_file in production. An unset key disables outbound
// provisioning (target creation and pushes fail with a clear error).
type Config struct {
	module.Config
	EncryptionKey     string `mapstructure:"encryption_key" description:"base64 32-byte key encrypting outbound SCIM target bearer tokens; prefer encryption_key_file" default:"" secret:"true"`
	EncryptionKeyFile string `mapstructure:"encryption_key_file" description:"file containing the base64 32-byte provisioning encryption key" default:""`
	// The previous keys open what the key before this one sealed. They never
	// seal anything, so listing one costs a deployment nothing except the
	// obligation to take it out again once nothing depends on it.
	PreviousEncryptionKeys     []string      `mapstructure:"previous_encryption_keys" description:"retired base64 32-byte provisioning keys that still open stored tokens; prefer previous_encryption_key_files" secret:"true"`
	PreviousEncryptionKeyFiles []string      `mapstructure:"previous_encryption_key_files" description:"files containing retired base64 32-byte provisioning keys"`
	HTTPTimeout                time.Duration `mapstructure:"http_timeout" description:"outbound SCIM request timeout" default:"10s"`
	// A tenant names its own SCIM target, so the addresses reached on its
	// behalf are tenant-controlled. These are per-module on purpose: permitting
	// an internal SCIM directory should not also permit federation to reach it.
	OutboundAllowPlaintext bool          `mapstructure:"outbound_allow_plaintext" description:"permit http:// SCIM targets; leave false to require https" default:"false"`
	OutboundAllowNetworks  []string      `mapstructure:"outbound_allow_networks" description:"CIDR blocks a SCIM target may resolve to despite being private or reserved"`
	SyncInterval           time.Duration `mapstructure:"sync_interval" description:"background reconciliation interval for outbound SCIM targets; 0 disables" default:"0"`
}

// ResolveKeyring loads the configured provisioning keys so startup fails fast
// instead of surfacing a runtime secret error. A deployment that does not use
// outbound SCIM configures no key and gets no ring, which is not an error --
// it is how outbound provisioning stays switched off.
//
// Previous keys without an active one are refused rather than ignored. That
// configuration is a rotation someone got halfway through, and quietly
// disabling outbound provisioning is not what they were trying to do.
// ResolveOutboundPolicy loads the egress rules at startup so a malformed CIDR
// fails fast rather than at the first push to a directory.
func ResolveOutboundPolicy(cfg Config) (httpx.Policy, error) {
	policy, err := httpx.ParsePolicy(cfg.OutboundAllowPlaintext, cfg.OutboundAllowNetworks)
	if err != nil {
		return httpx.Policy{}, fmt.Errorf("provisioning outbound policy: %w", err)
	}
	return policy, nil
}

func ResolveKeyring(cfg Config) (*keyringx.Keyring, error) {
	active, err := secretx.Resolve("provisioning.encryption_key", cfg.EncryptionKey, cfg.EncryptionKeyFile, 4096)
	if err != nil {
		return nil, err
	}
	previous, err := secretx.ResolveList(
		"provisioning.previous_encryption_keys",
		cfg.PreviousEncryptionKeys, cfg.PreviousEncryptionKeyFiles, 4096,
	)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(active) == "" {
		if len(previous) > 0 {
			return nil, errors.New(
				"provisioning previous encryption keys are configured without an active key",
			)
		}
		return nil, nil
	}
	keys, err := keyringx.New(provisioningKeyDomain, active, previous...)
	if err != nil {
		return nil, fmt.Errorf("provisioning encryption key: %w", err)
	}
	return keys, nil
}
