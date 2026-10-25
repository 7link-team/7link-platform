package secrets

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/keyringx"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSealerPurposeTenantRowTamperingAndRotation(t *testing.T) {
	old := NewModule(Config{ActiveKey: testKey("a")})
	require.NoError(t, old.Initialize(t.Context()))
	require.NoError(t, old.RegisterPurpose("fleet.tool_credential"))
	aad, err := AssociatedData(1, 2)
	require.NoError(t, err)
	envelope, err := old.Seal("fleet.tool_credential", aad, []byte("sensitive"))
	require.NoError(t, err)
	require.NotContains(t, envelope, "sensitive")
	require.False(t, old.NeedsRewrap(envelope))
	fresh := NewModule(Config{ActiveKey: testKey("b"), PreviousKeys: []string{testKey("a")}})
	require.NoError(t, fresh.Initialize(t.Context()))
	require.NoError(t, fresh.RegisterPurpose("fleet.tool_credential"))
	require.NoError(t, fresh.RegisterPurpose("fleet.wake_webhook_secret"))
	plain, err := fresh.Open("fleet.tool_credential", aad, envelope)
	require.NoError(t, err)
	require.Equal(t, "sensitive", string(plain))
	require.True(t, fresh.NeedsRewrap(envelope))
	for _, ids := range [][2]int64{{2, 2}, {1, 3}} {
		other, _ := AssociatedData(guid.ID(ids[0]), guid.ID(ids[1]))
		_, err = fresh.Open("fleet.tool_credential", other, envelope)
		require.Error(t, err)
	}
	_, err = fresh.Open("fleet.wake_webhook_secret", aad, envelope)
	require.Error(t, err)
	_, err = fresh.Open("fleet.tool_credential", aad, envelope+"x")
	require.Error(t, err)
	for _, bad := range [][]byte{nil, []byte("1:2"), []byte(`{"tenant_id":"1"}`), []byte(`{"tenant_id":"1","row_id":"2","other":"x"}`)} {
		_, err = fresh.Seal("fleet.tool_credential", bad, nil)
		require.ErrorIs(t, err, ErrBinding)
	}
	require.ErrorIs(t, fresh.RegisterPurpose("field"), ErrPurpose)
	_, err = fresh.Seal("other.secret", aad, nil)
	require.ErrorIs(t, err, ErrPurpose)
	_, err = AssociatedData(0, 2)
	require.ErrorIs(t, err, ErrBinding)
	store := keyringx.SealedStore{Name: "fixture.secret", Purpose: "fleet.tool_credential", Values: func(context.Context) ([]keyringx.SealedValue, error) {
		return []keyringx.SealedValue{{Ref: "2", AAD: aad, Ciphertext: envelope}}, nil
	}, Write: func(_ context.Context, value keyringx.SealedValue, replacement string) (bool, error) {
		require.Equal(t, envelope, value.Ciphertext)
		envelope = replacement
		return true, nil
	}}
	reports, err := fresh.Rewrap(t.Context(), keyringx.RewrapStale, []keyringx.SealedStore{store})
	require.NoError(t, err)
	require.EqualValues(t, 1, reports[0].Rewrapped)
	require.False(t, fresh.NeedsRewrap(envelope))
	plain, err = fresh.Open("fleet.tool_credential", aad, envelope)
	require.NoError(t, err)
	require.Equal(t, "sensitive", string(plain))
	_, err = fresh.Rewrap(t.Context(), keyringx.RewrapStale, []keyringx.SealedStore{{Purpose: "unknown.field"}})
	require.ErrorIs(t, err, ErrPurpose)
	require.True(t, (*Module)(nil).NeedsRewrap("invalid"))
	_, err = (*Module)(nil).Open("", nil, "")
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).Seal("", nil, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).Rewrap(t.Context(), keyringx.RewrapStale, nil)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestRewrapRejectsUnboundStoreRows(t *testing.T) {
	m := NewModule(Config{ActiveKey: testKey("a")})
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, m.RegisterPurpose("module.secret"))
	for _, mode := range []keyringx.RewrapMode{keyringx.SurveyOnly, keyringx.RewrapStale} {
		wrote := false
		_, err := m.Rewrap(t.Context(), mode, []keyringx.SealedStore{{Name: "module.secret", Purpose: "module.secret", Values: func(context.Context) ([]keyringx.SealedValue, error) {
			return []keyringx.SealedValue{{AAD: nil, Ciphertext: "invalid"}}, nil
		}, Write: func(context.Context, keyringx.SealedValue, string) (bool, error) { wrote = true; return true, nil }}})
		require.ErrorIs(t, err, ErrBinding)
		require.False(t, wrote)
	}
}
