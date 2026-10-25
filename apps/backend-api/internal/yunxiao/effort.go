package yunxiao

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) recordWorklogRevisionOn(ctx context.Context, tx *gorm.DB, row *model.Worklog, action, reason string, actor *guid.ID) error {
	raw, err := json.Marshal(worklogView(row))
	if err != nil {
		return err
	}
	id, err := m.nextID()
	if err != nil {
		return err
	}
	revision := &model.WorklogRevision{ID: id, TenantID: row.TenantID, ProjectID: row.ProjectID, WorklogID: row.ID, Version: row.Version, Snapshot: string(raw), Action: action, Reason: reason, ActorID: actor, RecordedAt: m.now()}
	return query.BindWorklogRevisions(tx, m.names).WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(revision)
}

// observeWorklogOn preserves a pre-existing version without inventing its
// original actor, confirmation or event time. RecordedAt is observation time.
func (m *Module) observeWorklogOn(ctx context.Context, tx *gorm.DB, row *model.Worklog) error {
	return m.recordWorklogRevisionOn(ctx, tx, row, "LEGACY_OBSERVED", "", nil)
}

func (m *Module) effortInputAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in WorklogInput) error {
	if err := m.projectAuthorityOn(ctx, tx, tenant, project, "worklog_submit"); err != nil {
		return err
	}
	if in.WorkItemID == nil {
		return nil
	}
	if *in.WorkItemID <= 0 {
		return ErrInvalid
	}
	if m.effortWorkOn == nil {
		return ErrUnavailable
	}
	return m.effortWorkOn(ctx, tx, tenant, project, *in.WorkItemID)
}

func (m *Module) effortRowOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, write bool) (*model.Worklog, error) {
	permission := "work_item_read"
	if write {
		permission = "worklog_submit"
	}
	if err := m.projectAuthorityOn(ctx, tx, tenant, project, permission); err != nil {
		return nil, err
	}
	q := query.BindWorklogs(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id))
	if write {
		base = base.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := base.Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if write && (row.PrincipalID != authn.PrincipalIDFromContext(ctx) || row.ActorKind != "HUMAN" || row.ExecutionID != nil) {
		return nil, ErrForbidden
	}
	if row.WorkItemID != nil {
		if m.effortWorkOn == nil {
			return nil, ErrUnavailable
		}
		if err = m.effortWorkOn(ctx, tx, tenant, project, *row.WorkItemID); err != nil {
			return nil, err
		}
	}
	return row, nil
}

func (m *Module) confirmEffortOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int) (WorklogView, error) {
	var empty WorklogView
	row, err := m.effortRowOn(ctx, tx, tenant, project, id, true)
	if err != nil {
		return empty, err
	}
	if row.Version != version {
		return empty, ErrConflict
	}
	if row.WorkItemID == nil || row.Timezone == nil || strings.TrimSpace(*row.Timezone) == "" || *row.Timezone == "Local" {
		return empty, ErrInvalid
	}
	if _, err = time.LoadLocation(*row.Timezone); err != nil {
		return empty, ErrInvalid
	}
	if err = m.observeWorklogOn(ctx, tx, row); err != nil {
		return empty, err
	}
	actor, now := authn.PrincipalIDFromContext(ctx), m.now()
	q := query.BindWorklogs(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"confirmed_by": actor, "confirmed_at": now, "version": version + 1, "updated_at": now})
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	row.ConfirmedBy, row.ConfirmedAt, row.Version, row.UpdatedAt = &actor, &now, version+1, now
	if err = m.workChangedOn(ctx, tx, tenant, project, id, row.Version, "worklog.confirmed", false); err != nil {
		return empty, err
	}
	return worklogView(row), nil
}

type EffortPath struct {
	WorkProjectPath
	WorklogID string `path:"worklog_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type EffortCalibration struct {
	WorklogInput
	Reason string `json:"reason" minLength:"1" maxLength:"500"`
}

func (EffortCalibration) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	// Legacy save callers may omit context; this contextual command requires it.
	schema.Required = append(schema.Required, "timezone", "work_item_id")
	for _, name := range []string{"timezone", "work_item_id"} {
		field := *schema.Properties[name]
		field.Nullable = false
		schema.Properties[name] = &field
	}
	return schema
}

type EffortWriteHeaders struct {
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"64" required:"true"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted current worklog version."`
	Cookie         string `header:"Cookie" hidden:"true"`
}

func (h EffortWriteHeaders) canonical() WriteHeaders {
	return WriteHeaders(h)
}

