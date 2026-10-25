package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func (m *Module) spawnAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if row.Type == "agent.spawn_requested" && row.ID > 0 {
		return m.sourceGroupAudienceOn(ctx, tx, row, actor)
	}
	if m.spawnFacts == nil {
		return false, ErrUnavailable
	}
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	kind := "grant"
	if row.Type == "agent.spawn_requested" {
		kind = "request"
	}
	f, found, err := m.spawnFacts(ctx, tx, row.TenantID, id, kind)
	if err != nil || !found {
		return false, err
	}
	if kind == "request" {
		if f.State != "REQUESTED" && f.State != "UNAVAILABLE" {
			return false, nil
		}
		if len(f.ProjectIDs) == 0 {
			return true, nil
		}
		for _, project := range f.ProjectIDs {
			yes, err := m.projectPermissionOn(ctx, tx, row.TenantID, project, actor, "project_read")
			if err != nil {
				return false, err
			}
			if yes {
				return true, nil
			}
		}
		return false, nil
	}
	if row.Type == "governance.spawn_grant_expiring" {
		return actor == f.GrantorID && f.State == "ACTIVE" && !m.now().Before(f.EndsAt.Add(-72*time.Hour)) && m.now().Before(f.EndsAt), nil
	}
	pct, err := strconv.Atoi(p["pct"])
	if err != nil || (pct != 80 && pct != 100) {
		return false, ErrInvalid
	}
	if f.Spent < f.Cap-f.Cap/5 || (pct == 100 && f.Spent < f.Cap) {
		return false, nil
	}
	if actor == f.GrantorID {
		return true, nil
	}
	return m.projectPermissionOn(ctx, tx, row.TenantID, 0, actor, "project_takeover")
}

func (m *Module) consumeSpawnEventOn(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if !validTransaction(tx) || e.EventID <= 0 || e.TenantID <= 0 || e.Subject.ID <= 0 {
		return ErrInvalid
	}
	if m.spawnFacts == nil || m.candidates == nil {
		return ErrUnavailable
	}
	kind, typ, key := "grant", "governance.spawn_grant_threshold", "spawn_grant_threshold"
	if e.EventType == "governance.spawn_request_changed" {
		kind, typ, key = "request", "agent.spawn_requested", "agent_spawn_requested"
	}
	f, found, err := m.spawnFacts(ctx, tx, e.TenantID, e.Subject.ID, kind)
	if err != nil || !found {
		return err
	}
	if e.EventType == "spawn_grant.granted" {
		return m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: "spawn", SourceID: e.Subject.ID, Ordinal: 1})
	}
	pct := 80
	if kind == "grant" {
		if e.EventType == "spawn_grant.exhausted" {
			pct = 100
		} else if e.EventType != "spawn_grant.budget_threshold" {
			return ErrInvalid
		}
		if f.Spent < f.Cap-f.Cap/5 || (pct == 100 && f.Spent < f.Cap) {
			return ErrInvalid
		}
	}
	params := map[string]string{"subject_id": e.Subject.ID.String(), "grant_id": f.GrantID.String(), "pct": strconv.Itoa(pct), "spent_micros": strconv.FormatInt(f.Spent, 10), "cap_micros": strconv.FormatInt(f.Cap, 10)}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			permission := "project_takeover"
			if id == f.GrantorID {
				permission = "spawn_grant_manage"
			}
			if kind == "request" {
				permission = "agent_manage"
			}
			row := &model.Notification{TenantID: e.TenantID, RecipientPrincipalID: id, Type: typ, Permission: permission, Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			path := "/admin/" + e.TenantID.String() + "/governance/spawn-grants?spawn_grant_id=" + f.GrantID.String()
			severity := Info
			if pct == 100 {
				severity = Action
			}
			dedupe := "spawn_grant:" + f.GrantID.String() + ":" + strconv.Itoa(pct)
			if kind == "request" {
				dedupe = "spawn_req:" + f.GrantID.String()
				severity = Action
				path = "/admin/" + e.TenantID.String() + "/governance/inbox"
			}
			n := Notification{TenantID: e.TenantID, Recipients: []guid.ID{id}, Type: typ, Severity: severity, Permission: permission, TemplateKey: "notification." + key, Params: params, LinkPath: path, DedupeKey: dedupe}
			if kind == "request" {
				err = m.notifySourceOn(ctx, tx, n, e.Subject.ID, e.Subject.ID)
			} else {
				_, err = m.Notify(ctx, tx, n)
			}
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			break
		}
	}
	return nil
}
