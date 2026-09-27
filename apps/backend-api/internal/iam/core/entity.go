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

const (
	entityBindingScope     = "entity"
	maxEntityDepth         = 16
	maxEntityMetadataBytes = 16 << 10
)

// CreateEntity persists one tenant-owned entity using a generated identifier.
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

// ListEntities returns every entity owned by one tenant in stable hierarchy
// order.
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

// GetEntity returns one tenant-qualified entity.
func (r *Repository) GetEntity(ctx context.Context, tenantID, entityID guid.ID) (Entity, error) {
	row, err := r.getEntityRow(ctx, tenantID, entityID)
	if err != nil {
		return Entity{}, err
	}
	return entityFromRow(row)
}

func (r *Repository) ResolveEntityTenant(ctx context.Context, entityID guid.ID) (guid.ID, bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, false, err
	}
	row, err := tables.Entities.WithContext(ctx).Select(tables.Entities.TenantID).
		Where(tables.Entities.ID.Eq(entityID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve entity tenant: %w", err)
	}
	return row.TenantID, true, nil
}

// UpdateEntity replaces mutable fields on one tenant-qualified entity.
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

// DeleteEntity removes one tenant-qualified entity.
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

// ListEntityRoleBindings returns direct role bindings on one tenant entity.
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

// PutEntityRoleBinding idempotently creates or replaces one direct entity role
// binding.
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

// DeleteEntityRoleBinding removes one direct tenant entity role binding.
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

func (r *Repository) findEntityRoleBinding(
	ctx context.Context,
	tenantID, entityID, roleID, principalID guid.ID,
) (EntityRoleBinding, bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return EntityRoleBinding{}, false, err
	}
	row, err := tables.RoleBindings.WithContext(ctx).Where(
		tables.RoleBindings.TenantID.Eq(tenantID),
		tables.RoleBindings.ScopeType.Eq(entityBindingScope),
		tables.RoleBindings.ScopeID.Eq(entityID),
		tables.RoleBindings.RoleID.Eq(roleID),
		tables.RoleBindings.PrincipalID.Eq(principalID),
	).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EntityRoleBinding{}, false, nil
	}
	if err != nil {
		return EntityRoleBinding{}, false, fmt.Errorf("get entity role binding: %w", err)
	}
	return entityRoleBindingFromRow(row), true, nil
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

// CreateEntity validates and creates one normalized tenant entity.
func (s *Service) CreateEntity(ctx context.Context, entity Entity) (Entity, error) {
	if err := normalizeEntity(&entity); err != nil {
		return Entity{}, err
	}
	if entity.Metadata == nil {
		entity.Metadata = map[string]any{}
	}
	record := newAuditRecord(ctx, entity.TenantID, "entity_created", "entity", 0)
	record.PolicyChanged = true
	var created Entity
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := validateEntityParent(ctx, repo, entity.TenantID, 0, entity.ParentID); err != nil {
			return err
		}
		var err error
		created, err = repo.CreateEntity(ctx, entity)
		if err == nil && s.roots != nil {
			_, err = s.roots(ctx, repo.writer(), FileRootProvisionInput{
				Scope: "entity", TenantID: created.TenantID, OwnerID: created.ID,
			}, repo.now())
		}
		record.TargetID = created.ID
		return err
	})
	return created, err
}

// ListEntities returns every entity visible inside one tenant boundary.
func (s *Service) ListEntities(ctx context.Context, tenantID guid.ID) ([]Entity, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, err
	}
	return s.repo.ListEntities(ctx, tenantID)
}

// GetEntity returns one tenant-qualified entity.
func (s *Service) GetEntity(ctx context.Context, tenantID, entityID guid.ID) (Entity, error) {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return Entity{}, err
	}
	return s.repo.GetEntity(ctx, tenantID, entityID)
}

