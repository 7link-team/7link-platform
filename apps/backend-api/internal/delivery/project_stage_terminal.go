package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

type ProjectClientOutcome struct {
	WorkItemID, ResultID guid.ID
	Accepted, Declined   bool
	DecidedAt            time.Time
	ReceiptID            guid.ID
}
type ProjectStagePackageFacts struct {
	Published, Pending, HasAccepted, OpenRework bool
	Outcomes                                    []ProjectClientOutcome
}
type ProjectStagePackages struct {
	FactsOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error)
	ArchiveOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	DecisionOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, string) error
}
type ProjectStagePackagesConnector struct {
	Bind func(ProjectStagePackages) error
}
type ProjectStageExecutionsConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error
}

func (m *Module) provideStageTerminal(s *module.Services) error {
	if e := module.Provide[ProjectStagePackagesConnector](s, ProjectStagePackagesConnector{Bind: func(v ProjectStagePackages) error {
		if v.FactsOn == nil || v.ArchiveOn == nil || v.DecisionOn == nil || m.stagePackages.FactsOn != nil {
			return ErrInvalidArgument
		}
		m.stagePackages = v
		return nil
	}}); e != nil {
		return e
	}
	return module.Provide[ProjectStageExecutionsConnector](s, ProjectStageExecutionsConnector{Bind: func(v func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error {
		if v == nil || m.stageExecutions != nil {
			return ErrInvalidArgument
		}
		m.stageExecutions = v
		return nil
	}})
}
func (m *Module) injectStageTerminal(s *module.Services) error {
	v, e := module.Resolve[governance.ProjectTerminalCleanup](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e == nil {
		m.stageCleanup = v
	}
	return e
}
func (m *Module) stageTerminalCandidates(ctx context.Context, db *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	ids, e := m.stageExecutorCandidates(ctx, db, tenant, project)
	if e != nil {
		return nil, e
	}
	if m.stageCleanup.PrincipalsOn == nil {
		return nil, ErrUnavailable
	}
	other, e := m.stageCleanup.PrincipalsOn(ctx, db, tenant, project)
	if e != nil {
		return nil, e
	}
	ids = append(ids, other...)
	if m.stageCancellation != nil {
		other, e = m.stageCancellation.cancellationPrincipalsOn(ctx, db, tenant, project)
		if e != nil {
			return nil, e
		}
		ids = append(ids, other...)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
func (m *Module) stageTerminalCriteriaOn(ctx context.Context, tx *gorm.DB, p *model.Project, target string) ([]stageCriterion, error) {
	if m.stagePackages.FactsOn == nil {
		return nil, ErrUnavailable
	}
	facts, e := m.stagePackages.FactsOn(ctx, tx, p.TenantID, p.ProjectID)
	if e != nil {
		return nil, e
	}
	out := []stageCriterion{{nil, "no_pending_delivery", !facts.Pending, "No delivery may still await a client receipt."}}
	d := query.BindDeliveries(tx, m.service.names)
	pending, e := d.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(d.TenantID.Eq(p.TenantID), d.ProjectID.Eq(p.ProjectID), d.ArchivedAt.IsNull(), d.State.In("READY", "SENT")).Limit(1).Find()
	if e != nil {
		return nil, e
	}
	out[0].Passed = out[0].Passed && len(pending) == 0
	w := query.BindWorkItems(tx, m.service.names)
	rows, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(p.TenantID), w.ProjectID.Eq(p.ProjectID), w.ArchivedAt.IsNull(), w.IsContainer.Is(false)).Order(w.ID).Find()
	if e != nil {
		return nil, e
	}
	if target == "CLOSED" {
		if m.stageExecutions == nil {
			return nil, ErrUnavailable
		}
		quiet, e := m.stageExecutions(ctx, tx, p.TenantID, p.ProjectID)
		if e != nil {
			return nil, e
		}
		all := true
		for _, r := range rows {
			all = all && (r.State == "ACCEPTED" || r.State == "CANCELLED") //nolint:misspell // Preserve the published domain or serialized contract spelling.
		}
		return append(out, stageCriterion{nil, "executions_terminal", quiet, "No execution may remain CREATED, RUNNING or PAUSED."}, stageCriterion{nil, "work_terminal", all, "All work must be ACCEPTED or CANCELLED."}), nil //nolint:misspell // Preserve the published domain or serialized contract spelling.
	}
	latest := map[guid.ID]ProjectClientOutcome{}
	for _, v := range facts.Outcomes {
		old, exists := latest[v.WorkItemID]
		if exists && v.DecidedAt.Equal(old.DecidedAt) && v.ReceiptID == old.ReceiptID {
			allAccepted := old.Accepted && v.Accepted
			v.Declined = !allAccepted && (old.Accepted || old.Declined) && (v.Accepted || v.Declined)
			v.Accepted = allAccepted
			latest[v.WorkItemID] = v
		} else if !exists || v.DecidedAt.After(old.DecidedAt) || (v.DecidedAt.Equal(old.DecidedAt) && v.ReceiptID > old.ReceiptID) {
			latest[v.WorkItemID] = v
		}
	}
	missing := []guid.ID{}
	for _, r := range rows {
		if r.State == "CANCELLED" || !slices.Contains([]string{"FEATURE", "BUG", "DOC", "DESIGN"}, r.Type) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
			continue
		}
		v, ok := latest[r.ID]
		if !ok || (!v.Declined && (!v.Accepted || r.CurrentClaimID == nil || v.ResultID != *r.CurrentClaimID || r.State != "ACCEPTED" || r.BaselineStale)) {
			if len(missing) < 100 {
				missing = append(missing, r.ID)
			}
		}
	}
	return append(out, stageCriterion{missing, "client_accepted_work", len(missing) == 0, "Current client acceptance is required for every deliverable work item."}, stageCriterion{nil, "accepted_delivery", facts.HasAccepted, "At least one actual client receipt must accept a delivery item."}, stageCriterion{nil, "rework_terminal", !facts.OpenRework, "Every client rejection needs a terminal rework disposition."}), nil
}
func (m *Module) finishProjectOn(ctx context.Context, tx *gorm.DB, p *model.Project, source, reason string, locked []guid.ID) error {
	if m.stageCleanup.EndOn == nil || m.stagePackages.ArchiveOn == nil || m.service.channels.ArchiveProjectChannelsOn == nil {
		return ErrUnavailable
	}
	if e := m.stageCleanup.EndOn(ctx, tx, p.TenantID, p.ProjectID, source, reason, locked); e != nil {
		return e
	}
	if e := m.stagePackages.ArchiveOn(ctx, tx, p.TenantID, p.ProjectID); e != nil {
		return e
	}
	if e := m.service.channels.ArchiveProjectChannelsOn(ctx, tx, p.TenantID, p.ProjectID, collab.ProvisioningAudit{AuthoritySource: source}); e != nil {
		return e
	}
	d := query.BindDeliveries(tx, m.service.names)
	_, e := d.WithContext(ctx).Where(d.TenantID.Eq(p.TenantID), d.ProjectID.Eq(p.ProjectID), d.State.Eq("DRAFT"), d.ArchivedAt.IsNull()).UpdateSimple(d.ArchivedAt.Value(time.Now().UTC().Truncate(time.Millisecond)), d.Version.Add(1))
	return e
}
func (m *Module) cancelStageOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, expected int, body stageChangeBody, cookie string, locked []guid.ID) (stageReceipt, error) {
	if m.stageCancellation == nil || m.stagePackages.DecisionOn == nil {
		return stageReceipt{}, ErrUnavailable
	}

	p, kind, e := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_stage_change")
	if e != nil {
		return stageReceipt{}, e
	}
	if kind != "HUMAN" {
		return stageReceipt{}, ErrWorkForbidden
	}
	if p.Version != expected {
		return stageReceipt{}, ErrWorkVersion
	}
	if body.ConfirmCode != p.Code {
		return stageReceipt{}, ErrInvalidArgument
	}
	id, e := m.ids.Next()
	if e != nil {
		return stageReceipt{}, e
	}
	decision := guid.ID(id)
	source, e := m.stageCancellation.CancelProjectOn(ctx, tx, tenant, project, decision, body.Reason, cookie, body.ReauthProof)
	if e != nil {
		return stageReceipt{}, e
	}
	if e = m.stagePackages.DecisionOn(ctx, tx, tenant, project, decision, expected, body.Reason, source); e != nil {
		return stageReceipt{}, e
	}
	if e = m.finishProjectOn(ctx, tx, p, source, body.Reason, locked); e != nil {
		return stageReceipt{}, e
	}
	q := query.BindProjects(tx, m.service.names)
	p, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if e != nil {
		return stageReceipt{}, e
	}
	t := query.BindStageTransitions(tx, m.service.names)
	r, e := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ProjectID.Eq(project), t.DecisionID.Eq(decision)).First()
	if e != nil {
		return stageReceipt{}, e
	}
	view, e := projectRecordView(*p)
	return stageReceipt{Transition: stageTransitionView{*r, []stageCriterion{}}, Project: view}, e
}

