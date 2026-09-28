// Command dbgen generates authn's typed query package.
package main

import (
	"7link.com/platform/internal/iam/authn/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(
		model.Credential{},
		model.Session{},
		model.PasswordHistory{},
		model.MFAEnrollment{},
		model.RecoveryCode{},
		model.MFAChallenge{},
		model.PasskeyUser{},
		model.Passkey{},
		model.PasskeyChallenge{},
		model.PasswordRecoveryToken{},
		model.NotificationOutbox{},
		model.EmailVerificationToken{},
		model.StepUpChallenge{},
		model.ReauthenticationChallenge{},
		model.PersonalToken{},
		model.AccountViewToken{},
	)
}
