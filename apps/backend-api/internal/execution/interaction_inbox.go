package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"slices"
	"time"
)

func (m *Module) registerInteractionInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "INTERACTION", Read: m.interactionInbox})
}

// Runtime approvals now have a real pending/decision lifecycle. Read it with
// the same current Human/work/lease fences as the canonical decision command.
func (m *Module) interactionInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, governance.ErrForbidden
	}
	if req.Kind != "INTERACTION" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, governance.ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	q := query.BindApprovals(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("PENDING"), q.ExpiresAt.Gt(time.Now().UTC())).Find()
	if err != nil {
		return nil, err
	}
	out := []governance.InboxItem{}
	for _, candidate := range rows {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			row, e := m.rowOn(ctx, tx, req.TenantID, candidate.ExecutionID, false)
			if e != nil {
				return e
			}
			if req.ProjectID > 0 && req.ProjectID != row.ProjectID {
				return nil
			}
			if e = m.humanAuthority(ctx, tx, req.TenantID, row.ProjectID, "interaction_respond"); e != nil {
				return e
			}
			if m.work.AuthorizeOn == nil {
				return ErrUnavailable
			}
			if e = m.work.AuthorizeOn(ctx, tx, link(row, req.PrincipalID), "RECEIPT"); e != nil {
				return e
			}
			a := query.BindApprovals(tx, m.names)
			r, e := a.WithContext(ctx).Where(a.TenantID.Eq(req.TenantID), a.ID.Eq(candidate.ID), a.ExecutionID.Eq(row.ID)).First()
			if e != nil {
				return e
			}
			now := time.Now().UTC()
			if r.State != "PENDING" || !r.ExpiresAt.After(now) || row.State != "RUNNING" || row.CancelRequestedAt != nil || row.DispatchEpoch != r.DispatchEpoch || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(now) {
				return nil
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "HIGH", CreatedAt: r.CreatedAt, DueAt: &r.ExpiresAt, Kind: "INTERACTION", ID: r.ID}, ProjectID: row.ProjectID, Title: r.Tool, Reason: "RUNTIME_PERMISSION_REQUIRED", ObjectType: "execution", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
			return nil
		})
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// HeavyCommands reuses canonical launch/cancel authorization, fencing, budget
// reservation and outbox. CreateOn is a single-executor work command; it cannot
// represent concurrent alternatives for the same work. ParallelOn stays nil
// until the source owner supplies a genuine ensemble launch command.
type HeavyCommands struct {
	CreateOn   func(context.Context, *gorm.DB, guid.ID, ExecutionCreate) (ExecutionView, error)
	ParallelOn func(context.Context, *gorm.DB, guid.ID, []ExecutionCreate) ([]ExecutionView, error)
	CancelOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionView, error)
}

func (m *Module) provideHeavy(s *module.Services) error {
	if err := module.Provide[AttentionExecutions](s, AttentionExecutions{ListOn: m.attentionExecutionsOn}); err != nil {
		return err
	}
	if err := module.Provide[InteractionReminderSource](s, m.interactionReminderSourceOn); err != nil {
		return err
	}
	if err := module.Provide[InteractionNotificationOn](s, m.interactionNotificationOn); err != nil {
		return err
	}
	if err := module.Provide[QualityBranchBefore](s, m.qualityBranchBeforeOn); err != nil {
		return err
	}
	return module.Provide[HeavyCommands](s, HeavyCommands{CreateOn: m.CreateOn, ParallelOn: m.parallelEnsembleOn, CancelOn: m.cancelHeavyOn})
}

type InteractionReminderSource func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error)

func (m *Module) interactionReminderSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
	if !validInteractionTransaction(tx) || tenant <= 0 || id <= 0 {
		return 0, 0, time.Time{}, false, ErrInvalidArgument
	}
	q := query.BindApprovals(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, 0, time.Time{}, false, err
	}
	r, err := m.rowOn(ctx, tx, tenant, a.ExecutionID, false)
	if errors.Is(err, ErrNotFound) {
		return 0, 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, 0, time.Time{}, false, err
	}
	now := time.Now().UTC()
	active := a.State == "PENDING" && a.ExpiresAt.After(now) && r.State == "RUNNING" && r.CancelRequestedAt == nil && r.DispatchEpoch == a.DispatchEpoch && r.LeaseExpiresAt != nil && r.LeaseExpiresAt.After(now)
	return r.ID, r.ProjectID, a.ExpiresAt.Add(-5 * time.Minute), active, nil
}

