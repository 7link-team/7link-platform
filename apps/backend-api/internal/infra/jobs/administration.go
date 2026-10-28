package jobs

import (
	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	jobsi18n "7link.com/platform/internal/infra/jobs/i18n"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// AdminConnector lets the later IAM module bind current system authority to
// infrastructure without reversing module activation order.
type AdminConnector struct {
	Bind      func(*authz.Registrar, func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error
	BindAudit func(AdminAudit) error
}

// AdminAudit persists the administrative change inside its transaction.
type AdminAudit func(context.Context, *gorm.DB, string, guid.ID, string, guid.ID, map[string]any) error
type adminAccess struct {
	registrar *authz.Registrar
	check     func(context.Context, *gorm.DB, string, guid.ID) (bool, error)
	audit     AdminAudit
}

func (m *Module) provideAdmin(s *module.Services) error {
	return module.Provide[AdminConnector](s, AdminConnector{Bind: func(r *authz.Registrar, check func(context.Context, *gorm.DB, string, guid.ID) (bool, error)) error {
		if r == nil || check == nil {
			return ErrUnavailable
		}
		if err := r.Registry().Contribute([]authz.Action{{Resource: "system_job", Verb: "view", Scope: authz.ActionScopeSystem, Menu: true, Summary: "view background jobs"}, {Resource: "system_job", Verb: "manage", Scope: authz.ActionScopeSystem, Summary: "manage background jobs"}}, nil); err != nil {
			return err
		}
		m.admin.registrar, m.admin.check = r, check
		return nil
	}, BindAudit: func(record AdminAudit) error {
		if record == nil {
			return ErrUnavailable
		}
		m.admin.audit = record
		return nil
	}})
}

func (m *Module) adminAuditOn(ctx context.Context, tx *gorm.DB, action string, tenant guid.ID, resource string, id guid.ID, detail map[string]any) error {
	if m.admin.audit == nil {
		return ErrUnavailable
	}
	return m.admin.audit(ctx, tx, action, tenant, resource, id, detail)
}

var (
	errAdminForbidden  = errors.New("jobs: forbidden")
	errAdminNotFound   = errors.New("jobs: not found")
	errAdminVersion    = errors.New("jobs: version conflict")
	errAdminTransition = errors.New("jobs: illegal transition")
	errAdminRunning    = errors.New("jobs: schedule already active")
)

func (m *Module) adminOn(ctx context.Context, tx *gorm.DB, manage bool) error {
	if m == nil || m.admin.check == nil {
		return ErrUnavailable
	}
	code := "system_job_view"
	if manage {
		code = "system_job_manage"
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 {
		return errAdminForbidden
	}
	allowed, err := m.admin.check(ctx, tx, code, actor)
	if err != nil {
		return err
	}
	if !allowed {
		return errAdminForbidden
	}
	return nil
}
func adminHTTPError(ctx context.Context, err error) error {
	status, code := 503, 170503
	key := respx.ServiceUnavailable
	switch {
	case errors.Is(err, errAdminForbidden):
		status, code = 403, 170403
		key = respx.Forbidden
	case errors.Is(err, errAdminNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		status, code = 404, 170404
		key = respx.NotFound
	case errors.Is(err, errAdminVersion):
		status, code = 409, 170409
		key = jobsi18n.VersionConflict
	case errors.Is(err, errAdminTransition):
		status, code = 409, 170420
		key = jobsi18n.InvalidTransition
	case errors.Is(err, errAdminRunning):
		status, code = 409, 170421
		key = jobsi18n.ScheduleActive
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrUnknownKind):
		status, code = 400, 170400
		key = respx.InvalidArgument
	}
	return respx.ErrStatus(ctx, status, code, key, nil)
}

type adminJobView struct {
	Module             string          `json:"module,omitempty"`
	DisplayName        string          `json:"display_name,omitempty"`
	Description        string          `json:"description,omitempty"`
	ID                 guid.ID         `json:"id"`
	TenantID           guid.ID         `json:"tenant_id"`
	Kind               string          `json:"kind"`
	Queue              string          `json:"queue"`
	State              string          `json:"state"`
	Priority           int             `json:"priority"`
	Attempts           int             `json:"attempts"`
	MaxAttempts        int             `json:"max_attempts"`
	ManualRetries      int             `json:"manual_retries"`
	Version            int             `json:"version"`
	HasHandler         bool            `json:"has_handler"`
	RunAt              time.Time       `json:"run_at"`
	CreatedAt          time.Time       `json:"created_at"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	CorrelationID      string          `json:"correlation_id,omitempty"`
	LastErrorCode      string          `json:"last_error_code"`
	Payload            json.RawMessage `json:"payload,omitempty"`
	ErrorHistory       json.RawMessage `json:"error_history,omitempty"`
	FanoutScheduleName string          `json:"fanout_schedule_name,omitempty"`
	FanoutKind         string          `json:"fanout_kind,omitempty"`
	FanoutDisplayName  string          `json:"fanout_display_name,omitempty"`
	FanoutDescription  string          `json:"fanout_description,omitempty"`
	FanoutFireAt       *time.Time      `json:"fanout_fire_at,omitempty"`
}

func (m *Module) adminJob(ctx context.Context, row *model.Job, detail bool) (adminJobView, error) {
	m.mu.RLock()
	handler, known := m.handlers[row.Kind]
	var target Handler
	if row.Kind == fanoutKind && row.FanoutKind != nil {
		target = m.handlers[*row.FanoutKind]
	}
	m.mu.RUnlock()
	v := adminJobView{ID: row.ID, TenantID: row.TenantID, Kind: row.Kind, Queue: row.Queue, State: row.State, Priority: row.Priority, Attempts: row.Attempts, MaxAttempts: row.MaxAttempts, ManualRetries: row.ManualRetries, Version: row.Version, HasHandler: known, RunAt: time.UnixMilli(row.RunAtMS).UTC(), CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt, LastErrorCode: row.LastErrorCode}
	v.Module = handler.Module
	if handler.DisplayNameKey != "" {
		v.DisplayName = platformi18n.TContext(ctx, handler.DisplayNameKey)
	}
	if handler.DescriptionKey != "" {
		v.Description = platformi18n.TContext(ctx, handler.DescriptionKey)
	}
	// Dispatch identity comes from the frozen job snapshot, never a schedule
	// whose declaration may have changed since this occurrence was queued.
	if row.Kind == fanoutKind && row.FanoutKind != nil && row.FanoutFireMS != nil && row.ConcurrencyKey != nil {
		v.FanoutScheduleName = *row.ConcurrencyKey
		v.FanoutKind = *row.FanoutKind
		fireAt := time.UnixMilli(*row.FanoutFireMS).UTC()
		v.FanoutFireAt = &fireAt
		if target.DisplayNameKey != "" {
			v.FanoutDisplayName = platformi18n.TContext(ctx, target.DisplayNameKey)
		}
		if target.DescriptionKey != "" {
			v.FanoutDescription = platformi18n.TContext(ctx, target.DescriptionKey)
		}
	}
	if detail {
		v.StartedAt = row.StartedAt
		v.CorrelationID = row.CorrelationID
		payload, err := audit.RedactAdministrativeJSON([]byte(row.Payload))
		if err != nil {
			return v, err
		}
		v.Payload = json.RawMessage(payload)
		v.ErrorHistory = json.RawMessage(row.ErrorHistory)
	}
	return v, nil
}

type AdminJobInput struct {
	JobID string `path:"job_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type adminJobMutationInput struct {
	AdminJobInput
	IfMatch string `header:"If-Match"`
	Body    struct {
		Reason string `json:"reason" minLength:"1" maxLength:"2000"`
		State  string `json:"state,omitempty" enum:",CANCELLED"` //nolint:misspell // Spec freezes this protocol state spelling.
	}
}
type adminJobOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[adminJobView]
}
type adminJobListInput struct {
	Offset int `query:"offset" default:"0" minimum:"0"`
	Limit  int `query:"limit" default:"50" minimum:"1" maximum:"200"`

	State      string `query:"state" enum:",PENDING,RUNNING,SUCCEEDED,DEAD,CANCELLED"` //nolint:misspell // Spec freezes this protocol state spelling.
	Queue      string `query:"queue" maxLength:"64"`
	Kind       string `query:"kind" maxLength:"128"`
	Tenant     string `query:"tenant_id" maxLength:"24"`
	Priority   string `query:"priority" maxLength:"8"`
	CreatedAt  string `query:"created_at" maxLength:"64"`
	FinishedAt string `query:"finished_at" maxLength:"64"`
	Sort       string `query:"sort" default:"-id" enum:"id,-id,created_at,-created_at,run_at,-run_at,priority,-priority"`
}
type adminJobPage struct {
	Items      []adminJobView  `json:"items"`
	HasMore    bool            `json:"has_more"`
	NextOffset int             `json:"next_offset,omitempty"`
	Summary    adminJobSummary `json:"summary"`
}
type adminJobSummary struct {
	Running                 int64 `json:"running"`
	Pending                 int64 `json:"pending"`
	Dead24h                 int64 `json:"dead_24h"`
	OldestPendingAgeSeconds int64 `json:"oldest_pending_age_seconds"`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil {
		return ErrUnavailable
	}
	if m.admin.registrar == nil {
		slog.Warn("job administration HTTP is disabled; enable IAM to bind platform authorization")
		return nil
	}
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"background-jobs"}, Errors: []int{400, 401, 403, 404, 409, 422, 428, 503}}
	}
	view, manage := authz.Guard{Resource: "system_job", Verb: "view"}, authz.Guard{Resource: "system_job", Verb: "manage"}
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.list", http.MethodGet, "/platform/jobs", "List background jobs without payloads"), view, m.listAdminJobs)
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.get", http.MethodGet, "/platform/jobs/{job_id}", "Read a background job with redacted payload"), view, m.getAdminJob)
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.retry", http.MethodPost, "/platform/jobs/{job_id}/attempts", "Retry a dead background job"), manage, func(ctx context.Context, in *adminJobMutationInput) (*adminJobOutput, error) {
		return m.mutateAdminJobHTTP(ctx, in, true)
	})
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.cancel", http.MethodPatch, "/platform/jobs/{job_id}", "Cancel a pending or dead background job"), manage, func(ctx context.Context, in *adminJobMutationInput) (*adminJobOutput, error) {
		return m.mutateAdminJobHTTP(ctx, in, false)
	})
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.retry-batch", http.MethodPost, "/platform/job-attempts", "Retry dead background jobs individually"), manage, m.batchAdminJobs)
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.schedules.list", http.MethodGet, "/platform/job-schedules", "List declared background schedules"), view, m.listAdminSchedules)
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.schedules.update", http.MethodPatch, "/platform/job-schedules/{schedule_id}", "Pause or resume a declared background schedule"), manage, m.patchAdminSchedule)
	authz.RegisterPlatform(m.admin.registrar, api, op("jobs.schedules.run", http.MethodPost, "/platform/job-schedules/{schedule_id}/runs", "Run a declared background schedule once"), manage, m.runAdminSchedule)
	return nil
}

