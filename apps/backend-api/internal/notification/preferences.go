package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

type PreferenceView struct {
	TenantID      guid.ID `json:"tenant_id"`
	TypeKey       string  `json:"type_key" maxLength:"96"`
	InApp         bool    `json:"in_app"`
	BrowserPush   bool    `json:"browser_push"`
	Email         string  `json:"email" enum:"OFF,IMMEDIATE,DAILY_DIGEST"`
	QuietEnabled  bool    `json:"quiet_enabled"`
	QuietStartMin int     `json:"quiet_start_min" minimum:"0" maximum:"1439"`
	QuietEndMin   int     `json:"quiet_end_min" minimum:"0" maximum:"1439"`
	Timezone      string  `json:"timezone" maxLength:"64"`
	DigestHour    int     `json:"digest_hour" minimum:"0" maximum:"23"`
	Version       int64   `json:"version"`
}

func preferenceView(p *model.Preference) PreferenceView {
	return PreferenceView{p.TenantID, p.TypeKey, p.InApp, p.BrowserPush, p.Email, p.QuietEnabled, p.QuietStartMin, p.QuietEndMin, p.Timezone, p.DigestHour, p.Version}
}
func (m *Module) preferenceOn(ctx context.Context, tx *gorm.DB, principal, tenant guid.ID, key string) (PreferenceView, error) {
	member, err := m.recipientScopeOn(ctx, tx, tenant, principal, "")
	if err != nil {
		return PreferenceView{}, err
	}
	if !member {
		return PreferenceView{}, ErrForbidden
	}
	q := query.BindPreferences(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.TypeKey.Eq(key)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PreferenceView{TenantID: tenant, TypeKey: key, InApp: true, Email: defaultEmail(key, Info), QuietStartMin: 1320, QuietEndMin: 480, Timezone: "UTC", DigestHour: 9}, nil
	}
	if err != nil {
		return PreferenceView{}, err
	}
	return preferenceView(r), nil
}
func (m *Module) savePreferenceOn(ctx context.Context, tx *gorm.DB, principal, tenant guid.ID, key string, in PreferenceView, version int64) (PreferenceView, error) {
	if principal <= 0 || tenant < 0 || key != "*" && (!typePattern.MatchString(key) || len(key) > 96) || !in.InApp || in.Email != "OFF" && in.Email != "IMMEDIATE" && in.Email != "DAILY_DIGEST" || in.QuietStartMin < 0 || in.QuietStartMin > 1439 || in.QuietEndMin < 0 || in.QuietEndMin > 1439 || in.DigestHour < 0 || in.DigestHour > 23 || len(in.Timezone) > 64 {
		return PreferenceView{}, ErrInvalid
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return PreferenceView{}, ErrInvalid
	}
	if key != "*" && in.QuietEnabled {
		return PreferenceView{}, ErrInvalid
	}
	if err := m.principal.LockActive(ctx, tx, principal); err != nil {
		return PreferenceView{}, err
	}
	old, err := m.preferenceOn(ctx, tx, principal, tenant, key)
	if err != nil {
		return PreferenceView{}, err
	}
	if old.Version != version {
		return PreferenceView{}, ErrConflict
	}
	q := query.BindPreferences(tx, m.names)
	if old.Version == 0 {
		count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).Count()
		if err != nil {
			return PreferenceView{}, err
		}
		if count >= 100 {
			return PreferenceView{}, ErrInvalid
		}
	}
	row := model.Preference{TenantID: tenant, PrincipalID: principal, TypeKey: key, InApp: true, BrowserPush: in.BrowserPush, Email: in.Email, QuietEnabled: in.QuietEnabled, QuietStartMin: in.QuietStartMin, QuietEndMin: in.QuietEndMin, Timezone: in.Timezone, DigestHour: in.DigestHour, Version: version + 1}
	if old.Version == 0 {
		err = q.WithContext(ctx).Create(&row)
	} else {
		_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.TypeKey.Eq(key), q.Version.Eq(version)).Updates(map[string]any{"in_app": true, "browser_push": row.BrowserPush, "email": row.Email, "quiet_enabled": row.QuietEnabled, "quiet_start_min": row.QuietStartMin, "quiet_end_min": row.QuietEndMin, "timezone": row.Timezone, "digest_hour": row.DigestHour, "version": row.Version})
	}
	if err != nil {
		return PreferenceView{}, err
	}
	return preferenceView(&row), nil
}
