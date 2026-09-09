package organization

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core"
	organizationquery "7link.com/platform/internal/iam/organization/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
)

// NewDirectoryAuthorizationSourceStore exposes organization-owned metadata
// without leaking organization persistence models into the core module.
func NewDirectoryAuthorizationSourceStore(runtime *appenv.Runtime) core.DirectoryAuthorizationSourceStore {
	return core.DirectoryAuthorizationSourceStore{
		ListMembershipExpiries: func(
			ctx context.Context, tenantID, subject guid.ID, evaluatedAt time.Time,
		) (core.DirectoryAuthorizationSourceExpiries, error) {
			if runtime == nil || runtime.Database() == nil || runtime.Database().Write() == nil {
				return core.DirectoryAuthorizationSourceExpiries{}, ErrUnavailable
			}
			tables := organizationquery.Bind(runtime.Database().Write(), runtime.TablePrefix())
			result := core.DirectoryAuthorizationSourceExpiries{
				Groups: map[guid.ID]time.Time{}, Positions: map[guid.ID]time.Time{},
			}
			groupMembers, err := tables.GroupMembers.WithContext(ctx).Where(
				tables.GroupMembers.TenantID.Eq(tenantID),
				tables.GroupMembers.PrincipalID.Eq(subject),
			).Find()
			if err != nil {
				return core.DirectoryAuthorizationSourceExpiries{}, fmt.Errorf("list authorization group membership expiries: %w", err)
			}
			for _, membership := range groupMembers {
				if activeAuthorizationWindow(membership.StartsAt, membership.EndsAt, evaluatedAt) && membership.EndsAt != nil {
					result.Groups[membership.GroupID] = membership.EndsAt.UTC()
				}
			}
			positionMembers, err := tables.PositionMembers.WithContext(ctx).Where(
				tables.PositionMembers.TenantID.Eq(tenantID),
				tables.PositionMembers.PrincipalID.Eq(subject),
			).Find()
			if err != nil {
				return core.DirectoryAuthorizationSourceExpiries{}, fmt.Errorf("list authorization position membership expiries: %w", err)
			}
			for _, membership := range positionMembers {
				if activeAuthorizationWindow(membership.StartsAt, membership.EndsAt, evaluatedAt) && membership.EndsAt != nil {
					result.Positions[membership.PositionID] = membership.EndsAt.UTC()
				}
			}
			return result, nil
		},
		ListLabels: func(
			ctx context.Context,
			tenantID guid.ID,
			roleIDs, groupIDs, positionIDs []guid.ID,
		) (core.DirectoryAuthorizationSourceLabels, error) {
			if runtime == nil || runtime.Database() == nil || runtime.Database().Write() == nil {
				return core.DirectoryAuthorizationSourceLabels{}, ErrUnavailable
			}
			tables := organizationquery.Bind(runtime.Database().Write(), runtime.TablePrefix())
			result := core.DirectoryAuthorizationSourceLabels{
				Groups: map[guid.ID]string{}, GroupTypes: map[guid.ID]string{},
				Positions: map[guid.ID]string{}, RoleDepartments: map[guid.ID][]guid.ID{},
			}
			if len(groupIDs) > 0 {
				rows, err := tables.Groups.WithContext(ctx).Where(
					tables.Groups.TenantID.Eq(tenantID),
					tables.Groups.ID.In(authorizationDirectoryIDValues(groupIDs)...),
				).Find()
				if err != nil {
					return core.DirectoryAuthorizationSourceLabels{}, fmt.Errorf("list authorization group names: %w", err)
				}
				for _, row := range rows {
					result.Groups[row.ID], result.GroupTypes[row.ID] = row.Name, row.GroupType
				}
			}
			if len(positionIDs) > 0 {
				rows, err := tables.Positions.WithContext(ctx).Where(
					tables.Positions.TenantID.Eq(tenantID),
					tables.Positions.ID.In(authorizationDirectoryIDValues(positionIDs)...),
				).Find()
				if err != nil {
					return core.DirectoryAuthorizationSourceLabels{}, fmt.Errorf("list authorization position names: %w", err)
				}
				for _, row := range rows {
					result.Positions[row.ID] = row.Name
				}
			}
			if len(roleIDs) > 0 {
				rows, err := tables.RoleScopeDepartments.WithContext(ctx).Where(
					tables.RoleScopeDepartments.TenantID.Eq(tenantID),
					tables.RoleScopeDepartments.RoleID.In(authorizationDirectoryIDValues(roleIDs)...),
				).Order(
					tables.RoleScopeDepartments.RoleID.Asc(),
					tables.RoleScopeDepartments.DepartmentID.Asc(),
				).Find()
				if err != nil {
					return core.DirectoryAuthorizationSourceLabels{}, fmt.Errorf("list authorization role departments: %w", err)
				}
				for _, row := range rows {
					result.RoleDepartments[row.RoleID] = append(result.RoleDepartments[row.RoleID], row.DepartmentID)
				}
			}
			return result, nil
		},
	}
}

func activeAuthorizationWindow(startsAt, endsAt *time.Time, evaluatedAt time.Time) bool {
	return (startsAt == nil || !startsAt.After(evaluatedAt)) && (endsAt == nil || endsAt.After(evaluatedAt))
}

func authorizationDirectoryIDValues(ids []guid.ID) []driver.Valuer {
	values := make([]driver.Valuer, len(ids))
	for index, id := range ids {
		values[index] = id
	}
	return values
}
