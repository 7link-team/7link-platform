package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Metadata changes still create a complete immutable baseline and explicitly
// record all impacted work. Criterion changes use the same version boundary.
type scopeChangeBody struct {
	Criteria      scopeCriteriaBody `json:"criteria,omitempty"`
	BaseVersionNo int               `json:"base_version_no" minimum:"1"`
	Summary       string            `json:"summary" minLength:"5" maxLength:"2000"`
	Title         *string           `json:"title,omitempty" minLength:"1" maxLength:"200"`
	Description   *string           `json:"description,omitempty" maxLength:"20000"`
	Priority      *int              `json:"priority,omitempty" minimum:"0" maximum:"3"`
	StaleWorkIDs  []guid.ID         `json:"stale_work_ids,omitempty" maxItems:"500"`
	ReauthProof   string            `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type scopeChangeView struct {
	ID                 guid.ID `json:"id"`
	RequirementID      guid.ID `json:"requirement_id"`
	FromVersionNo      int     `json:"from_version_no"`
	ToVersionNo        int     `json:"to_version_no"`
	RequirementVersion int     `json:"requirement_version"`
	ContentHash        string  `json:"content_hash"`
}

func (m *Module) scopeChangeOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, expected int, cookie string, in scopeChangeBody) (scopeChangeView, error) {
	var empty scopeChangeView
	in.Summary = strings.TrimSpace(in.Summary)
	if utf8.RuneCountInString(in.Summary) < 5 || utf8.RuneCountInString(in.Summary) > 2000 || in.BaseVersionNo < 1 || len(in.StaleWorkIDs) > 500 || (in.Title == nil && in.Description == nil && in.Priority == nil && len(in.Criteria.Add) == 0 && len(in.Criteria.Retire) == 0 && len(in.Criteria.Supersede) == 0) {
		return empty, ErrInvalidArgument
	}
	if m.deliveryAuthority == nil || m.scopeReauthentication == nil {
		return empty, ErrUnavailable
	}
	// Reauthentication takes the principal lock before project/requirement locks.
	if tx == nil || actor <= 0 {
		return empty, ErrInvalidArgument
	}
	if err := m.scopeReauthentication(ctx, tx, actor, cookie, in.ReauthProof, "scope_change", "requirement:"+id.String()); err != nil {
		return empty, err
	}
	c, err := m.requirementWriterForStageOn(ctx, tx, tenant, project, actor, true)
	if err != nil {
		return empty, err
	}
	q := query.BindRequirements(tx, m.service.names)
	scope, err := workScope(c, project, q.CreatedBy)
	if err != nil {
		return empty, err
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.Version != expected || row.CurrentVersionNo != in.BaseVersionNo {
		return empty, ErrRequirementVersion
	}
	if row.Status != "BASELINED" {
		return empty, ErrRequirementFrozen
	}
	decision, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: "HUMAN", Code: "scope_change", Resource: governance.ResourceRef{Kind: "requirement", ID: id}})
	if err != nil {
		return empty, err
	}
	if !decision.Allowed || !strings.HasPrefix(decision.AuthoritySource, "ROLE:") || decision.DelegationID != 0 {
		return empty, ErrWorkForbidden
	}
	before := requirementCreateBody{Title: row.Title, Description: row.Description, Priority: row.Priority}
	if in.Title != nil {
		row.Title = *in.Title
	}
	if in.Description != nil {
		row.Description = *in.Description
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	validated, err := validateRequirement(requirementCreateBody{Title: row.Title, Description: row.Description, Priority: row.Priority})
	if err != nil {
		return empty, err
	}
	row.Title = validated.Title
	if row.Title == before.Title && row.Description == before.Description && row.Priority == before.Priority && len(in.Criteria.Add) == 0 && len(in.Criteria.Retire) == 0 && len(in.Criteria.Supersede) == 0 {
		return empty, ErrInvalidArgument
	}
	next := row.CurrentVersionNo + 1
	criteria, criteriaDiff, err := m.applyScopeCriteriaOn(ctx, tx, tenant, project, id, actor, next, in.Criteria)
	if err != nil {
		return empty, err
	}
	snapshot := requirementSnapshot{Title: row.Title, Description: row.Description, Priority: row.Priority, Criteria: criteria}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	criteriaJSON, _ := json.Marshal(snapshot.Criteria)
	changeID, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	versionID, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	diff, _ := json.Marshal(map[string]any{"before": before, "after": validated, "criteria": criteriaDiff})
	change := &model.ChangeRecord{TenantID: tenant, ID: guid.ID(changeID), ProjectID: project, RequirementID: id, Kind: "SCOPE", FromVersionNo: row.CurrentVersionNo, ToVersionNo: next, Summary: in.Summary, Diff: string(diff), AuthoritySource: decision.AuthoritySource, ActorID: actor, CreatedAt: now}
	if err = query.BindChanges(tx, m.service.names).WithContext(ctx).Create(change); err != nil {
		return empty, err
	}
	work := query.BindWorkItems(tx, m.service.names)
	// Task criteria currently have no Requirement criterion mapping. Every live
	// linked work therefore becomes stale on the new baseline; no old contract or
	// accepted result is rewritten and no per-criterion coverage is guessed.
	linked, err := work.WithContext(ctx).Where(work.TenantID.Eq(tenant), work.ProjectID.Eq(project), work.RequirementID.Eq(id), work.ArchivedAt.IsNull(), work.State.Neq("CANCELLED")).Order(work.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Find() //nolint:misspell // Frozen domain state.
	if err != nil {
		return empty, err
	}
	byID := make(map[guid.ID]bool, len(linked))
	for _, w := range linked {
		byID[w.ID] = true
	}
	seen := map[guid.ID]bool{}
	for _, wid := range in.StaleWorkIDs {
		if !byID[wid] || seen[wid] {
			return empty, ErrInvalidArgument
		}
		seen[wid] = true
	}
	for _, w := range linked {
		wid := w.ID
		var e error
		if e = query.BindChangeImpacts(tx, m.service.names).WithContext(ctx).Create(&model.ChangeImpact{TenantID: tenant, ProjectID: project, ChangeRecordID: change.ID, WorkItemID: wid, Impact: "STALE", VersionBefore: w.Version}); e != nil {
			return empty, e
		}
		result, e := work.WithContext(ctx).Where(work.TenantID.Eq(tenant), work.ID.Eq(wid), work.Version.Eq(w.Version)).UpdateSimple(work.BaselineStale.Value(true), work.BaselineStaleSince.Value(next), work.Version.Value(w.Version+1), work.UpdatedAt.Value(now), work.UpdatedBy.Value(actor))
		if e != nil {
			return empty, e
		}
		if result.RowsAffected != 1 {
			return empty, ErrWorkVersion
		}
		transitionID, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		sourceRef := "scope-change:" + change.ID.String()
		if e = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(&model.WorkItemTransition{TenantID: tenant, ID: guid.ID(transitionID), ProjectID: project, WorkItemID: wid, FromState: &w.State, ToState: w.State, Trigger: "DECISION", ActorKind: "HUMAN", ActorID: &actor, AuthoritySource: decision.AuthoritySource, ReasonCode: "SCOPE_BASELINE_STALE", SourceRef: &sourceRef, VersionAfter: w.Version + 1, CreatedAt: now}); e != nil {
			return empty, e
		}
	}
	versionRow := &model.RequirementVersion{TenantID: tenant, ID: guid.ID(versionID), ProjectID: project, RequirementID: id, VersionNo: next, Title: row.Title, Description: row.Description, Priority: row.Priority, Criteria: string(criteriaJSON), ChangeRecordID: &change.ID, ContentHash: hash, CreatedAt: now, CreatedBy: &actor}
	if err = m.freezeRequirementSuiteOn(ctx, tx, row, versionRow); err != nil {
		return empty, err
	}
	if err = query.BindRequirementVersions(tx, m.service.names).WithContext(ctx).Create(versionRow); err != nil {
		return empty, err
	}
	row.ReviewState = "NEEDS_WORK"
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(&model.Requirement{ReviewState: row.ReviewState}); err != nil {
		return empty, err
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected)).UpdateSimple(q.Title.Value(row.Title), q.Description.Value(row.Description), q.Priority.Value(row.Priority), q.CurrentVersionNo.Value(next), q.Version.Value(expected+1), q.UpdatedAt.Value(now), q.UpdatedBy.Value(actor))
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrRequirementVersion
	}
	row.Version++
	row.CurrentVersionNo = next
	if err = m.requirementEventOn(ctx, tx, row, actor, "delivery.scope_changed", "requirement.versioned", hash); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "requirement.scope_changed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: decision.AuthoritySource}, Subject: events.Subject{Kind: "requirement", ID: id}, SubjectVersion: int64(row.Version), Data: map[string]any{"action": "SCOPE_CHANGE", "change_record_id": change.ID.String(), "version_no": next}})
	if err != nil {
		return empty, err
	}
	return scopeChangeView{ID: change.ID, RequirementID: id, FromVersionNo: in.BaseVersionNo, ToVersionNo: next, RequirementVersion: row.Version, ContentHash: hash}, nil
}
