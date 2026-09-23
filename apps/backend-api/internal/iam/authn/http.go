package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/go-webauthn/webauthn/protocol"
)

type meOutput struct {
	Subject           string     `json:"subject"`
	Issuer            string     `json:"issuer"`
	PreferredUsername *string    `json:"preferred_username,omitempty"`
	Email             *string    `json:"email,omitempty"`
	EmailVerified     *bool      `json:"email_verified,omitempty"`
	OrganizationID    string     `json:"organization_id,omitempty"`
	ACR               int        `json:"acr"`
	AMR               []string   `json:"amr,omitempty"`
	AuthTime          *time.Time `json:"auth_time,omitempty"`
}

type meInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
}

// Session tenancy is a validated request context, not a mutation of the global
// login session. Token tenancy remains exclusively the signed token's choice.
func sessionTenant(ctx context.Context, web *Service, in *meInput, claims *authnext.Claims) (*authnext.Claims, error) {
	if strings.TrimSpace(in.Authorization) != "" {
		return claims, nil
	}
	if web == nil || web.memberTenant == nil {
		return nil, huma.Error503ServiceUnavailable("authentication_unavailable")
	}
	tenantID, err := web.memberTenant(ctx, claims.PrincipalID, 0)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("authentication_unavailable")
	}
	resolved := *claims
	resolved.OrganizationID, resolved.TenantID = "", tenantID
	if tenantID > 0 {
		resolved.OrganizationID = tenantID.String()
	}
	return &resolved, nil
}

type logoutInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
}

type loginInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		LoginName string `json:"login_name" minLength:"1" maxLength:"200"`
		Password  string `json:"password" minLength:"1" maxLength:"200"`
		ReturnURL string `json:"return_url,omitempty" maxLength:"2048"`
	}
}

type registrationInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		Email         string `json:"email" format:"email" maxLength:"320"`
		Password      string `json:"password" minLength:"8" maxLength:"1024"`
		DisplayName   string `json:"display_name,omitempty" maxLength:"128"`
		CaptchaType   string `json:"captcha_type" minLength:"1"`
		CaptchaID     string `json:"captcha_id" minLength:"1"`
		CaptchaAnswer string `json:"captcha_answer" minLength:"1"`
	}
}

type captchaInput struct{}

type AuthenticationCapabilities struct {
	Registration     bool `json:"registration"`
	PasswordRecovery bool `json:"password_recovery"`
	Passkey          bool `json:"passkey"`
}

type sessionListInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
}

type sessionMutationInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	ID            string `path:"session_id" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
}

type passwordChangeInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
		NewPassword     string `json:"new_password" minLength:"12" maxLength:"1024"`
	}
}

type passwordRecoveryStartInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		Identifier string `json:"identifier" minLength:"1" maxLength:"320" doc:"Login name or verified primary email"`
	}
}

type passwordRecoveryCompleteInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		Token       string `json:"token" minLength:"32" maxLength:"128"`
		NewPassword string `json:"new_password" minLength:"12" maxLength:"1024"`
	}
}

type emailVerificationStartInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
}

type emailVerificationCompleteInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   emailVerificationCredential
}

type emailVerificationCredential struct {
	Token string `json:"token,omitempty"`
	Email string `json:"email,omitempty"`
	Code  string `json:"code,omitempty"`
}

func (emailVerificationCredential) Schema(huma.Registry) *huma.Schema {
	one, two, six, tokenMax, emailMax := 1, 2, 6, 128, 320
	return &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"token": {Type: huma.TypeString, MinLength: &one, MaxLength: &tokenMax},
			"email": {Type: huma.TypeString, Format: "email", MinLength: &one, MaxLength: &emailMax},
			"code":  {Type: huma.TypeString, MinLength: &six, MaxLength: &six, Pattern: "^[0-9]{6}$"},
		},
		OneOf: []*huma.Schema{
			{Type: huma.TypeObject, Required: []string{"token"}, MinProperties: &one, MaxProperties: &one},
			{Type: huma.TypeObject, Required: []string{"email", "code"}, MinProperties: &two, MaxProperties: &two},
		},
	}
}

type mfaLoginInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		ChallengeID string `json:"challenge_id" minLength:"32" maxLength:"128"`
		Code        string `json:"code" minLength:"6" maxLength:"64"`
	}
}

type mfaStatusInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
}

type mfaPasswordInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
	}
}

type mfaCodeInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		Code string `json:"code" minLength:"6" maxLength:"64"`
	}
}

type mfaVerificationInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
		Code            string `json:"code" minLength:"6" maxLength:"64"`
	}
}

type stepUpOptionsInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
}

type stepUpVerifyInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		ChallengeID string `json:"challenge_id" minLength:"32" maxLength:"128"`
		Code        string `json:"code" minLength:"6" maxLength:"64"`
	}
}

type passkeyLoginBeginInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		ReturnURL string `json:"return_url,omitempty" maxLength:"2048"`
	}
}

type webAuthnAssertion json.RawMessage

func (webAuthnAssertion) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[protocol.CredentialAssertionResponse](), true, "WebAuthnAssertion")
}

func (value webAuthnAssertion) MarshalJSON() ([]byte, error) {
	return json.RawMessage(value).MarshalJSON()
}

func (value *webAuthnAssertion) UnmarshalJSON(raw []byte) error {
	return unmarshalRawJSON((*json.RawMessage)(value), raw)
}

type webAuthnCreation json.RawMessage

func (webAuthnCreation) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[protocol.CredentialCreationResponse](), true, "WebAuthnCreation")
}

func (value webAuthnCreation) MarshalJSON() ([]byte, error) {
	return json.RawMessage(value).MarshalJSON()
}

func (value *webAuthnCreation) UnmarshalJSON(raw []byte) error {
	return unmarshalRawJSON((*json.RawMessage)(value), raw)
}

type webAuthnAssertionOptions json.RawMessage

func (webAuthnAssertionOptions) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[protocol.CredentialAssertion](), true, "WebAuthnAssertionOptions")
}

func (value webAuthnAssertionOptions) MarshalJSON() ([]byte, error) {
	return json.RawMessage(value).MarshalJSON()
}

type webAuthnCreationOptions json.RawMessage

func (webAuthnCreationOptions) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[protocol.CredentialCreation](), true, "WebAuthnCreationOptions")
}

func (value webAuthnCreationOptions) MarshalJSON() ([]byte, error) {
	return json.RawMessage(value).MarshalJSON()
}

func unmarshalRawJSON(target *json.RawMessage, raw []byte) error {
	var value json.RawMessage
	if err := value.UnmarshalJSON(raw); err != nil {
		return err
	}
	*target = value
	return nil
}

type passkeyFinishInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		ChallengeID string            `json:"challenge_id" minLength:"32" maxLength:"128"`
		Credential  webAuthnAssertion `json:"credential"`
	}
}

type passkeyRegistrationFinishInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		ChallengeID string           `json:"challenge_id" minLength:"32" maxLength:"128"`
		Name        string           `json:"name" minLength:"1" maxLength:"200"`
		Credential  webAuthnCreation `json:"credential"`
	}
}

type passkeyMutationInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	ID            string `path:"passkey_id" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
	Body          struct {
		Name string `json:"name" minLength:"1" maxLength:"200"`
	}
}

type passkeyDeleteInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	ID            string `path:"passkey_id" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
	Body          struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
	}
}

type passkeyAssertionOptions struct {
	ChallengeID string                   `json:"challenge_id"`
	ExpiresAt   time.Time                `json:"expires_at"`
	Options     webAuthnAssertionOptions `json:"options"`
}

type passkeyCreationOptions struct {
	ChallengeID string                  `json:"challenge_id"`
	ExpiresAt   time.Time               `json:"expires_at"`
	Options     webAuthnCreationOptions `json:"options"`
}

type logoutData struct {
	LogoutURL string `json:"logout_url" doc:"IdP end-session URL the browser must visit to finish logout"`
}

type logoutOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      respx.Envelope[logoutData]
}

type loginOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      respx.Envelope[authnext.LoginResult]
}

type stepUpOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      respx.Envelope[authnext.StepUpResult]
}

type mfaConfirmationOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      respx.Envelope[authnext.MFAConfirmation]
}

// RegisterHTTP registers the authn module's HTTP operations.
func RegisterHTTP(a huma.API, authenticator authz.TokenVerifier, web *Service) {
	web.registerSharedSecurity(a)
	defer documentSharedSecurity(a)
	authenticated := authz.NewAuthenticationOnlyRegistrar(authenticator)
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Return the authenticated local principal",
		Tags:        []string{"me"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *meInput) (*respx.Body[meOutput], error) {
		claims, err := authenticator.Authenticate(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		claims, err = sessionTenant(ctx, web, in, claims)
		if err != nil {
			return nil, err
		}
		output, err := sessionView(ctx, web, in, claims)
		if err != nil {
			return nil, err
		}
		return respx.OK(ctx, output), nil
	})

	if web == nil || !web.Enabled() {
		return
	}
	registerReauthenticationHTTP(a, web)
	registerPersonalTokenHTTP(a, web)
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-login-options", Method: http.MethodGet, Path: "/login/options",
		Summary: "Return enabled public authentication methods", Tags: []string{"login"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[AuthenticationCapabilities], error) {
		capabilities := web.Capabilities()
		return respx.OK(ctx, AuthenticationCapabilities{
			Registration: capabilities.Registration, PasswordRecovery: capabilities.PasswordRecovery,
			Passkey: capabilities.Passkey,
		}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-registration-captcha", Method: http.MethodPost, Path: "/registrations/captchas",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Issue a graphic captcha for registration", Tags: []string{"registrations"},
		Errors: []int{http.StatusInternalServerError},
	}, func(ctx context.Context, _ *captchaInput) (*respx.Body[captchaOutput], error) {
		out, err := web.IssueCaptcha()
		if err != nil {
			return nil, huma.Error500InternalServerError("captcha_unavailable")
		}
		return respx.OK(ctx, *out), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-registration", Method: http.MethodPost, Path: "/registrations",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Register a global principal pending email verification", Tags: []string{"registrations"}, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, func(ctx context.Context, in *registrationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("registration_request_rejected")
		}
		if !web.VerifyCaptcha(in.Body.CaptchaType, in.Body.CaptchaID, in.Body.CaptchaAnswer) {
			return nil, huma.Error422UnprocessableEntity("captcha_invalid")
		}
		if err := web.Register(ctx, in.Body.Email, in.Body.Password, in.Body.DisplayName); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidRegistration):
				return nil, huma.Error422UnprocessableEntity("invalid_registration")
			case errors.Is(err, authnext.ErrRegistrationDisabled):
				return nil, huma.Error503ServiceUnavailable("registration_unavailable")
			case errors.Is(err, authnext.ErrRegistrationConflict):
				return respx.OK(ctx, map[string]bool{"accepted": true}), nil
			default:
				return nil, huma.Error500InternalServerError("authentication_unavailable")
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-session", Method: http.MethodPost, Path: "/login",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Log in with a local username and password", Tags: []string{"login"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, func(ctx context.Context, in *loginInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("login_request_rejected")
		}
		result, err := web.BeginLogin(ctx, in.Body.LoginName, in.Body.Password, in.Body.ReturnURL)
		if err != nil {
			switch {
			case errors.Is(err, authnext.ErrReturnURL):
				return nil, huma.Error422UnprocessableEntity("return_url_not_allowed")
			case errors.Is(err, authnext.ErrAdditionalVerification):
				return nil, huma.Error409Conflict("additional_verification_required")
			case errors.Is(err, authnext.ErrInvalidCredentials):
				return nil, huma.Error401Unauthorized("invalid_login")
			default:
				return nil, huma.Error500InternalServerError("authentication_unavailable")
			}
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-session-verification", Method: http.MethodPost, Path: "/login/mfa",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Complete an MFA login challenge", Tags: []string{"login"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
	}, func(ctx context.Context, in *mfaLoginInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("login_request_rejected")
		}
		result, err := web.VerifyLoginMFA(ctx, in.Body.ChallengeID, in.Body.Code)
		if err != nil {
			if errors.Is(err, authnext.ErrInvalidMFA) || errors.Is(err, authnext.ErrMFAChallenge) {
				return nil, huma.Error401Unauthorized("invalid_mfa_challenge")
			}
			return nil, huma.Error500InternalServerError("authentication_unavailable")
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-passkey-login-options", Method: http.MethodPost, Path: "/login/passkey/options",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Create WebAuthn authentication options for passkey login", Tags: []string{"login"}, Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyLoginBeginInput) (*respx.Body[passkeyAssertionOptions], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("passkey_request_rejected")
		}
		options, err := web.BeginPasskeyLogin(ctx, in.Origin, in.Body.ReturnURL)
		if err != nil {
			switch {
			case errors.Is(err, ErrCSRF):
				return nil, huma.Error403Forbidden("passkey_request_rejected")
			case errors.Is(err, authnext.ErrReturnURL):
				return nil, huma.Error422UnprocessableEntity("return_url_not_allowed")
			case errors.Is(err, authnext.ErrPasskeyDisabled):
				return nil, huma.Error503ServiceUnavailable("passkey_unavailable")
			default:
				return nil, huma.Error500InternalServerError("authentication_unavailable")
			}
		}
		return respx.OK(ctx, passkeyAssertionOptions{
			ChallengeID: options.ChallengeID,
			ExpiresAt:   options.ExpiresAt,
			Options:     webAuthnAssertionOptions(options.Options),
		}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-verify-passkey-login", Method: http.MethodPost, Path: "/login/passkey/verify",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Verify a WebAuthn assertion and log in", Tags: []string{"login"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyFinishInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("passkey_request_rejected")
		}
		result, err := web.FinishPasskeyLogin(
			ctx, in.Origin, in.Body.ChallengeID, json.RawMessage(in.Body.Credential),
		)
		if err != nil {
			switch {
			case errors.Is(err, authnext.ErrPasskeyChallenge), errors.Is(err, authnext.ErrPasskeyCredential):
				return nil, huma.Error401Unauthorized("invalid_passkey_challenge")
			case errors.Is(err, authnext.ErrPasskeyDisabled):
				return nil, huma.Error503ServiceUnavailable("passkey_unavailable")
			default:
				return nil, huma.Error500InternalServerError("authentication_unavailable")
			}
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-get-current-session", Method: http.MethodGet, Path: "/me/session",
		Summary: "Return the current browser session", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *meInput) (*respx.Body[meOutput], error) {
		claims, err := web.Authenticate(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		claims, err = sessionTenant(ctx, web, in, claims)
		if err != nil {
			return nil, err
		}
		output, err := sessionView(ctx, web, in, claims)
		if err != nil {
			return nil, err
		}
		return respx.OK(ctx, output), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-delete-current-session", Method: http.MethodDelete, Path: "/me/session",
		Summary: "Destroy the current browser session", Tags: []string{"me"},
		Errors: []int{http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		logoutURL, err := web.Logout(ctx, in.Cookie)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("authentication_unavailable")
		}
		return &logoutOutput{SetCookie: web.ClearCookie(), Body: respx.OK(ctx, logoutData{LogoutURL: logoutURL}).Body}, nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-list-sessions", Method: http.MethodGet, Path: "/me/sessions",
		Summary: "List active browser sessions", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError},
	}, func(ctx context.Context, in *sessionListInput) (*respx.Body[[]authnext.BrowserSession], error) {
		sessions, err := web.ListSessions(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, sessions), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-session", Method: http.MethodDelete, Path: "/me/sessions/{session_id}",
		Summary: "Revoke a browser session", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
	}, func(ctx context.Context, in *sessionMutationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.RevokeSession(ctx, in.Authorization, in.Cookie, in.ID); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"revoked": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-sessions", Method: http.MethodDelete, Path: "/me/sessions",
		Summary: "Revoke every session and refresh token", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.LogoutAll(ctx, in.Authorization, in.Cookie); err != nil {
			return nil, authenticationError(err)
		}
		return &logoutOutput{SetCookie: web.ClearCookie(), Body: respx.OK(ctx, logoutData{LogoutURL: web.PostLogoutURL()}).Body}, nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-update-password", Method: http.MethodPatch, Path: "/me/password",
		Summary: "Change the current principal password", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, func(ctx context.Context, in *passwordChangeInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPatch, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.ChangePassword(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.NewPassword); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"changed": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-password-recovery-request", Method: http.MethodPost, Path: "/password-recoveries/requests",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Queue a password recovery notification", Tags: []string{"password-recoveries"}, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passwordRecoveryStartInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("recovery_request_rejected")
		}
		if err := web.BeginPasswordRecovery(ctx, in.Body.Identifier); err != nil {
			switch {
			case errors.Is(err, authnext.ErrRecoveryDisabled):
				return nil, huma.Error503ServiceUnavailable("password_recovery_unavailable")
			case errors.Is(err, authnext.ErrInvalidRecovery):
				return nil, huma.Error422UnprocessableEntity("invalid_recovery_request")
			default:
				return nil, huma.Error503ServiceUnavailable("password_recovery_unavailable")
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-password-recovery", Method: http.MethodPost, Path: "/password-recoveries",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Consume a password recovery credential", Tags: []string{"password-recoveries"},
		Errors: []int{http.StatusBadRequest, http.StatusConflict, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passwordRecoveryCompleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("recovery_request_rejected")
		}
		if err := web.CompletePasswordRecovery(ctx, in.Body.Token, in.Body.NewPassword); err != nil {
			switch {
			case errors.Is(err, authnext.ErrRecoveryDisabled):
				return nil, huma.Error503ServiceUnavailable("password_recovery_unavailable")
			case errors.Is(err, authnext.ErrPasswordReused):
				return nil, huma.Error409Conflict("password_reused")
			case errors.Is(err, authnext.ErrInvalidRecovery):
				return nil, huma.Error400BadRequest("invalid_recovery_credential")
			default:
				return nil, huma.Error503ServiceUnavailable("password_recovery_unavailable")
			}
		}
		return respx.OK(ctx, map[string]bool{"changed": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-email-verification-request", Method: http.MethodPost, Path: "/email-verification-requests",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Queue verification for the authenticated principal email", Tags: []string{"email-verification-requests"},
		DefaultStatus: http.StatusAccepted,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *emailVerificationStartInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.BeginEmailVerification(ctx, in.Authorization, in.Cookie); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidSession), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrInvalidBearer):
				return nil, huma.Error401Unauthorized("unauthorized")
			case errors.Is(err, authnext.ErrEmailRequired):
				return nil, huma.Error409Conflict("primary_email_required")
			case errors.Is(err, authnext.ErrEmailVerificationDisabled):
				return nil, huma.Error503ServiceUnavailable("email_verification_unavailable")
			default:
				return nil, huma.Error503ServiceUnavailable("email_verification_unavailable")
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-email-verification", Method: http.MethodPost, Path: "/email-verifications",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Consume a primary email verification credential", Tags: []string{"email-verifications"},
		Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *emailVerificationCompleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden("email_verification_request_rejected")
		}
		if err := web.CompleteEmailVerification(ctx, in.Body.Token, in.Body.Email, in.Body.Code); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidEmailVerification):
				return nil, huma.Error400BadRequest("invalid_email_verification_credential")
			case errors.Is(err, authnext.ErrEmailVerificationDisabled):
				return nil, huma.Error503ServiceUnavailable("email_verification_unavailable")
			default:
				return nil, huma.Error503ServiceUnavailable("email_verification_unavailable")
			}
		}
		return respx.OK(ctx, map[string]bool{"verified": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-get-mfa", Method: http.MethodGet, Path: "/me/mfa", Summary: "Return the current principal MFA status", Tags: []string{"me"}, Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaStatusInput) (*respx.Body[authnext.MFAStatus], error) {
		status, err := web.MFAStatus(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, status), nil
	})
	authz.RegisterPublic(a, huma.Operation{OperationID: "authn-create-totp-enrollment", Method: http.MethodPost, Path: "/me/mfa/totp-enrollments", Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication), Summary: "Begin TOTP enrollment after password verification", Tags: []string{"me"}, Security: authz.UserSecurity(), Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaPasswordInput) (*respx.Body[authnext.MFAEnrollment], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		enrollment, err := web.BeginTOTPEnrollment(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, enrollment), nil
	})
	authz.RegisterPublic(a, huma.Operation{OperationID: "authn-create-totp-factor", Method: http.MethodPost, Path: "/me/mfa/totp", Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication), Summary: "Confirm TOTP enrollment and issue recovery codes", Tags: []string{"me"}, Security: authz.UserSecurity(), Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaCodeInput) (*mfaConfirmationOutput, error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		confirmation, err := web.ConfirmTOTPEnrollment(ctx, in.Authorization, in.Cookie, in.Body.Code)
		if err != nil {
			return nil, authenticationError(err)
		}
		output := &mfaConfirmationOutput{Body: respx.OK(ctx, confirmation).Body}
		if confirmation.SessionRevoked {
			output.SetCookie = web.ClearCookie()
		}
		return output, nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-replace-recovery-codes", Method: http.MethodPut, Path: "/me/mfa/recovery-codes", Summary: "Replace all MFA recovery codes", Tags: []string{"me"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaVerificationInput) (*respx.Body[authnext.MFAConfirmation], error) {
		if err := web.ValidateCSRF(http.MethodPut, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		confirmation, err := web.RegenerateRecoveryCodes(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.Code)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, confirmation), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-delete-totp-factor", Method: http.MethodDelete, Path: "/me/mfa/totp", Summary: "Disable TOTP after password and factor verification", Tags: []string{"me"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaVerificationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.DisableTOTP(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.Code); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"disabled": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-step-up-challenge", Method: http.MethodPost, Path: "/me/session/step-up-challenges",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Begin a step-up MFA challenge for the current browser session", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *stepUpOptionsInput) (*respx.Body[authnext.StepUpOptions], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		options, err := web.BeginStepUp(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, options), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-step-up-verification", Method: http.MethodPost, Path: "/me/session/step-up-verifications",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Verify a step-up factor and elevate the browser session", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *stepUpVerifyInput) (*stepUpOutput, error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		result, err := web.VerifyStepUp(ctx, in.Authorization, in.Cookie, in.Body.ChallengeID, in.Body.Code)
		if err != nil {
			return nil, authenticationError(err)
		}
		return &stepUpOutput{SetCookie: web.SessionCookie(result.SessionID), Body: respx.OK(ctx, result).Body}, nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-list-passkeys", Method: http.MethodGet, Path: "/me/passkeys",
		Summary: "List the current principal passkeys", Tags: []string{"me"}, Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *sessionListInput) (*respx.Body[[]authnext.Passkey], error) {
		passkeys, err := web.ListPasskeys(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkeys), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-passkey-registration-challenge", Method: http.MethodPost, Path: "/me/passkey-registration-challenges",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Begin passkey registration after password verification", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *mfaPasswordInput) (*respx.Body[passkeyCreationOptions], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		options, err := web.BeginPasskeyRegistration(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkeyCreationOptions{
			ChallengeID: options.ChallengeID,
			ExpiresAt:   options.ExpiresAt,
			Options:     webAuthnCreationOptions(options.Options),
		}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-passkey", Method: http.MethodPost, Path: "/me/passkeys",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Verify and store a new passkey", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyRegistrationFinishInput) (*respx.Body[authnext.Passkey], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		passkey, err := web.FinishPasskeyRegistration(
			ctx, in.Authorization, in.Cookie, in.Body.ChallengeID, in.Body.Name,
			json.RawMessage(in.Body.Credential),
		)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkey), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-update-passkey", Method: http.MethodPatch, Path: "/me/passkeys/{passkey_id}",
		Summary: "Rename a passkey", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyMutationInput) (*respx.Body[authnext.Passkey], error) {
		if err := web.ValidateCSRF(http.MethodPatch, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		passkey, err := web.RenamePasskey(ctx, in.Authorization, in.Cookie, in.ID, in.Body.Name)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkey), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-passkey", Method: http.MethodDelete, Path: "/me/passkeys/{passkey_id}",
		Summary: "Delete a passkey after password verification", Tags: []string{"me"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyDeleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := web.DeletePasskey(ctx, in.Authorization, in.Cookie, in.ID, in.Body.CurrentPassword); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"deleted": true}), nil
	})
}

func loginResponse(ctx context.Context, web *Service, result authnext.LoginResult) *loginOutput {
	output := &loginOutput{Body: respx.OK(ctx, result).Body}
	if result.SessionID != "" {
		output.SetCookie = web.SessionCookie(result.SessionID)
	}
	return output
}

func authenticationError(err error) error {
	switch {
	case errors.Is(err, authz.ErrFieldDenied):
		return huma.Error403Forbidden("forbidden")
	case errors.Is(err, authz.ErrFieldsUnavailable):
		return huma.Error503ServiceUnavailable("authentication_unavailable")
	case errors.Is(err, authnext.ErrInvalidCredentials), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrMissingBearer), errors.Is(err, authnext.ErrInvalidSession):
		return huma.Error401Unauthorized("unauthorized")
	case errors.Is(err, authnext.ErrSessionNotFound):
		return huma.Error404NotFound("session_not_found")
	case errors.Is(err, authnext.ErrInvalidPassword):
		return huma.Error422UnprocessableEntity("invalid_password")
	case errors.Is(err, authnext.ErrPasswordReused):
		return huma.Error422UnprocessableEntity("password_reused")
	case errors.Is(err, authnext.ErrInvalidMFA):
		return huma.Error422UnprocessableEntity("invalid_mfa")
	case errors.Is(err, authnext.ErrMFAEnrollment):
		return huma.Error422UnprocessableEntity("invalid_mfa_enrollment")
	case errors.Is(err, authnext.ErrMFAAlreadyOn):
		return huma.Error409Conflict("mfa_already_enabled")
	case errors.Is(err, authnext.ErrMFANotEnabled):
		return huma.Error409Conflict("mfa_not_enabled")
	case errors.Is(err, authnext.ErrRecoveryCooldown):
		return huma.Error409Conflict("recovery_cooldown_active")
	case errors.Is(err, authnext.ErrPasskeyChallenge):
		return huma.Error422UnprocessableEntity("invalid_passkey_challenge")
	case errors.Is(err, authnext.ErrPasskeyCredential):
		return huma.Error422UnprocessableEntity("invalid_passkey_credential")
	case errors.Is(err, authnext.ErrPasskeyAttestation):
		return huma.Error422UnprocessableEntity("passkey_attestation_rejected")
	case errors.Is(err, authnext.ErrPasskeyLimit):
		return huma.Error409Conflict("passkey_limit_reached")
	case errors.Is(err, authnext.ErrPasskeyNotFound):
		return huma.Error404NotFound("passkey_not_found")
	case errors.Is(err, authnext.ErrPasskeyDisabled):
		return huma.Error503ServiceUnavailable("passkey_unavailable")
	default:
		return huma.Error500InternalServerError("authentication_unavailable")
	}
}
