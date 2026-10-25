package realtime

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"7link.com/platform/internal/infra/guid"
)

var eventTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

var kindRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func parseTopic(raw string) (Topic, bool, error) {
	var t Topic
	s := strings.Split(raw, "/")
	if len(s) < 3 || len(s) > 5 || s[0] != "tenant" || !kindRE.MatchString(s[2]) {
		return t, false, ErrInvalid
	}
	tenant, err := strconv.ParseInt(s[1], 10, 64)
	if err != nil || tenant < 0 || strconv.FormatInt(tenant, 10) != s[1] {
		return t, false, ErrInvalid
	}
	t.TenantID = guid.ID(tenant)
	t.Kind = s[2]
	t.Collection = len(s) == 3
	if len(s) >= 4 {
		id, err := strconv.ParseInt(s[3], 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != s[3] {
			return t, false, ErrInvalid
		}
		t.ID = guid.ID(id)
	}
	if len(s) == 5 && !kindRE.MatchString(s[4]) {
		return t, false, ErrInvalid
	}
	if tenant == 0 && (t.Kind != "inbox" || len(s) != 4) {
		return t, false, ErrInvalid
	}
	return t, len(s) == 5, nil
}
func (m *Module) authorize(ctx context.Context, tenant, principal guid.ID, raw string) (bool, string, error) {
	t, internal, err := parseTopic(raw)
	if err != nil {
		return false, "invalid", err
	}
	if t.TenantID != tenant {
		return false, "tenant_mismatch", nil
	}
	if internal {
		return false, "server_only", nil
	}
	if t.Kind == "inbox" {
		if !t.Collection && t.ID == principal {
			return true, "", nil
		}
		return false, "forbidden", nil
	}
	m.mu.RLock()
	fn := m.authorizers[t.Kind]
	m.mu.RUnlock()
	if fn == nil {
		return false, "forbidden", nil
	}
	allowed, err := fn(ctx, tenant, principal, t)
	if err != nil {
		return false, "", err
	}
	if !allowed {
		return false, "forbidden", nil
	}
	return true, "", nil
}
