package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

const (
	maxRelationshipDepth   = 16
	relationshipQueryBatch = 200
)

type relationshipNode = query.RelationshipWalkNode

type relationshipGrant struct {
	EntityID     guid.ID
	ResourceType string
	ResourceID   guid.ID
	Relation     string
	Path         []authz.RelationshipStep
}

// ListRelationships returns tenant entity relationships or, when EntityID is
// set, business-resource relationships owned by that entity.
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

// PutRelationship idempotently creates or replaces one entity or business
// resource relationship.
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

// DeleteRelationship removes one exact tenant relationship tuple.
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

// ListRelationships validates the tenant filter and returns matching
// relationships.
func (s *Service) ListRelationships(
	ctx context.Context,
	tenantID guid.ID,
	filter RelationshipFilter,
) ([]Relationship, error) {
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	if err := validateTenant(tenantID); err != nil ||
		(filter.ResourceType != "" && !validEntityType(filter.ResourceType)) {
		return nil, fmt.Errorf("%w: invalid relationship filter", ErrInvalidArgument)
	}
	return s.repo.ListRelationships(ctx, tenantID, filter)
}

// PutRelationship validates relationship facts, conditions, windows, and
// graph depth before an atomic audited upsert.
func (s *Service) PutRelationship(
	ctx context.Context,
	relationship Relationship,
) (Relationship, bool, error) {
	normalizeRelationship(&relationship)
	condition, err := policyx.CanonicalCondition(relationship.Condition)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("%w: %w", ErrInvalidRelationshipCondition, err)
	}
	relationship.Condition = condition
	if err := validateRelationshipShape(relationship); err != nil {
		return Relationship{}, false, err
	}
	if err := validateRelationshipWindow(relationship, s.repo.now().UTC()); err != nil {
		return Relationship{}, false, err
	}
	if !relationship.EntityID.Zero() && !s.resourceRelationDeclared(relationship.ResourceType, relationship.Relation) {
		return Relationship{}, false, fmt.Errorf("%w: resource type does not declare this relation", ErrInvalidRelationship)
	}
	targetType := "entity"
	if !relationship.EntityID.Zero() {
		targetType = relationship.ResourceType
	}
	record := newAuditRecord(ctx, relationship.TenantID, "relationship_put", targetType, relationship.ResourceID)
	record.Detail["subject_type"], record.Detail["subject_id"] = relationship.SubjectType, relationship.SubjectID
	record.Detail["subject_relation"], record.Detail["relation"] = relationship.SubjectRelation, relationship.Relation
	record.Detail["resource_type"] = relationship.ResourceType
	if !relationship.EntityID.Zero() {
		record.Detail["entity_id"] = relationship.EntityID
	}
	if relationship.StartsAt != nil {
		record.Detail["starts_at"] = relationship.StartsAt.Format(time.RFC3339Nano)
	}
	if relationship.EndsAt != nil {
		record.Detail["ends_at"] = relationship.EndsAt.Format(time.RFC3339Nano)
	}
	if len(relationship.Condition) > 0 {
		record.Detail["condition"] = relationship.Condition
	}
	var changed bool
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), relationship.TenantID); err != nil {
			return err
		}
		if err := validateRelationshipFacts(ctx, repo, relationship); err != nil {
			return err
		}
		if relationship.EntityID.Zero() {
			if err := repo.validateRelationshipAddition(ctx, relationship); err != nil {
				return err
			}
		}
		relationship.CreatedAt = repo.now().UTC()
		var err error
		relationship, changed, err = repo.PutRelationship(ctx, relationship)
		if err != nil || !changed {
			record.SkipAudit = !changed
			return err
		}
		record.PolicyChanged = true
		return nil
	})
	return relationship, changed, err
}

