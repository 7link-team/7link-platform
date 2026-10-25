package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"gorm.io/gen"
	"strconv"
	"time"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type WorkProjectPath struct {
	TenantInput
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type WorklogPath struct {
	WorkProjectPath
	WorklogID string `path:"worklog_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type worklogCreateInput struct {
	WorkProjectPath
	WriteHeaders
	Body WorklogInput
}
type worklogUpdateInput struct {
	WorklogPath
	WriteHeaders
	Body WorklogInput
}
type worklogStateInput struct {
	WorklogPath
	WriteHeaders
	Body struct {
		State  string `json:"state" enum:"SUBMITTED,APPROVED,REJECTED"`
		Reason string `json:"reason" maxLength:"500"`
	}
}
type worklogListInput struct {
	WorkProjectPath
	WorklogPeriod
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"id" enum:"id"`
}

type WorklogPeriod struct {
	WorkItemID string `query:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Exact work-item dimension."`
	From       string `query:"from" maxLength:"10" doc:"Inclusive work date, YYYY-MM-DD."`
	Until      string `query:"until" maxLength:"10" doc:"Exclusive work date, YYYY-MM-DD."`
}
type worklogSummaryInput struct {
	WorkProjectPath
	WorklogPeriod
}

func (m *Module) worklogPeriodConditions(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, p WorklogPeriod) ([]gen.Condition, error) {
	q := query.BindWorklogs(tx, m.names)
	out := []gen.Condition{}
	if p.WorkItemID != "" {
		id, e := guid.RequireID(p.WorkItemID)
		if e != nil {
			return nil, ErrInvalid
		}
		if m.effortWorkOn == nil {
			return nil, ErrUnavailable
		}
		if e = m.effortWorkOn(ctx, tx, tenant, project, id); e != nil {
			return nil, e
		}
		out = append(out, q.WorkItemID.Eq(id))
	}
	for _, date := range []string{p.From, p.Until} {
		if date != "" {
			v, e := time.Parse("2006-01-02", date)
			if e != nil || v.Format("2006-01-02") != date {
				return nil, ErrInvalid
			}
		}
	}
	if p.From != "" && p.Until != "" && p.From >= p.Until {
		return nil, ErrInvalid
	}
	if p.From != "" {
		out = append(out, q.WorkDate.Gte(p.From))
	}
	if p.Until != "" {
		out = append(out, q.WorkDate.Lt(p.Until))
	}
	return out, nil
}

type WorkflowPath struct {
	WorkProjectPath
	WorkItemType string `path:"work_item_type" enum:"TASK,BUG,RISK,INCIDENT"`
	TeamDefault  bool   `query:"team_default" doc:"Read or manage tenant-wide defaults; writes require tenant-wide workflow_manage."`
}
type workflowSaveInput struct {
	WorkflowPath
	WriteHeaders
	Body WorkflowInput
}
type workflowDeleteInput struct {
	WorkflowPath
	WriteHeaders
}

type workflowPreviewInput struct {
	WorkflowPath
	Body struct {
		Version    int           `json:"version" minimum:"0"`
		Remove     bool          `json:"remove"`
		Definition WorkflowInput `json:"definition"`
	}
}
type worklogSummary struct {
	HumanConfirmedMinutes      int64            `json:"human_confirmed_minutes" doc:"Self-confirmed Human actual effort; independent of supervisor review."`
	HumanUnconfirmedMinutes    int64            `json:"human_unconfirmed_minutes"`
	AgentWallMinutes           int64            `json:"agent_wall_minutes" doc:"Source-owned Execution wall time; never added to Human actual effort."`
	LegacyApprovedHumanMinutes int64            `json:"legacy_approved_human_minutes" doc:"Supervisor-approved Human records lacking self-confirmation; excluded from actual effort."`
	ByWorkItem                 map[string]int64 `json:"by_work_item"`
	ByDate                     map[string]int64 `json:"by_date"`
	ApprovedMinutes            int64            `json:"approved_minutes"`
	KnownCostMicros            int64            `json:"known_cost_micros"`
	UnknownCostCount           int              `json:"unknown_cost_count"`
	ByCategory                 map[string]int64 `json:"by_category"`
	ByPrincipal                map[string]int64 `json:"by_principal"`
}

// workScopedOperation binds token authentication to the same permission that
// the domain checks on the selected project, without a second tenant-wide veto.
func workScopedOperation(operation huma.Operation, permission string) huma.Operation {
	operation.Middlewares = append(operation.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
		tenant, _ := guid.Parse(ctx.Param("tenant_id"))
		next(huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), tenant, permission)))
	})
	return operation
}

