package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryAuthnTable(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	plain := Bind(db, "")
	prefixed := Bind(db, "sl_")
	actual := map[string][2]string{
		"credentials":               {plain.Credentials.TableName(), prefixed.Credentials.TableName()},
		"sessions":                  {plain.Sessions.TableName(), prefixed.Sessions.TableName()},
		"password_history":          {plain.PasswordHistory.TableName(), prefixed.PasswordHistory.TableName()},
		"mfa_enrollments":           {plain.MFAEnrollments.TableName(), prefixed.MFAEnrollments.TableName()},
		"recovery_codes":            {plain.RecoveryCodes.TableName(), prefixed.RecoveryCodes.TableName()},
		"mfa_challenges":            {plain.MFAChallenges.TableName(), prefixed.MFAChallenges.TableName()},
		"passkey_users":             {plain.PasskeyUsers.TableName(), prefixed.PasskeyUsers.TableName()},
		"passkeys":                  {plain.Passkeys.TableName(), prefixed.Passkeys.TableName()},
		"passkey_challenges":        {plain.PasskeyChallenges.TableName(), prefixed.PasskeyChallenges.TableName()},
		"password_recovery_tokens":  {plain.PasswordRecoveryTokens.TableName(), prefixed.PasswordRecoveryTokens.TableName()},
		"notification_outbox":       {plain.NotificationOutbox.TableName(), prefixed.NotificationOutbox.TableName()},
		"email_verification_tokens": {plain.EmailVerificationTokens.TableName(), prefixed.EmailVerificationTokens.TableName()},
		"stepup_challenges":         {plain.StepUpChallenges.TableName(), prefixed.StepUpChallenges.TableName()},
		"personal_tokens":           {plain.PersonalTokens.TableName(), prefixed.PersonalTokens.TableName()},
		"reauth_challenges":         {plain.ReauthenticationChallenges.TableName(), prefixed.ReauthenticationChallenges.TableName()},
	}

	require.Len(t, actual, 15)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}

// TestBindNamesMapPublishesPrefixedNames keeps the published set and the bound
// queries from drifting apart: a table reachable through Bind but missing from
// the map would be invisible to every consumer, and a name in the map that
// Bind does not bind would point at a table this module does not own.
func TestBindNamesMapPublishesPrefixedNames(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	published := BindNames(prefix).Map()
	require.NotEmpty(t, published, "a module that binds tables publishes them")
	for logical, name := range published {
		assert.Equal(t, prefix+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)
	}
}
