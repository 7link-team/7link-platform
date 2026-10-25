// Package secrets exposes purpose-bound business secret sealing over keyringx.
package secrets

import (
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"sync"
)

const Name = "secrets"

type Config struct {
	module.Config
	ActiveKey        string   `mapstructure:"active_key" secret:"true"`
	ActiveKeyFile    string   `mapstructure:"active_key_file"`
	PreviousKeys     []string `mapstructure:"previous_keys" secret:"true"`
	PreviousKeyFiles []string `mapstructure:"previous_key_files"`
}
type Module struct {
	config   Config
	mu       sync.RWMutex
	ring     *keyringx.Keyring
	purposes map[string]bool
}

func NewModule(config Config) *Module                      { return &Module{config: config, purposes: map[string]bool{}} }
func (*Module) ModuleName() string                         { return Name }
func (m *Module) Enabled() bool                            { return m != nil && m.config.Enabled }
func (m *Module) ProvideServices(s *module.Services) error { return module.Provide[Sealer](s, m) }
func (m *Module) Initialize(context.Context) error {
	active, err := secretx.Resolve("secrets.active_key", m.config.ActiveKey, m.config.ActiveKeyFile, 4096)
	if err != nil {
		return errors.New("secrets: active key unavailable")
	}
	previous, err := secretx.ResolveList("secrets.previous_keys", m.config.PreviousKeys, m.config.PreviousKeyFiles, 4096)
	if err != nil {
		return errors.New("secrets: previous keys unavailable")
	}
	ring, err := keyringx.New("business:", active, previous...)
	if err != nil {
		return errors.New("secrets: invalid key configuration")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ring = ring
	return nil
}

var _ module.ServiceProvider = (*Module)(nil)
var _ module.Initializer = (*Module)(nil)
var _ Sealer = (*Module)(nil)
