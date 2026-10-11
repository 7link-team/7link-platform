package governance

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/policyx"
)

func (r *Repository) listReviewableGrants(ctx context.Context, tenantID guid.ID, now time.Time) ([]reviewableGrantRow, error) {
	roleMembers := r.foreign.access.RoleMembers.String()
	roles := r.foreign.access.Roles.String()
	tenantMembers := r.foreign.access.TenantMembers.String()
	temporaryGrants := r.foreign.access.TemporaryRoleGrants.String()
	roleBindings := r.foreign.access.RoleBindings.String()
	entities := r.foreign.access.Entities.String()
	groupRoleBindings := r.foreign.directory.GroupRoleBindings.String()
	groups := r.foreign.directory.Groups.String()
	groupMembers := r.foreign.directory.GroupMembers.String()
	rows := make([]reviewableGrantRow, 0)
	queries := []struct {
		sql  string
		args []any
	}{
		{sql: `
SELECT members.principal_id, tenant_members.display_name AS principal_name,
       roles.id AS role_id, roles.name AS role_name, 'permanent' AS grant_type, NULL AS grant_id,
       members.created_at AS grant_created_at, members.expires_at AS grant_expires_at
FROM ` + roleMembers + ` AS members
JOIN ` + roles + ` AS roles ON roles.tenant_id = members.tenant_id AND roles.id = members.role_id
JOIN ` + tenantMembers + ` AS tenant_members
  ON tenant_members.tenant_id = members.tenant_id AND tenant_members.principal_id = members.principal_id AND tenant_members.status = 'active'
WHERE members.tenant_id = ? AND (members.expires_at IS NULL OR members.expires_at > ?)`, args: []any{tenantID, now}},
		{sql: `
SELECT grants.principal_id, tenant_members.display_name AS principal_name,
       roles.id AS role_id, roles.name AS role_name, 'temporary' AS grant_type, grants.id AS grant_id,
       grants.created_at AS grant_created_at, grants.ends_at AS grant_expires_at
FROM ` + temporaryGrants + ` AS grants
JOIN ` + roles + ` AS roles ON roles.tenant_id = grants.tenant_id AND roles.id = grants.role_id
JOIN ` + tenantMembers + ` AS tenant_members
  ON tenant_members.tenant_id = grants.tenant_id AND tenant_members.principal_id = grants.principal_id AND tenant_members.status = 'active'
WHERE grants.tenant_id = ? AND grants.starts_at <= ? AND grants.ends_at > ?`, args: []any{tenantID, now, now}},
		{sql: `
SELECT gm.principal_id, tm.display_name AS principal_name,
       roles.id AS role_id, roles.name AS role_name, 'group' AS grant_type, b.group_id AS grant_id,
       gm.created_at AS grant_created_at, gm.ends_at AS grant_expires_at
FROM ` + groupRoleBindings + ` AS b
JOIN ` + groups + ` AS g ON g.tenant_id = b.tenant_id AND g.id = b.group_id AND g.status = 'active' AND g.group_type = 'static'
JOIN ` + groupMembers + ` AS gm ON gm.tenant_id = b.tenant_id AND gm.group_id = b.group_id
JOIN ` + roles + ` AS roles ON roles.tenant_id = b.tenant_id AND roles.id = b.role_id
JOIN ` + tenantMembers + ` AS tm ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id AND tm.status = 'active'
WHERE b.tenant_id = ? AND (gm.starts_at IS NULL OR gm.starts_at <= ?) AND (gm.ends_at IS NULL OR gm.ends_at > ?)`, args: []any{tenantID, now, now}},
		{sql: `
SELECT bindings.principal_id, tm.display_name AS principal_name,
       roles.id AS role_id, roles.name AS role_name, 'entity' AS grant_type, bindings.scope_id AS grant_id,
       bindings.created_at AS grant_created_at, bindings.expires_at AS grant_expires_at
FROM ` + roleBindings + ` AS bindings
JOIN ` + entities + ` AS entities ON entities.tenant_id = bindings.tenant_id AND entities.id = bindings.scope_id AND entities.status = 'active'
JOIN ` + roles + ` AS roles ON roles.tenant_id = bindings.tenant_id AND roles.id = bindings.role_id
JOIN ` + tenantMembers + ` AS tm ON tm.tenant_id = bindings.tenant_id AND tm.principal_id = bindings.principal_id AND tm.status = 'active'
WHERE bindings.tenant_id = ? AND bindings.scope_type = 'entity' AND (bindings.expires_at IS NULL OR bindings.expires_at > ?)`, args: []any{tenantID, now}},
	}
	for _, query := range queries {
		var grantRows []reviewableGrantRow
		if err := r.writer().WithContext(ctx).Raw(query.sql, query.args...).Scan(&grantRows).Error; err != nil {
			return nil, fmt.Errorf("list reviewable role grants: %w", err)
		}
		rows = append(rows, grantRows...)
	}
	dynamic, err := r.listDynamicGroupGrants(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	relationships, err := r.listRelationshipGrants(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rows = append(rows, dynamic...)
	rows = append(rows, relationships...)
	return rows, nil
}

type dynamicGroupReviewRow struct {
	GroupID       guid.ID   `gorm:"column:group_id"`
	RuleJSON      string    `gorm:"column:rule_json"`
	Subject       guid.ID   `gorm:"column:subject"`
	PrincipalName string    `gorm:"column:principal_name"`
	Email         string    `gorm:"column:email"`
	DepartmentID  *guid.ID  `gorm:"column:department_id"`
	Status        string    `gorm:"column:status"`
	RoleID        guid.ID   `gorm:"column:role_id"`
	RoleName      string    `gorm:"column:role_name"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (r *Repository) listDynamicGroupGrants(ctx context.Context, tenantID guid.ID) ([]reviewableGrantRow, error) {
	groups := r.foreign.directory.Groups.String()
	groupRoleBindings := r.foreign.directory.GroupRoleBindings.String()
	memberDepartments := r.foreign.directory.MemberDepartments.String()
	roles := r.foreign.access.Roles.String()
	tenantMembers := r.foreign.access.TenantMembers.String()
	rows := make([]dynamicGroupReviewRow, 0)
	query := `SELECT directory_groups.id AS group_id, directory_groups.rule_json, members.principal_id AS subject,
members.display_name AS principal_name, members.email, departments.department_id, members.status,
bindings.role_id, roles.name AS role_name, bindings.created_at
FROM ` + groups + ` AS directory_groups
JOIN ` + groupRoleBindings + ` AS bindings ON bindings.tenant_id = directory_groups.tenant_id AND bindings.group_id = directory_groups.id
JOIN ` + roles + ` AS roles ON roles.tenant_id = bindings.tenant_id AND roles.id = bindings.role_id
JOIN ` + tenantMembers + ` AS members ON members.tenant_id = directory_groups.tenant_id AND members.status = 'active'
LEFT JOIN ` + memberDepartments + ` AS departments ON departments.tenant_id = members.tenant_id AND departments.principal_id = members.principal_id
WHERE directory_groups.tenant_id = ? AND directory_groups.group_type = 'dynamic' AND directory_groups.status = 'active'
ORDER BY directory_groups.id, members.principal_id, bindings.role_id`
	if err := r.writer().WithContext(ctx).Raw(query, tenantID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dynamic group review candidates: %w", err)
	}
	grants := make([]reviewableGrantRow, 0, len(rows))
	for _, row := range rows {
		departmentID := ""
		if row.DepartmentID != nil {
			departmentID = row.DepartmentID.String()
		}
		matched, err := policyx.EvaluateMemberRule([]byte(row.RuleJSON), policyx.MemberFacts{
			Subject: row.Subject.String(), Email: row.Email, DepartmentID: departmentID, Status: row.Status,
		})
		if err != nil {
			return nil, fmt.Errorf("evaluate dynamic group %q rule: %w", row.GroupID, err)
		}
		if !matched {
			continue
		}
		groupID := row.GroupID
		grants = append(grants, reviewableGrantRow{
			PrincipalID: row.Subject, PrincipalName: row.PrincipalName,
			RoleID: row.RoleID, RoleName: row.RoleName, GrantType: ReviewGrantDynamicGroup,
			GrantID: &groupID, GrantCreatedAt: row.CreatedAt,
		})
	}
	return grants, nil
}

type relationshipReviewRow struct {
	PrincipalID    guid.ID   `gorm:"column:principal_id"`
	PrincipalName  string    `gorm:"column:principal_name"`
	Relation       string    `gorm:"column:relation"`
	ResourceType   string    `gorm:"column:resource_type"`
	ResourceID     guid.ID   `gorm:"column:resource_id"`
	GrantID        guid.ID   `gorm:"column:grant_id"`
	GrantCreatedAt time.Time `gorm:"column:grant_created_at"`
	EntityName     string    `gorm:"column:entity_name"`
}

func (r *Repository) listRelationshipGrants(ctx context.Context, tenantID guid.ID) ([]reviewableGrantRow, error) {
	relationships := r.foreign.access.Relationships.String()
	resourceRelationships := r.foreign.access.ResourceRelationships.String()
	entities := r.foreign.access.Entities.String()
	tenantMembers := r.foreign.access.TenantMembers.String()
	rows := make([]relationshipReviewRow, 0)
	graphQuery := `SELECT relationships.subject_id AS principal_id, members.display_name AS principal_name,
relationships.relation, entities.type AS resource_type, relationships.resource_id,
relationships.resource_id AS grant_id, relationships.created_at AS grant_created_at, entities.name AS entity_name
FROM ` + relationships + ` AS relationships
JOIN ` + entities + ` AS entities ON entities.tenant_id = relationships.tenant_id AND entities.id = relationships.resource_id AND entities.status = 'active'
JOIN ` + tenantMembers + ` AS members ON members.tenant_id = relationships.tenant_id AND members.principal_id = relationships.subject_id AND members.status = 'active'
WHERE relationships.tenant_id = ? AND relationships.subject_type = 'principal'`
	if err := r.writer().WithContext(ctx).Raw(graphQuery, tenantID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list graph relationship grants: %w", err)
	}
	resourceRows := make([]relationshipReviewRow, 0)
	resourceQuery := `SELECT relationships.subject_id AS principal_id, members.display_name AS principal_name,
relationships.relation, relationships.resource_type, relationships.resource_id,
relationships.entity_id AS grant_id, relationships.created_at AS grant_created_at, '' AS entity_name
FROM ` + resourceRelationships + ` AS relationships
JOIN ` + entities + ` AS entities ON entities.tenant_id = relationships.tenant_id AND entities.id = relationships.entity_id AND entities.status = 'active'
JOIN ` + tenantMembers + ` AS members ON members.tenant_id = relationships.tenant_id AND members.principal_id = relationships.subject_id AND members.status = 'active'
WHERE relationships.tenant_id = ? AND relationships.subject_type = 'principal'`
	if err := r.writer().WithContext(ctx).Raw(resourceQuery, tenantID).Scan(&resourceRows).Error; err != nil {
		return nil, fmt.Errorf("list resource relationship grants: %w", err)
	}
	rows = append(rows, resourceRows...)
	grants := make([]reviewableGrantRow, 0, len(rows))
	for _, row := range rows {
		resource := row.EntityName
		if resource == "" {
			resource = strconv.FormatInt(int64(row.ResourceID), 10)
		}
		grantID := row.GrantID
		grants = append(grants, reviewableGrantRow{
			PrincipalID: row.PrincipalID, PrincipalName: row.PrincipalName,
			RoleName:  row.Relation + " of " + row.ResourceType + " " + resource,
			GrantType: ReviewGrantRelationship, GrantID: &grantID, GrantCreatedAt: row.GrantCreatedAt,
		})
	}
	return grants, nil
}