// DeleteRelationship validates and removes one exact relationship tuple.
func (s *Service) DeleteRelationship(ctx context.Context, relationship Relationship) (bool, error) {
	normalizeRelationship(&relationship)
	if err := validateRelationshipShape(relationship); err != nil {
		return false, err
	}
	targetType := "entity"
	if !relationship.EntityID.Zero() {
		targetType = relationship.ResourceType
	}
	record := newAuditRecord(ctx, relationship.TenantID, "relationship_deleted", targetType, relationship.ResourceID)
	record.Detail["subject_type"], record.Detail["subject_id"] = relationship.SubjectType, relationship.SubjectID
	record.Detail["subject_relation"], record.Detail["relation"] = relationship.SubjectRelation, relationship.Relation
	record.Detail["resource_type"] = relationship.ResourceType
	if !relationship.EntityID.Zero() {
		record.Detail["entity_id"] = relationship.EntityID
	}
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		var err error
		changed, err = repo.DeleteRelationship(ctx, relationship)
		record.PolicyChanged, record.SkipAudit = changed, !changed
		return err
	})
	return changed, err
}

func normalizeRelationship(relationship *Relationship) {
	if relationship == nil {
		return
	}
	relationship.SubjectType = strings.TrimSpace(relationship.SubjectType)
	relationship.SubjectRelation = strings.TrimSpace(relationship.SubjectRelation)
	relationship.Relation = strings.TrimSpace(relationship.Relation)
	relationship.ResourceType = strings.TrimSpace(relationship.ResourceType)
	relationship.StartsAt = normalizeRelationshipTime(relationship.StartsAt)
	relationship.EndsAt = normalizeRelationshipTime(relationship.EndsAt)
}

func normalizeRelationshipTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Truncate(time.Millisecond)
	return &normalized
}

func validateRelationshipWindow(relationship Relationship, now time.Time) error {
	if (relationship.StartsAt != nil && relationship.StartsAt.IsZero()) ||
		(relationship.EndsAt != nil && relationship.EndsAt.IsZero()) ||
		(relationship.StartsAt != nil && relationship.EndsAt != nil && !relationship.EndsAt.After(*relationship.StartsAt)) ||
		(relationship.EndsAt != nil && !relationship.EndsAt.After(now)) {
		return fmt.Errorf("%w: use a future end time after the optional start time", ErrInvalidRelationshipWindow)
	}
	return nil
}

func validateRelationshipShape(relationship Relationship) error {
	if validateTenant(relationship.TenantID) != nil || relationship.SubjectID.Zero() ||
		!validEntityType(relationship.ResourceType) || relationship.ResourceID.Zero() || !validRelation(relationship.Relation) {
		return fmt.Errorf("%w: invalid subject, relation, or resource", ErrInvalidRelationship)
	}
	switch relationship.SubjectType {
	case "principal":
		if relationship.SubjectRelation != "" {
			return fmt.Errorf("%w: principal subjects cannot specify a subject relation", ErrInvalidRelationship)
		}
	case "group", "position":
		if relationship.SubjectRelation != "member" {
			return fmt.Errorf("%w: group and position subjects require the member relation", ErrInvalidRelationship)
		}
	case "entity":
		if !validRelation(relationship.SubjectRelation) {
			return fmt.Errorf("%w: entity subjects require owner, editor, or viewer", ErrInvalidRelationship)
		}
	default:
		return fmt.Errorf("%w: unknown subject type", ErrInvalidRelationship)
	}
	return nil
}

func validateRelationshipFacts(ctx context.Context, repo *Repository, relationship Relationship) error {
	resourceID := relationship.ResourceID
	if !relationship.EntityID.Zero() {
		resourceID = relationship.EntityID
	}
	resource, err := repo.GetEntity(ctx, relationship.TenantID, resourceID)
	if err != nil {
		return err
	}
	if relationship.EntityID.Zero() && resource.Type != relationship.ResourceType {
		return fmt.Errorf("%w: resource type does not match the entity", ErrInvalidRelationship)
	}
	if resource.Status != EntityActive {
		return ErrRelationshipResourceInactive
	}
	switch relationship.SubjectType {
	case "principal":
		member, err := repo.GetMember(ctx, relationship.TenantID, relationship.SubjectID)
		if err != nil {
			if errors.Is(err, ErrMemberNotFound) {
				return ErrRelationshipSubjectMissing
			}
			return err
		}
		if member.Status != MemberActive {
			return ErrRelationshipSubjectInactive
		}
	case "entity":
		entity, err := repo.GetEntity(ctx, relationship.TenantID, relationship.SubjectID)
		if err != nil {
			if errors.Is(err, ErrEntityNotFound) {
				return ErrRelationshipSubjectMissing
			}
			return err
		}
		if entity.Status != EntityActive {
			return ErrRelationshipSubjectInactive
		}
	default:
		status, err := repo.directoryAssigneeStatus(
			ctx, relationship.TenantID, DirectoryAssigneeType(relationship.SubjectType), relationship.SubjectID,
		)
		if errors.Is(err, ErrDirectoryAssigneeNotFound) {
			return ErrRelationshipSubjectMissing
		}
		if err != nil {
			return fmt.Errorf("get relationship subject: %w", err)
		}
		if status != "active" {
			return ErrRelationshipSubjectInactive
		}
	}
	return nil
}

