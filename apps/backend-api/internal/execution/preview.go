package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

var (
	ErrPreviewPortNotOwned   = errors.New("execution: preview port not owned")
	ErrPreviewAccessDisabled = errors.New("execution: preview access disabled")
	ErrPreviewUnavailable    = errors.New("execution: isolated preview gateway unavailable")
	ErrPreviewQuota          = errors.New("execution: preview quota exceeded")
)

type previewServices struct{ runtime *fleet.PreviewRuntime }
type PreviewSessionView struct {
	Reused          bool       `json:"-"`
	StartedBy       guid.ID    `json:"started_by"`
	ID              guid.ID    `json:"id"`
	ExecutionID     guid.ID    `json:"execution_id"`
	Kind            string     `json:"kind"`
	Origin          string     `json:"origin"`
	Port            int        `json:"port"`
	MachineID       guid.ID    `json:"machine_id"`
	State           string     `json:"state"`
	StopReason      string     `json:"stop_reason,omitempty"`
	URLMode         string     `json:"url_mode"`
	PathModeWarning bool       `json:"path_mode_warning"`
	LastAccessAt    time.Time  `json:"last_access_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	Version         int        `json:"version"`
}
type PreviewAccessGrant struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

func (m *Module) previewView(row *model.PreviewSession) PreviewSessionView {
	mode := "SUBDOMAIN"
	if m.preview.runtime != nil && m.preview.runtime.Config != nil && m.preview.runtime.Config().Mode == "PATH" {
		mode = "PATH"
	}
	return PreviewSessionView{StartedBy: row.StartedBy, ID: row.ID, ExecutionID: row.ExecutionID, Kind: "SERVICE", Origin: "AGENT_OFFERED", Port: row.Port, MachineID: row.MachineID, State: row.State, StopReason: row.StopReason, URLMode: mode, PathModeWarning: mode == "PATH", LastAccessAt: row.LastAccessAt, ExpiresAt: row.ExpiresAt, EndedAt: row.EndedAt, Version: row.Version}
}
func (m *Module) bindPreview(s *module.Services) error {
	runtime, err := module.Resolve[*fleet.PreviewRuntime](s)
	if err != nil {
		return err
	}
	connector, err := module.Resolve[fleet.PreviewAccessResolverConnector](s)
	if err != nil {
		return err
	}
	if err = connector.Bind(fleet.PreviewAccessResolver{Authorize: m.authorizePreview, ExchangeCode: m.exchangePreviewCode, Touch: m.touchPreview, StopMachine: m.stopMachinePreviews, Revalidate: m.revalidatePreview}); err != nil {
		return err
	}
	m.preview.runtime = runtime
	return nil
}
func previewActor(ctx context.Context, tenant, actor guid.ID) context.Context {
	return authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
}
func previewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func previewDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func previewLabel() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])), nil
}
func (m *Module) previewConfigured() bool {
	return m.preview.runtime != nil && m.preview.runtime.Config != nil && m.preview.runtime.Config().Domain != "" && m.preview.runtime.MachineOn != nil && m.preview.runtime.Available != nil && m.preview.runtime.Ports != nil
}
func (m *Module) previewExecutionGrantOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string) (fleet.PreviewGrant, error) {
	var result fleet.PreviewGrant
	if !m.previewConfigured() {
		return result, ErrPreviewUnavailable
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return result, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, row.ProjectID, permission); err != nil {
		return result, err
	}
	machine, err := m.preview.runtime.MachineOn(ctx, tx, tenant, row.MachineID)
	if err != nil {
		return result, err
	}
	if !machine.Enabled {
		return result, ErrPreviewAccessDisabled
	}
	if machine.State != "ONLINE" && machine.State != "DRAINING" {
		return result, fleet.ErrMachineOffline
	}
	if machine.RunnerSessionID <= 0 || !m.preview.runtime.Available(tenant, row.MachineID) {
		return result, ErrPreviewUnavailable
	}
	row, err = m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return result, err
	}
	if !active(row.State) {
		return result, ErrConflict
	}
	var plan ExecutionPlan
	if err = json.Unmarshal([]byte(row.PlanJSON), &plan); err != nil {
		return result, err
	}
	if plan.Workdir == "" || plan.WorkdirID <= 0 {
		return result, ErrUnavailable
	}
	return fleet.PreviewGrant{TenantID: tenant, ProjectID: row.ProjectID, MachineID: row.MachineID, ExecutionID: id, RunnerSessionID: machine.RunnerSessionID, ConnEpoch: machine.ConnEpoch, DispatchEpoch: row.DispatchEpoch, Workspace: plan.Workdir, TunnelMaxStreams: machine.TunnelMaxStreams}, nil
}
func samePreviewRuntime(a, b fleet.PreviewGrant) bool {
	return a.TenantID == b.TenantID && a.ProjectID == b.ProjectID && a.ExecutionID == b.ExecutionID && a.MachineID == b.MachineID && a.RunnerSessionID == b.RunnerSessionID && a.ConnEpoch == b.ConnEpoch && a.DispatchEpoch == b.DispatchEpoch && a.Workspace == b.Workspace
}
func (m *Module) previewExecutionGrant(ctx context.Context, db *gorm.DB, tenant, id guid.ID, permission string) (fleet.PreviewGrant, error) {
	var grant fleet.PreviewGrant
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		grant, e = m.previewExecutionGrantOn(ctx, tx, tenant, id, permission)
		return e
	})
	return grant, err
}
func (m *Module) CreatePreview(ctx context.Context, tenant, id guid.ID, port int) (PreviewSessionView, error) {
	var out PreviewSessionView
	if port < 1 || port > 65535 {
		return out, ErrInvalidArgument
	}
	if !m.previewConfigured() {
		return out, ErrPreviewUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	observed, err := m.previewExecutionGrant(ctx, db, tenant, id, "execution_start")
	if err != nil {
		return out, err
	}
	observed.Port = port
	// Broker admission and the runner ownership probe can themselves read/write
	// fleet state. No caller transaction or business lock may span this wait.
	ports, err := m.preview.runtime.Ports(ctx, observed)
	if err != nil {
		if errors.Is(err, fleet.ErrMachineForbidden) {
			err = ErrPreviewPortNotOwned
		}
		return out, err
	}
	if !slices.Contains(ports, port) {
		return out, ErrPreviewPortNotOwned
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		grant, e := m.previewExecutionGrantOn(ctx, tx, tenant, id, "execution_start")
		if e != nil {
			return e
		}
		if !samePreviewRuntime(grant, observed) {
			return ErrFenced
		}
		grant.Port = port
		q := query.BindPreviewSessions(tx, m.names)
		existing, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.State.Eq("READY")).Order(q.ID).Find()
		if e != nil {
			return e
		}
		count := 0
		for _, r := range existing {
			now := time.Now()
			reason := ""
			if !now.Before(r.ExpiresAt) {
				reason = "MAX_LIFETIME"
			} else if now.Sub(r.LastAccessAt) >= 30*time.Minute {
				reason = "IDLE"
			} else if r.RunnerSessionID != grant.RunnerSessionID || r.ConnEpoch != grant.ConnEpoch || r.DispatchEpoch != grant.DispatchEpoch {
				reason = "EXECUTION_ENDED"
			}
			if reason != "" {
				if e = m.stopPreviewOn(ctx, tx, r, reason); e != nil {
					return e
				}
				continue
			}
			count++
			if r.Port == port {
				out = m.previewView(r)
				out.Reused = true
				return nil
			}
		}
		if count >= 3 {
			return ErrPreviewQuota
		}
		label, e := previewLabel()
		if e != nil {
			return e
		}
		next, e := m.ids.Next()
		if e != nil {
			return e
		}
		if next <= 0 {
			return ErrUnavailable
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		minutes := m.preview.runtime.Config().MaxSessionMinutes
		if minutes == 0 {
			minutes = 480
		}
		row := &model.PreviewSession{ID: guid.ID(next), TenantID: tenant, ProjectID: grant.ProjectID, ExecutionID: id, MachineID: grant.MachineID, RunnerSessionID: grant.RunnerSessionID, ConnEpoch: grant.ConnEpoch, DispatchEpoch: grant.DispatchEpoch, Port: port, Label: label, State: "READY", StartedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, LastAccessAt: now, ExpiresAt: now.Add(time.Duration(minutes) * time.Minute), Version: 1}
		if e = q.WithContext(ctx).Create(row); e != nil {
			return e
		}
		if e = m.previewEventOn(ctx, tx, row, "execution.preview_created"); e != nil {
			return e
		}
		out = m.previewView(row)
		return nil
	})
	return out, err
}
func (m *Module) previewOn(ctx context.Context, tx *gorm.DB, id guid.ID, lock bool) (*model.PreviewSession, error) {
	q := query.BindPreviewSessions(tx, m.names)
	stmt := q.WithContext(ctx).Where(q.ID.Eq(id))
	if lock {
		stmt = stmt.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := stmt.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return row, err
}
func (m *Module) previewGrantOn(ctx context.Context, tx *gorm.DB, observed *model.PreviewSession) (fleet.PreviewGrant, error) {
	grant, err := m.previewExecutionGrantOn(ctx, tx, observed.TenantID, observed.ExecutionID, "execution_read")
	if err != nil {
		return grant, err
	}
	row, err := m.previewOn(ctx, tx, observed.ID, true)
	if err != nil {
		return grant, err
	}
	now := time.Now().UTC()
	if row.State != "READY" || !now.Before(row.ExpiresAt) || now.Sub(row.LastAccessAt) >= 30*time.Minute {
		return grant, ErrConflict
	}
	if row.RunnerSessionID != grant.RunnerSessionID || row.ConnEpoch != grant.ConnEpoch || row.DispatchEpoch != grant.DispatchEpoch || row.MachineID != grant.MachineID || row.ProjectID != grant.ProjectID {
		return grant, ErrFenced
	}
	grant.SessionID = row.ID
	grant.Port = row.Port
	grant.ExpiresAt = row.ExpiresAt
	return grant, nil
}
func (m *Module) ListPreviews(ctx context.Context, tenant, id guid.ID) ([]PreviewSessionView, error) {
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	out := []PreviewSessionView{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if e = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return e
		}
		q := query.BindPreviewSessions(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id)).Order(q.ID.Desc()).Limit(100).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, m.previewView(r))
		}
		return nil
	})
	return out, err
}
func (m *Module) GetPreview(ctx context.Context, tenant, id guid.ID) (PreviewSessionView, error) {
	var out PreviewSessionView
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.previewOn(ctx, tx, id, false)
		if e != nil {
			return e
		}
		if row.TenantID != tenant {
			return ErrNotFound
		}
		if e = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return e
		}
		out = m.previewView(row)
		return nil
	})
	return out, err
}
func (m *Module) StopPreview(ctx context.Context, tenant, id guid.ID) (PreviewSessionView, error) {
	var out PreviewSessionView
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.previewOn(ctx, tx, id, false)
		if e != nil {
			return e
		}
		if row.TenantID != tenant {
			return ErrNotFound
		}
		permission := "execution_stop"
		if row.StartedBy == authn.PrincipalIDFromContext(ctx) {
			permission = "execution_read"
		}
		if e = m.humanAuthority(ctx, tx, tenant, row.ProjectID, permission); e != nil {
			return e
		}
		row, e = m.previewOn(ctx, tx, id, true)
		if e != nil {
			return e
		}
		if e = m.stopPreviewOn(ctx, tx, row, "USER"); e != nil {
			return e
		}
		out = m.previewView(row)
		return nil
	})
	if err == nil && m.preview.runtime != nil && m.preview.runtime.ResetSession != nil {
		err = m.preview.runtime.ResetSession(ctx, id, "USER")
	}
	return out, err
}
func (m *Module) stopPreviewOn(ctx context.Context, tx *gorm.DB, row *model.PreviewSession, reason string) error {
	if row.State == "STOPPED" {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row.State = "STOPPED"
	row.StopReason = reason
	row.EndedAt = &now
	row.Version++
	if err := query.BindPreviewSessions(tx, m.names).WithContext(ctx).Save(row); err != nil {
		return err
	}
	return m.previewEventOn(ctx, tx, row, "execution.preview_stopped")
}
func (m *Module) previewEventOn(ctx context.Context, tx *gorm.DB, row *model.PreviewSession, kind string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	event := audit.NewDomainEvent(ctx, row.TenantID, kind, "preview_session", row.ID)
	event.Detail["execution_id"] = row.ExecutionID.String()
	event.Detail["state"] = row.State
	event.Detail["reason"] = row.StopReason
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "execution.preview_changed", Subject: events.Subject{Kind: "preview_session", ID: row.ID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"execution_id": row.ExecutionID, "state": row.State, "reason": row.StopReason}})
	return err
}
func safePreviewPath(path string) bool {
	return path != "" && len(path) <= 2000 && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.ContainsAny(path, "\\\r\n\x00")
}
func (m *Module) IssuePreviewAccess(ctx context.Context, tenant, id guid.ID, returnPath string) (PreviewAccessGrant, error) {
	var out PreviewAccessGrant
	if returnPath == "" {
		returnPath = "/"
	}
	if !safePreviewPath(returnPath) {
		return out, ErrInvalidArgument
	}
	if !m.previewConfigured() {
		return out, ErrPreviewUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.previewOn(ctx, tx, id, false)
		if e != nil {
			return e
		}
		if row.TenantID != tenant {
			return ErrNotFound
		}
		if _, e = m.previewGrantOn(ctx, tx, row); e != nil {
			return e
		}
		code, e := previewToken()
		if e != nil {
			return e
		}
		next, e := m.ids.Next()
		if e != nil {
			return e
		}
		if next <= 0 {
			return ErrUnavailable
		}
		access := &model.PreviewAccess{ID: guid.ID(next), SessionID: id, PrincipalID: authn.PrincipalIDFromContext(ctx), CodeHash: previewDigest(code), CodeExpiresAt: time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond), ReturnPath: returnPath}
		if e = query.BindPreviewAccess(tx, m.names).WithContext(ctx).Create(access); e != nil {
			return e
		}
		out = PreviewAccessGrant{URL: m.preview.runtime.Config().URL(row.Label, "/__access?code="+url.QueryEscape(code)), ExpiresIn: 60}
		if out.URL == "" {
			return ErrPreviewUnavailable
		}
		return nil
	})
	return out, err
}
func (m *Module) exchangePreviewCode(ctx context.Context, label, code string) (string, string, time.Time, error) {
	var cookie, path string
	var expiry time.Time
	var denied *model.PreviewSession
	var deniedActor guid.ID
	if len(code) != 43 || len(label) != 26 {
		return cookie, path, expiry, ErrForbidden
	}
	db, err := m.writer()
	if err != nil {
		return cookie, path, expiry, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindPreviewAccess(tx, m.names)
		access, e := q.WithContext(ctx).Where(q.CodeHash.Eq(previewDigest(code))).First()
		if e != nil {
			return ErrForbidden
		}
		row, e := m.previewOn(ctx, tx, access.SessionID, false)
		if e != nil {
			return e
		}
		denied = row
		deniedActor = access.PrincipalID
		if row.Label != label {
			return ErrForbidden
		}
		actorCtx := previewActor(ctx, row.TenantID, access.PrincipalID)
		if _, e = m.previewGrantOn(actorCtx, tx, row); e != nil {
			return e
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if access.ConsumedAt != nil || !now.Before(access.CodeExpiresAt) {
			return ErrForbidden
		}
		token, e := previewToken()
		if e != nil {
			return e
		}
		digest := previewDigest(token)
		expiry = now.Add(15 * time.Minute)
		if row.ExpiresAt.Before(expiry) {
			expiry = row.ExpiresAt
		}
		updated, e := q.WithContext(ctx).Where(q.ID.Eq(access.ID), q.ConsumedAt.IsNull()).UpdateSimple(q.ConsumedAt.Value(now), q.CookieHash.Value(digest), q.CookieExpiresAt.Value(expiry))
		if e != nil {
			return e
		}
		if updated.RowsAffected != 1 {
			return ErrForbidden
		}
		name, cookiePath := "__Host-preview", "/"
		if m.preview.runtime.Config().Mode == "PATH" {
			name = "__Secure-preview"
			cookiePath = "/p/" + label + "/"
		}
		cookie = (&http.Cookie{Name: name, Value: token, Path: cookiePath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expiry, MaxAge: int(time.Until(expiry).Seconds())}).String()
		path = access.ReturnPath
		return nil
	})
	if err != nil && denied != nil && m.audit != nil {
		actorCtx := previewActor(ctx, denied.TenantID, deniedActor)
		auditErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			event := audit.NewDomainEvent(actorCtx, denied.TenantID, "execution.preview_access_denied", "preview_session", denied.ID)
			event.Detail["execution_id"] = denied.ExecutionID.String()
			return m.audit(actorCtx, tx, event)
		})
		err = errors.Join(err, auditErr)
	}
	return cookie, path, expiry, err
}
func (m *Module) previewCookie(label, token string, expiry time.Time) string {
	name, path := "__Host-preview", "/"
	if m.preview.runtime.Config().Mode == "PATH" {
		name = "__Secure-preview"
		path = "/p/" + label + "/"
	}
	return (&http.Cookie{Name: name, Value: token, Path: path, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expiry, MaxAge: int(time.Until(expiry).Seconds())}).String()
}
func (m *Module) authorizePreview(ctx context.Context, label, rawCookie, method string) (fleet.PreviewGrant, error) {
	var grant fleet.PreviewGrant
	if len(label) != 26 || method == "" {
		return grant, ErrForbidden
	}
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("Cookie", rawCookie)
	name := "__Host-preview"
	if m.preview.runtime != nil && m.preview.runtime.Config != nil && m.preview.runtime.Config().Mode == "PATH" {
		name = "__Secure-preview"
	}
	cookie, err := request.Cookie(name)
	if err != nil || len(cookie.Value) != 43 {
		return grant, ErrForbidden
	}
	db, err := m.writer()
	if err != nil {
		return grant, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindPreviewAccess(tx, m.names)
		access, e := q.WithContext(ctx).Where(q.CookieHash.Eq(previewDigest(cookie.Value))).First()
		if e != nil {
			return ErrForbidden
		}
		row, e := m.previewOn(ctx, tx, access.SessionID, false)
		if e != nil {
			return e
		}
		if row.Label != label {
			return ErrForbidden
		}
		actorCtx := previewActor(ctx, row.TenantID, access.PrincipalID)
		grant, e = m.previewGrantOn(actorCtx, tx, row)
		if e != nil {
			return e
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		access, e = q.WithContext(ctx).Where(q.ID.Eq(access.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
		if e != nil {
			return e
		}
		if access.ConsumedAt == nil || access.CookieExpiresAt == nil || !now.Before(*access.CookieExpiresAt) {
			return ErrForbidden
		}
		expiry := now.Add(15 * time.Minute)
		if row.ExpiresAt.Before(expiry) {
			expiry = row.ExpiresAt
		}
		grant.PrincipalID = access.PrincipalID
		grant.AccessID = access.ID
		grant.Cookie = m.previewCookie(label, cookie.Value, expiry)
		if _, e = q.WithContext(ctx).Where(q.ID.Eq(access.ID)).Update(q.CookieExpiresAt, expiry); e != nil {
			return e
		}
		sessions := query.BindPreviewSessions(tx, m.names)
		_, e = sessions.WithContext(ctx).Where(sessions.ID.Eq(row.ID)).Update(sessions.LastAccessAt, now)
		return e
	})
	return grant, err
}
func (m *Module) touchPreview(ctx context.Context, id guid.ID) error {
	db, err := m.writer()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.previewOn(ctx, tx, id, false)
		if e != nil {
			return e
		}
		actorCtx := previewActor(ctx, row.TenantID, row.StartedBy)
		if _, e = m.previewGrantOn(actorCtx, tx, row); e != nil {
			return e
		}
		q := query.BindPreviewSessions(tx, m.names)
		_, e = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.LastAccessAt, time.Now().UTC().Truncate(time.Millisecond))
		return e
	})
}
func (m *Module) expirePreviews(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindPreviewSessions(db, m.names)
	cursor := guid.ID(0)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("READY"), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			cursor = row.ID
			reason := ""
			if !now.Before(row.ExpiresAt) {
				reason = "MAX_LIFETIME"
			} else if now.Sub(row.LastAccessAt) >= 30*time.Minute {
				reason = "IDLE"
			} else {
				e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					_, err := m.previewGrantOn(previewActor(ctx, tenant, row.StartedBy), tx, row)
					return err
				})
				if e != nil {
					switch {
					case errors.Is(e, ErrPreviewAccessDisabled):
						reason = "ACCESS_DISABLED"
					case errors.Is(e, fleet.ErrMachineOffline), errors.Is(e, ErrPreviewUnavailable):
						reason = "MACHINE_OFFLINE"
					case errors.Is(e, ErrConflict), errors.Is(e, ErrFenced):
						reason = "EXECUTION_ENDED"
					case errors.Is(e, ErrForbidden):
						reason = "ACCESS_DISABLED"
					default:
						return e
					}
				}
			}
			if reason == "" {
				continue
			}
			if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				current, e := m.previewOn(ctx, tx, row.ID, true)
				if e != nil {
					return e
				}
				return m.stopPreviewOn(previewActor(ctx, tenant, row.StartedBy), tx, current, reason)
			}); err != nil {
				return err
			}
			if m.preview.runtime != nil && m.preview.runtime.ResetSession != nil {
				if err = m.preview.runtime.ResetSession(ctx, row.ID, reason); err != nil {
					return err
				}
			}
		}
	}
}

type PreviewOfferedPort struct {
	Port           int    `json:"port"`
	ProcessName    string `json:"process_name"`
	OfferedByAgent bool   `json:"offered_by_agent"`
}

func (m *Module) OfferedPreviewPorts(ctx context.Context, tenant, id guid.ID) ([]PreviewOfferedPort, error) {
	out := []PreviewOfferedPort{}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	observed, err := m.previewExecutionGrant(ctx, db, tenant, id, "execution_read")
	if err != nil {
		return nil, err
	}
	ports, err := m.preview.runtime.Ports(ctx, observed)
	if err != nil {
		return nil, err
	}
	current, err := m.previewExecutionGrant(ctx, db, tenant, id, "execution_read")
	if err != nil {
		return nil, err
	}
	if !samePreviewRuntime(current, observed) {
		return nil, ErrFenced
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, ErrInvalidArgument
		}
		out = append(out, PreviewOfferedPort{Port: port})
	}
	return out, nil
}
func (m *Module) stopMachinePreviews(ctx context.Context, tenant, machine guid.ID, reason string) error {
	db, err := m.writer()
	if err != nil {
		return err
	}
	q := query.BindPreviewSessions(db, m.names)
	cursor := guid.ID(0)
	var result error
	for {
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.Eq("READY"), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if e != nil {
			return errors.Join(result, e)
		}
		if len(rows) == 0 {
			return result
		}
		for _, row := range rows {
			cursor = row.ID
			e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				current, readErr := m.previewOn(ctx, tx, row.ID, true)
				if readErr != nil {
					return readErr
				}
				return m.stopPreviewOn(previewActor(ctx, tenant, row.StartedBy), tx, current, reason)
			})
			result = errors.Join(result, e)
			if m.preview.runtime != nil && m.preview.runtime.ResetSession != nil {
				result = errors.Join(result, m.preview.runtime.ResetSession(ctx, row.ID, reason))
			}
		}
	}
}

// revalidatePreview retains the actual browser reader on long-lived streams.
// A preview's opener and a reader can hold different current project grants.
func (m *Module) revalidatePreview(ctx context.Context, grant fleet.PreviewGrant) error {
	if grant.SessionID <= 0 || grant.AccessID <= 0 || grant.PrincipalID <= 0 {
		return ErrForbidden
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindPreviewAccess(tx, m.names)
		access, e := q.WithContext(ctx).Where(q.ID.Eq(grant.AccessID)).First()
		if e != nil {
			return ErrForbidden
		}
		if access.SessionID != grant.SessionID || access.PrincipalID != grant.PrincipalID {
			return ErrForbidden
		}
		row, e := m.previewOn(ctx, tx, grant.SessionID, false)
		if e != nil {
			return e
		}
		current, e := m.previewGrantOn(previewActor(ctx, row.TenantID, access.PrincipalID), tx, row)
		if e != nil {
			return e
		}
		if current.TenantID != grant.TenantID || current.ProjectID != grant.ProjectID || current.ExecutionID != grant.ExecutionID || current.MachineID != grant.MachineID || current.RunnerSessionID != grant.RunnerSessionID || current.ConnEpoch != grant.ConnEpoch || current.DispatchEpoch != grant.DispatchEpoch || current.Port != grant.Port || current.Workspace != grant.Workspace {
			return ErrFenced
		}
		access, e = q.WithContext(ctx).Where(q.ID.Eq(grant.AccessID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
		if e != nil {
			return e
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if access.ConsumedAt == nil || access.CookieExpiresAt == nil || !now.Before(*access.CookieExpiresAt) {
			return ErrForbidden
		}
		expires := now.Add(15 * time.Minute)
		if row.ExpiresAt.Before(expires) {
			expires = row.ExpiresAt
		}
		if _, e = q.WithContext(ctx).Where(q.ID.Eq(access.ID)).Update(q.CookieExpiresAt, expires); e != nil {
			return e
		}
		sessions := query.BindPreviewSessions(tx, m.names)
		_, e = sessions.WithContext(ctx).Where(sessions.ID.Eq(row.ID)).Update(sessions.LastAccessAt, now)
		return e
	})
}