// UpdateEntity validates and applies an entity patch while serializing
// hierarchy checks for the tenant.
func (s *Service) UpdateEntity(
	ctx context.Context,
	tenantID, entityID guid.ID,
	patch EntityPatch,
) (Entity, error) {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return Entity{}, err
	}
	if patch.ParentID == nil && patch.Type == nil && patch.Name == nil &&
		patch.Status == nil && patch.Metadata == nil {
		return Entity{}, fmt.Errorf("%w: no entity fields supplied", ErrInvalidArgument)
	}
	record := newAuditRecord(ctx, tenantID, "entity_updated", "entity", entityID)
	record.PolicyChanged = true
	var updated Entity
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		current, err := repo.GetEntity(ctx, tenantID, entityID)
		if err != nil {
			return err
		}
		if patch.Type != nil && strings.TrimSpace(*patch.Type) != current.Type {
			count, err := repo.entityRelationshipCount(ctx, tenantID, entityID)
			if err != nil {
				return err
			}
			if count > 0 {
				return ErrEntityHasRelationships
			}
		}
		applyEntityPatch(&current, patch)
		if err := normalizeEntity(&current); err != nil {
			return err
		}
		if err := validateEntityParent(ctx, repo, tenantID, entityID, current.ParentID); err != nil {
			return err
		}
		updated, err = repo.UpdateEntity(ctx, current)
		return err
	})
	return updated, err
}

// DeleteEntity removes one tenant-qualified entity.
func (s *Service) DeleteEntity(ctx context.Context, tenantID, entityID guid.ID) error {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return err
	}
	record := newAuditRecord(ctx, tenantID, "entity_deleted", "entity", entityID)
	record.PolicyChanged = true
	return s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		if s.rootContent != nil {
			hasContent, err := s.rootContent(ctx, repo.writer(), FileRootProvisionInput{
				Scope: "entity", TenantID: tenantID, OwnerID: entityID,
			})
			if err != nil {
				return err
			}
			if hasContent {
				return ErrEntityHasFiles
			}
		}
		return repo.DeleteEntity(ctx, tenantID, entityID)
	})
}

// ListEntityRoleBindings returns direct allow and deny bindings on one entity.
func (s *Service) ListEntityRoleBindings(
	ctx context.Context,
	tenantID, entityID guid.ID,
) ([]EntityRoleBinding, error) {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return nil, err
	}
	return s.repo.ListEntityRoleBindings(ctx, tenantID, entityID)
}

// PutEntityRoleBinding validates and idempotently replaces one direct entity
// role binding.
func (s *Service) PutEntityRoleBinding(
	ctx context.Context,
	tenantID, entityID, roleID, principalID guid.ID,
	effect BindingEffect,
	expiresAt time.Time,
) (EntityRoleBinding, bool, error) {
	if err := validateEntityBinding(tenantID, entityID, roleID, principalID, effect, expiresAt); err != nil {
		return EntityRoleBinding{}, false, err
	}
	record := newAuditRecord(ctx, tenantID, "entity_role_binding_put", "entity", entityID)
	record.Detail["role_id"], record.Detail["principal_id"], record.Detail["effect"] = roleID, principalID, effect
	var binding EntityRoleBinding
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if effect == BindingAllow {
			if err := s.requireGrantableRoleWithRepository(ctx, repo, tenantID, roleID); err != nil {
				return err
			}
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		if _, err := repo.GetEntity(ctx, tenantID, entityID); err != nil {
			return err
		}
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		active, err := repo.IsMemberActive(ctx, tenantID, principalID)
		if err != nil {
			return err
		}
		if !active {
			return ErrMemberInactive
		}
		changed, binding, err = repo.PutEntityRoleBinding(
			ctx, tenantID, entityID, roleID, principalID, effect, expiresAt,
		)
		record.PolicyChanged, record.SkipAudit = changed, !changed
		return err
	})
	return binding, changed, err
}

