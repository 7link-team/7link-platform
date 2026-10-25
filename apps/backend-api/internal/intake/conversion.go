package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (m *Module) convertNewSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected, revision int, hash string, requirement guid.ID) error {
	return m.convertSourceTargetOn(ctx, tx, tenant, project, id, expected, revision, hash, project, requirement, "NEW_REQUIREMENT")
}
func (m *Module) convertProjectSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected, revision int, hash string, target, requirement guid.ID) error {
	if target <= 0 || target == project {
		return ErrInvalidArgument
	}
	return m.convertSourceTargetOn(ctx, tx, tenant, project, id, expected, revision, hash, target, requirement, "NEW_PROJECT")
}
func (m *Module) convertSourceTargetOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected, revision int, hash string, target, requirement guid.ID, mode string) error {
	if requirement <= 0 || expected < 1 || revision < 1 || len(hash) != 64 {
		return ErrInvalidArgument
	}
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
		return err
	}
	return m.writeSourceTargetOn(ctx, tx, tenant, project, id, expected, revision, hash, target, requirement, mode, "HUMAN", "permission:scope_change")
}

func (m *Module) writeSourceTargetOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected, revision int, hash string, target, requirement guid.ID, mode, actorKind, authority string) error {
	if requirement <= 0 || expected < 1 || revision < 1 || len(hash) != 64 {
		return ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return err
	}
	if row.Kind != "NEW" || row.Status != "TRIAGED" || row.Version != expected || row.LatestCardRevision != revision || row.OpenRoundID != nil {
		return ErrConflict
	}
	card, err := m.readTriageSourceOn(ctx, tx, tenant, row)
	if err != nil {
		return err
	}
	if card.Hash != hash {
		return ErrConflict
	}
	at, actor := time.Now().UTC().Truncate(time.Millisecond), authn.PrincipalIDFromContext(ctx)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected)).Updates(map[string]any{"project_id": target, "status": "CONVERTED", "conversion_mode": mode, "requirement_id": requirement, "version": expected + 1, "resolved_at": at, "resolved_actor_kind": actorKind, "resolved_actor_id": actor, "resolved_authority_source": authority, "updated_at": at, "updated_by": actor})
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return m.sourceEvent(ctx, tx, tenant, target, id, "intake.converted", expected+1, map[string]any{"mode": mode, "source_project_id": project.String(), "project_id": target.String(), "requirement_id": requirement.String(), "card_revision": revision, "proposal_hash": hash, "actor_kind": actorKind}, authority)
}

// advanceReworkSourceOn retains the immutable rejection and advances only its
// processing state. Per-item decisions live in the caller's same transaction.
func (m *Module) advanceReworkSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected int, complete bool) (int, error) {
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "decision_request_changes"); err != nil {
		return 0, err
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return 0, err
	}
	if row.Kind != "REWORK" || row.Version != expected || row.OpenRoundID != nil || (row.Status != "RECEIVED" && row.Status != "TRIAGED" && row.Status != "CONVERTED") {
		return 0, ErrConflict
	}
	state := row.Status
	if complete {
		state = "CONVERTED"
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	values := map[string]any{"status": state, "version": expected + 1, "updated_at": at, "updated_by": actor}
	if complete {
		values["resolved_at"] = at
		values["resolved_actor_kind"] = "HUMAN"
		values["resolved_actor_id"] = actor
		values["resolved_authority_source"] = "permission:decision_request_changes"
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected)).Updates(values)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected != 1 {
		return 0, ErrConflict
	}
	return expected + 1, m.sourceEvent(ctx, tx, tenant, project, id, "intake.converted", expected+1, map[string]any{"mode": "REWORK_ITEMS", "complete": complete})
}