type effortCalibrationInput struct {
	EffortPath
	EffortWriteHeaders
	Body EffortCalibration
}
type effortConfirmationInput struct {
	EffortPath
	EffortWriteHeaders
}
type effortHistoryInput struct {
	EffortPath
	Cursor string `query:"cursor" maxLength:"200"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type EffortRevisionView struct {
	Version    int         `json:"version"`
	Action     string      `json:"action"`
	Reason     string      `json:"reason"`
	ActorID    *guid.ID    `json:"actor_id"`
	RecordedAt *time.Time  `json:"recorded_at" doc:"Null for a read-only legacy observation; otherwise observation time, not necessarily original event time."`
	Snapshot   WorklogView `json:"snapshot"`
}
type EffortHistoryPage = Page[EffortRevisionView]

func (m *Module) registerEffortHTTP(api huma.API) {
	base := "/projects/{project_id}/worklogs/{worklog_id}"
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"worklogs"}, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.calibrate", "PUT", base+"/calibration", "Calibrate own contextual effort and invalidate current confirmation"), "worklog_submit"), m.calibrateEffortHTTP)
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.confirm", "PUT", base+"/confirmation", "Confirm own actual effort without supervisor approval"), "worklog_submit"), m.confirmEffortHTTP)
	authz.RegisterTenantMember(m.registrar, api, workScopedOperation(op("worklog.history", "GET", base+"/history", "Read immutable effort revision history"), "work_item_read"), m.effortHistoryHTTP)
}

func effortIDs(p EffortPath, h WriteHeaders) (guid.ID, guid.ID, guid.ID, int, error) {
	tenant, project, err := workIDs(p.WorkProjectPath)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	id, err := guid.RequireID(p.WorklogID)
	if err != nil {
		return 0, 0, 0, 0, ErrInvalid
	}
	version, err := versionHeader(h.IfMatch)
	return tenant, project, id, version, err
}
func (m *Module) calibrateEffortHTTP(ctx context.Context, in *effortCalibrationInput) (*commandOutput[WorklogView], error) {
	tenant, project, id, version, err := effortIDs(in.EffortPath, in.EffortWriteHeaders.canonical())
	if err != nil {
		return nil, httpError(ctx, err)
	}
	authorize := func(tx *gorm.DB) error { _, e := m.effortRowOn(ctx, tx, tenant, project, id, true); return e }
	return commandAuthorized(m, ctx, tenant, in.EffortWriteHeaders.canonical(), "worklog.calibrate:"+project.String()+":"+id.String(), in.Body, 200, authorize, func(tx *gorm.DB) (WorklogView, int, error) {
		row, e := m.effortRowOn(ctx, tx, tenant, project, id, true)
		if e != nil {
			return WorklogView{}, 0, e
		}
		if in.Body.WorkItemID == nil || row.WorkItemID == nil || *in.Body.WorkItemID != *row.WorkItemID || in.Body.Timezone == nil || strings.TrimSpace(in.Body.Reason) == "" || !textValid(in.Body.Reason, 500) {
			return WorklogView{}, 0, ErrInvalid
		}
		v, e := m.saveWorklogOn(ctx, tx, tenant, project, id, version, in.Body.WorklogInput, in.Body.Reason)
		return v, v.Version, e
	})
}
func (m *Module) confirmEffortHTTP(ctx context.Context, in *effortConfirmationInput) (*commandOutput[WorklogView], error) {
	tenant, project, id, version, err := effortIDs(in.EffortPath, in.EffortWriteHeaders.canonical())
	if err != nil {
		return nil, httpError(ctx, err)
	}
	authorize := func(tx *gorm.DB) error { _, e := m.effortRowOn(ctx, tx, tenant, project, id, true); return e }
	return commandAuthorized(m, ctx, tenant, in.EffortWriteHeaders.canonical(), "worklog.confirm:"+project.String()+":"+id.String(), struct{}{}, 200, authorize, func(tx *gorm.DB) (WorklogView, int, error) {
		v, e := m.confirmEffortOn(ctx, tx, tenant, project, id, version)
		return v, v.Version, e
	})
}
func (m *Module) effortHistoryHTTP(ctx context.Context, in *effortHistoryInput) (*respx.Body[EffortHistoryPage], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	id, err := guid.RequireID(in.WorklogID)
	if err != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	after, err := parseCursor(in.Cursor, tenant, id)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.database() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := EffortHistoryPage{Items: []EffortRevisionView{}}
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.effortRowOn(ctx, tx, tenant, project, id, false)
		if e != nil {
			return e
		}
		q := query.BindWorklogRevisions(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorklogID.Eq(id), q.Version.Gt(int(after))).Order(q.Version).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
		}
		for _, r := range rows {
			v := EffortRevisionView{Version: r.Version, Action: r.Action, Reason: r.Reason, ActorID: r.ActorID, RecordedAt: &r.RecordedAt}
			if e = json.Unmarshal([]byte(r.Snapshot), &v.Snapshot); e != nil {
				return e
			}
			// Older clients could move a record between tasks. A visible current
			// task must not expose snapshots belonging to a now-hidden old task.
			if v.Snapshot.WorkItemID != nil && (row.WorkItemID == nil || *v.Snapshot.WorkItemID != *row.WorkItemID) {
				if m.effortWorkOn == nil {
					return ErrUnavailable
				}
				if e = m.effortWorkOn(ctx, tx, tenant, project, *v.Snapshot.WorkItemID); e != nil {
					return e
				}
			}
			out.Items = append(out.Items, v)
		}
		// Reading an untouched legacy row does not write an invented history event.
		if len(rows) == 0 && int(after) < row.Version {
			out.Items = append(out.Items, EffortRevisionView{Version: row.Version, Action: "LEGACY_OBSERVED", Snapshot: worklogView(row)})
		}
		if out.HasMore {
			out.NextCursor = pageCursor(tenant, id, guid.ID(out.Items[len(out.Items)-1].Version))
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
