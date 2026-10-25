package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	p "7link.com/platform/internal/planning/i18n"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"slices"
	"strings"
)

type PlanningTenantInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type PlanningListInput struct {
	Scope string `query:"scope" enum:"MEMBER,PROJECT,TEAM"`
	PlanningTenantInput
	ProjectID   string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `query:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor      string `query:"cursor" maxLength:"512"`
	Limit       int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort        string `query:"sort" enum:"id,-id" default:"id"`
}
type PlanningCreateInput struct {
	PlanningTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           PlanningWriteInput
}
type PlanningRecordPath struct {
	PlanningTenantInput
	RecordID string `path:"record_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type PlanningReportEditInput struct {
	PlanningRecordPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Version int                 `json:"version" minimum:"1"`
		Content PlanningReportInput `json:"content"`
	}
}
type PlanningRecordDecisionInput struct {
	PlanningRecordPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           PlanningDecisionInput
}
type PlanningReportDecisionInput struct {
	PlanningRecordPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Version             int    `json:"version" minimum:"1"`
		Action              string `json:"action" enum:"SUBMIT,CONFIRM,RETURN,REOPEN"`
		Reason              string `json:"reason,omitempty" maxLength:"2000"`
		ExpectedContentHash string `json:"expected_content_hash" minLength:"64" maxLength:"64"`
	}
}
type PlanningGoalChangeInput struct {
	PlanningRecordPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           PlanningGoalChange
}
type PlanningPlanEditInput struct {
	PlanningRecordPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Version             int               `json:"version" minimum:"1"`
		ExpectedContentHash string            `json:"expected_content_hash" minLength:"64" maxLength:"64"`
		Content             PlanningPlanInput `json:"content"`
	}
}
type PlanningPage struct {
	Items      []PlanningRecord `json:"items"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}
type PlanningCommandOutput struct {
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[PlanningRecord]
}

func (m *Module) database() (*gorm.DB, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}
func planningError(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, ErrStaleCheck):
		return respx.ErrStatus(ctx, 409, 180432, p.Conflict, nil)
	case errors.Is(e, ErrImprovementInvalid):
		return respx.ErrStatus(ctx, 422, 180461, p.Invalid, nil)
	case errors.Is(e, ErrPlanLimit):
		return respx.ErrStatus(ctx, 422, 180413, p.Invalid, nil)
	case errors.Is(e, ErrInvalid), errors.Is(e, idempotency.ErrInvalidKey):
		return respx.ErrStatus(ctx, 422, 180422, p.Invalid, nil)
	case errors.Is(e, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 180403, p.Forbidden, nil)
	case errors.Is(e, ErrNotFound):
		return respx.ErrStatus(ctx, 404, 180404, p.NotFound, nil)
	case errors.Is(e, ErrConflict), errors.Is(e, idempotency.ErrKeyReused), errors.Is(e, idempotency.ErrInProgress), errors.Is(e, gorm.ErrDuplicatedKey):
		return respx.ErrStatus(ctx, 409, 180409, p.Conflict, nil)
	case errors.Is(e, ErrUnavailable):
		return respx.ErrStatus(ctx, 503, 180503, p.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 180500, respx.InternalServerError, nil)
	}
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil {
		return ErrUnavailable
	}
	m.registerGoalTreeHTTP(api)
	m.registerSettingsHTTP(api)
	m.registerLoopHTTP(api)
	m.registerHeavyHTTP(api)
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	m.registerAggregationHTTP(api)
	m.registerReportBoardHTTP(api)
	m.registerGoalAlignmentHTTP(api)
	m.registerWorkbenchHTTP(api)
	m.registerMetricsHTTP(api)
	for _, kind := range []string{"goal", "plan", "report"} {
		path := "/" + kind + "s"
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".list", http.MethodGet, path, "List authorized "+kind+"s", 200), func(ctx context.Context, in *PlanningListInput) (*respx.Body[PlanningPage], error) {
			return m.listHTTP(ctx, kind, in)
		})
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".create", http.MethodPost, path, "Create a "+kind+" draft", 201), func(ctx context.Context, in *PlanningCreateInput) (*PlanningCommandOutput, error) {
			tenant, e := guid.RequireID(in.TenantID)
			if e != nil {
				return nil, planningError(ctx, ErrInvalid)
			}
			return m.command(ctx, tenant, in.IdempotencyKey, kind+".create", path, in.Body, func(tx *gorm.DB) (PlanningRecord, error) { return m.createOn(ctx, tx, tenant, kind, in.Body) })
		})
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".get", http.MethodGet, path+"/{record_id}", "Read a "+kind, 200), func(ctx context.Context, in *PlanningRecordPath) (*respx.Body[PlanningRecord], error) {
			tenant, id, e := recordIDs(in)
			if e != nil {
				return nil, planningError(ctx, e)
			}
			db, e := m.database()
			if e != nil {
				return nil, planningError(ctx, e)
			}
			var r PlanningRecord
			e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; r, e = m.readOn(ctx, tx, tenant, id, kind); return e })
			if e != nil {
				return nil, planningError(ctx, e)
			}
			return respx.OK(ctx, r), nil
		})
		if !m.registrar.IsDeclarationOnly() {
			if e := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); e != nil {
				return e
			}
		}
	}

	authz.RegisterTenantMember(m.registrar, api, op("planning.goal.change", "POST", "/goals/{record_id}/changes", "Edit, activate, close or refresh a version-bound goal", 201), func(ctx context.Context, in *PlanningGoalChangeInput) (*PlanningCommandOutput, error) {
		tenant, id, err := recordIDs(&in.PlanningRecordPath)
		if err != nil {
			return nil, planningError(ctx, err)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "goal.change", "/goals/"+id.String()+"/changes", in.Body, func(tx *gorm.DB) (PlanningRecord, error) { return m.changeGoalOn(ctx, tx, tenant, id, in.Body) })
	})
	authz.Register(m.registrar, api, op("planning.plan.edit", "PATCH", "/plans/{record_id}", "Replace goal-linked plan items and require reconfirmation", 200), authz.Guard{Resource: "plan", Verb: "submit"}, func(ctx context.Context, in *PlanningPlanEditInput) (*PlanningCommandOutput, error) {
		tenant, id, err := recordIDs(&in.PlanningRecordPath)
		if err != nil {
			return nil, planningError(ctx, err)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "plan.edit", "/plans/"+id.String(), in.Body, func(tx *gorm.DB) (PlanningRecord, error) {
			return m.editPlanOn(ctx, tx, tenant, id, in.Body.Version, in.Body.ExpectedContentHash, in.Body.Content)
		})
	})
	authz.RegisterTenantMember(m.registrar, api, op("planning.plan.decide", http.MethodPost, "/plans/{record_id}/decisions", "Submit, confirm or return a goal-linked plan", 201), func(ctx context.Context, in *PlanningRecordDecisionInput) (*PlanningCommandOutput, error) {
		tenant, id, e := recordIDs(&in.PlanningRecordPath)
		if e != nil {
			return nil, planningError(ctx, e)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "plan.decide", "/plans/"+id.String()+"/decisions", in.Body, func(tx *gorm.DB) (PlanningRecord, error) { return m.decidePlanOn(ctx, tx, tenant, id, in.Body) })
	})
	authz.Register(m.registrar, api, op("planning.report.edit", http.MethodPatch, "/reports/{record_id}", "Edit report sections and invalidate reconciliation", 200), authz.Guard{Resource: "report", Verb: "submit"}, func(ctx context.Context, in *PlanningReportEditInput) (*PlanningCommandOutput, error) {
		tenant, id, e := recordIDs(&in.PlanningRecordPath)
		if e != nil {
			return nil, planningError(ctx, e)
		}
		return m.command(ctx, tenant, in.IdempotencyKey, "report.edit", "/reports/"+id.String(), in.Body, func(tx *gorm.DB) (PlanningRecord, error) {
			return m.editReportOn(ctx, tx, tenant, id, in.Body.Version, in.Body.Content)
		})
	})
	authz.Register(m.registrar, api, op("planning.report.decide", http.MethodPost, "/reports/{record_id}/decisions", "Submit, confirm, return or reopen an evidence-backed report", 201), authz.Guard{Resource: "report", Verb: "read"}, func(ctx context.Context, in *PlanningReportDecisionInput) (*PlanningCommandOutput, error) {
		tenant, id, e := recordIDs(&in.PlanningRecordPath)
		if e != nil {
			return nil, planningError(ctx, e)
		}
		body := PlanningDecisionInput(in.Body)
		return m.command(ctx, tenant, in.IdempotencyKey, "report.decide", "/reports/"+id.String()+"/decisions", body, func(tx *gorm.DB) (PlanningRecord, error) { return m.decideReportOn(ctx, tx, tenant, id, body) })
	})
	authz.Register(m.registrar, api, op("planning.report.check", http.MethodGet, "/reports/{record_id}/reconciliation", "Reconcile all report claims against current facts", 200), authz.Guard{Resource: "report", Verb: "read"}, func(ctx context.Context, in *PlanningRecordPath) (*respx.Body[PlanningCheck], error) {
		tenant, id, e := recordIDs(in)
		if e != nil {
			return nil, planningError(ctx, e)
		}
		db, e := m.database()
		if e != nil {
			return nil, planningError(ctx, e)
		}
		var c PlanningCheck
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			c, e = m.recordReportCheckOn(ctx, tx, tenant, id)
			return e
		})
		if e != nil {
			return nil, planningError(ctx, e)
		}
		return respx.OK(ctx, c), nil
	})
	return nil
}
func recordIDs(in *PlanningRecordPath) (guid.ID, guid.ID, error) {
	t, e := guid.RequireID(in.TenantID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	id, e := guid.RequireID(in.RecordID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	return t, id, nil
}
func (m *Module) readOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (PlanningRecord, error) {
	if kind == "report" {
		r, _, e := m.reportOn(ctx, tx, tenant, id, "report_read", false)
		if e != nil {
			return PlanningRecord{}, e
		}
		out, e := reportView(r)
		if e != nil || r.State != "CONFIRMED" {
			return out, e
		}
		_, e = m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "report_confirm", true)
		if e == nil {
			e = m.authorizeReportReopenOn(ctx, tx, r, r.ConfirmedBy)
		}
		if errors.Is(e, ErrForbidden) {
			return out, nil
		}
		out.CanReopen = e == nil
		return out, e
	}
	var r PlanningRecord
	var goalRow *model.Goal
	if kind == "goal" {
		q := query.BindGoals(tx, m.names)
		v, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return r, ErrNotFound
		}
		if e != nil {
			return r, e
		}
		goalRow = v
		r = PlanningRecord{ProjectID: v.ProjectID, PrincipalID: v.PrincipalID}
	} else if kind == "plan" {
		q := query.BindPlans(tx, m.names)
		v, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return r, ErrNotFound
		}
		if e != nil {
			return r, e
		}
		r = planView(v)
	} else {
		return r, ErrInvalid
	}
	permission := kind + "_read"
	if kind == "plan" {
		permission = planReadPermission(authn.PrincipalIDFromContext(ctx), r.PrincipalID)
	}
	_, e := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, permission, false)
	if e != nil {
		return PlanningRecord{}, e
	}
	if kind == "goal" {
		return goalView(goalRow)
	}
	return r, nil
}
func listScope(in *PlanningListInput, tenant, actor guid.ID, kind string) (guid.ID, guid.ID, string, error) {
	project := guid.ID(0)
	subject := actor
	var e error
	if in.ProjectID != "" {
		project, e = guid.RequireID(in.ProjectID)
		if e != nil {
			return 0, 0, "", ErrInvalid
		}
	}
	if in.PrincipalID != "" {
		subject, e = guid.RequireID(in.PrincipalID)
		if e != nil {
			return 0, 0, "", ErrInvalid
		}
	}
	if in.Scope != "" {
		if kind != "report" || !slices.Contains([]string{"MEMBER", "PROJECT", "TEAM"}, in.Scope) {
			return 0, 0, "", ErrInvalid
		}
		if in.Scope != "MEMBER" {
			subject = 0
		}
		if (in.Scope == "PROJECT" && project == 0) || (in.Scope == "TEAM" && project != 0) || (in.Scope != "MEMBER" && in.PrincipalID != "") {
			return 0, 0, "", ErrInvalid
		}
	}
	if in.Limit < 1 || in.Limit > 100 || (in.Sort != "" && in.Sort != "id" && in.Sort != "-id") {
		return 0, 0, "", ErrInvalid
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%s:%s", tenant, actor, project, subject, kind, in.Sort))))
	return project, subject, scope, nil
}
func cursorPosition(cursor, scope string) (guid.ID, error) {
	if cursor == "" {
		return 0, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	if e != nil {
		return 0, ErrInvalid
	}
	pos, hash, ok := strings.Cut(string(b), ":")
	if !ok || hash != scope {
		return 0, ErrInvalid
	}
	id, e := guid.RequireID(pos)
	if e != nil {
		return 0, ErrInvalid
	}
	return id, nil
}
func (m *Module) listHTTP(ctx context.Context, kind string, in *PlanningListInput) (*respx.Body[PlanningPage], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	project, subject, scope, e := listScope(in, tenant, authn.PrincipalIDFromContext(ctx), kind)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	after, e := cursorPosition(in.Cursor, scope)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	out := PlanningPage{Items: []PlanningRecord{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if kind == "plan" && in.ProjectID == "" {
			return m.listProjectPlansOn(ctx, tx, tenant, subject, after, scope, in, &out)
		}
		permission := kind + "_read"
		if kind == "plan" {
			permission = planReadPermission(authn.PrincipalIDFromContext(ctx), subject)
		}
		if _, e := m.authorize(ctx, tx, tenant, project, subject, permission, false); e != nil {
			return e
		}
		records := []PlanningRecord{}
		switch kind {
		case "goal":
			q := query.BindGoals(tx, m.names)
			d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.PrincipalID.Eq(subject))
			if in.Sort == "-id" {
				d = d.Order(q.ID.Desc())
				if after > 0 {
					d = d.Where(q.ID.Lt(after))
				}
			} else {
				d = d.Order(q.ID)
				if after > 0 {
					d = d.Where(q.ID.Gt(after))
				}
			}
			rows, e := d.Limit(in.Limit + 1).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				v, e := goalView(r)
				if e != nil {
					return e
				}
				records = append(records, v)
			}
		case "plan":
			q := query.BindPlans(tx, m.names)
			d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(subject))
			if in.ProjectID != "" {
				d = d.Where(q.ProjectID.Eq(project))
			}
			if in.Sort == "-id" {
				d = d.Order(q.ID.Desc())
				if after > 0 {
					d = d.Where(q.ID.Lt(after))
				}
			} else {
				d = d.Order(q.ID)
				if after > 0 {
					d = d.Where(q.ID.Gt(after))
				}
			}
			rows, e := d.Limit(in.Limit + 1).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				records = append(records, planView(r))
			}
		case "report":
			q := query.BindReports(tx, m.names)
			d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.PrincipalID.Eq(subject))
			if in.Sort == "-id" {
				d = d.Order(q.ID.Desc())
				if after > 0 {
					d = d.Where(q.ID.Lt(after))
				}
			} else {
				d = d.Order(q.ID)
				if after > 0 {
					d = d.Where(q.ID.Gt(after))
				}
			}
			rows, e := d.Limit(in.Limit + 1).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				v, e := reportView(r)
				if e != nil {
					return e
				}
				records = append(records, v)
			}
		}
		if len(records) > in.Limit {
			out.HasMore = true
			records = records[:in.Limit]
		}
		out.Items = records
		if out.HasMore {
			out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(records[len(records)-1].ID.String() + ":" + scope))
		}
		return nil
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

// listProjectPlansOn filters each row using its current project authority and
// archive state before applying the visible page limit. Explicit project lists
// retain authorized history access.
func (m *Module) listProjectPlansOn(ctx context.Context, tx *gorm.DB, tenant, subject, after guid.ID, scope string, in *PlanningListInput, out *PlanningPage) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if tenant != authn.TenantIDFromContext(ctx) || actor <= 0 {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.members == nil || m.project.ArchivedOn == nil {
		return ErrUnavailable
	}
	_, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	q := query.BindPlans(tx, m.names)
	for {
		d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(subject))
		if in.Sort == "-id" {
			d = d.Order(q.ID.Desc())
			if after > 0 {
				d = d.Where(q.ID.Lt(after))
			}
		} else {
			d = d.Order(q.ID)
			if after > 0 {
				d = d.Where(q.ID.Gt(after))
			}
		}
		rows, e := d.Limit(100).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			after = row.ID
			_, e := m.authorize(ctx, tx, tenant, row.ProjectID, row.PrincipalID, planReadPermission(actor, row.PrincipalID), false)
			r := planView(row)
			if errors.Is(e, ErrForbidden) {
				continue
			}
			if e != nil {
				return e
			}
			archived, e := m.project.ArchivedOn(ctx, tx, tenant, row.ProjectID)
			if e != nil {
				return e
			}
			if archived {
				continue
			}
			if len(out.Items) == in.Limit {
				out.HasMore = true
				out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(out.Items[len(out.Items)-1].ID.String() + ":" + scope))
				return nil
			}
			out.Items = append(out.Items, r)
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
func (m *Module) command(ctx context.Context, tenant guid.ID, key, operation, path string, body any, fn func(*gorm.DB) (PlanningRecord, error)) (*PlanningCommandOutput, error) {
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, planningError(ctx, ErrForbidden)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	if m.replay == nil {
		return nil, planningError(ctx, ErrUnavailable)
	}
	hash, e := idempotency.HashRequest(http.MethodPost, path, map[string]string{"tenant_id": tenant.String()}, body)
	if e != nil {
		return nil, planningError(ctx, e)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "planning." + operation, Value: key, RequestHash: hash}
	if e = k.Validate(); e != nil {
		return nil, planningError(ctx, e)
	}
	var v PlanningRecord
	var replayed bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, replayedResult, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			r, e := fn(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(r)
			return idempotency.Result{StatusCode: 201, Body: b, ResourceKind: "planning_record", ResourceID: r.ID}, e
		})
		if e != nil {
			return e
		}
		replayed = replayedResult
		if e = json.Unmarshal(result.Body, &v); e != nil {
			return e
		}
		if replayed {
			return m.authorizeReplay(ctx, tx, tenant, v, operation, body)
		}
		return nil
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	out := &PlanningCommandOutput{Body: respx.OK(ctx, v).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}

func (m *Module) authorizeReplay(ctx context.Context, tx *gorm.DB, tenant guid.ID, r PlanningRecord, operation string, body any) error {
	if operation == "report.remind" {
		in, ok := body.(ReportReminderRequest)
		if !ok {
			return ErrInvalid
		}
		for _, id := range in.ReportIDs {
			if _, _, err := m.reportOn(ctx, tx, tenant, id, "report_confirm", true); err != nil {
				return err
			}
		}
		return nil
	}
	current, err := m.readOn(ctx, tx, tenant, r.ID, r.Kind)
	if err != nil {
		return err
	}
	permission := r.Kind + "_submit"
	human := false
	if r.Kind == "goal" {
		var goal PlanningGoalInput
		if err = json.Unmarshal(current.Content, &goal); err != nil {
			return err
		}
		permission = "goal_manage"
		human = goal.Level != "MEMBER"
		if goal.Level == "MEMBER" {
			permission = "goal_member_manage"
		}
		if in, ok := body.(PlanningGoalChange); ok {
			human = human || in.Action == "ACTIVATE" || in.Action == "CLOSE" || in.Action == "ASSIGN_CONTRIBUTOR" || in.Action == "REMOVE_CONTRIBUTOR"
		}
	}
	if strings.HasSuffix(operation, ".decide") {
		in, ok := body.(PlanningDecisionInput)
		if !ok {
			return ErrInvalid
		}
		if in.Action != "SUBMIT" {
			if r.Kind == "report" {
				row, _, e := m.reportOn(ctx, tx, tenant, r.ID, "report_confirm", true)
				if e != nil {
					return e
				}
				if in.Action == "REOPEN" {
					// The receipt retains the recorded confirmer after active
					// confirmation fields are cleared. Owner authority remains live.
					return m.authorizeReportReopenOn(ctx, tx, row, r.ReopenedFromConfirmedBy)
				}
				recipient, e := m.reportConfirmerOn(ctx, tx, row)
				if e != nil {
					return e
				}
				if recipient != authn.PrincipalIDFromContext(ctx) {
					return ErrForbidden
				}
			}
			permission = r.Kind + "_confirm"
			human = true
		}
	}
	_, err = m.authorize(ctx, tx, tenant, current.ProjectID, current.PrincipalID, permission, human)
	return err
}

var _ module.HTTPRegistrar = (*Module)(nil)

func planReadPermission(actor, subject guid.ID) string {
	if actor == subject {
		return "plan_submit"
	}
	return "plan_confirm"
}

func goalView(v *model.Goal) (PlanningRecord, error) {
	var g PlanningGoalInput
	if err := json.Unmarshal([]byte(v.Content), &g); err != nil {
		return PlanningRecord{}, err
	}
	return PlanningRecord{ID: v.ID, Kind: "goal", ProjectID: v.ProjectID, PrincipalID: v.PrincipalID, PeriodKind: v.PeriodKind, PeriodStart: v.PeriodStart, State: v.State, Content: json.RawMessage(v.Content), ContentHash: v.ContentHash, Version: v.Version, CreatedAt: v.CreatedAt, Progress: goalProgress(g)}, nil
}
func planView(v *model.Plan) PlanningRecord {
	return PlanningRecord{Unconfirmed: v.State == "SUBMITTED" && v.UnconfirmedAt != nil, ID: v.ID, Kind: "plan", ProjectID: v.ProjectID, PrincipalID: v.PrincipalID, PeriodKind: v.PeriodKind, PeriodStart: v.PeriodStart, State: v.State, Content: json.RawMessage(v.Content), ContentHash: v.ContentHash, Version: v.Version, CreatedAt: v.CreatedAt}
}
