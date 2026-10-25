package identity

import (
	"context"
	"net/http"
	"strings"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// SelfProfileView contains only the caller's editable contact/profile facts.
type SelfProfileView struct {
	ID            guid.ID `json:"id"`
	DisplayName   string  `json:"display_name"`
	Email         string  `json:"email"`
	Phone         string  `json:"phone"`
	EmailVerified bool    `json:"email_verified"`
	PhoneVerified bool    `json:"phone_verified"`
}

type SelfProfilePatch struct {
	DisplayName *string `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
	Email       *string `json:"email,omitempty" maxLength:"320"`
	Phone       *string `json:"phone,omitempty" maxLength:"32"`
}

type selfProfileInput struct {
	ClosureBrowserInput
	Body SelfProfilePatch
}

func selfProfileView(p Principal) SelfProfileView {
	return SelfProfileView{ID: p.ID, DisplayName: p.DisplayName, Email: p.Email, Phone: p.Phone, EmailVerified: p.EmailVerified, PhoneVerified: p.PhoneVerified}
}

func registerProfileHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	authz.RegisterTenantMember(registrar, api, huma.Operation{OperationID: "identity-human-contacts", Method: http.MethodGet, Path: "/human-contacts", Summary: "Find active Human contacts in your tenant", Tags: []string{"tenants"}, Errors: []int{401, 403, 422, 500, 503}}, func(ctx context.Context, in *humanContactsInput) (*respx.Body[HumanContactsPage], error) {
		page, err := s.humanContacts(ctx, authnext.TenantIDFromContext(ctx), authnext.PrincipalIDFromContext(ctx), *in)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, page), nil
	})
	const path = "/principals/{principal_id}/profile"
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-my-profile", Method: http.MethodGet, Path: path, Summary: "Read my editable profile and contact details", Tags: []string{"account"}, Errors: []int{401, 403, 404, 500, 503}}, func(ctx context.Context, in *ClosureBrowserInput) (*respx.Body[SelfProfileView], error) {
		p, err := s.GetGlobal(ctx, authnext.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, selfProfileView(p)), nil
	})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-update-my-profile", Method: http.MethodPatch, Path: path, Summary: "Update my nickname and contact details using existing credential revocation rules", Tags: []string{"account"}, Errors: []int{401, 403, 404, 422, 500, 503}}, func(ctx context.Context, in *selfProfileInput) (*respx.Body[SelfProfileView], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodPatch)
		if err != nil {
			return nil, err
		}
		p, err := s.UpdateGlobal(ctx, id, in.Body.DisplayName, in.Body.Email, in.Body.Phone)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, selfProfileView(p)), nil
	})
	if !registrar.IsDeclarationOnly() {
		// An invalid contract is a registration error, just like an invalid Huma
		// operation; never publish a form whose mutation schema is unavailable.
		if err := authz.RegisterRequestContract(api, path); err != nil {
			panic(err)
		}
	}
}

type humanContactsInput struct {
	Search string  `query:"search" maxLength:"128"`
	After  guid.ID `query:"after"`
	Limit  int     `query:"limit" default:"30" minimum:"1" maximum:"100"`
}

// HumanContact deliberately omits login, email, telephone and credential facts.
type HumanContact struct {
	ID          guid.ID `json:"id" gorm:"column:id"`
	DisplayName string  `json:"display_name" gorm:"column:display_name"`
	Avatar      string  `json:"avatar" gorm:"column:avatar"`
}

type HumanContactsPage struct {
	Items     []HumanContact `json:"items"`
	HasMore   bool           `json:"has_more"`
	NextAfter guid.ID        `json:"next_after,omitempty"`
}

func (s *Service) humanContacts(ctx context.Context, tenant, actor guid.ID, in humanContactsInput) (HumanContactsPage, error) {
	page := HumanContactsPage{Items: []HumanContact{}}
	if tenant <= 0 || actor <= 0 || in.After < 0 || in.Limit < 1 || in.Limit > 100 || len(in.Search) > 512 {
		return page, ErrInvalid
	}
	if s == nil || s.repo == nil || s.repo.foreign == nil || !s.repo.foreign.access.Bound() || !s.repo.foreign.directory.Bound() {
		return page, ErrUnavailable
	}
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		members := repo.foreign.access.TenantMembers.String()
		principals := tables.Principals.TableName()
		accounts := tables.ServiceAccounts.TableName()
		active := func(id guid.ID) *gorm.DB {
			return db.WithContext(ctx).Table(members+" AS member").
				Joins("JOIN "+principals+" AS principal ON principal.id = member.principal_id").
				Joins("JOIN "+repo.foreign.directory.Tenants.String()+" AS tenant ON tenant.id = member.tenant_id").
				Where("member.tenant_id = ? AND member.principal_id = ? AND member.status = ? AND principal.status = ? AND tenant.status = ?", tenant, id, "active", "active", "active").
				Where("NOT EXISTS (?)", db.Table(accounts+" AS service_account").Select("1").Where("service_account.principal_id = principal.id"))
		}
		var count int64
		if err = active(actor).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrProfileDenied
		}
		q := db.WithContext(ctx).Table(principals+" AS principal").Select("principal.id, principal.display_name, principal.avatar").
			Joins("JOIN "+members+" AS member ON member.principal_id = principal.id").
			Where("member.tenant_id = ? AND member.status = ? AND principal.status = ? AND principal.id <> ? AND principal.id > ?", tenant, "active", "active", actor, in.After).
			Where("NOT EXISTS (?)", db.Table(accounts+" AS service_account").Select("1").Where("service_account.principal_id = principal.id"))
		if search := strings.TrimSpace(in.Search); search != "" {
			q = q.Where("LOWER(principal.display_name) LIKE ?", "%"+strings.ToLower(search)+"%")
		}
		if err = q.Order("principal.id").Limit(in.Limit + 1).Scan(&page.Items).Error; err != nil {
			return err
		}
		if len(page.Items) > in.Limit {
			page.Items = page.Items[:in.Limit]
			page.HasMore = true
			page.NextAfter = page.Items[len(page.Items)-1].ID
		}
		return nil
	})
	if err != nil {
		return HumanContactsPage{}, err
	}
	return page, nil
}
