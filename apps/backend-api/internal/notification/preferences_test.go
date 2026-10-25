package notification

import (
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestPreferencesVersionScopeValidationAndGuaranteedInbox(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		p, err := f.m.preferenceOn(t.Context(), tx, 11, 7, "*")
		require.NoError(t, err)
		require.Zero(t, p.Version)
		require.True(t, p.InApp)
		p.QuietEnabled = true
		p.QuietStartMin = 1320
		p.QuietEndMin = 480
		p.Timezone = "Asia/Kuala_Lumpur"
		saved, err := f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", p, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, saved.Version)
		saved.Email = "IMMEDIATE"
		saved, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", saved, 1)
		require.NoError(t, err)
		require.EqualValues(t, 2, saved.Version)
		_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", saved, 1)
		require.ErrorIs(t, err, ErrConflict)
		for _, change := range []func(*PreferenceView){func(p *PreferenceView) { p.InApp = false }, func(p *PreferenceView) { p.Email = "INVALID" }, func(p *PreferenceView) { p.QuietStartMin = -1 }, func(p *PreferenceView) { p.QuietEndMin = 1440 }, func(p *PreferenceView) { p.DigestHour = 24 }, func(p *PreferenceView) { p.Timezone = "NOT/A_ZONE" }} {
			bad := saved
			change(&bad)
			_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", bad, 2)
			require.ErrorIs(t, err, ErrInvalid)
		}
		_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "machine.offline", saved, 0)
		require.ErrorIs(t, err, ErrInvalid)
		saved.QuietEnabled = false
		_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "machine.offline", saved, 0)
		require.NoError(t, err)
		_, err = f.m.savePreferenceOn(t.Context(), tx, 11, 7, "bad", saved, 0)
		require.ErrorIs(t, err, ErrInvalid)
		return nil
	}))
	f.members.active = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.preferenceOn(t.Context(), tx, 11, 7, "*")
		require.ErrorIs(t, err, ErrForbidden)
		return nil
	}))
}