// scopeChangeEffectiveOn binds reopening to an actual post-delivery baseline change.
func (m *Module) scopeChangeEffectiveOn(ctx context.Context, tx *gorm.DB, p *model.Project, id *guid.ID) (bool, error) {
	if id == nil || *id <= 0 {
		return false, nil
	}
	transitions := query.BindStageTransitions(tx, m.service.names)
	used, e := transitions.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(transitions.TenantID.Eq(p.TenantID), transitions.ProjectID.Eq(p.ProjectID), transitions.ScopeChangeID.Eq(*id), transitions.FromStage.Eq("DELIVERED"), transitions.ToStage.Eq("ACTIVE")).Limit(1).Find()
	if e != nil {
		return false, e
	}
	if len(used) > 0 {
		return false, nil
	}
	c := query.BindChanges(tx, m.service.names)
	r, e := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(p.TenantID), c.ProjectID.Eq(p.ProjectID), c.ID.Eq(*id)).Find()
	if e != nil {
		return false, e
	}
	if len(r) != 1 || r[0].Kind != "SCOPE" || r[0].ToVersionNo != r[0].FromVersionNo+1 || p.DeliveredAt == nil || r[0].CreatedAt.Before(*p.DeliveredAt) {
		return false, nil
	}
	q := query.BindRequirements(tx, m.service.names)
	current, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(p.TenantID), q.ProjectID.Eq(p.ProjectID), q.ID.Eq(r[0].RequirementID), q.CurrentVersionNo.Gte(r[0].ToVersionNo), q.ArchivedAt.IsNull()).Find()
	return len(current) == 1, e
}