func (s *Service) resourceRelationDeclared(resourceType, relation string) bool {
	for _, action := range s.registry.All() {
		index := sort.SearchStrings(action.AllowedRelations, relation)
		if action.Resource == resourceType && index < len(action.AllowedRelations) &&
			action.AllowedRelations[index] == relation {
			return true
		}
	}
	return false
}

func (r *Repository) validateRelationshipAddition(ctx context.Context, relationship Relationship) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	var count int64
	err = db.WithContext(ctx).Table(tables.Relationships.TableName()).Where(
		"tenant_id = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ? AND relation = ? AND resource_type = ? AND resource_id = ?",
		relationship.TenantID, relationship.SubjectType, relationship.SubjectID,
		relationship.SubjectRelation, relationship.Relation, relationship.ResourceType, relationship.ResourceID,
	).Count(&count).Error
	if err != nil {
		return fmt.Errorf("check IAM relationship topology: %w", err)
	}
	if count > 0 {
		return nil
	}

	source := relationshipNode{
		Type: relationship.SubjectType, ID: relationship.SubjectID, Relation: relationship.SubjectRelation,
	}
	target := relationshipNode{Type: "entity", ID: relationship.ResourceID, Relation: relationship.Relation}
	walk := query.RelationshipWalkParams{
		Relationships: query.BindNames(r.prefix()).Relationships,
		Dialect:       db.Dialector.Name(),
		TenantID:      relationship.TenantID,
		MaxDepth:      maxRelationshipDepth,
		Seed:          target,
		Against:       source,
	}
	downstream, err := query.RelationshipWalkDownstream(ctx, db, walk)
	if err != nil {
		return err
	}
	if downstream.ReachesSource != 0 {
		return ErrRelationshipHierarchy
	}

	walk.Seed, walk.Against = source, relationshipNode{}
	upstream, err := query.RelationshipWalkUpstream(ctx, db, walk)
	if err != nil {
		return err
	}
	if upstream.MaxDepth+1+downstream.MaxDepth > maxRelationshipDepth {
		return ErrRelationshipHierarchy
	}
	return nil
}

