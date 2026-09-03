package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface {
	TableName() string
}

func TestAuthnModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()

	models := []tableNamer{
		Credential{}, Session{}, PasswordHistory{}, MFAEnrollment{}, RecoveryCode{},
		MFAChallenge{}, PasskeyUser{}, Passkey{}, PasskeyChallenge{}, PasswordRecoveryToken{},
		NotificationOutbox{}, EmailVerificationToken{}, StepUpChallenge{},
	}
	expected := []string{
		"iam_credentials", "iam_sessions", "iam_password_history", "iam_mfa_enrollments",
		"iam_recovery_codes", "iam_mfa_challenges", "iam_passkey_users", "iam_passkeys",
		"iam_passkey_challenges", "iam_password_recovery_tokens", "iam_notification_outbox",
		"iam_email_verification_tokens", "iam_stepup_challenges",
	}

	require.Len(t, models, 13)
	seen := make(map[string]struct{}, len(models))
	for index, item := range models {
		name := item.TableName()
		assert.Equal(t, expected[index], name)
		_, duplicate := seen[name]
		assert.False(t, duplicate, "duplicate model table %s", name)
		seen[name] = struct{}{}
	}
}
