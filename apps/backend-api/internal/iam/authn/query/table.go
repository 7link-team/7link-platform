package query

import (
	"7link.com/platform/internal/iam/authn/model"
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

// Bind applies prefix to every table owned by the authentication module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Credentials:                q.Credential.Table(prefix + model.Credential{}.TableName()),
		Sessions:                   q.Session.Table(prefix + model.Session{}.TableName()),
		PasswordHistory:            q.PasswordHistory.Table(prefix + model.PasswordHistory{}.TableName()),
		MFAEnrollments:             q.MFAEnrollment.Table(prefix + model.MFAEnrollment{}.TableName()),
		RecoveryCodes:              q.RecoveryCode.Table(prefix + model.RecoveryCode{}.TableName()),
		MFAChallenges:              q.MFAChallenge.Table(prefix + model.MFAChallenge{}.TableName()),
		PasskeyUsers:               q.PasskeyUser.Table(prefix + model.PasskeyUser{}.TableName()),
		Passkeys:                   q.Passkey.Table(prefix + model.Passkey{}.TableName()),
		PasskeyChallenges:          q.PasskeyChallenge.Table(prefix + model.PasskeyChallenge{}.TableName()),
		PasswordRecoveryTokens:     q.PasswordRecoveryToken.Table(prefix + model.PasswordRecoveryToken{}.TableName()),
		NotificationOutbox:         q.NotificationOutbox.Table(prefix + model.NotificationOutbox{}.TableName()),
		EmailVerificationTokens:    q.EmailVerificationToken.Table(prefix + model.EmailVerificationToken{}.TableName()),
		StepUpChallenges:           q.StepUpChallenge.Table(prefix + model.StepUpChallenge{}.TableName()),
		ReauthenticationChallenges: q.ReauthenticationChallenge.Table(prefix + model.ReauthenticationChallenge{}.TableName()),
		PersonalTokens:             q.PersonalToken.Table(prefix + model.PersonalToken{}.TableName()),
	}
}
