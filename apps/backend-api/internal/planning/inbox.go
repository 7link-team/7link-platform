package planning

import (
	"context"
	"errors"
	"slices"
	"strings"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) registerInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	if err = m.registerCollaborationInbox(s, sources); err != nil {
		return err
	}
	for _, kind := range []string{"PLAN_CONFIRMATION", "REPORT_CONFIRMATION"} {
		if err = sources.Register(governance.InboxSource{Kind: kind, Read: planningInboxSource(m.planningInbox)}); err != nil {
			return err
		}
	}
	if err = sources.Register(governance.InboxSource{Kind: "REPORT_BLOCKED", Read: planningInboxSource(m.reportOverdueInbox)}); err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "REPORT_OVERDUE", Read: planningInboxSource(m.reportOverdueInbox)})
}

// Translate module availability at the public inbox capability boundary.
func planningInboxSource(read func(context.Context, governance.InboxRequest) ([]governance.InboxItem, error)) func(context.Context, governance.InboxRequest) ([]governance.InboxItem, error) {
	return func(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
		items, err := read(ctx, req)
		if errors.Is(err, ErrUnavailable) {
			return nil, governance.ErrUnavailable
		}
		return items, err
	}
}

func (m *Module) reportOverdueInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if (req.Kind != "REPORT_OVERDUE" && req.Kind != "REPORT_BLOCKED") || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return out, ErrInvalid
	}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return out, ErrForbidden
	}
	db, err := m.database()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReports(tx, m.names)
		states := []string{"OVERDUE"}
		if req.Kind == "REPORT_BLOCKED" {
			states = []string{"DRAFT", "READY", "RETURNED", "BLOCKED", "OVERDUE"}
		}
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.In(states...)).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			if req.ProjectID != 0 && r.ProjectID != req.ProjectID {
				continue
			}
			recipient, err := m.reportRecipientOn(ctx, tx, r)
			if err != nil {
				return err
			}
			if recipient != req.PrincipalID {
				continue
			}
			for _, permission := range []string{"report_read", "report_submit"} {
				_, err = m.authorize(ctx, tx, req.TenantID, r.ProjectID, r.PrincipalID, permission, true)
				if errors.Is(err, ErrForbidden) {
					goto nextOverdue
				}
				if err != nil {
					return err
				}
			}
			if req.Kind == "REPORT_BLOCKED" && r.State != "BLOCKED" {
				yes, err := m.reportCheckedNotificationOn(ctx, tx, req.TenantID, r.ID, req.PrincipalID, r.Version)
				if err != nil {
					return err
				}
				if !yes {
					continue
				}
			}
			{
				item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "HIGH", DueAt: r.DueAt, CreatedAt: r.CreatedAt, Kind: req.Kind, ID: r.ID}, Title: "REPORT · " + r.PeriodKind + " · " + r.PeriodStart, ProjectID: r.ProjectID, ObjectType: "report", ObjectID: r.ID, SourcePrincipalID: r.PrincipalID, SourceScope: "MEMBER", TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
				if r.PrincipalID == 0 {
					item.SourceScope = "PROJECT"
					if r.ProjectID == 0 {
						item.SourceScope = "TEAM"
					}
				}
				if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
					out = append(out, item)
				}
			}
		nextOverdue:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// planningInbox uses the same current authority as the source decision. It does
// not infer overdue or blocked reports from dates or missing submissions.
func (m *Module) planningInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return out, ErrForbidden
	}
	kind := "plan"
	if req.Kind == "REPORT_CONFIRMATION" {
		kind = "report"
	} else if req.Kind != "PLAN_CONFIRMATION" {
		return out, ErrInvalid
	}
	if req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return out, ErrInvalid
	}
	db, err := m.database()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidates := []PlanningRecord{}
		if kind == "plan" {
			q := query.BindPlans(tx, m.names)
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("SUBMITTED")).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				candidates = append(candidates, PlanningRecord{AuthorID: r.PrincipalID, ID: r.ID, ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, CreatedAt: r.CreatedAt})
			}
		} else {
			q := query.BindReports(tx, m.names)
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("SUBMITTED")).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				candidates = append(candidates, PlanningRecord{AuthorID: reportAuthor(r), ID: r.ID, ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, CreatedAt: r.CreatedAt})
			}
		}
		for _, r := range candidates {
			if kind == "report" {
				q := query.BindReports(tx, m.names)
				row, e := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.ID.Eq(r.ID)).First()
				if e != nil {
					return e
				}
				recipient, e := m.reportConfirmerOn(ctx, tx, row)
				if e != nil {
					return e
				}
				if recipient != req.PrincipalID {
					continue
				}
			}
			if r.AuthorID == req.PrincipalID || (req.ProjectID != 0 && r.ProjectID != req.ProjectID) {
				continue
			}
			permissions := []string{"plan_confirm"}
			if kind == "report" {
				permissions = []string{"report_read", "report_confirm"}
			}
			for _, permission := range permissions {
				_, e := m.authorize(ctx, tx, req.TenantID, r.ProjectID, r.PrincipalID, permission, true)
				if errors.Is(e, ErrForbidden) {
					goto next
				}
				if e != nil {
					return e
				}
			}
			{
				item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "NORMAL", CreatedAt: r.CreatedAt, Kind: req.Kind, ID: r.ID}, ProjectID: r.ProjectID, Title: strings.ToUpper(kind) + " · " + r.PeriodKind + " · " + r.PeriodStart, ObjectType: kind, ObjectID: r.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
				item.SourcePrincipalID = r.PrincipalID
				item.SourceScope = "MEMBER"
				if kind == "report" && r.PrincipalID == 0 {
					item.SourceScope = "PROJECT"
					if r.ProjectID == 0 {
						item.SourceScope = "TEAM"
					}
				}
				if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
					out = append(out, item)
				}
			}
		next:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
