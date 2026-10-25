package notification

import (
	"7link.com/platform/internal/infra/guid"
	notificationi18n "7link.com/platform/internal/notification/i18n"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
)

type principalInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type inboxInput struct {
	principalInput
	Tenant string `query:"tenant_id"`
	Offset int    `query:"offset" default:"0" minimum:"0" maximum:"1000000"`
	Read   string `query:"read" enum:",unread,read"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type summaryInput struct {
	principalInput
	Tenant string `query:"tenant_id"`
}
type patchInput struct {
	NotificationID string `path:"notification_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body           struct {
		Read     *bool `json:"read,omitempty"`
		Archived *bool `json:"archived,omitempty"`
	}
}
type readsInput struct {
	principalInput
	Body struct {
		TenantID guid.ID `json:"tenant_id"`
		UpToID   guid.ID `json:"up_to_id"`
	}
}
type markedView struct {
	Marked int `json:"marked"`
}
type preferenceInput struct {
	principalInput
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	TypeKey  string `path:"type_key" maxLength:"96"`
}
type preferenceWriteInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	TenantID    string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	TypeKey     string `path:"type_key" maxLength:"96"`
	IfMatch     string `header:"If-Match"`
	Body        PreferenceView
}

type platformPreferenceInput struct {
	principalInput
	TypeKey string `path:"type_key" maxLength:"96"`
}

type platformPreferenceWriteInput struct {
	principalInput
	TypeKey string `path:"type_key" maxLength:"96"`
	IfMatch string `header:"If-Match"`
	Body    PreferenceView
}

