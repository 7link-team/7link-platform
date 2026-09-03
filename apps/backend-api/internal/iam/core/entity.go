package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

const entityBindingScope = "entity"

func (r *Repository) CreateEntity(ctx context.Context, entity Entity) (Entity, error) {
	tables, _, err := r.tables()
	if err != nil {
		return Entity{}, err
	}
	id, err := r.nextID()
	if err != nil {
		return Entity{}, fmt.Errorf("generate entity id: %w", err)
	}
	metadata, err := encodeEntityMetadata(entity.Metadata)
	if err != nil {
		return Entity{}, err
	}
	now := r.now().UTC().Truncate(time.Millisecond)
	row := model.Entity{
		TenantID: entity.TenantID, ID: id, ParentID: nullableID(entity.ParentID),
		Type: entity.Type, Name: entity.Name, NameKey: entityNameKey(entity.Name),
		Status: string(entity.Status), Metadata: metadata, CreatedAt: now, UpdatedAt: now,
	}
	if err := tables.Entities.WithContext(ctx).Create(&row); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return Entity{}, ErrEntityConflict
		}
		return Entity{}, fmt.Errorf("insert entity: %w", err)
	}
	return entityFromRow(&row)
}

func (r *Repository) ListEntities(ctx context.Context, tenantID guid.ID) ([]Entity, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.Entities.WithContext(ctx).
		Where(tables.Entities.TenantID.Eq(tenantID)).
		Order(tables.Entities.ParentID, tables.Entities.Type, tables.Entities.Name, tables.Entities.ID).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	return entitiesFromRows(rows)
}

func (r *Repository) GetEntity(ctx context.Context, tenantID, entityID guid.ID) (Entity, error) {
	row, err := r.getEntityRow(ctx, tenantID, entityID)
	if err != nil {
		return Entity{}, err
	}
	return entityFromRow(row)
}

func (r *Repository) UpdateEntity(ctx context.Context, entity Entity) (Entity, error) {
	var updated Entity
	err := r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		metadata, err := encodeEntityMetadata(entity.Metadata)
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(tables.Entities.TableName()).
			Where("tenant_id = ? AND id = ?", entity.TenantID, entity.ID).
			Updates(map[string]any{
				"parent_id": nullableID(entity.ParentID), "type": entity.Type,
				"name": entity.Name, "name_key": entityNameKey(entity.Name),
				"status": string(entity.Status), "metadata": metadata,
				"updated_at": repo.now().UTC().Truncate(time.Millisecond),
			})
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
				return ErrEntityConflict
			}
			return fmt.Errorf("update entity: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrEntityNotFound
		}
		updated, err = repo.GetEntity(ctx, entity.TenantID, entity.ID)
		return err
	})
	return updated, err
}

func (r *Repository) DeleteEntity(ctx context.Context, tenantID, entityID guid.ID) error {
	return r.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getEntityRow(ctx, tenantID, entityID); err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		children, err := tables.Entities.WithContext(ctx).
			Where(tables.Entities.TenantID.Eq(tenantID), tables.Entities.ParentID.Eq(entityID)).Count()
		if err != nil {
			return fmt.Errorf("count entity children: %w", err)
		}
		if children > 0 {
			return ErrEntityHasChildren
		}
		bindings, err := tables.RoleBindings.WithContext(ctx).
			Where(
				tables.RoleBindings.TenantID.Eq(tenantID),
				tables.RoleBindings.ScopeType.Eq(entityBindingScope),
				tables.RoleBindings.ScopeID.Eq(entityID),
			).Count()
		if err != nil {
			return fmt.Errorf("count entity bindings: %w", err)
		}
		if bindings > 0 {
			return ErrEntityHasBindings
		}
		relationships, err := repo.entityRelationshipCount(ctx, tenantID, entityID)
		if err != nil {
			return err
		}
		if relationships > 0 {
			return ErrEntityHasRelationships
		}
		info, err := tables.Entities.WithContext(ctx).
			Where(tables.Entities.TenantID.Eq(tenantID), tables.Entities.ID.Eq(entityID)).Delete()
		if err != nil {
			return fmt.Errorf("delete entity: %w", err)
		}
		if info.RowsAffected == 0 {
			return ErrEntityNotFound
		}
		return nil
	})
}

func (r *Repository) ListEntityRoleBindings(
	ctx context.Context,
	tenantID, entityID guid.ID,
) ([]EntityRoleBinding, error) {
	if _, err := r.getEntityRow(ctx, tenantID, entityID); err != nil {
		return nil, err
	}
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.RoleBindings.WithContext(ctx).
		Where(
			tables.RoleBindings.TenantID.Eq(tenantID),
			tables.RoleBindings.ScopeType.Eq(entityBindingScope),
			tables.RoleBindings.ScopeID.Eq(entityID),
		).
		Order(tables.RoleBindings.RoleID, tables.RoleBindings.PrincipalID).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list entity role bindings: %w", err)
	}
	bindings := make([]EntityRoleBinding, len(rows))
	for index, row := range rows {
		bindings[index] = entityRoleBindingFromRow(row)
	}
	return bindings, nil
}

