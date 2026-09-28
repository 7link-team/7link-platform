package core

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type systemScopePageInput struct {
	OwnerID string       `path:"owner_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset  int          `query:"offset" minimum:"0" default:"0"`
	Limit   int          `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Search  string       `query:"search" maxLength:"128"`
	Status  MemberStatus `query:"status" enum:"active,disabled"`
}

type APISystemAccountOwner struct {
	ID     guid.ID `json:"id"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
}

type systemAccountOwnersInput struct {
	Category string `query:"category" enum:"tenant,platform" required:"true"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

func (s *Service) ListSystemAccountOwners(ctx context.Context, category string, offset, limit int) ([]APISystemAccountOwner, int64, error) {
	if (category != "tenant" && category != "platform") || offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidArgument
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	directory, err := repo.directoryTables()
	if err != nil {
		return nil, 0, err
	}
	query := repo.writer().WithContext(ctx).Table(directory.Tenants.String()).Where("scope_kind = ?", category)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]APISystemAccountOwner, 0)
	err = query.Select("id, name, status").Order("name ASC, id ASC").Offset(offset).Limit(limit).Scan(&items).Error
	return items, total, err
}

type SystemScopeMemberInput struct {
	OwnerID     string `path:"owner_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type systemScopeCreateInput struct {
	OwnerID string `path:"owner_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body    struct {
		Subject      string       `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		DisplayName  string       `json:"display_name" minLength:"1" maxLength:"128"`
		Email        string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       MemberStatus `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}