func (m *Module) registerWorkHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"worklogs"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	// These reads enforce permission on the current project inside the handler.
	// Keep PAT authentication bound to that same permission before member auth runs.
	readOp := func(id, path, summary string) huma.Operation {
		return workScopedOperation(op(id, "GET", path, summary, 200), "work_item_read")
	}
	manage := authz.Guard{Resource: "workflow", Verb: "manage", FilterData: true}
	base := "/projects/{project_id}"
	authz.RegisterTenantMember(m.registrar, api, readOp("worklog.list", base+"/worklogs", "List project worklogs"), m.listWorklogsHTTP)
	authz.RegisterTenantMember(m.registrar, api, readOp("worklog.summary", base+"/worklog-summary", "Summarize approved project worklogs"), m.summaryWorklogsPeriodHTTP)
	m.registerEffortHTTP(api)
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.create", "POST", base+"/worklogs", "Create own draft worklog", 201), "worklog_submit"), m.createWorklogHTTP)
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.update", "PATCH", base+"/worklogs/{worklog_id}", "Edit own draft worklog", 200), "worklog_submit"), m.updateWorklogHTTP)
	// Domain state command chooses submit or approve authority under current locks;
	// the read guard admits either role without incorrectly requiring both.
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.state.update", "PUT", base+"/worklogs/{worklog_id}/state", "Submit approve return or withdraw a worklog", 200), "work_item_read"), m.stateWorklogHTTP)
	authz.RegisterTenantMember(m.registrar, api, readOp("workflow.config.get", base+"/workflow-configs/{work_item_type}", "Read state groups and transition matrix"), m.getWorkflowHTTP)
	authz.Register(m.registrar, api, op("workflow.config.replace", "PUT", base+"/workflow-configs/{work_item_type}", "Replace versioned workflow configuration", 200), manage, m.saveWorkflowHTTP)
	authz.Register(m.registrar, api, op("workflow.config.preview", "POST", base+"/workflow-configs/{work_item_type}/previews", "Preview effective workflow configuration without saving", 200), manage, m.previewWorkflowHTTP)
	authz.Register(m.registrar, api, op("workflow.config.delete", "DELETE", base+"/workflow-configs/{work_item_type}", "Remove workflow configuration override", 200), manage, m.deleteWorkflowHTTP)
	for _, path := range []string{base + "/worklogs", base + "/worklogs/{worklog_id}", base + "/worklogs/{worklog_id}/state", base + "/workflow-configs/{work_item_type}"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
			return err
		}
	}
	return nil
}
func workIDs(p WorkProjectPath) (guid.ID, guid.ID, error) {
	t, e := guid.RequireID(p.TenantID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	id, e := guid.RequireID(p.ProjectID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	return t, id, nil
}
func workCommand[T any](m *Module, ctx context.Context, p WorkProjectPath, h WriteHeaders, operation, permission string, body any, status int, fn func(*gorm.DB) (T, int, error)) (*commandOutput[T], error) {
	tenant, project, err := workIDs(p)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return commandAuthorized(m, ctx, tenant, h, operation+":"+project.String(), body, status, func(tx *gorm.DB) error { return m.projectAuthorityOn(ctx, tx, tenant, project, permission) }, fn)
}
func (m *Module) createWorklogHTTP(ctx context.Context, in *worklogCreateInput) (*commandOutput[WorklogView], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "worklog.create:"+project.String(), in.Body, 201, func(tx *gorm.DB) error { return m.effortInputAuthorityOn(ctx, tx, tenant, project, in.Body) }, func(tx *gorm.DB) (WorklogView, int, error) {
		v, e := m.saveWorklogOn(ctx, tx, tenant, project, 0, 0, in.Body)
		return v, v.Version, e
	})
}
func (m *Module) updateWorklogHTTP(ctx context.Context, in *worklogUpdateInput) (*commandOutput[WorklogView], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	id, e := guid.RequireID(in.WorklogID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	version, e := versionHeader(in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "worklog.update:"+id.String()+":"+project.String(), in.Body, 200, func(tx *gorm.DB) error {
		if _, e := m.effortRowOn(ctx, tx, tenant, project, id, true); e != nil {
			return e
		}
		return m.effortInputAuthorityOn(ctx, tx, tenant, project, in.Body)
	}, func(tx *gorm.DB) (WorklogView, int, error) {
		v, e := m.saveWorklogOn(ctx, tx, tenant, project, id, version, in.Body)
		return v, v.Version, e
	})
}
func (m *Module) stateWorklogHTTP(ctx context.Context, in *worklogStateInput) (*commandOutput[WorklogView], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	id, e := guid.RequireID(in.WorklogID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	version, e := versionHeader(in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "worklog.state:"+id.String()+":"+project.String(), in.Body, 200, func(tx *gorm.DB) error {
		row, e := m.effortRowOn(ctx, tx, tenant, project, id, false)
		if e != nil {
			return e
		}
		permission := "worklog_approve"
		if in.Body.State == "SUBMITTED" && row.PrincipalID == authn.PrincipalIDFromContext(ctx) {
			permission = "worklog_submit"
		}
		return m.projectAuthorityOn(ctx, tx, tenant, project, permission)
	}, func(tx *gorm.DB) (WorklogView, int, error) {
		v, e := m.worklogStateOn(ctx, tx, tenant, project, id, version, in.Body.State, in.Body.Reason)
		return v, v.Version, e
	})
}
func (m *Module) listWorklogsHTTP(ctx context.Context, in *worklogListInput) (*respx.Body[Page[WorklogView]], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	page := Page[WorklogView]{Items: []WorklogView{}}
	limit := in.Limit
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var cursor guid.ID
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalid)
		}
	}
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
			return e
		}
		q := query.BindWorklogs(tx, m.names)
		filters, e := m.worklogPeriodConditions(ctx, tx, tenant, project, in.WorklogPeriod)
		if e != nil {
			return e
		}
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Gt(cursor)).Where(filters...).Order(q.ID).Limit(limit + 1).Find()
		if e != nil {
			return e
		}
		page.HasMore = len(rows) > limit
		if page.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			page.Items = append(page.Items, worklogView(row))
		}
		if page.HasMore {
			page.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, page), nil
}
func (m *Module) summaryWorklogsHTTP(ctx context.Context, in *WorkProjectPath) (*respx.Body[worklogSummary], error) {
	return m.summaryWorklogsPeriodHTTP(ctx, &worklogSummaryInput{WorkProjectPath: *in})
}
func (m *Module) summaryWorklogsPeriodHTTP(ctx context.Context, in *worklogSummaryInput) (*respx.Body[worklogSummary], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := worklogSummary{ByCategory: map[string]int64{}, ByPrincipal: map[string]int64{}, ByWorkItem: map[string]int64{}, ByDate: map[string]int64{}}
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
			return e
		}
		q := query.BindWorklogs(tx, m.names)
		filters, e := m.worklogPeriodConditions(ctx, tx, tenant, project, in.WorklogPeriod)
		if e != nil {
			return e
		}
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("APPROVED")).Where(filters...).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.ApprovedMinutes += r.Minutes
			out.ByCategory[r.Category] += r.Minutes
			out.ByPrincipal[r.PrincipalID.String()] += r.Minutes
			key := "unassigned"
			if r.WorkItemID != nil {
				key = r.WorkItemID.String()
			}
			out.ByWorkItem[key] += r.Minutes
			out.ByDate[r.WorkDate] += r.Minutes
			if r.CostMicros == nil {
				out.UnknownCostCount++
			} else {
				out.KnownCostMicros += *r.CostMicros
			}
		}
		all, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Where(filters...).Find()
		if e != nil {
			return e
		}
		for _, r := range all {
			if r.ActorKind == "AGENT" && r.ExecutionID != nil {
				out.AgentWallMinutes += r.Minutes
				continue
			}
			if r.ActorKind != "HUMAN" {
				continue
			}
			if r.ConfirmedBy != nil && *r.ConfirmedBy == r.PrincipalID && r.ConfirmedAt != nil && r.Timezone != nil && r.WorkItemID != nil {
				out.HumanConfirmedMinutes += r.Minutes
			} else {
				out.HumanUnconfirmedMinutes += r.Minutes
				if r.State == "APPROVED" {
					out.LegacyApprovedHumanMinutes += r.Minutes
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) getWorkflowHTTP(ctx context.Context, in *WorkflowPath) (*respx.Body[WorkflowView], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out WorkflowView
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
			return e
		}
		scope := project
		if in.TeamDefault {
			scope = 0
		}
		var e error
		out, e = m.effectiveWorkflowOn(ctx, tx, tenant, scope, in.WorkItemType)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) saveWorkflowHTTP(ctx context.Context, in *workflowSaveInput) (*commandOutput[WorkflowView], error) {
	return m.writeWorkflowHTTP(ctx, in.WorkflowPath, in.WriteHeaders, in.Body, false)
}

func (m *Module) previewWorkflowHTTP(ctx context.Context, in *workflowPreviewInput) (*respx.Body[WorkflowPreview], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	if in.TeamDefault {
		project = 0
	}
	var out WorkflowPreview
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.previewWorkflowOn(ctx, tx, tenant, project, in.WorkItemType, in.Body.Version, in.Body.Definition, in.Body.Remove)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) deleteWorkflowHTTP(ctx context.Context, in *workflowDeleteInput) (*commandOutput[WorkflowView], error) {
	return m.writeWorkflowHTTP(ctx, in.WorkflowPath, in.WriteHeaders, WorkflowInput{}, true)
}
func (m *Module) writeWorkflowHTTP(ctx context.Context, p WorkflowPath, h WriteHeaders, body WorkflowInput, remove bool) (*commandOutput[WorkflowView], error) {
	tenant, project, err := workIDs(p.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	version := 0
	if h.IfMatch != "" {
		version, err = versionHeader(h.IfMatch)
		if err != nil {
			return nil, httpError(ctx, err)
		}
	}
	scope := project
	if p.TeamDefault {
		scope = 0
	}
	return commandAuthorized(m, ctx, tenant, h, "workflow.config:"+scope.String()+":"+p.WorkItemType+":"+strconv.FormatBool(remove), body, 200,
		func(tx *gorm.DB) error { return m.workflowAuthorityOn(ctx, tx, tenant, scope) },
		func(tx *gorm.DB) (WorkflowView, int, error) {
			v, e := m.saveWorkflowOn(ctx, tx, tenant, scope, p.WorkItemType, version, body, remove)
			if e == nil {
				v, e = m.effectiveWorkflowOn(ctx, tx, tenant, scope, p.WorkItemType)
			}
			return v, v.Version, e
		})
}
