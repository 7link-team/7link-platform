package authn

import (
	"context"
	"errors"
	"slices"
	"strings"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
)

// WithFieldPermissionChecker supplies the shared tenant policy evaluator.
func WithFieldPermissionChecker(checker authz.FieldPermissionChecker) WebOption {
	return func(service *Service) { service.fields = checker }
}

// ProjectProfile projects authenticated claims without modifying the raw identity
// used by session validation and internal provisioning. An unscoped session has
// no authority to expose tenant-controlled attributes.
func (s *Service) ProjectProfile(ctx context.Context, claims *authnext.Claims, scope string) (ProfileClaims, error) {
	view := ProfileClaims{}
	if claims == nil {
		return view, authnext.ErrInvalidCredentials
	}
	scopes := strings.Fields(scope)
	profile, email := slices.Contains(scopes, "profile"), slices.Contains(scopes, "email")
	if claims.OrganizationID == "" || (!profile && !email) {
		return view, nil
	}
	tenantID, err := guid.Parse(claims.OrganizationID)
	if err != nil || tenantID <= 0 || (claims.TenantID != 0 && claims.TenantID != tenantID) {
		return view, authz.ErrFieldDenied
	}
	if s == nil {
		return view, authz.ErrFieldsUnavailable
	}
	fields, err := authz.ProfileConstraint(ctx, s.fields, tenantID, claims.PrincipalID)
	if err != nil {
		return view, err
	}
	if claims.Raw != nil {
		// Userinfo and bearer session views use current profile values, not an
		// old token's omitted or masked values after a policy change.
		if s.repo == nil {
			return view, authz.ErrFieldsUnavailable
		}
		claims, err = s.claims(ctx, claims.PrincipalID)
		if err != nil {
			if errors.Is(err, authnext.ErrInvalidSession) {
				return view, err
			}
			return view, errors.Join(authz.ErrFieldsUnavailable, err)
		}
	}
	if profile {
		view.PreferredUsername = fields.ReadString("login_name", claims.PreferredUsername)
	}
	if email {
		view.Email = fields.ReadString("email", claims.Email)
		view.EmailVerified = fields.Verified("email", claims.EmailVerified)
		// The number travels with the address: both are contacts the same scope
		// asks for, and both answer to the same field policy.
		view.Phone = fields.ReadString("phone", claims.Phone)
		view.PhoneVerified = fields.Verified("phone", claims.PhoneVerified)
	}
	return view, nil
}

func (s *Service) tokenProfile(ctx context.Context, payload map[string]any, principalID guid.ID, tenantID, scope, username, email string, verified bool) error {
	view, err := s.ProjectProfile(ctx, &authnext.Claims{
		PrincipalID: principalID, OrganizationID: tenantID,
		PreferredUsername: username, Email: email, EmailVerified: verified,
	}, scope)
	if err != nil {
		return err
	}
	if view.PreferredUsername != nil {
		payload["preferred_username"] = *view.PreferredUsername
	}
	if view.Email != nil {
		payload["email"] = *view.Email
	}
	if view.EmailVerified != nil {
		payload["email_verified"] = *view.EmailVerified
	}
	return nil
}

func sessionView(ctx context.Context, service *Service, in *meInput, claims *authnext.Claims) (meOutput, error) {
	scope := "profile email"
	if strings.TrimSpace(in.Authorization) != "" && (claims.AccountView == nil || claims.AccountView.ScopeType != "personal") {
		scope, _ = claims.Raw["scope"].(string)
	}
	profile, err := service.ProjectProfile(ctx, claims, scope)
	if err != nil {
		return meOutput{}, authenticationError(err)
	}
	view := meOutput{
		Subject: claims.Subject, Issuer: claims.Issuer,
		PreferredUsername: profile.PreferredUsername, Email: profile.Email, EmailVerified: profile.EmailVerified,
		Phone: profile.Phone, PhoneVerified: profile.PhoneVerified,
		OrganizationID: claims.OrganizationID, ACR: claims.ACR, AMR: claims.AMR,
	}
	if !claims.AuthTime.IsZero() {
		authTime := claims.AuthTime.UTC()
		view.AuthTime = &authTime
	}
	return view, nil
}