// Fence every principal before project serialization. Canonical cancellation
// binding fingerprints are reread with current locks, preventing stale RR reads.
func (m *Module) lockProjectStageOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, target string, locked []guid.ID) error {
	fingerprint := ""
	if target == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		if m.stageCancellation == nil {
			return ErrUnavailable
		}
		var e error
		fingerprint, e = m.stageCancellation.cancellationBindingsOn(ctx, tx, tenant, project, false)
		if e != nil {
			return e
		}
		ids, e := m.stageCancellation.cancellationPrincipalsOn(ctx, tx, tenant, project)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if id != actor && !slices.Contains(locked, id) {
				return ErrWorkVersion
			}
		}
	}
	if e := m.lockStagePrincipalsOn(ctx, tx, actor, locked); e != nil {
		return e
	}
	_, _, e := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_stage_change")
	if e != nil && !errors.Is(e, ErrWorkProjectReadOnly) {
		return e
	}
	if target == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		current, e := m.stageCancellation.cancellationBindingsOn(ctx, tx, tenant, project, true)
		if e != nil {
			return e
		}
		if current != fingerprint {
			return ErrWorkVersion
		}
	}
	return nil
}
func (m *Module) stageReplayAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, target string, locked []guid.ID) error {
	if e := m.lockProjectStageOn(ctx, tx, tenant, project, actor, target, locked); e != nil {
		return e
	}
	if m.stageAuthority == nil {
		return ErrUnavailable
	}
	kind := "HUMAN"
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || human.ID != actor {
		kind = "AGENT"
	}
	authority, e := m.stageAuthority(ctx, tx, tenant, project, actor, kind)
	if e != nil {
		return e
	}
	if !authority.Allowed || authority.AuthoritySource == "" {
		return ErrWorkForbidden
	}
	if slices.Contains([]string{"DELIVERED", "CLOSED", "CANCELLED"}, target) && (kind != "HUMAN" || authority.DelegationID != 0 || !strings.HasPrefix(authority.AuthoritySource, "ROLE:")) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return ErrWorkForbidden
	}
	return nil
}
