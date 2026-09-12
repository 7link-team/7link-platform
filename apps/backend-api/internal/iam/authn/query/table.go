package query

import (
	"7link.com/platform/internal/iam/authn/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Credentials                = credential
	Sessions                   = session
	PasswordHistoryTable       = passwordHistory
	MFAEnrollments             = mFAEnrollment
	RecoveryCodes              = recoveryCode
	MFAChallenges              = mFAChallenge
	PasskeyUsers               = passkeyUser
	Passkeys                   = passkey
	PasskeyChallenges          = passkeyChallenge
	PasswordRecoveryTokens     = passwordRecoveryToken
	NotificationOutboxTable    = notificationOutbox
	EmailVerificationTokens    = emailVerificationToken
	StepUpChallenges           = stepUpChallenge
	ReauthenticationChallenges = reauthenticationChallenge
	PersonalTokens             = personalToken
)

// Tables is the complete generated query set bound to one deployment prefix.
type Tables struct {
	Credentials                *Credentials
	Sessions                   *Sessions
	PasswordHistory            *PasswordHistoryTable
	MFAEnrollments             *MFAEnrollments
	RecoveryCodes              *RecoveryCodes
	MFAChallenges              *MFAChallenges
	PasskeyUsers               *PasskeyUsers
	Passkeys                   *Passkeys
	PasskeyChallenges          *PasskeyChallenges
	PasswordRecoveryTokens     *PasswordRecoveryTokens
	NotificationOutbox         *NotificationOutboxTable
	EmailVerificationTokens    *EmailVerificationTokens
	StepUpChallenges           *StepUpChallenges
	ReauthenticationChallenges *ReauthenticationChallenges
	PersonalTokens             *PersonalTokens
}

// TableNames is the physical name of every table authn owns under one
// deployment prefix.
type TableNames struct {
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

// BindNames returns the physical name of every table authn owns. It is the only
// place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of authn's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Credentials:                gormx.NewTableName(prefix, model.Credential{}.TableName()),
		Sessions:                   gormx.NewTableName(prefix, model.Session{}.TableName()),
		PasswordHistory:            gormx.NewTableName(prefix, model.PasswordHistory{}.TableName()),
		MFAEnrollments:             gormx.NewTableName(prefix, model.MFAEnrollment{}.TableName()),
		RecoveryCodes:              gormx.NewTableName(prefix, model.RecoveryCode{}.TableName()),
		MFAChallenges:              gormx.NewTableName(prefix, model.MFAChallenge{}.TableName()),
		PasskeyUsers:               gormx.NewTableName(prefix, model.PasskeyUser{}.TableName()),
		Passkeys:                   gormx.NewTableName(prefix, model.Passkey{}.TableName()),
		PasskeyChallenges:          gormx.NewTableName(prefix, model.PasskeyChallenge{}.TableName()),
		PasswordRecoveryTokens:     gormx.NewTableName(prefix, model.PasswordRecoveryToken{}.TableName()),
		NotificationOutbox:         gormx.NewTableName(prefix, model.NotificationOutbox{}.TableName()),
		EmailVerificationTokens:    gormx.NewTableName(prefix, model.EmailVerificationToken{}.TableName()),
		StepUpChallenges:           gormx.NewTableName(prefix, model.StepUpChallenge{}.TableName()),
		ReauthenticationChallenges: gormx.NewTableName(prefix, model.ReauthenticationChallenge{}.TableName()),
		PersonalTokens:             gormx.NewTableName(prefix, model.PersonalToken{}.TableName()),
	}
}

// Bind applies prefix to every table owned by the authentication module. It
// builds from BindNames, so the prefix reaches the generated query objects
// through one path rather than fifteen.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		Credentials:                q.Credential.Table(names.Credentials.String()),
		Sessions:                   q.Session.Table(names.Sessions.String()),
		PasswordHistory:            q.PasswordHistory.Table(names.PasswordHistory.String()),
		MFAEnrollments:             q.MFAEnrollment.Table(names.MFAEnrollments.String()),
		RecoveryCodes:              q.RecoveryCode.Table(names.RecoveryCodes.String()),
		MFAChallenges:              q.MFAChallenge.Table(names.MFAChallenges.String()),
		PasskeyUsers:               q.PasskeyUser.Table(names.PasskeyUsers.String()),
		Passkeys:                   q.Passkey.Table(names.Passkeys.String()),
		PasskeyChallenges:          q.PasskeyChallenge.Table(names.PasskeyChallenges.String()),
		PasswordRecoveryTokens:     q.PasswordRecoveryToken.Table(names.PasswordRecoveryTokens.String()),
		NotificationOutbox:         q.NotificationOutbox.Table(names.NotificationOutbox.String()),
		EmailVerificationTokens:    q.EmailVerificationToken.Table(names.EmailVerificationTokens.String()),
		StepUpChallenges:           q.StepUpChallenge.Table(names.StepUpChallenges.String()),
		ReauthenticationChallenges: q.ReauthenticationChallenge.Table(names.ReauthenticationChallenges.String()),
		PersonalTokens:             q.PersonalToken.Table(names.PersonalTokens.String()),
	}
}
