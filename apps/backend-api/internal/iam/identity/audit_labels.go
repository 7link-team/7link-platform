package identity

import (
	"context"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
)

// auditPrincipalLabels resolves only identities the caller could read through
// the existing account directory. It does not give audit readers new access to
// identity fields, or expose another tenant's accounts through audit history.
func (s *Service) auditPrincipalLabels(ctx context.Context, tenant guid.ID, ids []guid.ID) (map[guid.ID]audit.PrincipalLabel, error) {
	labels := make(map[guid.ID]audit.PrincipalLabel)
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || s == nil || s.avatarPermissions == nil || len(ids) == 0 {
		return labels, nil
	}
	var fields authz.FieldConstraint
	humanAllowed, serviceAllowed := true, true
	if tenant > 0 {
		if claims.TenantID != tenant {
			return labels, nil
		}
		var err error
		humanAllowed, err = s.avatarPermissions.Check(ctx, tenant, "user_view", claims.PrincipalID)
		if err != nil {
			return labels, err
		}
		serviceAllowed, err = s.avatarPermissions.Check(ctx, tenant, "service_account_view", claims.PrincipalID)
		if err != nil || (!humanAllowed && !serviceAllowed) {
			return labels, err
		}
		fields, err = s.profileConstraint(ctx, tenant)
		if err != nil {
			return nil, err
		}
	} else {
		allowed, err := s.avatarPermissions.CheckPlatform(ctx, "system_account_view", claims.PrincipalID)
		if err != nil || !allowed {
			return labels, err
		}
		fields = authz.FieldConstraint{Fields: make(map[string]authz.FieldDecision)}
		for _, field := range authz.ProfileFields() {
			fields.Fields[field.Name] = authz.DecideField(field, nil, nil)
		}
	}
	if fields.ReadString("login_name", "") == nil && fields.ReadString("display_name", "") == nil {
		return labels, nil
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	tables, db, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID                     guid.ID
		LoginName, DisplayName string
		Anonymized             bool
	}
	query := db.WithContext(ctx).Table(tables.Principals.TableName()+" AS principal").
		Select("principal.id, principal.login_name, principal.display_name, lifecycle.anonymized_at IS NOT NULL AS anonymized").
		Joins("LEFT JOIN "+tables.PrincipalLifecycles.TableName()+" AS lifecycle ON lifecycle.principal_id = principal.id").
		Where("principal.id IN ?", ids)
	if tenant > 0 {
		query = query.Joins("JOIN "+repo.foreign.access.TenantMembers.String()+" AS member ON member.principal_id = principal.id").Where("member.tenant_id = ?", tenant)
		serviceAccount := db.Table(tables.ServiceAccounts.TableName() + " AS service_account").Select("1").Where("service_account.principal_id = principal.id")
		visibleServiceAccount := db.Table(tables.ServiceAccounts.TableName()+" AS visible_service_account").Select("1").
			Where("visible_service_account.principal_id = principal.id AND visible_service_account.owner_tenant_id = ? AND visible_service_account.status <> 'deleted'", tenant)
		if !humanAllowed {
			query = query.Where("EXISTS (?)", visibleServiceAccount)
		} else if !serviceAllowed {
			query = query.Where("NOT EXISTS (?)", serviceAccount)
		} else {
			query = query.Where("NOT EXISTS (?) OR EXISTS (?)", serviceAccount, visibleServiceAccount)
		}
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Anonymized {
			labels[row.ID] = audit.PrincipalLabel{Anonymized: true}
			continue
		}
		labels[row.ID] = audit.PrincipalLabel{
			LoginName:   fields.ReadString("login_name", row.LoginName),
			DisplayName: fields.ReadString("display_name", row.DisplayName),
		}
	}
	return labels, nil
}
