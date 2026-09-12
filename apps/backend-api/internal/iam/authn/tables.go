package authn

import (
	"7link.com/platform/internal/iam/authn/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// CredentialTableNames is the physical name of every table authn owns, as authn hands
// them to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table authn does not have and cannot misspell the one it wants.
type CredentialTableNames struct {
	Credentials                gormx.TableName
	Sessions                   gormx.TableName
	PasswordHistory            gormx.TableName
	MFAEnrollments             gormx.TableName
	RecoveryCodes              gormx.TableName
	MFAChallenges              gormx.TableName
	PasskeyUsers               gormx.TableName
	Passkeys                   gormx.TableName
	PasskeyChallenges          gormx.TableName
	PasswordRecoveryTokens     gormx.TableName
	NotificationOutbox         gormx.TableName
	EmailVerificationTokens    gormx.TableName
	StepUpChallenges           gormx.TableName
	ReauthenticationChallenges gormx.TableName
	PersonalTokens             gormx.TableName
}

// PublishedCredentialTableNames binds every name authn owns to one deployment prefix. It
// builds from the module's own BindNames, so the prefix is applied the same way
// it is everywhere else authn queries, and a table added to authn cannot be
// forgotten here without the drift test noticing.
func PublishedCredentialTableNames(prefix string) CredentialTableNames {
	names := query.BindNames(prefix)
	return CredentialTableNames{
		Credentials:                names.Credentials,
		Sessions:                   names.Sessions,
		PasswordHistory:            names.PasswordHistory,
		MFAEnrollments:             names.MFAEnrollments,
		RecoveryCodes:              names.RecoveryCodes,
		MFAChallenges:              names.MFAChallenges,
		PasskeyUsers:               names.PasskeyUsers,
		Passkeys:                   names.Passkeys,
		PasskeyChallenges:          names.PasskeyChallenges,
		PasswordRecoveryTokens:     names.PasswordRecoveryTokens,
		NotificationOutbox:         names.NotificationOutbox,
		EmailVerificationTokens:    names.EmailVerificationTokens,
		StepUpChallenges:           names.StepUpChallenges,
		ReauthenticationChallenges: names.ReauthenticationChallenges,
		PersonalTokens:             names.PersonalTokens,
	}
}

// Bound reports whether these names arrived. PublishedCredentialTableNames fills every field
// at once, so one zero field means the consumer was never given the set rather
// than that authn published an incomplete one.
func (t CredentialTableNames) Bound() bool { return !t.Credentials.IsZero() }
