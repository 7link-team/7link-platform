package identity

import (
	"context"
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
)

// PrincipalView is a public projection. Principal remains the raw transactional
// identity contract used for authentication and provisioning.
type PrincipalView struct {
	ID                 guid.ID   `json:"id"`
	LoginName          *string   `json:"login_name,omitempty"`
	DisplayName        *string   `json:"display_name,omitempty"`
	Email              *string   `json:"email,omitempty"`
	EmailVerified      *bool     `json:"email_verified,omitempty"`
	ActivationRequired bool      `json:"activation_required"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (s *Service) profileConstraint(ctx context.Context, tenantID guid.ID) (authz.FieldConstraint, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID != tenantID {
		return authz.FieldConstraint{}, authz.ErrFieldDenied
	}
	return authz.ProfileConstraint(ctx, s.fields, tenantID, claims.PrincipalID)
}

func profileView(principal Principal, fields authz.FieldConstraint) PrincipalView {
	return PrincipalView{
		ID: principal.ID, LoginName: fields.ReadString("login_name", principal.LoginName),
		DisplayName: fields.ReadString("display_name", principal.DisplayName),
		Email:       fields.ReadString("email", principal.Email), EmailVerified: fields.EmailVerified(principal.EmailVerified),
		ActivationRequired: principal.ActivationRequired, Status: principal.Status,
		CreatedAt: principal.CreatedAt, UpdatedAt: principal.UpdatedAt,
	}
}

func requireProfileWrites(raw []byte, fields authz.FieldConstraint, create bool) error {
	var submitted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &submitted); err != nil {
		return ErrInvalid
	}
	for _, name := range []string{"login_name", "display_name", "email"} {
		if _, present := submitted[name]; present {
			if err := fields.RequireWrite(create, name); err != nil {
				return err
			}
		}
	}
	return nil
}
