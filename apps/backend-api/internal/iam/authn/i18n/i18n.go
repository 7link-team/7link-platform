package authni18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "authn"

	LoginRequestRejected               = Namespace + ".login_request_rejected"
	ReturnURLNotAllowed                = Namespace + ".return_url_not_allowed"
	AdditionalVerificationRequired     = Namespace + ".additional_verification_required"
	InvalidLogin                       = Namespace + ".invalid_login"
	AuthenticationUnavailable          = Namespace + ".authentication_unavailable"
	InvalidMFAChallenge                = Namespace + ".invalid_mfa_challenge"
	PasskeyRequestRejected             = Namespace + ".passkey_request_rejected"
	PasskeyUnavailable                 = Namespace + ".passkey_unavailable"
	InvalidPasskeyChallenge            = Namespace + ".invalid_passkey_challenge"
	InvalidPasskeyCredential           = Namespace + ".invalid_passkey_credential"
	PasskeyAttestationRejected         = Namespace + ".passkey_attestation_rejected"
	PasskeyLimitReached                = Namespace + ".passkey_limit_reached"
	PasskeyNotFound                    = Namespace + ".passkey_not_found"
	RecoveryRequestRejected            = Namespace + ".recovery_request_rejected"
	PasswordRecoveryUnavailable        = Namespace + ".password_recovery_unavailable"
	InvalidRecoveryRequest             = Namespace + ".invalid_recovery_request"
	InvalidRecoveryCredential          = Namespace + ".invalid_recovery_credential"
	RecoveryCooldownActive             = Namespace + ".recovery_cooldown_active"
	PasswordReused                     = Namespace + ".password_reused"
	InvalidPassword                    = Namespace + ".invalid_password"
	PrimaryEmailRequired               = Namespace + ".primary_email_required"
	EmailVerificationUnavailable       = Namespace + ".email_verification_unavailable"
	EmailVerificationRequestRejected   = Namespace + ".email_verification_request_rejected"
	InvalidEmailVerificationCredential = Namespace + ".invalid_email_verification_credential"
	SessionNotFound                    = Namespace + ".session_not_found"
	InvalidMFA                         = Namespace + ".invalid_mfa"
	InvalidMFAEnrollment               = Namespace + ".invalid_mfa_enrollment"
	MFAAlreadyEnabled                  = Namespace + ".mfa_already_enabled"
	MFANotEnabled                      = Namespace + ".mfa_not_enabled"
	RegistrationRequestRejected        = Namespace + ".registration_request_rejected"
	InvalidRegistration                = Namespace + ".invalid_registration"
	RegistrationUnavailable            = Namespace + ".registration_unavailable"
	CaptchaUnavailable                 = Namespace + ".captcha_unavailable"
	CaptchaInvalid                     = Namespace + ".captcha_invalid"
	RegistrationConflict               = Namespace + ".registration_conflict"
	InvalidPersonalToken               = Namespace + ".invalid_personal_token"
)

//go:embed locales/*.json
var locales embed.FS

// Register publishes the authn locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
