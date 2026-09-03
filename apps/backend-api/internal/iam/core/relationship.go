package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (r *Repository) ListRelationships(
	ctx context.Context,
	tenantID guid.ID,
	filter RelationshipFilter,
) ([]Relationship, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	if !filter.EntityID.Zero() {
		listing := tables.ResourceRelationships.WithContext(ctx).Where(
			tables.ResourceRelationships.TenantID.Eq(tenantID),
			tables.ResourceRelationships.EntityID.Eq(filter.EntityID),
		)
		if filter.ResourceType != "" {
			listing = listing.Where(tables.ResourceRelationships.ResourceType.Eq(filter.ResourceType))
		}
		if !filter.ResourceID.Zero() {
			listing = listing.Where(tables.ResourceRelationships.ResourceID.Eq(filter.ResourceID))
		}
		rows, err := listing.Order(
			tables.ResourceRelationships.ResourceType,
			tables.ResourceRelationships.ResourceID,
			tables.ResourceRelationships.Relation,
			tables.ResourceRelationships.SubjectType,
			tables.ResourceRelationships.SubjectID,
			tables.ResourceRelationships.SubjectRelation,
		).Find()
		if err != nil {
			return nil, fmt.Errorf("list IAM resource relationships: %w", err)
		}
		result := make([]Relationship, len(rows))
		for index, row := range rows {
			result[index] = resourceRelationshipFromRow(row)
		}
		return result, nil
	}

	listing := tables.Relationships.WithContext(ctx).Where(tables.Relationships.TenantID.Eq(tenantID))
	if filter.ResourceType != "" {
		listing = listing.Where(tables.Relationships.ResourceType.Eq(filter.ResourceType))
	}
	if !filter.ResourceID.Zero() {
		listing = listing.Where(tables.Relationships.ResourceID.Eq(filter.ResourceID))
	}
	rows, err := listing.Order(
		tables.Relationships.ResourceType,
		tables.Relationships.ResourceID,
		tables.Relationships.Relation,
		tables.Relationships.SubjectType,
		tables.Relationships.SubjectID,
		tables.Relationships.SubjectRelation,
	).Find()
	if err != nil {
		return nil, fmt.Errorf("list IAM relationships: %w", err)
	}
	result := make([]Relationship, len(rows))
	for index, row := range rows {
		result[index] = relationshipFromRow(row)
	}
	return result, nil
}

func (r *Repository) PutRelationship(
	ctx context.Context,
	relationship Relationship,
) (Relationship, bool, error) {
	var (
		stored  Relationship
		changed bool
	)
	err := r.runInTx(ctx, func(repo *Repository) error {
		if relationship.EntityID.Zero() {
			var err error
			stored, changed, err = repo.putEntityRelationship(ctx, relationship)
			return err
		}
		var err error
		stored, changed, err = repo.putResourceRelationship(ctx, relationship)
		return err
	})
	return stored, changed, err
}

func (r *Repository) putEntityRelationship(
	ctx context.Context,
	relationship Relationship,
) (Relationship, bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return Relationship{}, false, err
	}
	row := relationshipToRow(relationship)
	where := func(database *gorm.DB) *gorm.DB {
		return database.Where(
			"tenant_id = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ? AND relation = ? AND resource_type = ? AND resource_id = ?",
			row.TenantID, row.SubjectType, row.SubjectID, row.SubjectRelation,
			row.Relation, row.ResourceType, row.ResourceID,
		)
	}
	var current model.Relationship
	err = where(db.WithContext(ctx).Table(tables.Relationships.TableName())).Take(&current).Error
	if err == nil {
		if relationshipMutableFieldsEqual(current.StartsAt, current.EndsAt, current.ConditionJSON, row.StartsAt, row.EndsAt, row.ConditionJSON) {
			return relationshipFromRow(&current), false, nil
		}
		result := where(db.WithContext(ctx).Table(tables.Relationships.TableName())).Updates(map[string]any{
			"starts_at": row.StartsAt, "ends_at": row.EndsAt, "condition_json": row.ConditionJSON,
		})
		if result.Error != nil {
			return Relationship{}, false, fmt.Errorf("update IAM relationship window: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return Relationship{}, false, ErrInvalidRelationship
		}
		row.CreatedAt = current.CreatedAt
		return relationshipFromRow(&row), true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Relationship{}, false, fmt.Errorf("get IAM relationship: %w", err)
	}
	if err := tables.Relationships.WithContext(ctx).Create(&row); err != nil {
		return Relationship{}, false, fmt.Errorf("insert IAM relationship: %w", err)
	}
	return relationshipFromRow(&row), true, nil
}