func (a *Authorizer) loadRelationshipGrants(
	ctx context.Context,
	tenantID, subject guid.ID,
	trusted policyx.TrustedContext,
) ([]relationshipGrant, error) {
	now := trusted.Time.UTC().Truncate(time.Millisecond)
	frontier, err := a.relationshipRoots(ctx, tenantID, subject, now)
	if err != nil {
		return nil, err
	}
	visited := make(map[relationshipNode]bool, len(frontier))
	paths := make(map[relationshipNode][]authz.RelationshipStep)
	for _, node := range frontier {
		visited[node] = true
	}
	grants := make([]relationshipGrant, 0)
	for depth := 0; depth < maxRelationshipDepth && len(frontier) > 0; depth++ {
		resourceRows, err := listActiveResourceRelationshipsBySubjects(
			ctx, a.db, a.prefix, tenantID, frontier, now,
		)
		if err != nil {
			return nil, err
		}
		for _, row := range resourceRows {
			matches, err := relationshipConditionMatches(row.ConditionJSON, trusted)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			source := relationshipNode{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation}
			path := append([]authz.RelationshipStep(nil), paths[source]...)
			path = append(path, authz.RelationshipStep{
				EntityID: row.EntityID.String(), SubjectType: row.SubjectType,
				SubjectID: row.SubjectID.String(), SubjectRelation: row.SubjectRelation,
				Relation: row.Relation, ResourceType: row.ResourceType,
				ResourceID: row.ResourceID.String(), Condition: relationshipCondition(row.ConditionJSON),
			})
			grants = append(grants, relationshipGrant{
				EntityID: row.EntityID, ResourceType: row.ResourceType,
				ResourceID: row.ResourceID, Relation: row.Relation, Path: path,
			})
		}
		rows, err := listActiveRelationshipsBySubjects(ctx, a.db, a.prefix, tenantID, frontier, now)
		if err != nil {
			return nil, err
		}
		next := make([]relationshipNode, 0, len(rows))
		for _, row := range rows {
			matches, err := relationshipConditionMatches(row.ConditionJSON, trusted)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			source := relationshipNode{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation}
			target := relationshipNode{Type: "entity", ID: row.ResourceID, Relation: row.Relation}
			if visited[target] {
				continue
			}
			path := append([]authz.RelationshipStep(nil), paths[source]...)
			path = append(path, authz.RelationshipStep{
				SubjectType: row.SubjectType, SubjectID: row.SubjectID.String(),
				SubjectRelation: row.SubjectRelation, Relation: row.Relation,
				ResourceType: row.ResourceType, ResourceID: row.ResourceID.String(),
				Condition: relationshipCondition(row.ConditionJSON),
			})
			visited[target], paths[target] = true, path
			grants = append(grants, relationshipGrant{
				ResourceType: row.ResourceType, ResourceID: row.ResourceID, Relation: row.Relation, Path: path,
			})
			next = append(next, target)
		}
		frontier = next
	}
	return grants, nil
}

func (a *Authorizer) relationshipRoots(
	ctx context.Context,
	tenantID, subject guid.ID,
	now time.Time,
) ([]relationshipNode, error) {
	names, err := a.authorizationTables()
	if err != nil {
		return nil, err
	}
	roots := []relationshipNode{{Type: "principal", ID: subject}}
	groups := make([]guid.ID, 0)
	err = a.db.WithContext(ctx).Table(names.Directory.GroupMembers.String()+" AS gm").
		Select("gm.group_id").
		Joins("JOIN "+names.Directory.Groups.String()+" AS g ON g.tenant_id = gm.tenant_id AND g.id = gm.group_id AND g.status = 'active' AND g.group_type = 'static'").
		Where("gm.tenant_id = ? AND gm.principal_id = ?", tenantID, subject).
		Where("(gm.starts_at IS NULL OR gm.starts_at <= ?) AND (gm.ends_at IS NULL OR gm.ends_at > ?)", now, now).
		Order("gm.group_id ASC").Pluck("gm.group_id", &groups).Error
	if err != nil {
		return nil, fmt.Errorf("list relationship groups: %w", err)
	}
	for _, id := range groups {
		roots = append(roots, relationshipNode{Type: "group", ID: id, Relation: "member"})
	}
	dynamicGroups, err := matchingDynamicGroupIDs(ctx, a.db, names, tenantID, subject)
	if err != nil {
		return nil, err
	}
	for _, id := range dynamicGroups {
		roots = append(roots, relationshipNode{Type: "group", ID: id, Relation: "member"})
	}
	positions := make([]guid.ID, 0)
	err = a.db.WithContext(ctx).Table(names.Directory.PositionMembers.String()+" AS pm").
		Select("pm.position_id").
		Joins("JOIN "+names.Directory.Positions.String()+" AS p ON p.tenant_id = pm.tenant_id AND p.id = pm.position_id AND p.status = 'active'").
		Where("pm.tenant_id = ? AND pm.principal_id = ?", tenantID, subject).
		Where("(pm.starts_at IS NULL OR pm.starts_at <= ?) AND (pm.ends_at IS NULL OR pm.ends_at > ?)", now, now).
		Order("pm.position_id ASC").Pluck("pm.position_id", &positions).Error
	if err != nil {
		return nil, fmt.Errorf("list relationship positions: %w", err)
	}
	for _, id := range positions {
		roots = append(roots, relationshipNode{Type: "position", ID: id, Relation: "member"})
	}
	return roots, nil
}