func adminVersion(ctx context.Context, value string) (int, error) {
	if value == "" {
		return 0, respx.ErrStatus(ctx, 428, 170428, jobsi18n.VersionRequired, nil)
	}
	v, err := strconv.Atoi(strings.Trim(value, "\""))
	if err != nil || v < 1 {
		return 0, ErrInvalidRequest
	}
	return v, nil
}
func (m *Module) getAdminJob(ctx context.Context, in *AdminJobInput) (*adminJobOutput, error) {
	id, err := guid.RequireID(in.JobID)
	if err != nil {
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	var out adminJobView
	err = m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if e := m.adminOn(ctx, tx, false); e != nil {
			return e
		}
		q := query.BindJobs(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		out, e = m.adminJob(ctx, row, true)
		return e
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return &adminJobOutput{ETag: fmt.Sprintf("\"%d\"", out.Version), Body: respx.OK(ctx, out).Body}, nil
}

func (m *Module) mutateAdminJobOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, reason string, retry bool) (adminJobView, error) {
	if id <= 0 || version < 1 || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 2000 {
		return adminJobView{}, ErrInvalidRequest
	}
	if err := m.adminOn(ctx, tx, true); err != nil {
		return adminJobView{}, err
	}
	q := query.BindJobs(tx, m.names)
	snapshot, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return adminJobView{}, err
	}
	if err = m.guard(ctx, tx, "jobs:pending:"+snapshot.TenantID.String()); err != nil {
		return adminJobView{}, err
	}
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(id)).First()
	if err != nil {
		return adminJobView{}, err
	}
	if row.Version != version {
		return adminJobView{}, errAdminVersion
	}
	if (retry && row.State != "DEAD") || (!retry && row.State != "PENDING" && row.State != "DEAD") {
		return adminJobView{}, errAdminTransition
	}
	nowMS, err := gormx.DatabaseClockMillis(ctx, tx)
	if err != nil {
		return adminJobView{}, err
	}
	now := time.UnixMilli(nowMS).UTC()
	updates := map[string]any{"state": "CANCELLED", "finished_at": now, "updated_at": now, "version": version + 1} //nolint:misspell // Spec freezes this protocol state spelling.
	if retry {
		m.mu.RLock()
		_, known := m.handlers[row.Kind]
		m.mu.RUnlock()
		if !known {
			return adminJobView{}, ErrUnknownKind
		}
		pending, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select(q.ID).Where(q.TenantID.Eq(row.TenantID), q.State.Eq("PENDING")).Limit(m.config.MaxPendingPerTenant).Find()
		if e != nil {
			return adminJobView{}, e
		}
		if len(pending) >= m.config.MaxPendingPerTenant {
			return adminJobView{}, ErrQueueFull
		}
		updates = map[string]any{"state": "PENDING", "attempts": 0, "manual_retries": row.ManualRetries + 1, "run_at_ms": nowMS, "finished_at": nil, "started_at": nil, "lease_owner": nil, "lease_expires_at_ms": nil, "updated_at": now, "version": version + 1}
	}
	changed, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(version), q.State.Eq(row.State)).Updates(updates)
	if err != nil {
		return adminJobView{}, err
	}
	if changed.RowsAffected != 1 {
		return adminJobView{}, errAdminVersion
	}
	audit.Annotate(ctx, audit.Metadata("job_id", id.String()), audit.Metadata("reason", reason))
	action := "jobs.cancelled"
	if retry {
		action = "jobs.retried"
	}
	if err = m.adminAuditOn(ctx, tx, action, row.TenantID, "job", id, map[string]any{"reason": reason, "version": version + 1}); err != nil {
		return adminJobView{}, err
	}
	row, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return adminJobView{}, err
	}
	return m.adminJob(ctx, row, false)
}
func (m *Module) mutateAdminJobHTTP(ctx context.Context, in *adminJobMutationInput, retry bool) (*adminJobOutput, error) {
	id, err := guid.RequireID(in.JobID)
	if err != nil {
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	v, err := adminVersion(ctx, in.IfMatch)
	if err != nil {
		if in.IfMatch == "" {
			return nil, err
		}
		return nil, adminHTTPError(ctx, err)
	}
	if !retry && in.Body.State != "CANCELLED" { //nolint:misspell // Spec freezes this protocol state spelling.
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	var out adminJobView
	err = m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		var e error
		out, e = m.mutateAdminJobOn(ctx, tx, id, v, in.Body.Reason, retry)
		return e
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return &adminJobOutput{ETag: fmt.Sprintf("\"%d\"", out.Version), Body: respx.OK(ctx, out).Body}, nil
}

func (m *Module) listAdminJobs(ctx context.Context, in *adminJobListInput) (*respx.Body[adminJobPage], error) {
	out := adminJobPage{Items: []adminJobView{}}
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if err := m.adminOn(ctx, tx, false); err != nil {
			return err
		}
		q := query.BindJobs(tx, m.names)
		scope := q.WithContext(ctx)
		summaryScope := q.WithContext(ctx)
		if in.State != "" {
			scope = scope.Where(q.State.Eq(in.State))
		}
		if in.Queue != "" {
			scope = scope.Where(q.Queue.Eq(in.Queue))
		}
		if in.Kind != "" {
			scope = scope.Where(q.Kind.Eq(in.Kind))
		}
		if in.Tenant != "" {
			raw := strings.TrimPrefix(in.Tenant, "eq:")
			v, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || v < 0 {
				return ErrInvalidRequest
			}
			scope = scope.Where(q.TenantID.Eq(guid.ID(v)))
			summaryScope = summaryScope.Where(q.TenantID.Eq(guid.ID(v)))
		}
		// Operational totals span every page and state; only tenant scope narrows them.
		ms, e := gormx.DatabaseClockMillis(ctx, tx)
		if e != nil {
			return e
		}
		if out.Summary.Running, e = summaryScope.Session(&gorm.Session{}).Where(q.State.Eq("RUNNING")).Count(); e != nil {
			return e
		}
		if out.Summary.Pending, e = summaryScope.Session(&gorm.Session{}).Where(q.State.Eq("PENDING")).Count(); e != nil {
			return e
		}
		if out.Summary.Dead24h, e = summaryScope.Session(&gorm.Session{}).Where(q.State.Eq("DEAD"), q.FinishedAt.Gte(time.UnixMilli(ms).UTC().Add(-24*time.Hour))).Count(); e != nil {
			return e
		}
		oldest, e := summaryScope.Session(&gorm.Session{}).Select(q.CreatedAt).Where(q.State.Eq("PENDING")).Order(q.CreatedAt).First()
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil && ms > oldest.CreatedAt.UnixMilli() {
			out.Summary.OldestPendingAgeSeconds = (ms - oldest.CreatedAt.UnixMilli()) / 1000
		}
		if in.Priority != "" {
			op, raw, ok := strings.Cut(in.Priority, ":")
			if !ok {
				op, raw = "eq", in.Priority
			}
			v, e := strconv.Atoi(raw)
			if e != nil || v < 0 || v > 9 {
				return ErrInvalidRequest
			}
			switch op {
			case "eq":
				scope = scope.Where(q.Priority.Eq(v))
			case "lte":
				scope = scope.Where(q.Priority.Lte(v))
			default:
				return ErrInvalidRequest
			}
		}
		for name, raw := range map[string]string{"created_at": in.CreatedAt, "finished_at": in.FinishedAt} {
			if raw == "" {
				continue
			}
			op, value, ok := strings.Cut(raw, ":")
			if !ok {
				return ErrInvalidRequest
			}
			at, e := time.Parse(time.RFC3339Nano, value)
			if e != nil {
				return ErrInvalidRequest
			}
			f := q.CreatedAt
			if name == "finished_at" {
				f = q.FinishedAt
			}
			switch op {
			case "gt":
				scope = scope.Where(f.Gt(at))
			case "gte":
				scope = scope.Where(f.Gte(at))
			case "lt":
				scope = scope.Where(f.Lt(at))
			case "lte":
				scope = scope.Where(f.Lte(at))
			default:
				return ErrInvalidRequest
			}
		}
		order := q.ID.Asc()
		switch in.Sort {
		case "id":
			// The initialized ascending ID order is already correct.
		case "", "-id":
			order = q.ID.Desc()
		case "created_at":
			order = q.CreatedAt.Asc()
		case "-created_at":
			order = q.CreatedAt.Desc()
		case "run_at":
			order = q.RunAtMS.Asc()
		case "-run_at":
			order = q.RunAtMS.Desc()
		case "priority":
			order = q.Priority.Asc()
		case "-priority":
			order = q.Priority.Desc()
		default:
			return ErrInvalidRequest
		}
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 200 {
			return ErrInvalidRequest
		}
		if in.Offset < 0 {
			return ErrInvalidRequest
		}
		rows, e := scope.Order(order, q.ID).Offset(in.Offset).Limit(limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > limit {
			out.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			v, e := m.adminJob(ctx, row, false)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		if out.HasMore && len(rows) > 0 {
			out.NextOffset = in.Offset + limit
		}
		return nil
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type adminBatchInput struct {
	Body struct {
		IDs    []guid.ID `json:"job_ids" minItems:"1" maxItems:"100"`
		Reason string    `json:"reason" minLength:"1" maxLength:"2000"`
	}
}
type adminBatchSkip struct {
	ID   guid.ID `json:"id"`
	Code int     `json:"code"`
}
type adminBatchResult struct {
	Retried []guid.ID        `json:"retried"`
	Skipped []adminBatchSkip `json:"skipped"`
}

func (m *Module) batchAdminJobs(ctx context.Context, in *adminBatchInput) (*respx.Body[adminBatchResult], error) {
	if len(in.Body.IDs) < 1 || len(in.Body.IDs) > 100 || strings.TrimSpace(in.Body.Reason) == "" || utf8.RuneCountInString(in.Body.Reason) > 2000 {
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	for _, id := range in.Body.IDs {
		if id <= 0 {
			return nil, adminHTTPError(ctx, ErrInvalidRequest)
		}
	}
	out := adminBatchResult{Retried: []guid.ID{}, Skipped: []adminBatchSkip{}}
	for _, id := range in.Body.IDs {
		err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
			if err := m.adminOn(ctx, tx, true); err != nil {
				return err
			}
			q := query.BindJobs(tx, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
			if err != nil {
				return err
			}
			_, err = m.mutateAdminJobOn(ctx, tx, id, row.Version, in.Body.Reason, true)
			return err
		})
		if errors.Is(err, errAdminForbidden) || errors.Is(err, ErrUnavailable) {
			return nil, adminHTTPError(ctx, err)
		}
		if err == nil {
			out.Retried = append(out.Retried, id)
			continue
		}
		code := 170420
		if errors.Is(err, gorm.ErrRecordNotFound) {
			code = 170404
		} else if errors.Is(err, errAdminVersion) {
			code = 170409
		} else if !errors.Is(err, errAdminTransition) && !errors.Is(err, ErrUnknownKind) && !errors.Is(err, ErrQueueFull) {
			return nil, adminHTTPError(ctx, err)
		}
		out.Skipped = append(out.Skipped, adminBatchSkip{ID: id, Code: code})
	}
	return respx.OK(ctx, out), nil
}

type adminScheduleView struct {
	Module      string    `json:"module,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	Description string    `json:"description,omitempty"`
	ID          guid.ID   `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Queue       string    `json:"queue"`
	Enabled     bool      `json:"enabled"`
	Orphaned    bool      `json:"orphaned"`
	TenantScope string    `json:"tenant_scope"`
	IntervalMS  *int64    `json:"interval_ms,omitempty"`
	Cron        *string   `json:"cron,omitempty"`
	Timezone    string    `json:"timezone"`
	NextRunAt   time.Time `json:"next_run_at"`
	LastJobID   *guid.ID  `json:"last_job_id,omitempty"`
	Version     int       `json:"version"`
}

func (m *Module) adminSchedule(ctx context.Context, row *model.Schedule) adminScheduleView {
	m.mu.RLock()
	_, known := m.schedules[row.Name]
	handler := m.handlers[row.Kind]
	m.mu.RUnlock()
	v := adminScheduleView{ID: row.ID, Name: row.Name, Kind: row.Kind, Queue: row.Queue, Enabled: row.Enabled, Orphaned: !known, TenantScope: row.TenantScope, IntervalMS: row.IntervalMS, Cron: row.Cron, Timezone: row.Timezone, NextRunAt: time.UnixMilli(row.NextRunAtMS).UTC(), LastJobID: row.LastJobID, Version: row.Version}
	v.Module = handler.Module
	if handler.DisplayNameKey != "" {
		v.DisplayName = platformi18n.TContext(ctx, handler.DisplayNameKey)
	}
	if handler.DescriptionKey != "" {
		v.Description = platformi18n.TContext(ctx, handler.DescriptionKey)
	}
	return v
}
func (m *Module) listAdminSchedules(ctx context.Context, _ *struct{}) (*respx.Body[[]adminScheduleView], error) {
	out := []adminScheduleView{}
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if e := m.adminOn(ctx, tx, false); e != nil {
			return e
		}
		q := query.BindSchedules(tx, m.names)
		rows, e := q.WithContext(ctx).Order(q.Name, q.ID).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, m.adminSchedule(ctx, r))
		}
		return nil
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type AdminScheduleInput struct {
	ScheduleID string `path:"schedule_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type adminSchedulePatchInput struct {
	AdminScheduleInput
	IfMatch string `header:"If-Match"`
	Body    struct {
		Enabled bool `json:"enabled"`
	}
}
type adminScheduleRunInput struct {
	AdminScheduleInput
	Key string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64" pattern:"^[!-~]+$"`
}

func (m *Module) patchAdminSchedule(ctx context.Context, in *adminSchedulePatchInput) (*respx.Body[adminScheduleView], error) {
	id, err := guid.RequireID(in.ScheduleID)
	if err != nil {
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	v, err := adminVersion(ctx, in.IfMatch)
	if err != nil {
		if in.IfMatch == "" {
			return nil, err
		}
		return nil, adminHTTPError(ctx, err)
	}
	var out adminScheduleView
	err = m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if e := m.adminOn(ctx, tx, true); e != nil {
			return e
		}
		if e := m.guard(ctx, tx, "jobs:schedules"); e != nil {
			return e
		}
		q := query.BindSchedules(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		if row.Version != v {
			return errAdminVersion
		}
		if m.adminSchedule(ctx, row).Orphaned {
			return errAdminTransition
		}
		ms, e := gormx.DatabaseClockMillis(ctx, tx)
		if e != nil {
			return e
		}
		changed, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(v)).UpdateSimple(q.Enabled.Value(in.Body.Enabled), q.Version.Add(1), q.UpdatedAt.Value(time.UnixMilli(ms).UTC()))
		if e != nil {
			return e
		}
		if changed.RowsAffected != 1 {
			return errAdminVersion
		}
		row.Enabled = in.Body.Enabled
		row.Version++
		out = m.adminSchedule(ctx, row)
		return m.adminAuditOn(ctx, tx, "jobs.schedule_updated", 0, "job_schedule", id, map[string]any{"enabled": row.Enabled, "version": row.Version})
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type adminScheduleRunResult struct {
	JobID guid.ID `json:"job_id"`
}

func (m *Module) runAdminSchedule(ctx context.Context, in *adminScheduleRunInput) (*respx.Body[adminScheduleRunResult], error) {
	id, err := guid.RequireID(in.ScheduleID)
	if err != nil || in.Key == "" || len(in.Key) > 64 {
		return nil, adminHTTPError(ctx, ErrInvalidRequest)
	}
	for _, b := range []byte(in.Key) {
		if b < 33 || b > 126 {
			return nil, adminHTTPError(ctx, ErrInvalidRequest)
		}
	}
	var out adminScheduleRunResult
	err = m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if e := m.adminOn(ctx, tx, true); e != nil {
			return e
		}
		if e := m.guard(ctx, tx, "jobs:pending:0"); e != nil {
			return e
		}
		if e := m.guard(ctx, tx, "jobs:schedules"); e != nil {
			return e
		}
		s := query.BindSchedules(tx, m.names)
		row, e := s.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(s.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		m.mu.RLock()
		declaration, known := m.schedules[row.Name]
		m.mu.RUnlock()
		if !known {
			return errAdminTransition
		}
		q := query.BindJobs(tx, m.names)
		key := "manual:" + id.String() + ":" + in.Key
		existing, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(guid.ID(0)), q.IdempotencyKey.Eq(key)).First()
		if e == nil {
			out.JobID = existing.ID
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		active, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select(q.ID).Where(q.ConcurrencyKey.Eq(row.Name), q.State.In("PENDING", "RUNNING")).Limit(1).Find()
		if e != nil {
			return e
		}
		if len(active) > 0 {
			return errAdminRunning
		}
		ms, e := gormx.DatabaseClockMillis(ctx, tx)
		if e != nil {
			return e
		}
		kind := row.Kind
		if declaration.TenantScope == ScopePerTenant {
			if m.activeTenants == nil {
				return ErrUnavailable
			}
			kind = fanoutKind
		}
		job, e := m.Enqueue(ctx, tx, EnqueueRequest{Kind: kind, Payload: json.RawMessage(row.Payload), IdempotencyKey: key, ConcurrencyKey: row.Name, CreatedBy: authn.PrincipalIDFromContext(ctx)})
		if e != nil {
			return e
		}
		if declaration.TenantScope == ScopePerTenant && job.Created {
			if _, e = q.WithContext(ctx).Where(q.ID.Eq(job.JobID)).UpdateSimple(q.FanoutKind.Value(row.Kind), q.FanoutFireMS.Value(ms)); e != nil {
				return e
			}
		}
		changed, e := s.WithContext(ctx).Where(s.ID.Eq(id), s.Version.Eq(row.Version)).UpdateSimple(s.LastJobID.Value(job.JobID), s.Version.Add(1), s.UpdatedAt.Value(time.UnixMilli(ms).UTC()))
		if e != nil {
			return e
		}
		if changed.RowsAffected != 1 {
			return errAdminVersion
		}
		out.JobID = job.JobID
		return m.adminAuditOn(ctx, tx, "jobs.schedule_run", 0, "job_schedule", id, map[string]any{"job_id": job.JobID.String()})
	})
	if err != nil {
		return nil, adminHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

// QueueAdmission acquires the same tenant quota guard Enqueue uses. A producer
// calls it before principal/business locks when it may enqueue in that transaction.
type QueueAdmission func(context.Context, *gorm.DB, guid.ID) error

func (m *Module) admitQueueOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	if m == nil || m.guard == nil || tx == nil || tenant < 0 {
		return ErrInvalidRequest
	}
	return m.guard(ctx, tx, "jobs:pending:"+tenant.String())
}