// QualityBranchBefore exposes only an authorized source predicate; the caller
// must independently authorize the work whose acceptance it is calculating.
type QualityBranchBefore func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error)

func (m *Module) qualityBranchBeforeOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, before time.Time) (bool, error) {
	if err := transaction(tx, tenant, project); err != nil || work <= 0 || before.IsZero() {
		return false, ErrInvalidArgument
	}
	if err := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); err != nil {
		return false, err
	}
	q := query.BindExecutions(tx, m.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work), q.BranchReason.Eq("QUALITY"), q.CreatedAt.Lt(before)).Count()
	return n > 0, err
}

func (m *Module) cancelHeavyOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ExecutionView, error) {
	row, err := m.lockControlOn(ctx, tx, tenant, id, "CANCEL")
	if err != nil {
		return ExecutionView{}, err
	}
	return m.CancelOn(ctx, tx, tenant, id, row.Version)
}

// InteractionNotificationOn returns only routing facts after the canonical
// current Human, work, lease, cancel and dispatch fences. No runtime input leaks.
type InteractionNotificationOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error)

func (m *Module) interactionNotificationOn(ctx context.Context, tx *gorm.DB, tenant, approval, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
	if !validInteractionTransaction(tx) || tenant <= 0 || approval <= 0 || recipient <= 0 {
		return 0, 0, false, ErrInvalidArgument
	}
	q := query.BindApprovals(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(approval)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	row, err := m.rowOn(ctx, tx, tenant, a.ExecutionID, false)
	if errors.Is(err, ErrNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	now := time.Now().UTC()
	if a.State != "PENDING" || !a.ExpiresAt.After(now) || row.State != "RUNNING" || row.CancelRequestedAt != nil || row.DispatchEpoch != a.DispatchEpoch || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(now) {
		return row.ID, row.ProjectID, false, nil
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
	if err = m.humanAuthority(readCtx, tx, tenant, row.ProjectID, "interaction_respond"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return row.ID, row.ProjectID, false, nil
		}
		return 0, 0, false, err
	}
	if m.work.AuthorizeOn == nil {
		return 0, 0, false, ErrUnavailable
	}
	if err = m.work.AuthorizeOn(readCtx, tx, link(row, recipient), "RECEIPT"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return row.ID, row.ProjectID, false, nil
		}
		return 0, 0, false, err
	}
	return row.ID, row.ProjectID, true, nil
}
func validInteractionTransaction(tx *gorm.DB) bool {
	if tx == nil || tx.Statement == nil {
		return false
	}
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}

// AttentionExecutions exposes only real interrupted execution identities.
type AttentionExecution struct {
	ID, ProjectID, WorkItemID guid.ID
	CreatedAt                 time.Time
}
type AttentionExecutions struct {
	ListOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]AttentionExecution, error)
}

func (m *Module) attentionExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]AttentionExecution, error) {
	if !validInteractionTransaction(tx) || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("INTERRUPTED"))
	if project > 0 {
		base = base.Where(q.ProjectID.Eq(project))
	}
	rows, err := base.Find()
	if err != nil {
		return nil, err
	}
	out := make([]AttentionExecution, 0, len(rows))
	for _, r := range rows {
		out = append(out, AttentionExecution{ID: r.ID, ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, CreatedAt: r.UpdatedAt})
	}
	return out, nil
}
func (m *Module) attentionSource(s *module.Services) func(context.Context, governance.InboxRequest) ([]governance.InboxItem, error) {
	return func(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
		source, err := module.Resolve[governance.ExecutionAttentionInbox](s)
		if errors.Is(err, module.ErrServiceMissing) {
			return m.attentionInbox(ctx, req)
		}
		if err != nil {
			return nil, err
		}
		return source(ctx, req)
	}
}
