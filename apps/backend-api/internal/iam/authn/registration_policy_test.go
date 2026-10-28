package authn

import (
	"errors"
	"testing"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistrationPolicyPreservesConfiguredDefaultsAndPersistsAcrossInstances(t *testing.T) {
	s, h, _ := newRegistrationService(t)
	for _, enabled := range []bool{false, true} {
		s.cfg.Registration.Enabled = enabled
		value, err := s.RegistrationPolicy(t.Context())
		require.NoError(t, err)
		assert.Equal(t, enabled, value.Enabled)
		assert.True(t, value.Available)
	}
	_, err := s.SetRegistrationPolicy(t.Context(), false)
	require.NoError(t, err)
	assert.False(t, s.Capabilities().Registration)
	assert.ErrorIs(t, s.Register(t.Context(), "new@example.test", "correct registration password", "New"), authnext.ErrRegistrationDisabled)
	other, err := NewWebService(s.cfg, h.repository, WithRegistrationPrincipalCreator(registrationCreatorForHarness(h)))
	require.NoError(t, err)
	assert.False(t, other.Capabilities().Registration)
	_, err = other.SetRegistrationPolicy(t.Context(), true)
	require.NoError(t, err)
	assert.True(t, s.Capabilities().Registration, "an existing instance reads the persisted override")
	assert.True(t, other.Capabilities().Registration)
}

func TestRegistrationPolicyAuditFailureRollsBackAndStorageFailureClosesRegistration(t *testing.T) {
	s, h, _ := newRegistrationService(t)
	h.auditErr = errors.New("audit unavailable")
	_, err := s.SetRegistrationPolicy(t.Context(), false)
	require.ErrorContains(t, err, "audit unavailable")
	assert.True(t, s.Capabilities().Registration, "failed audit rolls back the first policy insert")
	h.auditErr = nil
	_, err = s.SetRegistrationPolicy(t.Context(), false)
	require.NoError(t, err)
	h.auditErr = errors.New("audit unavailable")
	_, err = s.SetRegistrationPolicy(t.Context(), true)
	require.Error(t, err)
	value, err := s.RegistrationPolicy(t.Context())
	require.NoError(t, err)
	assert.False(t, value.Enabled, "failed audit rolls back an existing policy update")
	require.NoError(t, h.db.Migrator().DropTable(h.repository.names.RegistrationPolicy.String()))
	assert.False(t, s.Capabilities().Registration)
	assert.ErrorIs(t, s.Register(t.Context(), "new@example.test", "correct registration password", "New"), authnext.ErrUnavailable)
	_, err = s.RegistrationPolicy(t.Context())
	require.Error(t, err)
}

func TestRegistrationPolicyAvailabilityDoesNotImplyPolicyOrVerificationBypass(t *testing.T) {
	s, _, _ := newRegistrationService(t)
	s.cfg.EmailVerification.Enabled = false
	value, err := s.SetRegistrationPolicy(t.Context(), true)
	require.NoError(t, err)
	assert.True(t, value.Enabled)
	assert.False(t, value.Available)
	assert.False(t, s.Capabilities().Registration)
}
