package authn

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	authni18n "7link.com/platform/internal/iam/authn/i18n"
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
	Phone             *string    `json:"phone,omitempty"`
	PhoneVerified     *bool      `json:"phone_verified,omitempty"`
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
	personalView := claims.AccountView != nil && claims.AccountView.ScopeType == "personal"
	if strings.TrimSpace(in.Authorization) != "" && !personalView {
		return claims, nil
	}
	if web == nil || web.memberTenant == nil {
		return nil, huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
	}
	tenantID, err := web.memberTenant(ctx, claims.PrincipalID, 0)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
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
		// The challenge is presented on every attempt rather than after some
		// number of failures, because a progressive challenge would need a
		// pre-authentication per-address failure counter, and the only counter
		// this module has is the one shared_security keeps in Redis -- absent
		// in exactly the deployments least able to absorb credential stuffing.
		CaptchaType   string `json:"captcha_type" minLength:"1"`
		CaptchaID     string `json:"captcha_id" minLength:"1"`
		CaptchaAnswer string `json:"captcha_answer" minLength:"1"`
	}
}

type registrationInput struct {
	Origin string `header:"Origin" hidden:"true"`
	Body   struct {
		Entry         string `json:"entry,omitempty" enum:"user,tenant" doc:"Public entrance to resume after email verification"`
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
	// Captcha tells a client what to render. Without it the client has to guess
	// -- and the one that guessed "an image, always" spun asking for challenge
	// kinds it could draw until it gave up.
	Captcha CaptchaCapability `json:"captcha"`
}

type sessionListInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
}

type sessionMutationInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	ID            string `path:"session_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
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
		Entry      string `json:"entry,omitempty" enum:"user,tenant" doc:"Public entrance to resume after password recovery"`
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

type phoneVerificationStartInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
}

// phoneVerificationCompleteInput carries the code and nothing else. The session
// says whose number this is, so no request, log or URL has to hold the number.
type phoneVerificationCompleteInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		Code string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
	}
}

type emailVerificationCredential struct {
	Token    string `json:"token,omitempty"`
	Email    string `json:"email,omitempty"`
	Code     string `json:"code,omitempty"`
	Password string `json:"password,omitempty"`
}

func (emailVerificationCredential) Schema(huma.Registry) *huma.Schema {
	one, six, tokenMax, emailMax, passwordMax := 1, 6, 128, 320, 1024
	properties := map[string]*huma.Schema{
		"token": {Type: huma.TypeString, MinLength: &one, MaxLength: &tokenMax},
		"email": {Type: huma.TypeString, Format: "email", MinLength: &one, MaxLength: &emailMax},
		"code":  {Type: huma.TypeString, MinLength: &six, MaxLength: &six, Pattern: "^[0-9]{6}$"},
		"password": {Type: huma.TypeString, MinLength: &one, MaxLength: &passwordMax,
			Description: "Password of the registration being confirmed; required to activate a pending self-registration"},
	}
	// Each credential form may also carry password, and nothing else.
	form := func(required ...string) *huma.Schema {
		allowed := map[string]*huma.Schema{"password": properties["password"]}
		for _, name := range required {
			allowed[name] = properties[name]
		}
		return &huma.Schema{Type: huma.TypeObject, Required: required, Properties: allowed, AdditionalProperties: false}
	}
	return &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: false,
		Properties:           properties,
		OneOf:                []*huma.Schema{form("token"), form("email", "code")},
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
	PrincipalID   string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	Body          struct {
		CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
	}
}

type mfaCodeInput struct {
	PrincipalID   string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
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

// aliasWebAuthnExtensions describes extension inputs and outputs as the open
// objects they are on the wire. Both types carry the members the library does
// not model in a field their custom JSON codecs merge into the top level, so
// the closed schema reflected from their fields would refuse what a client
// legitimately forwards from getClientExtensionResults().
func aliasWebAuthnExtensions(registry huma.Registry) {
	open := reflect.TypeFor[map[string]any]()
	registry.RegisterTypeAlias(reflect.TypeFor[protocol.AuthenticationExtensions](), open)
	registry.RegisterTypeAlias(reflect.TypeFor[protocol.AuthenticationExtensionsClientOutputs](), open)
}

type webAuthnAssertion json.RawMessage

func (webAuthnAssertion) Schema(registry huma.Registry) *huma.Schema {
	aliasWebAuthnExtensions(registry)
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
	aliasWebAuthnExtensions(registry)
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
	aliasWebAuthnExtensions(registry)
	return registry.Schema(reflect.TypeFor[protocol.CredentialAssertion](), true, "WebAuthnAssertionOptions")
}

func (value webAuthnAssertionOptions) MarshalJSON() ([]byte, error) {
	return json.RawMessage(value).MarshalJSON()
}

type webAuthnCreationOptions json.RawMessage

func (webAuthnCreationOptions) Schema(registry huma.Registry) *huma.Schema {
	aliasWebAuthnExtensions(registry)
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
	ID            string `path:"passkey_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body          struct {
		Name string `json:"name" minLength:"1" maxLength:"200"`
	}
}

type passkeyDeleteInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Origin        string `header:"Origin" hidden:"true"`
	ID            string `path:"passkey_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
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

// captchaInvalidCode is the business code of a rejected captcha, which shares
// its 422 with rejected registration data; the registration page tells the two
// apart by code. Login reports it the same way, so a client can tell "solve the
// challenge again" from "those credentials are wrong" -- which arrives as 401
// -- and refresh only the challenge. The authn module owns 101000-101999.
const captchaInvalidCode = 101001

// refuseUnansweredCaptcha turns a verification result into the refusal a client
// can act on. A challenge that was not solved is the caller's to fix and says
// so; one that could not be checked is this deployment's problem and says that
// instead, because telling somebody who solved it that they got it wrong sends
// them round a loop they cannot win. Either way the attempt is refused: a
// captcha fails closed.
func refuseUnansweredCaptcha(ctx context.Context, solved bool, err error) error {
	// A status somebody already chose is theirs to keep. Nothing reaching here
	// today carries one, and the guard is what keeps that true: this function
	// decides between two statuses, and a third arriving from below must not be
	// flattened into either of them.
	if respx.IsStatusError(err) {
		return err
	}
	if err != nil {
		slog.Warn("captcha verification unavailable", "error", err)
		return huma.Error503ServiceUnavailable(authni18n.CaptchaUnavailable)
	}
	if !solved {
		return respx.ErrStatus(ctx, http.StatusUnprocessableEntity, captchaInvalidCode, authni18n.CaptchaInvalid, nil)
	}
	return nil
}

// RegisterHTTP registers the authn module's HTTP operations.
func RegisterHTTP(a huma.API, authenticator authz.TokenVerifier, web *Service) {
	registerAccountViewBoundary(a)
	web.registerSharedSecurity(a)
	defer documentSharedSecurity(a)
	authenticated := authz.NewAuthenticationOnlyRegistrar(authenticator)
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Return the authenticated local principal",
		Tags:        []string{"account"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *meInput) (*respx.Body[meOutput], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		claims, err := sessionTenant(ctx, web, in, claims)
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
	registerAccountViewHTTP(a, web)
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-login-options", Method: http.MethodGet, Path: "/login/options",
		Summary: "Return enabled public authentication methods", Tags: []string{"authentication"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[AuthenticationCapabilities], error) {
		capabilities := web.Capabilities()
		return respx.OK(ctx, AuthenticationCapabilities{
			Registration: capabilities.Registration, PasswordRecovery: capabilities.PasswordRecovery,
			Passkey: capabilities.Passkey, Captcha: web.CaptchaCapability(),
		}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-login-captcha", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/login/captchas",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Issue a graphic captcha for login", Tags: []string{"authentication"},
		Errors: []int{http.StatusInternalServerError},
	}, func(ctx context.Context, _ *captchaInput) (*respx.Body[captchaOutput], error) {
		out, err := web.IssueCaptcha()
		if err != nil {
			return nil, huma.Error500InternalServerError(authni18n.CaptchaUnavailable)
		}
		return respx.OK(ctx, *out), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-registration-captcha", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/registrations/captchas",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Issue a graphic captcha for registration", Tags: []string{"authentication"},
		Errors: []int{http.StatusInternalServerError},
	}, func(ctx context.Context, _ *captchaInput) (*respx.Body[captchaOutput], error) {
		out, err := web.IssueCaptcha()
		if err != nil {
			return nil, huma.Error500InternalServerError(authni18n.CaptchaUnavailable)
		}
		return respx.OK(ctx, *out), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-registration", Method: http.MethodPost, Path: "/registrations",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Register a global principal pending email verification", Tags: []string{"authentication"}, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, func(ctx context.Context, in *registrationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.RegistrationRequestRejected)
		}
		solved, captchaErr := web.VerifyCaptcha(ctx, in.Body.CaptchaType, in.Body.CaptchaID, in.Body.CaptchaAnswer)
		if refusal := refuseUnansweredCaptcha(ctx, solved, captchaErr); refusal != nil {
			return nil, refusal
		}
		if err := web.RegisterForEntry(ctx, in.Body.Email, in.Body.Password, in.Body.DisplayName, in.Body.Entry); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidRegistration):
				return nil, huma.Error422UnprocessableEntity(authni18n.InvalidRegistration)
			case errors.Is(err, authnext.ErrRegistrationDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.RegistrationUnavailable)
			case errors.Is(err, authnext.ErrRegistrationConflict):
				return respx.OK(ctx, map[string]bool{"accepted": true}), nil
			default:
				return nil, huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-session", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/login",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Log in with a local username and password", Tags: []string{"authentication"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, func(ctx context.Context, in *loginInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.LoginRequestRejected)
		}
		solved, captchaErr := web.VerifyCaptcha(ctx, in.Body.CaptchaType, in.Body.CaptchaID, in.Body.CaptchaAnswer)
		if refusal := refuseUnansweredCaptcha(ctx, solved, captchaErr); refusal != nil {
			return nil, refusal
		}
		result, err := web.BeginLogin(ctx, in.Body.LoginName, in.Body.Password, in.Body.ReturnURL)
		if err != nil {
			switch {
			case errors.Is(err, authnext.ErrReturnURL):
				return nil, huma.Error422UnprocessableEntity(authni18n.ReturnURLNotAllowed)
			case errors.Is(err, authnext.ErrAdditionalVerification):
				return nil, huma.Error409Conflict(authni18n.AdditionalVerificationRequired)
			case errors.Is(err, authnext.ErrInvalidCredentials):
				return nil, huma.Error401Unauthorized(authni18n.InvalidLogin)
			default:
				return nil, huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
			}
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-session-verification", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/login/mfa",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Complete an MFA login challenge", Tags: []string{"authentication"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
	}, func(ctx context.Context, in *mfaLoginInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.LoginRequestRejected)
		}
		result, err := web.VerifyLoginMFA(ctx, in.Body.ChallengeID, in.Body.Code)
		if err != nil {
			if errors.Is(err, authnext.ErrInvalidMFA) || errors.Is(err, authnext.ErrMFAChallenge) {
				return nil, huma.Error401Unauthorized(authni18n.InvalidMFAChallenge)
			}
			return nil, huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-passkey-login-options", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/login/passkey/options",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Create WebAuthn authentication options for passkey login", Tags: []string{"authentication"}, Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyLoginBeginInput) (*respx.Body[passkeyAssertionOptions], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.PasskeyRequestRejected)
		}
		options, err := web.BeginPasskeyLogin(ctx, in.Origin, in.Body.ReturnURL)
		if err != nil {
			switch {
			case errors.Is(err, ErrCSRF):
				return nil, huma.Error403Forbidden(authni18n.PasskeyRequestRejected)
			case errors.Is(err, authnext.ErrReturnURL):
				return nil, huma.Error422UnprocessableEntity(authni18n.ReturnURLNotAllowed)
			case errors.Is(err, authnext.ErrPasskeyDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.PasskeyUnavailable)
			default:
				return nil, huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
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
		Summary:  "Verify a WebAuthn assertion and log in", Tags: []string{"authentication"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyFinishInput) (*loginOutput, error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.PasskeyRequestRejected)
		}
		result, err := web.FinishPasskeyLogin(
			ctx, in.Origin, in.Body.ChallengeID, json.RawMessage(in.Body.Credential),
		)
		if err != nil {
			switch {
			case errors.Is(err, authnext.ErrPasskeyChallenge), errors.Is(err, authnext.ErrPasskeyCredential):
				return nil, huma.Error401Unauthorized(authni18n.InvalidPasskeyChallenge)
			case errors.Is(err, authnext.ErrPasskeyDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.PasskeyUnavailable)
			default:
				return nil, huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
			}
		}
		return loginResponse(ctx, web, result), nil
	})
	authz.RegisterSessionAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-get-current-session", Method: http.MethodGet, Path: "/sessions/current",
		Summary: "Return the current browser session", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError},
	}, func(ctx context.Context, in *meInput) (*respx.Body[authnext.BrowserSession], error) {
		if strings.TrimSpace(in.Authorization) != "" || currentSessionHash(in.Cookie, web.web.CookieName) == "" {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		output, err := web.CurrentSession(ctx, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, output), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-get-session", Method: http.MethodGet, Path: "/sessions/{session_id}",
		Summary: "Read an active browser session owned by the caller", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, func(ctx context.Context, in *sessionMutationInput) (*respx.Body[authnext.BrowserSession], error) {
		output, err := web.GetSession(ctx, in.Authorization, in.Cookie, in.ID)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, output), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-delete-current-session", Method: http.MethodDelete, Path: "/sessions/current",
		Summary: "Destroy the current browser session", Tags: []string{"account"},
		// Public so that logging out with an expired cookie still clears it, but
		// the cookie is what names the session to destroy.
		Security: []map[string][]string{{authz.SessionScheme: {}}},
		Errors:   []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		if strings.TrimSpace(in.Authorization) != "" {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		logoutURL, err := web.Logout(ctx, in.Cookie)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
		}
		return &logoutOutput{SetCookie: web.ClearCookie(), Body: respx.OK(ctx, logoutData{LogoutURL: logoutURL}).Body}, nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-list-sessions", Method: http.MethodGet, Path: "/principals/{principal_id}/sessions",
		Summary: "List active browser sessions", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError},
	}, func(ctx context.Context, in *sessionListInput) (*respx.Body[[]authnext.BrowserSession], error) {
		sessions, err := web.ListSessions(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, sessions), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-session", Method: http.MethodDelete, Path: "/sessions/{session_id}",
		Summary: "Revoke a browser session", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
	}, func(ctx context.Context, in *sessionMutationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.RevokeSession(ctx, in.Authorization, in.Cookie, in.ID); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"revoked": true}), nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-sessions", Method: http.MethodDelete, Path: "/principals/{principal_id}/sessions",
		Summary: "Revoke every session and refresh token", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.LogoutAll(ctx, in.Authorization, in.Cookie); err != nil {
			return nil, authenticationError(err)
		}
		return &logoutOutput{SetCookie: web.ClearCookie(), Body: respx.OK(ctx, logoutData{LogoutURL: web.PostLogoutURL()}).Body}, nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-update-password", Method: http.MethodPatch, Path: "/principals/{principal_id}/password",
		Summary: "Change the current principal password", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, func(ctx context.Context, in *passwordChangeInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPatch, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.ChangePassword(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.NewPassword); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"changed": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-password-recovery-request", Method: http.MethodPost, Path: "/password-recoveries/requests",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Queue a password recovery notification", Tags: []string{"authentication"}, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passwordRecoveryStartInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.RecoveryRequestRejected)
		}
		if err := web.BeginPasswordRecoveryForEntry(ctx, in.Body.Identifier, in.Body.Entry); err != nil {
			switch {
			case errors.Is(err, authnext.ErrRecoveryDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.PasswordRecoveryUnavailable)
			case errors.Is(err, authnext.ErrInvalidRecovery):
				return nil, huma.Error422UnprocessableEntity(authni18n.InvalidRecoveryRequest)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.PasswordRecoveryUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-password-recovery", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/password-recoveries",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Consume a password recovery credential", Tags: []string{"authentication"},
		Errors: []int{http.StatusBadRequest, http.StatusConflict, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passwordRecoveryCompleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.RecoveryRequestRejected)
		}
		if err := web.CompletePasswordRecovery(ctx, in.Body.Token, in.Body.NewPassword); err != nil {
			switch {
			case errors.Is(err, authnext.ErrRecoveryDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.PasswordRecoveryUnavailable)
			case errors.Is(err, authnext.ErrPasswordReused):
				return nil, huma.Error409Conflict(authni18n.PasswordReused)
			case errors.Is(err, authnext.ErrInvalidRecovery):
				return nil, huma.Error400BadRequest(authni18n.InvalidRecoveryCredential)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.PasswordRecoveryUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"changed": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-email-verification-request", Method: http.MethodPost, Path: "/email-verifications/requests",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Queue verification for the authenticated principal email", Tags: []string{"authentication"},
		DefaultStatus: http.StatusAccepted,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *emailVerificationStartInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.BeginEmailVerification(ctx, in.Authorization, in.Cookie); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidSession), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrInvalidBearer):
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			case errors.Is(err, authnext.ErrEmailRequired):
				return nil, huma.Error409Conflict(authni18n.PrimaryEmailRequired)
			case errors.Is(err, authnext.ErrEmailVerificationDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.EmailVerificationUnavailable)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.EmailVerificationUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterPublic(a, huma.Operation{
		OperationID: "authn-create-email-verification", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/email-verifications",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Consume a primary email verification credential", Tags: []string{"authentication"},
		Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *emailVerificationCompleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateLoginOrigin(in.Origin); err != nil {
			return nil, huma.Error403Forbidden(authni18n.EmailVerificationRequestRejected)
		}
		if err := web.CompleteEmailVerification(ctx, in.Body.Token, in.Body.Email, in.Body.Code, in.Body.Password); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidEmailVerification):
				return nil, huma.Error400BadRequest(authni18n.InvalidEmailVerificationCredential)
			case errors.Is(err, authnext.ErrEmailVerificationDisabled):
				return nil, huma.Error503ServiceUnavailable(authni18n.EmailVerificationUnavailable)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.EmailVerificationUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"verified": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-phone-verification-request", Method: http.MethodPost,
		Path:     "/phone-verifications/requests",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Queue a verification code for the authenticated principal phone number",
		Tags:     []string{"authentication"}, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *phoneVerificationStartInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.BeginPhoneVerification(ctx, in.Authorization, in.Cookie); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidSession), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrInvalidBearer):
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			case errors.Is(err, authnext.ErrPhoneRequired):
				return nil, huma.Error409Conflict(authni18n.PrimaryPhoneRequired)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.PhoneVerificationUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"accepted": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-phone-verification", Method: http.MethodPost,
		DefaultStatus: http.StatusCreated, Path: "/phone-verifications",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Consume a phone verification code", Tags: []string{"authentication"},
		Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *phoneVerificationCompleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.CompletePhoneVerification(ctx, in.Authorization, in.Cookie, in.Body.Code); err != nil {
			switch {
			case errors.Is(err, authnext.ErrInvalidSession), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrInvalidBearer):
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			case errors.Is(err, authnext.ErrInvalidPhoneVerification):
				return nil, huma.Error400BadRequest(authni18n.InvalidPhoneVerificationCode)
			default:
				return nil, huma.Error503ServiceUnavailable(authni18n.PhoneVerificationUnavailable)
			}
		}
		return respx.OK(ctx, map[string]bool{"verified": true}), nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-get-mfa", Method: http.MethodGet, Path: "/principals/{principal_id}/mfa", Summary: "Return the current principal MFA status", Tags: []string{"account"}, Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaStatusInput) (*respx.Body[authnext.MFAStatus], error) {
		status, err := web.MFAStatus(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, status), nil
	})
	authz.RegisterPublic(a, huma.Operation{OperationID: "authn-create-totp-enrollment", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/principals/{principal_id}/mfa/totp-enrollments", Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication), Summary: "Begin TOTP enrollment after password verification", Tags: []string{"account"}, Security: authz.UserSecurity(), Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusConflict, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaPasswordInput) (*respx.Body[authnext.MFAEnrollment], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := enrollmentPrincipalScope(ctx, web, in.Authorization, in.Cookie, in.PrincipalID); err != nil {
			return nil, err
		}
		enrollment, err := web.BeginTOTPEnrollment(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, enrollment), nil
	})
	authz.RegisterPublic(a, huma.Operation{OperationID: "authn-create-totp-factor", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/principals/{principal_id}/mfa/totp", Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication), Summary: "Confirm TOTP enrollment and issue recovery codes", Tags: []string{"account"}, Security: authz.UserSecurity(), Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaCodeInput) (*mfaConfirmationOutput, error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := enrollmentPrincipalScope(ctx, web, in.Authorization, in.Cookie, in.PrincipalID); err != nil {
			return nil, err
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
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-replace-recovery-codes", Method: http.MethodPut, Path: "/principals/{principal_id}/mfa/recovery-codes", Summary: "Replace all MFA recovery codes", Tags: []string{"account"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaVerificationInput) (*respx.Body[authnext.MFAConfirmation], error) {
		if err := web.ValidateCSRF(http.MethodPut, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		confirmation, err := web.RegenerateRecoveryCodes(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.Code)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, confirmation), nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{OperationID: "authn-delete-totp-factor", Method: http.MethodDelete, Path: "/principals/{principal_id}/mfa/totp", Summary: "Disable TOTP after password and factor verification", Tags: []string{"account"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError}}, func(ctx context.Context, in *mfaVerificationInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.DisableTOTP(ctx, in.Authorization, in.Cookie, in.Body.CurrentPassword, in.Body.Code); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"disabled": true}), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-step-up-challenge", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/sessions/current/step-up-challenges",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Begin a step-up MFA challenge for the current browser session", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *stepUpOptionsInput) (*respx.Body[authnext.StepUpOptions], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		options, err := web.BeginStepUp(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, options), nil
	})
	authz.RegisterAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-step-up-verification", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/sessions/current/step-up-verifications",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Verify a step-up factor and elevate the browser session", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *stepUpVerifyInput) (*stepUpOutput, error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		result, err := web.VerifyStepUp(ctx, in.Authorization, in.Cookie, in.Body.ChallengeID, in.Body.Code)
		if err != nil {
			return nil, authenticationError(err)
		}
		return &stepUpOutput{SetCookie: web.SessionCookie(result.SessionID), Body: respx.OK(ctx, result).Body}, nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-list-passkeys", Method: http.MethodGet, Path: "/principals/{principal_id}/passkeys",
		Summary: "List the current principal passkeys", Tags: []string{"account"}, Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *sessionListInput) (*respx.Body[[]authnext.Passkey], error) {
		passkeys, err := web.ListPasskeys(ctx, in.Authorization, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkeys), nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-passkey-registration-challenge", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/principals/{principal_id}/passkey-registration-challenges",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Begin passkey registration after password verification", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *mfaPasswordInput) (*respx.Body[passkeyCreationOptions], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
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
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-create-passkey", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/principals/{principal_id}/passkeys",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Summary:  "Verify and store a new passkey", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyRegistrationFinishInput) (*respx.Body[authnext.Passkey], error) {
		if err := web.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
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
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-update-passkey", Method: http.MethodPatch, Path: "/principals/{principal_id}/passkeys/{passkey_id}",
		Summary: "Rename a passkey", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyMutationInput) (*respx.Body[authnext.Passkey], error) {
		if err := web.ValidateCSRF(http.MethodPatch, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		passkey, err := web.RenamePasskey(ctx, in.Authorization, in.Cookie, in.ID, in.Body.Name)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, passkey), nil
	})
	authz.RegisterPrincipalAuthenticated(authenticated, a, huma.Operation{
		OperationID: "authn-delete-passkey", Method: http.MethodDelete, Path: "/principals/{principal_id}/passkeys/{passkey_id}",
		Summary: "Delete a passkey after password verification", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *passkeyDeleteInput) (*respx.Body[map[string]bool], error) {
		if err := web.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, huma.Error403Forbidden(respx.CSRFRejected)
		}
		if err := web.DeletePasskey(ctx, in.Authorization, in.Cookie, in.ID, in.Body.CurrentPassword); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"deleted": true}), nil
	})
}

func enrollmentPrincipalScope(ctx context.Context, web *Service, authorization, cookie, principalID string) error {
	claims, err := web.authenticateMFAEnrollment(ctx, authorization, cookie)
	if err != nil {
		return authenticationError(err)
	}
	return authz.PrincipalScopeError(claims, principalID)
}

func loginResponse(ctx context.Context, web *Service, result authnext.LoginResult) *loginOutput {
	output := &loginOutput{Body: respx.OK(ctx, result).Body}
	if result.SessionID != "" {
		output.SetCookie = web.SessionCookie(result.SessionID)
	}
	return output
}

func authenticationError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	switch {
	case errors.Is(err, authz.ErrFieldDenied):
		return huma.Error403Forbidden(respx.Forbidden)
	case errors.Is(err, authz.ErrFieldsUnavailable):
		return huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
	case errors.Is(err, authnext.ErrInvalidCredentials), errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrMissingBearer), errors.Is(err, authnext.ErrInvalidSession):
		return huma.Error401Unauthorized(respx.Unauthorized)
	case errors.Is(err, authnext.ErrSessionNotFound):
		return huma.Error404NotFound(authni18n.SessionNotFound)
	case errors.Is(err, authnext.ErrInvalidPassword):
		return huma.Error422UnprocessableEntity(authni18n.InvalidPassword)
	case errors.Is(err, authnext.ErrPasswordReused):
		return huma.Error422UnprocessableEntity(authni18n.PasswordReused)
	case errors.Is(err, authnext.ErrInvalidMFA):
		return huma.Error422UnprocessableEntity(authni18n.InvalidMFA)
	case errors.Is(err, authnext.ErrMFAEnrollment):
		return huma.Error422UnprocessableEntity(authni18n.InvalidMFAEnrollment)
	case errors.Is(err, authnext.ErrMFAAlreadyOn):
		return huma.Error409Conflict(authni18n.MFAAlreadyEnabled)
	case errors.Is(err, authnext.ErrMFANotEnabled):
		return huma.Error409Conflict(authni18n.MFANotEnabled)
	case errors.Is(err, authnext.ErrRecoveryCooldown):
		return huma.Error409Conflict(authni18n.RecoveryCooldownActive)
	case errors.Is(err, authnext.ErrPasskeyChallenge):
		return huma.Error422UnprocessableEntity(authni18n.InvalidPasskeyChallenge)
	case errors.Is(err, authnext.ErrPasskeyCredential):
		return huma.Error422UnprocessableEntity(authni18n.InvalidPasskeyCredential)
	case errors.Is(err, authnext.ErrPasskeyAttestation):
		return huma.Error422UnprocessableEntity(authni18n.PasskeyAttestationRejected)
	case errors.Is(err, authnext.ErrPasskeyLimit):
		return huma.Error409Conflict(authni18n.PasskeyLimitReached)
	case errors.Is(err, authnext.ErrPasskeyNotFound):
		return huma.Error404NotFound(authni18n.PasskeyNotFound)
	case errors.Is(err, authnext.ErrPasskeyDisabled):
		return huma.Error503ServiceUnavailable(authni18n.PasskeyUnavailable)
	default:
		return huma.Error500InternalServerError(authni18n.AuthenticationUnavailable)
	}
}
