package secrets

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/keyringx"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

var ErrUnavailable = errors.New("secrets: unavailable")
var ErrPurpose = errors.New("secrets: unregistered purpose")
var ErrBinding = errors.New("secrets: tenant and row binding required")
var purposePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_.-]*$`)

// Sealer keeps plaintext entirely in the calling module. RegisterPurpose and
// AssociatedData are required before encryption; envelopes remain keyringx data.
type Sealer interface {
	RegisterPurpose(string) error
	Seal(string, []byte, []byte) (string, error)
	Open(string, []byte, string) ([]byte, error)
	NeedsRewrap(string) bool
	Rewrap(context.Context, keyringx.RewrapMode, []keyringx.SealedStore) ([]keyringx.StoreReport, error)
}
type binding struct {
	TenantID guid.ID `json:"tenant_id"`
	RowID    guid.ID `json:"row_id"`
}

func AssociatedData(tenant, row guid.ID) ([]byte, error) {
	if tenant <= 0 || row <= 0 {
		return nil, ErrBinding
	}
	return json.Marshal(binding{tenant, row})
}
func validBinding(aad []byte) bool {
	var b binding
	if json.Unmarshal(aad, &b) != nil || b.TenantID <= 0 || b.RowID <= 0 {
		return false
	}
	canonical, _ := json.Marshal(b)
	return bytes.Equal(aad, canonical)
}
func (m *Module) RegisterPurpose(purpose string) error {
	if m == nil || len(purpose) > 160 || !purposePattern.MatchString(purpose) {
		return ErrPurpose
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.purposes == nil {
		m.purposes = map[string]bool{}
	}
	m.purposes[purpose] = true
	return nil
}
func (m *Module) check(purpose string, aad []byte) error {
	if m == nil || m.ring == nil {
		return ErrUnavailable
	}
	if !m.purposes[purpose] {
		return ErrPurpose
	}
	if !validBinding(aad) {
		return ErrBinding
	}
	return nil
}
func (m *Module) Seal(purpose string, aad, plain []byte) (string, error) {
	if m == nil {
		return "", ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.check(purpose, aad); err != nil {
		return "", err
	}
	return m.ring.Seal(purpose, aad, plain)
}
func (m *Module) Open(purpose string, aad []byte, envelope string) ([]byte, error) {
	if m == nil {
		return nil, ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.check(purpose, aad); err != nil {
		return nil, err
	}
	return m.ring.Open(purpose, aad, envelope)
}
func (m *Module) NeedsRewrap(envelope string) bool {
	if m == nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ring == nil || m.ring.NeedsRewrap(envelope)
}

// Rewrap retains each owner's CAS write callback and keyringx survey semantics.
func (m *Module) Rewrap(ctx context.Context, mode keyringx.RewrapMode, stores []keyringx.SealedStore) ([]keyringx.StoreReport, error) {
	if m == nil {
		return nil, ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ring == nil {
		return nil, ErrUnavailable
	}
	validated := append([]keyringx.SealedStore(nil), stores...)
	for i, s := range validated {
		if !m.purposes[s.Purpose] {
			return nil, ErrPurpose
		}
		if s.Values != nil {
			values := s.Values
			validated[i].Values = func(ctx context.Context) ([]keyringx.SealedValue, error) {
				rows, err := values(ctx)
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					if !validBinding(row.AAD) {
						return nil, ErrBinding
					}
				}
				return rows, nil
			}
		}
	}
	return keyringx.Rewrap(ctx, m.ring, mode, validated...)
}
