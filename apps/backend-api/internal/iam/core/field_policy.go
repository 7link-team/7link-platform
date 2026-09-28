package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

var ErrFieldPolicyConflict = errors.New("field policy version conflict")

func (s *Service) profileConstraint(ctx context.Context, tenantID guid.ID) (authz.FieldConstraint, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID != tenantID {
		return authz.FieldConstraint{}, authz.ErrFieldDenied
	}
	return authz.ProfileConstraint(ctx, s.checker, tenantID, claims.PrincipalID)
}

func requireMemberProfileWrites(raw []byte, fields authz.FieldConstraint, create bool) error {
	var submitted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &submitted); err != nil {
		return ErrInvalidArgument
	}
	for _, name := range []string{"display_name", "email"} {
		if _, present := submitted[name]; present {
			if err := fields.RequireWrite(create, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func memberProfileView(member TenantMember, fields authz.FieldConstraint) APITenantMember {
	view := memberFromDomain(member)
	view.DisplayName, view.Email = "", ""
	if value := fields.ReadString("display_name", member.DisplayName); value != nil {
		view.DisplayName = *value
	}
	if value := fields.ReadString("email", member.Email); value != nil {
		view.Email = *value
	}
	return view
}

// ManagedFieldPolicy includes version zero for a code-defined default that has
// not been overridden. Deleting an override restores that declared default.
type ManagedFieldPolicy struct {
	Resource string            `json:"resource"`
	Field    string            `json:"field"`
	Version  int64             `json:"version"`
	Policy   authz.FieldPolicy `json:"policy"`
}

func defaultFieldPolicy(field authz.Field) authz.FieldPolicy {
	policy := authz.FieldPolicy{
		Read:  authz.FieldAccess{Default: "allow", AllowRoles: []guid.ID{}, DenyRoles: []guid.ID{}},
		Write: authz.FieldAccess{Default: "allow", AllowRoles: []guid.ID{}, DenyRoles: []guid.ID{}},
		Mask:  "omit",
	}
	if field.Mutability == "readOnly" {
		policy.Write.Default = "deny"
	}
	if field.Returned == "never" {
		policy.Read.Default = "deny"
	}
	return policy
}

func (s *Service) requireFieldPolicyAccess(ctx context.Context, tenantID guid.ID, permission string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || tenantID <= 0 || claims.TenantID != tenantID || claims.PrincipalID <= 0 {
		return ErrPrivilegeEscalation
	}
	allowed, err := s.checker.CheckBulk(ctx, tenantID, []string{permission}, claims.PrincipalID)
	if err != nil {
		return err
	}
	if !allowed[permission] {
		return ErrPrivilegeEscalation
	}
	return nil
}

func (s *Service) ListFieldPolicies(ctx context.Context, tenantID guid.ID, resource string) ([]ManagedFieldPolicy, error) {
	if err := s.requireFieldPolicyAccess(ctx, tenantID, "field_policy_view"); err != nil {
		return nil, err
	}
	fields := s.registry.Fields(resource)
	if len(fields) == 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.repo.fieldPolicyRows(ctx, tenantID, resource)
	if err != nil {
		return nil, err
	}
	stored := map[string]model.FieldPolicy{}
	for _, row := range rows {
		stored[row.Resource+"."+row.Field] = row
	}
	out := []ManagedFieldPolicy{}
	for _, f := range fields {
		if !f.Configurable {
			continue
		}
		item := ManagedFieldPolicy{Resource: f.Resource, Field: f.Name, Policy: defaultFieldPolicy(f)}
		if row, ok := stored[f.Resource+"."+f.Name]; ok {
			item.Policy, err = decodeFieldPolicy(f, row.PolicyJSON)
			if err != nil {
				return nil, err
			}
			item.Version = row.Version
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) SetFieldPolicy(ctx context.Context, tenantID guid.ID, resource, name string, policy authz.FieldPolicy, version int64) (ManagedFieldPolicy, error) {
	if err := s.requireFieldPolicyAccess(ctx, tenantID, "field_policy_update"); err != nil {
		return ManagedFieldPolicy{}, err
	}
	field, ok := s.registry.FindField(resource, name)
	if !ok || version < 0 || version == math.MaxInt64 {
		return ManagedFieldPolicy{}, ErrInvalidArgument
	}
	canonical, err := policy.Canonical(field)
	if err != nil {
		return ManagedFieldPolicy{}, errors.Join(ErrInvalidArgument, err)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ManagedFieldPolicy{}, err
	}
	record := newAuditRecord(ctx, tenantID, "field_policy_updated", "field_policy", 0)
	record.Detail["resource"], record.Detail["field"] = resource, name
	record.Detail["policy"] = canonical
	record.PolicyChanged = true
	result := ManagedFieldPolicy{Resource: resource, Field: name, Policy: canonical}
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		// A tenant-monotonic version cannot be reused after delete/recreate.
		revision, err := repo.revisions.Current(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		if revision < 0 || revision == math.MaxInt64 {
			return ErrUnavailable
		}
		result.Version = revision + 1
		roles := append(slices.Clone(canonical.Read.AllowRoles), canonical.Read.DenyRoles...)
		roles = append(roles, canonical.Write.AllowRoles...)
		roles = append(roles, canonical.Write.DenyRoles...)
		slices.Sort(roles)
		roles = slices.Compact(roles)
		for _, role := range roles {
			if _, err := repo.GetRole(ctx, tenantID, role); err != nil {
				return err
			}
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		row := model.FieldPolicy{TenantID: tenantID, Resource: resource, Field: name, PolicyJSON: string(encoded), Version: result.Version, UpdatedAt: repo.now().UTC().Truncate(time.Millisecond)}
		if version == 0 {
			err = tables.FieldPolicies.WithContext(ctx).Create(&row)
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrFieldPolicyConflict
			}
			return err
		}
		changed := db.WithContext(ctx).Table(tables.FieldPolicies.TableName()).
			Where("tenant_id = ? AND resource = ? AND field_name = ? AND version = ?", tenantID, resource, name, version).
			Updates(map[string]any{"policy_json": row.PolicyJSON, "version": row.Version, "updated_at": row.UpdatedAt})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrFieldPolicyConflict
		}
		return nil
	})
	return result, err
}

func (s *Service) DeleteFieldPolicy(ctx context.Context, tenantID guid.ID, resource, name string, version int64) error {
	if err := s.requireFieldPolicyAccess(ctx, tenantID, "field_policy_update"); err != nil {
		return err
	}
	field, ok := s.registry.FindField(resource, name)
	if !ok || !field.Configurable || version <= 0 {
		return ErrInvalidArgument
	}
	record := newAuditRecord(ctx, tenantID, "field_policy_deleted", "field_policy", 0)
	record.Detail["resource"], record.Detail["field"] = resource, name
	record.PolicyChanged = true
	return s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(tables.FieldPolicies.TableName()).
			Where("tenant_id = ? AND resource = ? AND field_name = ? AND version = ?", tenantID, resource, name, version).Delete(&model.FieldPolicy{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFieldPolicyConflict
		}
		return nil
	})
}

func (r *Repository) fieldPolicyRows(ctx context.Context, tenantID guid.ID, resource string) ([]model.FieldPolicy, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	query := db.WithContext(ctx).Table(tables.FieldPolicies.TableName()).Where("tenant_id = ?", tenantID)
	if resource != "" {
		query = query.Where("resource = ?", resource)
	}
	rows := []model.FieldPolicy{}
	err = query.Order("resource, field_name").Find(&rows).Error
	return rows, err
}

func decodeFieldPolicy(field authz.Field, raw string) (authz.FieldPolicy, error) {
	var policy authz.FieldPolicy
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, errors.Join(ErrUnavailable, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return policy, ErrUnavailable
	}
	canonical, err := policy.Canonical(field)
	if err != nil {
		return policy, errors.Join(ErrUnavailable, err)
	}
	return canonical, nil
}

// FieldConstraint uses the tenant revision to read membership and field rules
// consistently. It never grants the endpoint or the business rows themselves.
func (a *Authorizer) FieldConstraint(ctx context.Context, tenantID, entityID guid.ID, resource string, subject guid.ID) (authz.FieldConstraint, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return authz.FieldConstraint{}, err
	}
	if pinned != a {
		return pinned.FieldConstraint(ctx, tenantID, entityID, resource, subject)
	}
	fields := a.registry.Fields(resource)
	if tenantID <= 0 || subject <= 0 || entityID < 0 || resource == "" || len(fields) == 0 {
		return authz.FieldConstraint{}, ErrInvalidArgument
	}
	names, err := a.authorizationTables()
	if err != nil {
		return authz.FieldConstraint{}, err
	}
	for range policySnapshotAttempts {
		before, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return authz.FieldConstraint{}, err
		}
		_, active, err := tenantMembershipState(ctx, a.db, names, tenantID, subject)
		if err != nil {
			return authz.FieldConstraint{}, err
		}
		var principals int64
		if err := a.db.WithContext(ctx).Table(names.Principal.String()).Where("id = ? AND status = 'active'", subject).Count(&principals).Error; err != nil {
			return authz.FieldConstraint{}, err
		}
		if entityID > 0 {
			var entities int64
			if err := a.db.WithContext(ctx).Table(names.Tables.Entities.String()).Where("tenant_id = ? AND id = ? AND status = 'active'", tenantID, entityID).Count(&entities).Error; err != nil {
				return authz.FieldConstraint{}, err
			}
			active = active && entities == 1
		}
		result := authz.FieldConstraint{Fields: map[string]authz.FieldDecision{}}
		if active && principals == 1 {
			rows := []model.FieldPolicy{}
			if err := a.db.WithContext(ctx).Table(names.Tables.FieldPolicies.String()).Where("tenant_id = ? AND resource = ?", tenantID, resource).Find(&rows).Error; err != nil {
				return authz.FieldConstraint{}, err
			}
			policies := map[string]authz.FieldPolicy{}
			for _, row := range rows {
				field, ok := a.registry.FindField(resource, row.Field)
				if !ok {
					return authz.FieldConstraint{}, ErrUnavailable
				}
				policy, err := decodeFieldPolicy(field, row.PolicyJSON)
				if err != nil {
					return authz.FieldConstraint{}, err
				}
				policies[row.Field] = policy
			}
			roles := []guid.ID{}
			if len(policies) > 0 {
				roles, err = a.fieldPolicyRoles(ctx, tenantID, entityID, subject)
				if err != nil {
					return authz.FieldConstraint{}, err
				}
			}
			for _, field := range fields {
				var policy *authz.FieldPolicy
				if rule, ok := policies[field.Name]; ok {
					policy = &rule
				}
				result.Fields[field.Name] = authz.DecideField(field, policy, roles)
			}
		}
		after, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return authz.FieldConstraint{}, err
		}
		if before == after {
			result.Revision = after
			return result, nil
		}
	}
	return authz.FieldConstraint{}, ErrAuthorizationChanged
}

