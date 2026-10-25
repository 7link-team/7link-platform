package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type AutomationScope struct {
	TenantInput
	ProjectID string `query:"project_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type automationListInput struct {
	AutomationScope
	Cursor      string `query:"cursor" maxLength:"19"`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort        string `query:"sort" default:"id" enum:"id"`
	TestEventID string `query:"test_event_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type AutomationRulePath struct {
	AutomationScope
	RuleID string `path:"rule_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type automationCreateInput struct {
	AutomationScope
	WriteHeaders
	Body AutomationRuleInput
}
type automationUpdateInput struct {
	AutomationRulePath
	WriteHeaders
	Body AutomationRuleInput
}
type automationArchiveInput struct {
	AutomationRulePath
	WriteHeaders
}
type automationRunListInput struct {
	AutomationRulePath
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"id" enum:"id"`
}
type AutomationCatalog struct {
	Rules    Page[AutomationRuleView] `json:"rules"`
	Previews []AutomationPreview      `json:"previews"`
}
type AutomationRunView struct {
	RunAs              string  `json:"run_as"`
	ActionsResult      string  `json:"actions_result"`
	ID                 guid.ID `json:"id"`
	RuleID             guid.ID `json:"rule_id"`
	RuleVersion        int     `json:"rule_version"`
	RuleName           string  `json:"rule_name"`
	TriggerEventID     guid.ID `json:"trigger_event_id"`
	WorkItemID         guid.ID `json:"work_item_id"`
	Outcome            string  `json:"outcome"`
	Error              string  `json:"error,omitempty"`
	DurationMS         int64   `json:"duration_ms"`
	DefinitionSnapshot string  `json:"definition_snapshot"`
}