type systemScopeUpdateInput struct {
	SystemScopeMemberInput
	Body struct {
		DisplayName  *string       `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
		Email        *string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID *string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       *MemberStatus `json:"status,omitempty" enum:"active,disabled"`
	}
}
type systemScopeRoleMemberInput struct {
	SystemScopeMemberInput
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type systemScopeMemberRolesInput struct {
	SystemScopeMemberInput
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type systemScopeAdministratorEvaluator struct {
	AuthorizationEvaluator
	registry *authz.Registry
}

func (a systemScopeAdministratorEvaluator) CheckBulk(_ context.Context, _ guid.ID, codes []string, _ guid.ID) (map[string]bool, error) {
	allowed := make(map[string]bool, len(codes))
	for _, code := range codes {
		action, ok := a.registry.Find(code)
		allowed[code] = ok && action.Scope != authz.ActionScopeSystem && action.Scope != authz.ActionScopeOperatingPlatform
	}
	return allowed, nil
}

func (a systemScopeAdministratorEvaluator) CheckOperatingPlatform(_ context.Context, _ guid.ID, code string, _ guid.ID) (bool, error) {
	action, ok := a.registry.Find(code)
	return ok && authz.IsOperatingPlatformPermission(action), nil
}

func (s *Service) systemScopeOwner(ctx context.Context, raw, kind string) (guid.ID, error) {
	id, err := guid.RequireID(raw)
	if err != nil {
		return 0, err
	}
	actual, err := s.repo.rootKind(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrMemberNotFound
		}
		return 0, err
	}
	if actual != kind {
		return 0, ErrMemberNotFound
	}
	return id, nil
}

func (s *Service) systemScopeMutation(ctx context.Context, raw, kind string, subject guid.ID, mutate func(*Service, guid.ID) error) error {
	err := s.systemAccessMutation(ctx, subject, "platform_administer", func(tx *Service, _ guid.ID) error {
		owner, err := tx.systemScopeOwner(ctx, raw, kind)
		if err != nil {
			return err
		}
		if err := tx.repo.revisions.Lock(ctx, tx.repo.writer(), owner); err != nil {
			return err
		}
		full, err := systemFullAdministrator(ctx, tx.repo.writer(), tx.repo.names.PlatformAdministrators.String(), authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return err
		}
		if !full {
			return ErrPrivilegeEscalation
		}
		clone := *tx
		clone.checker = systemScopeAdministratorEvaluator{AuthorizationEvaluator: tx.checker, registry: tx.registry}
		clone.writes = newTransactionCoordinator(tx.repo, func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
			event.TenantID = 0
			if event.Detail == nil {
				event.Detail = map[string]any{}
			}
			event.Detail["scope_kind"], event.Detail["scope_id"] = kind, owner.String()
			return s.writes.audit(ctx, db, event)
		})
		return mutate(&clone, owner)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrMemberNotFound
	}
	return err
}

func registerSystemScopeHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "iam-system-account-owners", Method: http.MethodGet, Path: "/system/account-owners", Summary: "List owners for account management", Tags: []string{"system"}}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemAccountOwnersInput) (*respx.Body[[]APISystemAccountOwner], error) {
		items, total, err := svc.ListSystemAccountOwners(ctx, in.Category, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("account owners", err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	for _, scope := range []struct{ kind, segment string }{{"tenant", "tenants"}, {"platform", "operating-platforms"}} {
		kind, base := scope.kind, "/system/"+scope.segment+"/{owner_id}"
		op := func(name, method, suffix string) huma.Operation {
			return huma.Operation{OperationID: "iam-system-" + kind + "-" + name, Method: method, Path: base + suffix, Summary: "Manage scoped account memberships", Tags: []string{"system"}}
		}
		read := authz.Guard{Resource: "system_account", Verb: "view"}
		write := authz.Guard{Resource: "platform", Verb: "administer"}
		authz.RegisterPlatform(registrar, api, op("members", http.MethodGet, "/members"), read, func(ctx context.Context, in *systemScopePageInput) (*respx.Body[[]APITenantMember], error) {
			owner, err := svc.systemScopeOwner(ctx, in.OwnerID, kind)
			if err != nil {
				return nil, apiError("membership owner", err)
			}
			members, total, err := svc.ListTenantMembers(ctx, owner, MemberFilter{Offset: in.Offset, Limit: in.Limit, Search: in.Search, Status: in.Status})
			if err != nil {
				return nil, apiError("members", err)
			}
			out := make([]APITenantMember, 0, len(members))
			for _, member := range members {
				roles, err := svc.ListTenantMemberRoles(ctx, owner, member.PrincipalID)
				if err != nil {
					return nil, apiError("member roles", err)
				}
				item := memberFromDomain(member)
				item.RoleIDs = append([]guid.ID{}, roles...)
				out = append(out, item)
			}
			return respx.List(ctx, out, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})
		authz.RegisterPlatform(registrar, api, op("member", http.MethodGet, "/members/{principal_id}"), read, func(ctx context.Context, in *SystemScopeMemberInput) (*respx.Body[APITenantMember], error) {
			owner, err := svc.systemScopeOwner(ctx, in.OwnerID, kind)
			if err != nil {
				return nil, apiError("membership owner", err)
			}
			subject, err := guid.RequireID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			member, err := svc.GetTenantMember(ctx, owner, subject)
			if err != nil {
				return nil, apiError("member", err)
			}
			roles, err := svc.ListTenantMemberRoles(ctx, owner, subject)
			if err != nil {
				return nil, apiError("member roles", err)
			}
			out := memberFromDomain(member)
			out.RoleIDs = append([]guid.ID{}, roles...)
			return respx.OK(ctx, out), nil
		})
		authz.RegisterPlatform(registrar, api, op("member-create", http.MethodPost, "/members"), write, func(ctx context.Context, in *systemScopeCreateInput) (*respx.Body[APITenantMember], error) {
			subject, err := guid.RequireID(in.Body.Subject)
			if err != nil {
				return nil, err
			}
			department, err := guid.OptionalID(in.Body.DepartmentID)
			if err != nil {
				return nil, err
			}
			var member TenantMember
			err = svc.systemScopeMutation(ctx, in.OwnerID, kind, subject, func(tx *Service, owner guid.ID) error {
				var err error
				member, err = tx.putRootMember(ctx, kind, owner, subject, in.Body.DisplayName, in.Body.Email, department, in.Body.Status, true)
				return err
			})
			if err != nil {
				return nil, apiError("create member", err)
			}
			return respx.OK(ctx, memberFromDomain(member)), nil
		})
		authz.RegisterPlatform(registrar, api, op("member-update", http.MethodPatch, "/members/{principal_id}"), write, func(ctx context.Context, in *systemScopeUpdateInput) (*respx.Body[APITenantMember], error) {
			subject, err := guid.RequireID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			var member TenantMember
			err = svc.systemScopeMutation(ctx, in.OwnerID, kind, subject, func(tx *Service, owner guid.ID) error {
				current, err := tx.GetTenantMember(ctx, owner, subject)
				if err != nil {
					return err
				}
				if in.Body.DisplayName != nil {
					current.DisplayName = *in.Body.DisplayName
				}
				if in.Body.Email != nil {
					current.Email = *in.Body.Email
				}
				if in.Body.Status != nil {
					current.Status = *in.Body.Status
				}
				if in.Body.DepartmentID != nil {
					current.DepartmentID, err = guid.OptionalID(*in.Body.DepartmentID)
					if err != nil {
						return err
					}
				}
				member, err = tx.putRootMember(ctx, kind, owner, subject, current.DisplayName, current.Email, current.DepartmentID, current.Status, false)
				return err
			})
			if err != nil {
				return nil, apiError("update member", err)
			}
			return respx.OK(ctx, memberFromDomain(member)), nil
		})
		authz.RegisterPlatform(registrar, api, op("member-disable", http.MethodDelete, "/members/{principal_id}"), write, func(ctx context.Context, in *SystemScopeMemberInput) (*respx.Body[MutationResult], error) {
			subject, err := guid.RequireID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			err = svc.systemScopeMutation(ctx, in.OwnerID, kind, subject, func(tx *Service, owner guid.ID) error {
				_, err := tx.setRootMemberStatus(ctx, kind, owner, subject, MemberDisabled, nil)
				return err
			})
			if err != nil {
				return nil, apiError("disable member", err)
			}
			return respx.OK(ctx, MutationResult{Changed: true}), nil
		})
		authz.RegisterPlatform(registrar, api, op("roles", http.MethodGet, "/roles"), read, func(ctx context.Context, in *systemScopePageInput) (*respx.Body[[]APIRole], error) {
			owner, err := svc.systemScopeOwner(ctx, in.OwnerID, kind)
			if err != nil {
				return nil, apiError("role owner", err)
			}
			roles, total, err := svc.ListRolesPage(ctx, owner, in.Offset, in.Limit)
			if err != nil {
				return nil, apiError("roles", err)
			}
			return respx.List(ctx, rolesFromDomain(roles), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})
		authz.RegisterPlatform(registrar, api, op("member-roles", http.MethodGet, "/members/{principal_id}/roles"), read, func(ctx context.Context, in *systemScopeMemberRolesInput) (*respx.Body[[]string], error) {
			owner, err := svc.systemScopeOwner(ctx, in.OwnerID, kind)
			if err != nil {
				return nil, apiError("role owner", err)
			}
			subject, err := guid.RequireID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			roles, total, err := svc.ListTenantMemberRolesPage(ctx, owner, subject, in.Offset, in.Limit)
			if err != nil {
				return nil, apiError("member roles", err)
			}
			return respx.List(ctx, guidStrings(roles), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})
		for _, route := range []struct{ method, action string }{{http.MethodPut, "add"}, {http.MethodDelete, "remove"}} {
			method := route.method
			authz.RegisterPlatform(registrar, api, op("role-member-"+route.action, method, "/roles/{role_id}/members/{principal_id}"), write, func(ctx context.Context, in *systemScopeRoleMemberInput) (*respx.Body[MutationResult], error) {
				subject, err := guid.RequireID(in.PrincipalID)
				if err != nil {
					return nil, err
				}
				role, err := guid.RequireID(in.RoleID)
				if err != nil {
					return nil, err
				}
				var changed bool
				err = svc.systemScopeMutation(ctx, in.OwnerID, kind, subject, func(tx *Service, owner guid.ID) error {
					if _, err := tx.GetRole(ctx, owner, role); err != nil {
						return err
					}
					var err error
					if method == http.MethodPut {
						changed, err = tx.AddMember(ctx, owner, role, subject)
					} else {
						changed, err = tx.RemoveMember(ctx, owner, role, subject)
					}
					return err
				})
				if err != nil {
					return nil, apiError("role member", err)
				}
				return respx.OK(ctx, MutationResult{Changed: changed}), nil
			})
		}
	}
}