func httpError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrInvalid):
		return respx.ErrStatus(ctx, 400, 174400, notificationi18n.Invalid, nil)
	case errors.Is(err, ErrNotFound):
		return respx.ErrStatus(ctx, 404, 174404, notificationi18n.NotFound, nil)
	case errors.Is(err, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 174403, notificationi18n.Forbidden, nil)
	case errors.Is(err, ErrConflict):
		return respx.ErrStatus(ctx, 409, 174409, notificationi18n.Conflict, nil)
	default:
		return respx.ErrStatus(ctx, 503, 174503, notificationi18n.Unavailable, nil)
	}
}
func operation(id, method, path, summary, tag string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{tag}, Errors: []int{400, 401, 403, 404, 409, 422, 428, 503}}
}
func (m *Module) RegisterHTTP(ctx context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return ErrUnavailable
	}
	if _, err := m.writer(); err != nil {
		slog.WarnContext(ctx, "notification routes unavailable; configure a writable database")
	}
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.list", http.MethodGet, "/principals/{principal_id}/notifications", "List own notifications", "principals"), m.listHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.summary", http.MethodGet, "/principals/{principal_id}/notification-summary", "Count own unread notifications", "principals"), m.summaryHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.reads.mark", http.MethodPost, "/principals/{principal_id}/notification-reads", "Mark own notifications read through an identifier", "principals"), m.readsHTTP)
	authz.RegisterAuthenticated(m.registrar, api, operation("notification.patch", http.MethodPatch, "/notifications/{notification_id}", "Mark or archive an owned notification", "notifications"), m.patchHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.preference.get", http.MethodGet, "/principals/{principal_id}/tenants/{tenant_id}/notification-preferences/{type_key}", "Read own notification preferences", "principals"), m.preferenceHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.platform-preference.get", http.MethodGet, "/principals/{principal_id}/platform-notification-preferences/{type_key}", "Read own platform notification preferences", "principals"), m.platformPreferenceHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.platform-preference.put", http.MethodPut, "/principals/{principal_id}/platform-notification-preferences/{type_key}", "Save own platform notification preferences", "principals"), m.platformPreferenceWriteHTTP)
	authz.RegisterPrincipalAuthenticated(m.registrar, api, operation("notification.preference.put", http.MethodPut, "/principals/{principal_id}/tenants/{tenant_id}/notification-preferences/{type_key}", "Save own notification preferences", "principals"), m.preferenceWriteHTTP)
	return nil
}
func optionalID(s string) (guid.ID, error) {
	if s == "" {
		return 0, nil
	}
	id, err := guid.RequireID(s)
	if err != nil {
		return 0, ErrInvalid
	}
	return id, nil
}
func (m *Module) listHTTP(ctx context.Context, in *inboxInput) (*respx.Body[[]NotificationView], error) {
	tenant, err := optionalID(in.Tenant)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := []NotificationView{}
	total := 0
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := m.inboxRows(ctx, tx, authn.PrincipalIDFromContext(ctx), tenant, 0, in.Read, math.MaxInt)
		if err != nil {
			return err
		}
		total = len(rows)
		start := min(in.Offset, total)
		end := min(start+in.Limit, total)
		for _, r := range rows[start:end] {
			v, err := view(ctx, r)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.List(ctx, out, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(out), Total: int64(total)}), nil
}
func (m *Module) summaryHTTP(ctx context.Context, in *summaryInput) (*respx.Body[NotificationSummary], error) {
	tenant, err := optionalID(in.Tenant)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out NotificationSummary
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.summaryOn(ctx, tx, authn.PrincipalIDFromContext(ctx), tenant)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) patchHTTP(ctx context.Context, in *patchInput) (*respx.Body[NotificationView], error) {
	ctx, changes := collectInboxChanges(ctx)
	id, err := guid.RequireID(in.NotificationID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out NotificationView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.patchOn(ctx, tx, authn.PrincipalIDFromContext(ctx), id, in.Body.Read, in.Body.Archived)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	m.broadcastInboxChanges(ctx, changes)
	return respx.OK(ctx, out), nil
}
func (m *Module) readsHTTP(ctx context.Context, in *readsInput) (*respx.Body[markedView], error) {
	ctx, changes := collectInboxChanges(ctx)
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out markedView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out.Marked, e = m.markReadOn(ctx, tx, authn.PrincipalIDFromContext(ctx), in.Body.TenantID, in.Body.UpToID)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	m.broadcastInboxChanges(ctx, changes)
	return respx.OK(ctx, out), nil
}
func (m *Module) preferenceHTTP(ctx context.Context, in *preferenceInput) (*respx.Body[PreferenceView], error) {
	tenant, err := preferenceTenantID(in.TenantID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out PreferenceView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.preferenceOn(ctx, tx, authn.PrincipalIDFromContext(ctx), tenant, in.TypeKey)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) preferenceWriteHTTP(ctx context.Context, in *preferenceWriteInput) (*respx.Body[PreferenceView], error) {
	if in.IfMatch == "" {
		return nil, respx.ErrStatus(ctx, 428, 174428, notificationi18n.Conflict, nil)
	}
	tenant, err := preferenceTenantID(in.TenantID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	version, err := strconv.ParseInt(strings.Trim(in.IfMatch, "\""), 10, 64)
	if err != nil || version < 0 || version == math.MaxInt64 {
		return nil, httpError(ctx, ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out PreferenceView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.savePreferenceOn(ctx, tx, authn.PrincipalIDFromContext(ctx), tenant, in.TypeKey, in.Body, version)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func preferenceTenantID(value string) (guid.ID, error) {
	if value == "0" {
		return 0, nil
	}
	return guid.RequireID(value)
}

func (m *Module) platformPreferenceHTTP(ctx context.Context, in *platformPreferenceInput) (*respx.Body[PreferenceView], error) {
	return m.preferenceHTTP(ctx, &preferenceInput{principalInput: in.principalInput, TenantID: "0", TypeKey: in.TypeKey})
}

func (m *Module) platformPreferenceWriteHTTP(ctx context.Context, in *platformPreferenceWriteInput) (*respx.Body[PreferenceView], error) {
	return m.preferenceWriteHTTP(ctx, &preferenceWriteInput{PrincipalID: in.PrincipalID, TenantID: "0", TypeKey: in.TypeKey, IfMatch: in.IfMatch, Body: in.Body})
}