func (r *Repository) putResourceRelationship(
	ctx context.Context,
	relationship Relationship,
) (Relationship, bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return Relationship{}, false, err
	}
	row := resourceRelationshipToRow(relationship)
	where := func(database *gorm.DB) *gorm.DB {
		return database.Where(
			"tenant_id = ? AND entity_id = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ? AND relation = ? AND resource_type = ? AND resource_id = ?",
			row.TenantID, row.EntityID, row.SubjectType, row.SubjectID,
			row.SubjectRelation, row.Relation, row.ResourceType, row.ResourceID,
		)
	}
	var current model.ResourceRelationship
	err = where(db.WithContext(ctx).Table(tables.ResourceRelationships.TableName())).Take(&current).Error
	if err == nil {
		if relationshipMutableFieldsEqual(current.StartsAt, current.EndsAt, current.ConditionJSON, row.StartsAt, row.EndsAt, row.ConditionJSON) {
			return resourceRelationshipFromRow(&current), false, nil
		}
		result := where(db.WithContext(ctx).Table(tables.ResourceRelationships.TableName())).Updates(map[string]any{
			"starts_at": row.StartsAt, "ends_at": row.EndsAt, "condition_json": row.ConditionJSON,
		})
		if result.Error != nil {
			return Relationship{}, false, fmt.Errorf("update IAM resource relationship window: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return Relationship{}, false, ErrInvalidRelationship
		}
		row.CreatedAt = current.CreatedAt
		return resourceRelationshipFromRow(&row), true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Relationship{}, false, fmt.Errorf("get IAM resource relationship: %w", err)
	}
	if err := tables.ResourceRelationships.WithContext(ctx).Create(&row); err != nil {
		return Relationship{}, false, fmt.Errorf("insert IAM resource relationship: %w", err)
	}
	return resourceRelationshipFromRow(&row), true, nil
}