func (r *Repository) PutEntityRoleBinding(
	ctx context.Context,
	tenantID, entityID, roleID, principalID guid.ID,
	effect BindingEffect,
	expiresAt time.Time,
) (bool, EntityRoleBinding, error) {
	var (
		changed bool
		binding EntityRoleBinding
	)
	err := r.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getEntityRow(ctx, tenantID, entityID); err != nil {
			return err
		}
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		where := func(database *gorm.DB) *gorm.DB {
			return database.Where(
				"tenant_id = ? AND role_id = ? AND principal_id = ? AND scope_type = ? AND scope_id = ?",
				tenantID, roleID, principalID, entityBindingScope, entityID,
			)
		}
		var current model.RoleBinding
		err = where(db.WithContext(ctx).Table(tables.RoleBindings.TableName())).Take(&current).Error
		expires := nullableTime(expiresAt)
		if err == nil {
			if current.Effect == string(effect) && equalOptionalTime(current.ExpiresAt, expires) {
				binding = entityRoleBindingFromRow(&current)
				return nil
			}
			result := where(db.WithContext(ctx).Table(tables.RoleBindings.TableName())).
				Updates(map[string]any{"effect": string(effect), "expires_at": expires})
			if result.Error != nil {
				return fmt.Errorf("update entity role binding: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return ErrEntityNotFound
			}
			current.Effect, current.ExpiresAt = string(effect), expires
			binding, changed = entityRoleBindingFromRow(&current), true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("get entity role binding: %w", err)
		}
		row := model.RoleBinding{
			TenantID: tenantID, RoleID: roleID, PrincipalID: principalID,
			ScopeType: entityBindingScope, ScopeID: entityID, Effect: string(effect),
			ExpiresAt: expires, CreatedAt: repo.now().UTC().Truncate(time.Millisecond),
		}
		if err := tables.RoleBindings.WithContext(ctx).Create(&row); err != nil {
			return fmt.Errorf("insert entity role binding: %w", err)
		}
		binding, changed = entityRoleBindingFromRow(&row), true
		return nil
	})
	return changed, binding, err
}

func (r *Repository) DeleteEntityRoleBinding(
	ctx context.Context,
	tenantID, entityID, roleID, principalID guid.ID,
) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	info, err := tables.RoleBindings.WithContext(ctx).
		Where(
			tables.RoleBindings.TenantID.Eq(tenantID),
			tables.RoleBindings.ScopeType.Eq(entityBindingScope),
			tables.RoleBindings.ScopeID.Eq(entityID),
			tables.RoleBindings.RoleID.Eq(roleID),
			tables.RoleBindings.PrincipalID.Eq(principalID),
		).Delete()
	if err != nil {
		return false, fmt.Errorf("delete entity role binding: %w", err)
	}
	return info.RowsAffected > 0, nil
}

func (r *Repository) getEntityRow(ctx context.Context, tenantID, entityID guid.ID) (*model.Entity, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.Entities.WithContext(ctx).
		Where(tables.Entities.TenantID.Eq(tenantID), tables.Entities.ID.Eq(entityID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get entity: %w", err)
	}
	return row, nil
}

func entitiesFromRows(rows []*model.Entity) ([]Entity, error) {
	entities := make([]Entity, len(rows))
	for index, row := range rows {
		entity, err := entityFromRow(row)
		if err != nil {
			return nil, err
		}
		entities[index] = entity
	}
	return entities, nil
}

func entityFromRow(row *model.Entity) (Entity, error) {
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
		return Entity{}, fmt.Errorf("decode entity metadata: %w", err)
	}
	entity := Entity{
		ID: row.ID, TenantID: row.TenantID, Type: row.Type, Name: row.Name,
		Status: EntityStatus(row.Status), Metadata: metadata,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.ParentID != nil {
		entity.ParentID = *row.ParentID
	}
	return entity, nil
}

func entityRoleBindingFromRow(row *model.RoleBinding) EntityRoleBinding {
	binding := EntityRoleBinding{
		EntityID: row.ScopeID, RoleID: row.RoleID, PrincipalID: row.PrincipalID,
		Effect: BindingEffect(row.Effect), CreatedAt: row.CreatedAt.UTC(),
	}
	if row.ExpiresAt != nil {
		binding.ExpiresAt = row.ExpiresAt.UTC()
	}
	return binding
}

func encodeEntityMetadata(metadata map[string]any) (string, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode entity metadata: %w", err)
	}
	return string(encoded), nil
}

func entityNameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func nullableID(id guid.ID) *guid.ID {
	if id.Zero() {
		return nil
	}
	return &id
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC().Truncate(time.Millisecond)
	return &value
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