func listActiveRelationshipsBySubjects(
	ctx context.Context,
	db *gorm.DB,
	prefix string,
	tenantID guid.ID,
	subjects []relationshipNode,
	now time.Time,
) ([]*model.Relationship, error) {
	rows := make([]*model.Relationship, 0)
	tables := query.BindNames(prefix)
	for start := 0; start < len(subjects); start += relationshipQueryBatch {
		end := min(start+relationshipQueryBatch, len(subjects))
		conditions := make([]string, 0, end-start)
		arguments := make([]any, 0, (end-start)*3)
		for _, subject := range subjects[start:end] {
			conditions = append(conditions, "(rel.subject_type = ? AND rel.subject_id = ? AND rel.subject_relation = ?)")
			arguments = append(arguments, subject.Type, subject.ID, subject.Relation)
		}
		batch := make([]*model.Relationship, 0)
		err := db.WithContext(ctx).Table(tables.Relationships.String()+" AS rel").Select("rel.*").
			Joins("JOIN "+tables.Entities.String()+" AS resource ON resource.tenant_id = rel.tenant_id AND resource.id = rel.resource_id AND resource.type = rel.resource_type AND resource.status = 'active'").
			Where("rel.tenant_id = ? AND (rel.starts_at IS NULL OR rel.starts_at <= ?) AND (rel.ends_at IS NULL OR rel.ends_at > ?)", tenantID, now, now).
			Where("("+strings.Join(conditions, " OR ")+")", arguments...).
			Order("rel.resource_type ASC, rel.resource_id ASC, rel.relation ASC, rel.subject_type ASC, rel.subject_id ASC").
			Find(&batch).Error
		if err != nil {
			return nil, fmt.Errorf("traverse IAM relationships: %w", err)
		}
		rows = append(rows, batch...)
	}
	return rows, nil
}

func listActiveResourceRelationshipsBySubjects(
	ctx context.Context,
	db *gorm.DB,
	prefix string,
	tenantID guid.ID,
	subjects []relationshipNode,
	now time.Time,
) ([]*model.ResourceRelationship, error) {
	rows := make([]*model.ResourceRelationship, 0)
	tables := query.BindNames(prefix)
	for start := 0; start < len(subjects); start += relationshipQueryBatch {
		end := min(start+relationshipQueryBatch, len(subjects))
		conditions := make([]string, 0, end-start)
		arguments := make([]any, 0, (end-start)*3)
		for _, subject := range subjects[start:end] {
			conditions = append(conditions, "(rel.subject_type = ? AND rel.subject_id = ? AND rel.subject_relation = ?)")
			arguments = append(arguments, subject.Type, subject.ID, subject.Relation)
		}
		batch := make([]*model.ResourceRelationship, 0)
		err := db.WithContext(ctx).Table(tables.ResourceRelationships.String()+" AS rel").Select("rel.*").
			Joins("JOIN "+tables.Entities.String()+" AS entity ON entity.tenant_id = rel.tenant_id AND entity.id = rel.entity_id AND entity.status = 'active'").
			Where("rel.tenant_id = ? AND (rel.starts_at IS NULL OR rel.starts_at <= ?) AND (rel.ends_at IS NULL OR rel.ends_at > ?)", tenantID, now, now).
			Where("("+strings.Join(conditions, " OR ")+")", arguments...).
			Order("rel.entity_id ASC, rel.resource_type ASC, rel.resource_id ASC, rel.relation ASC, rel.subject_type ASC, rel.subject_id ASC").
			Find(&batch).Error
		if err != nil {
			return nil, fmt.Errorf("traverse IAM resource relationships: %w", err)
		}
		rows = append(rows, batch...)
	}
	return rows, nil
}

func relationshipConditionMatches(value string, trusted policyx.TrustedContext) (bool, error) {
	matched, err := policyx.EvaluateCondition(relationshipCondition(value), trusted)
	if err != nil {
		return false, fmt.Errorf("evaluate relationship condition: %w", err)
	}
	return matched, nil
}

func validRelation(value string) bool {
	return value == "owner" || value == "editor" || value == "viewer"
}

func relationshipAllowed(action authz.Action, grant relationshipGrant) bool {
	if action.Resource != "entity" && action.Resource != grant.ResourceType {
		return false
	}
	index := sort.SearchStrings(action.AllowedRelations, grant.Relation)
	return index < len(action.AllowedRelations) && action.AllowedRelations[index] == grant.Relation
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