// DeleteEntityRoleBinding removes a direct binding and prevents an
// authenticated actor from expanding privileges by deleting a deny.
func (s *Service) DeleteEntityRoleBinding(
	ctx context.Context,
	tenantID, entityID, roleID, principalID guid.ID,
) (bool, error) {
	if err := validateEntityBinding(
		tenantID, entityID, roleID, principalID, BindingAllow, time.Time{},
	); err != nil {
		return false, err
	}
	record := newAuditRecord(ctx, tenantID, "entity_role_binding_deleted", "entity", entityID)
	record.Detail["role_id"], record.Detail["principal_id"] = roleID, principalID
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if _, err := repo.GetEntity(ctx, tenantID, entityID); err != nil {
			return err
		}
		if err := s.requireGrantablePermissionsOn(ctx, repo, tenantID); err != nil {
			return err
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		binding, exists, err := repo.findEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID)
		if err != nil {
			return err
		}
		if !exists {
			record.SkipAudit = true
			return nil
		}
		if binding.Effect == BindingDeny {
			if err := s.requireGrantableRoleWithRepository(ctx, repo, tenantID, roleID); err != nil {
				return err
			}
		}
		changed, err = repo.DeleteEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID)
		record.PolicyChanged, record.SkipAudit = changed, !changed
		return err
	})
	return changed, err
}

func normalizeEntity(entity *Entity) error {
	if entity == nil || validateTenant(entity.TenantID) != nil {
		return fmt.Errorf("%w: invalid entity tenant", ErrInvalidArgument)
	}
	entity.Type = strings.TrimSpace(entity.Type)
	entity.Name = strings.TrimSpace(entity.Name)
	if !validEntityType(entity.Type) || entity.Name == "" || len(entity.Name) > 200 {
		return fmt.Errorf("%w: invalid entity type or name", ErrInvalidArgument)
	}
	if entity.Status == "" {
		entity.Status = EntityActive
	}
	if entity.Status != EntityActive && entity.Status != EntityDisabled {
		return fmt.Errorf("%w: invalid entity status", ErrInvalidArgument)
	}
	if entity.Metadata == nil {
		entity.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(entity.Metadata)
	if err != nil || len(metadata) > maxEntityMetadataBytes {
		return fmt.Errorf("%w: invalid entity metadata", ErrInvalidArgument)
	}
	return nil
}

func validateEntityParent(
	ctx context.Context,
	repo *Repository,
	tenantID, entityID, parentID guid.ID,
) error {
	if parentID.Zero() {
		return nil
	}
	current := parentID
	for ancestors := 1; ; ancestors++ {
		if current == entityID {
			return ErrEntityHierarchy
		}
		parent, err := repo.GetEntity(ctx, tenantID, current)
		if err != nil {
			return err
		}
		if parent.ParentID.Zero() {
			return nil
		}
		if ancestors >= maxEntityDepth-1 {
			return ErrEntityHierarchy
		}
		current = parent.ParentID
	}
}

func validateEntityRef(tenantID, entityID guid.ID) error {
	if err := validateTenant(tenantID); err != nil {
		return err
	}
	if entityID.Zero() {
		return fmt.Errorf("%w: invalid entity id", ErrInvalidArgument)
	}
	return nil
}

func validateEntityBinding(
	tenantID, entityID, roleID, principalID guid.ID,
	effect BindingEffect,
	expiresAt time.Time,
) error {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return err
	}
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return err
	}
	if principalID.Zero() {
		return fmt.Errorf("%w: invalid binding principal", ErrInvalidArgument)
	}
	if effect != BindingAllow && effect != BindingDeny {
		return fmt.Errorf("%w: invalid binding effect", ErrInvalidArgument)
	}
	if !expiresAt.IsZero() && !expiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("%w: binding expiry must be in the future", ErrInvalidArgument)
	}
	return nil
}

func applyEntityPatch(entity *Entity, patch EntityPatch) {
	if patch.ParentID != nil {
		entity.ParentID = *patch.ParentID
	}
	if patch.Type != nil {
		entity.Type = *patch.Type
	}
	if patch.Name != nil {
		entity.Name = *patch.Name
	}
	if patch.Status != nil {
		entity.Status = *patch.Status
	}
	if patch.Metadata != nil {
		entity.Metadata = *patch.Metadata
	}
}

func validEntityType(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
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