func (r *Repository) DeleteRelationship(ctx context.Context, relationship Relationship) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	if !relationship.EntityID.Zero() {
		result := db.WithContext(ctx).Table(tables.ResourceRelationships.TableName()).Where(
			"tenant_id = ? AND entity_id = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ? AND relation = ? AND resource_type = ? AND resource_id = ?",
			relationship.TenantID, relationship.EntityID, relationship.SubjectType,
			relationship.SubjectID, relationship.SubjectRelation, relationship.Relation,
			relationship.ResourceType, relationship.ResourceID,
		).Delete(&model.ResourceRelationship{})
		if result.Error != nil {
			return false, fmt.Errorf("delete IAM resource relationship: %w", result.Error)
		}
		return result.RowsAffected > 0, nil
	}
	result := db.WithContext(ctx).Table(tables.Relationships.TableName()).Where(
		"tenant_id = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ? AND relation = ? AND resource_type = ? AND resource_id = ?",
		relationship.TenantID, relationship.SubjectType, relationship.SubjectID,
		relationship.SubjectRelation, relationship.Relation, relationship.ResourceType,
		relationship.ResourceID,
	).Delete(&model.Relationship{})
	if result.Error != nil {
		return false, fmt.Errorf("delete IAM relationship: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) entityRelationshipCount(ctx context.Context, tenantID, entityID guid.ID) (int, error) {
	tables, db, err := r.tables()
	if err != nil {
		return 0, err
	}
	var entityCount int64
	if err := db.WithContext(ctx).Table(tables.Relationships.TableName()).
		Where("tenant_id = ? AND (resource_id = ? OR (subject_type = 'entity' AND subject_id = ?))", tenantID, entityID, entityID).
		Count(&entityCount).Error; err != nil {
		return 0, fmt.Errorf("count entity relationships: %w", err)
	}
	var resourceCount int64
	if err := db.WithContext(ctx).Table(tables.ResourceRelationships.TableName()).
		Where("tenant_id = ? AND (entity_id = ? OR (subject_type = 'entity' AND subject_id = ?))", tenantID, entityID, entityID).
		Count(&resourceCount).Error; err != nil {
		return 0, fmt.Errorf("count entity resource relationships: %w", err)
	}
	return int(entityCount + resourceCount), nil
}

func relationshipToRow(relationship Relationship) model.Relationship {
	return model.Relationship{
		TenantID: relationship.TenantID, SubjectType: relationship.SubjectType,
		SubjectID: relationship.SubjectID, SubjectRelation: relationship.SubjectRelation,
		Relation: relationship.Relation, ResourceType: relationship.ResourceType,
		ResourceID: relationship.ResourceID, StartsAt: cloneOptionalTime(relationship.StartsAt),
		EndsAt: cloneOptionalTime(relationship.EndsAt), ConditionJSON: string(relationship.Condition),
		CreatedAt: relationship.CreatedAt.UTC().Truncate(time.Millisecond),
	}
}

func resourceRelationshipToRow(relationship Relationship) model.ResourceRelationship {
	return model.ResourceRelationship{
		TenantID: relationship.TenantID, EntityID: relationship.EntityID,
		SubjectType: relationship.SubjectType, SubjectID: relationship.SubjectID,
		SubjectRelation: relationship.SubjectRelation, Relation: relationship.Relation,
		ResourceType: relationship.ResourceType, ResourceID: relationship.ResourceID,
		StartsAt: cloneOptionalTime(relationship.StartsAt), EndsAt: cloneOptionalTime(relationship.EndsAt),
		ConditionJSON: string(relationship.Condition), CreatedAt: relationship.CreatedAt.UTC().Truncate(time.Millisecond),
	}
}

func relationshipFromRow(row *model.Relationship) Relationship {
	return Relationship{
		TenantID: row.TenantID, SubjectType: row.SubjectType, SubjectID: row.SubjectID,
		SubjectRelation: row.SubjectRelation, Relation: row.Relation,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		StartsAt: cloneOptionalTime(row.StartsAt), EndsAt: cloneOptionalTime(row.EndsAt),
		Condition: relationshipCondition(row.ConditionJSON), CreatedAt: row.CreatedAt.UTC(),
	}
}

func resourceRelationshipFromRow(row *model.ResourceRelationship) Relationship {
	return Relationship{
		TenantID: row.TenantID, EntityID: row.EntityID, SubjectType: row.SubjectType,
		SubjectID: row.SubjectID, SubjectRelation: row.SubjectRelation, Relation: row.Relation,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		StartsAt: cloneOptionalTime(row.StartsAt), EndsAt: cloneOptionalTime(row.EndsAt),
		Condition: relationshipCondition(row.ConditionJSON), CreatedAt: row.CreatedAt.UTC(),
	}
}

func relationshipMutableFieldsEqual(
	currentStart, currentEnd *time.Time,
	currentCondition string,
	nextStart, nextEnd *time.Time,
	nextCondition string,
) bool {
	return equalOptionalTime(currentStart, nextStart) &&
		equalOptionalTime(currentEnd, nextEnd) && currentCondition == nextCondition
}

func relationshipCondition(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}

func cloneOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC().Truncate(time.Millisecond)
	return &cloned
}