func automationScopePath(scope AutomationScope) WorkProjectPath {
	return WorkProjectPath(scope)
}
func (m *Module) registerAutomationRulesHTTP(api huma.API) error {
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: id, Tags: []string{"automation-rules"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("automation.rule-list", "GET", "/automation-rules", 200), m.listAutomationRulesHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("automation.rule-create", "POST", "/automation-rules", 201), m.createAutomationRuleHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("automation.rule-update", "PATCH", "/automation-rules/{rule_id}", 200), m.updateAutomationRuleHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("automation.rule-archive", "DELETE", "/automation-rules/{rule_id}", 200), m.archiveAutomationRuleHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("automation.run-list", "GET", "/automation-rules/{rule_id}/runs", 200), m.listAutomationRunsHTTP)
	for _, path := range []string{"/tenants/{tenant_id}/automation-rules", "/tenants/{tenant_id}/automation-rules/{rule_id}"} {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) listAutomationRulesHTTP(ctx context.Context, in *automationListInput) (*respx.Body[AutomationCatalog], error) {
	tenant, project, e := workIDs(automationScopePath(in.AutomationScope))
	if e != nil {
		return nil, httpError(ctx, e)
	}
	out := AutomationCatalog{Rules: Page[AutomationRuleView]{Items: []AutomationRuleView{}}, Previews: []AutomationPreview{}}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	e = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
			return e
		}
		cursor := guid.ID(0)
		if in.Cursor != "" {
			v, e := guid.RequireID(in.Cursor)
			if e != nil {
				return ErrInvalid
			}
			cursor = v
		}
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		q := query.BindAutomationRules(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Gt(cursor)).Order(q.ID).Limit(limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > limit {
			out.Rules.HasMore = true
			rows = rows[:limit]
			out.Rules.NextCursor = rows[limit-1].ID.String()
		}
		for _, row := range rows {
			view, e := automationRuleView(row)
			if e != nil {
				return e
			}
			out.Rules.Items = append(out.Rules.Items, view)
			if in.TestEventID != "" {
				event, e := guid.RequireID(in.TestEventID)
				if e != nil {
					return ErrInvalid
				}
				preview, e := m.previewAutomationOn(ctx, tx, tenant, project, row.ID, event)
				if e != nil {
					return e
				}
				out.Previews = append(out.Previews, preview)
			}
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createAutomationRuleHTTP(ctx context.Context, in *automationCreateInput) (*commandOutput[AutomationRuleView], error) {
	path := automationScopePath(in.AutomationScope)
	tenant, project, e := workIDs(path)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return workCommand(m, ctx, path, in.WriteHeaders, "automation.rule-create", "workflow_manage", in.Body, 201, func(tx *gorm.DB) (AutomationRuleView, int, error) {
		row, e := m.saveAutomationDefinitionOn(ctx, tx, tenant, project, 0, 0, in.Body)
		return row, row.Version, e
	})
}
func (m *Module) updateAutomationRuleHTTP(ctx context.Context, in *automationUpdateInput) (*commandOutput[AutomationRuleView], error) {
	path := automationScopePath(in.AutomationScope)
	tenant, project, e := workIDs(path)
	id, e2 := guid.RequireID(in.RuleID)
	version, e3 := versionHeader(in.IfMatch)
	if e != nil || e2 != nil || e3 != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return workCommand(m, ctx, path, in.WriteHeaders, "automation.rule-update:"+id.String(), "workflow_manage", in.Body, 200, func(tx *gorm.DB) (AutomationRuleView, int, error) {
		row, e := m.saveAutomationDefinitionOn(ctx, tx, tenant, project, id, version, in.Body)
		return row, row.Version, e
	})
}
func (m *Module) archiveAutomationRuleHTTP(ctx context.Context, in *automationArchiveInput) (*commandOutput[AutomationRuleView], error) {
	path := automationScopePath(in.AutomationScope)
	tenant, project, e := workIDs(path)
	id, e2 := guid.RequireID(in.RuleID)
	version, e3 := versionHeader(in.IfMatch)
	if e != nil || e2 != nil || e3 != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return workCommand(m, ctx, path, in.WriteHeaders, "automation.rule-archive:"+id.String(), "workflow_manage", struct{}{}, 200, func(tx *gorm.DB) (AutomationRuleView, int, error) {
		q := query.BindAutomationRules(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if e != nil {
			return AutomationRuleView{}, 0, e
		}
		if _, e = m.registerAutomationRuleOn(ctx, tx, tenant, project, id, version, row.Name, false); e != nil {
			return AutomationRuleView{}, 0, e
		}
		now := m.now()
		if e = tx.WithContext(ctx).Table(m.names.AutomationRules.String()).Where("tenant_id = ? AND id = ?", tenant, id).Update("archived_at", now).Error; e != nil {
			return AutomationRuleView{}, 0, e
		}
		row.ArchivedAt = &now
		row.Enabled = false
		row.Version++
		view, e := automationRuleView(row)
		return view, row.Version, e
	})
}
func (m *Module) listAutomationRunsHTTP(ctx context.Context, in *automationRunListInput) (*respx.Body[Page[AutomationRunView]], error) {
	tenant, project, e := workIDs(automationScopePath(in.AutomationScope))
	id, e2 := guid.RequireID(in.RuleID)
	if e != nil || e2 != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	out := Page[AutomationRunView]{Items: []AutomationRunView{}}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	e = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
			return e
		}
		cursor := guid.ID(0)
		if in.Cursor != "" {
			v, e := guid.RequireID(in.Cursor)
			if e != nil {
				return ErrInvalid
			}
			cursor = v
		}
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		var rows []model.AutomationRun
		if e := tx.WithContext(ctx).Table(m.names.AutomationRuns.String()).Where("tenant_id = ? AND project_id = ? AND rule_id = ? AND id > ?", tenant, project, id, cursor).Order("id").Limit(limit + 1).Find(&rows).Error; e != nil {
			return e
		}
		if len(rows) > limit {
			out.HasMore = true
			rows = rows[:limit]
			out.NextCursor = rows[limit-1].ID.String()
		}
		for _, row := range rows {
			if m.workItemOn == nil {
				return ErrUnavailable
			}
			if e := m.workItemOn(ctx, tx, tenant, project, row.WorkItemID); e != nil {
				return e
			}
			out.Items = append(out.Items, AutomationRunView{RunAs: row.RunAs, ActionsResult: row.ActionsResult, ID: row.ID, RuleID: row.RuleID, RuleVersion: row.RuleVersion, RuleName: row.RuleName, TriggerEventID: row.TriggerEventID, WorkItemID: row.WorkItemID, Outcome: row.Outcome, Error: row.Error, DurationMS: row.DurationMS, DefinitionSnapshot: row.DefinitionSnapshot})
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
