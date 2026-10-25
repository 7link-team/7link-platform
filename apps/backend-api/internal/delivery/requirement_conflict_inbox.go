package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) registerRequirementConflictInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if err != nil {
		return err
	}
	leads, err := module.Resolve[governance.ProjectLead](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "REQUIREMENT_CONFLICT", Read: func(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
		return m.requirementConflictInbox(ctx, req, leads)
	}})
}

// A conflict is actionable only for a current Human lead with the same current
// scope and project authority required by the canonical resolution command.
func (m *Module) requirementConflictInbox(ctx context.Context, req governance.InboxRequest, leads governance.ProjectLead) ([]governance.InboxItem, error) {
	if req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.TenantID != authn.TenantIDFromContext(ctx) {
		return nil, governance.ErrForbidden
	}
	if req.Kind != "REQUIREMENT_CONFLICT" || req.ProjectID < 0 || req.Limit < 1 || req.Limit > 101 {
		return nil, governance.ErrInvalidArgument
	}
	if m == nil || m.service == nil || leads == nil {
		return nil, governance.ErrUnavailable
	}
	out := []governance.InboxItem{}
	read := func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("DRAFT"), q.EscalationReason.Eq("REQUIREMENT_CONFLICT"), q.CurrentExecutionID.IsNull(), q.ArchivedAt.IsNull())
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		rows, err := stmt.Find()
		if err != nil {
			return err
		}
		for _, candidate := range rows {
			_, work, _, err := m.lockedWorkActorOn(ctx, tx, req.TenantID, candidate.ProjectID, candidate.ID, req.PrincipalID, candidate.AccountablePrincipalID, "scope_change", false)
			if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrInvalidMember) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, ErrWorkProjectReadOnly) {
				continue
			}
			if err != nil {
				return err
			}
			if work.State != "DRAFT" || work.CurrentExecutionID != nil || work.EscalationReason == nil || *work.EscalationReason != "REQUIREMENT_CONFLICT" {
				continue
			}
			var issues []workReadinessIssue
			if err = json.Unmarshal([]byte(work.ReadinessIssues), &issues); err != nil {
				return ErrUnavailable
			}
			if !slices.ContainsFunc(issues, func(issue workReadinessIssue) bool { return issue.Code == "REQUIREMENT_CONFLICT" }) {
				continue
			}
			if _, err = m.readWork(ctx, tx, req.TenantID, work.ProjectID, work.ID, req.PrincipalID); errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			yes, err := leads(ctx, tx, req.TenantID, work.ProjectID, req.PrincipalID)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			transitions := query.BindWorkTransitions(tx, m.service.names)
			raised, err := transitions.WithContext(ctx).Where(transitions.TenantID.Eq(req.TenantID), transitions.ProjectID.Eq(work.ProjectID), transitions.WorkItemID.Eq(work.ID), transitions.ReasonCode.Eq("REQUIREMENT_CONFLICT_RAISE")).Order(transitions.VersionAfter.Desc()).First()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "HIGH", CreatedAt: raised.CreatedAt, Kind: req.Kind, ID: raised.ID}, ProjectID: work.ProjectID, Title: work.Title, Reason: "REQUIREMENT_CONFLICT", ObjectType: "work_item", ObjectID: work.ID, SourceRevision: strconv.Itoa(raised.VersionAfter), SourceScope: "PROJECT", TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	}
	var err error
	if req.Transaction != nil {
		err = read(req.Transaction)
	} else {
		if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return nil, governance.ErrUnavailable
		}
		err = m.runtime.Database().Write().WithContext(ctx).Transaction(read)
	}
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