func (a *Authorizer) fieldPolicyRoles(ctx context.Context, tenantID, entityID, subject guid.ID) ([]guid.ID, error) {
	now := a.now().UTC().Truncate(time.Millisecond)
	params, err := a.effectiveRolesParams(tenantID, subject, now)
	if err != nil {
		return nil, err
	}
	roles, err := query.FieldPolicyRoles(ctx, a.db, params)
	if err != nil {
		return nil, err
	}
	names, err := a.authorizationTables()
	if err != nil {
		return nil, err
	}
	groups, err := matchingDynamicGroupIDs(ctx, a.db, names, tenantID, subject)
	if err != nil {
		return nil, err
	}
	if len(groups) > 0 {
		dynamic := []guid.ID{}
		if err := a.db.WithContext(ctx).Table(names.Directory.GroupRoleBindings.String()).Where("tenant_id = ? AND group_id IN ?", tenantID, groups).Pluck("role_id", &dynamic).Error; err != nil {
			return nil, err
		}
		roles = append(roles, dynamic...)
	}
	if entityID > 0 {
		inherited, err := query.DirectoryAncestorRoleIDs(ctx, a.db, query.DirectoryAncestorParams{
			Tables:   names.Tables,
			TenantID: tenantID,
			EntityID: entityID,
			Subject:  subject,
			Now:      now,
		})
		if err != nil {
			return nil, err
		}
		roles = append(roles, inherited...)
	}
	slices.Sort(roles)
	return slices.Compact(roles), nil
}
