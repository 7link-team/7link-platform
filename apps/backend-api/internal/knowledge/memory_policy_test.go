package knowledge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryPolicyValidationIsDatabaseIndependentAndFailClosed(t *testing.T) {
	for _, raw := range []string{`{"profile":"STANDARD"}`, `{"profile":"STANDARD","overrides":{"ttl_days":1,"max_items":500}}`, `{"retrieval_policy":"RECENT"}`, `{"retrieval_policy":"RECENT","ttl_days":3650,"requires_approval_for_rules":true}`} {
		require.NoError(t, validateMemoryPolicy(t.Context(), 1, json.RawMessage(raw)), raw)
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"profile":"STANDARD","overrides":null}`, `{"retrieval_policy":"RECENT","ttl_days":0}`, `{"retrieval_policy":"RECENT","max_items":501}`, `{"retrieval_policy":"RECENT","extra":true}`, `{"profile":"STANDARD"} {}`} {
		require.Error(t, validateMemoryPolicy(t.Context(), 1, json.RawMessage(raw)), raw)
	}
	for _, raw := range []string{`{"retrieval_policy":"SEMANTIC"}`, `{"retrieval_policy":"HYBRID"}`, `{"profile":"UNKNOWN"}`, `{"retrieval_policy":"RECENT","requires_approval_for_rules":false}`} {
		require.ErrorIs(t, validateMemoryPolicy(t.Context(), 1, json.RawMessage(raw)), ErrPolicyUnsupported, raw)
	}
	require.ErrorIs(t, validateMemoryPolicy(t.Context(), 0, json.RawMessage(`{"profile":"STANDARD"}`)), ErrInvalid)
}
