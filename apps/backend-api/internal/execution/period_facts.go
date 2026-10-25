package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"errors"
	"gorm.io/gorm"
	"slices"
	"time"
)

// PeriodFacts contains persisted execution states for authorized report work
// IDs. Created counts execution creation, never runner start or completion.
// Cost is deliberately absent: this aggregate does not store settled costs.
type PeriodFacts struct {
	Created int64            `json:"created"`
	States  map[string]int64 `json:"states"`
}
type PeriodReader struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (PeriodFacts, error)
}

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.providePackageEvidenceScanner(s); err != nil {
		return err
	}
	if err := m.provideClientThreads(s); err != nil {
		return err
	}
	if err := m.provideDispatchHistory(s); err != nil {
		return err
	}
	if err := m.provideHeavy(s); err != nil {
		return err
	}
	if err := m.provideRealtimeTopics(s); err != nil {
		return err
	}
	if err := m.provideForecastFacts(s); err != nil {
		return err
	}
	if err := m.provideQualityLauncher(s); err != nil {
		return err
	}
	if err := m.provideLoopExecutionFacts(s); err != nil {
		return err
	}
	if err := m.provideVerificationExecutor(s); err != nil {
		return err
	}
	if err := m.provideNotificationFacts(s); err != nil {
		return err
	}
	return module.Provide[PeriodReader](s, PeriodReader{ReadOn: m.periodFactsOn})
}
func (m *Module) periodFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID, start, end time.Time) (PeriodFacts, error) {
	out := PeriodFacts{States: map[string]int64{}}
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || !start.Before(end) || len(work) > 500 {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if e := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); e != nil {
		return out, e
	}
	if len(work) == 0 {
		return out, nil
	}
	ids := make([]driver.Valuer, len(work))
	for i, id := range work {
		if id <= 0 {
			return out, ErrInvalidArgument
		}
		ids[i] = id
	}
	q := query.BindExecutions(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.CreatedAt.Gte(start), q.CreatedAt.Lt(end))
	var rows []struct {
		State string
		Total int64
	}
	if e := base.Select(q.State, q.ID.Count().As("total")).Group(q.State).Scan(&rows); e != nil {
		return out, e
	}
	for _, r := range rows {
		out.States[r.State] = r.Total
		out.Created += r.Total
	}
	return out, nil
}

var _ module.ServiceProvider = (*Module)(nil)

func (m *Module) registerInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "EXECUTION_ATTENTION", Read: m.attentionSource(s)})
}

// attentionInbox exposes persisted interruptions with a real recovery
// command. Runtime failures are not automatic verification verdicts.
func (m *Module) attentionInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, governance.ErrForbidden
	}
	if req.Kind != "EXECUTION_ATTENTION" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, governance.ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return nil, governance.ErrUnavailable
	}
	q := query.BindExecutions(db, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("INTERRUPTED"))
	if req.ProjectID > 0 {
		base = base.Where(q.ProjectID.Eq(req.ProjectID))
	}
	rows, err := base.Order(q.CreatedAt, q.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, candidate := range rows {
		// Keep the recovery command's principal -> project -> machine -> execution
		// lock order for each candidate, without holding another project's locks.
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			row, e := m.lockControlOn(ctx, tx, req.TenantID, candidate.ID, "RECOVER")
			if e != nil {
				return e
			}
			if row.State != "INTERRUPTED" {
				return nil
			}
			if e = m.humanAuthority(ctx, tx, req.TenantID, row.ProjectID, "execution_read"); e != nil {
				return e
			}
			v, e := view(row)
			if e != nil {
				return e
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "HIGH", CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, ProjectID: row.ProjectID, Title: v.Plan.Objective, Reason: row.StateReason, ObjectType: "execution", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
			return nil
		})
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
			continue
		}
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrCapabilityUnavailable) {
			return nil, governance.ErrUnavailable
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
