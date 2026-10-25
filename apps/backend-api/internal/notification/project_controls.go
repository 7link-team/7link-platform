package notification

import (
	"context"
	"encoding/json"
	"strconv"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) projectControlAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.projectControls.ReadableOn == nil {
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
	var f delivery.ProjectControlFact
	var found bool
	if row.Type == "project.autonomy_lost" || row.Type == "project.autonomy_restored" {
		if m.projectControls.AutonomyOn == nil {
			return false, ErrUnavailable
		}
		version, parseErr := strconv.ParseInt(p["autonomy_version"], 10, 64)
		if parseErr != nil || version <= 0 {
			return false, nil //nolint:nilerr // Legacy or malformed episode markers suppress stale notices; they are not storage failures.
		}
		found, err = m.projectControls.AutonomyOn(ctx, tx, row.TenantID, id, row.Type == "project.autonomy_lost", version)
		if err != nil || !found || id != row.ProjectID {
			return false, err
		}
	} else {
		if row.Type == "project.andon_pulled" {
			if m.projectControls.AndonOn == nil {
				return false, ErrUnavailable
			}
			f, found, err = m.projectControls.AndonOn(ctx, tx, row.TenantID, id)
		} else {
			if m.projectControls.ModeOn == nil {
				return false, ErrUnavailable
			}
			f, found, err = m.projectControls.ModeOn(ctx, tx, row.TenantID, id)
		}
		if err != nil || !found || f.ProjectID != row.ProjectID || f.OccurrenceID.String() != p["occurrence_id"] {
			return false, err
		}
	}
	readable, err := m.projectControls.ReadableOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	if err != nil || !readable {
		return false, err
	}
	lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	if err != nil || lead {
		return lead, err
	}
	return m.templateTeamOwnerOn(ctx, tx, row.TenantID, actor)
}

func (m *Module) projectControlEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if !validTransaction(tx) || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectKind != "project" || e.SubjectID != e.ProjectID || m.candidates == nil {
		return ErrInvalid
	}
	if e.Type == "project.andon_cleared" {
		return nil
	}
	var f delivery.ProjectControlFact
	var found bool
	var err error
	var autonomyVersion int64
	subject := e.SubjectID
	template, severity := "notification.project_andon_pulled", Urgent
	if e.Type == "project.autonomy_lost" || e.Type == "project.autonomy_restored" {
		if m.projectControls.AutonomyOn == nil || e.ID <= 0 {
			return ErrUnavailable
		}
		var data struct {
			Version int64 `json:"autonomy_version"`
		}
		if json.Unmarshal(e.Data, &data) != nil || data.Version <= 0 {
			return nil //nolint:nilerr // Consume legacy or malformed events without projecting an unbound autonomy notice.
		}
		autonomyVersion = data.Version
		found, err = m.projectControls.AutonomyOn(ctx, tx, e.TenantID, e.ProjectID, e.Type == "project.autonomy_lost", autonomyVersion)
		f = delivery.ProjectControlFact{ProjectID: e.ProjectID, OccurrenceID: e.ID}
		template, severity = "notification.project_autonomy_restored", Action
		if e.Type == "project.autonomy_lost" {
			template, severity = "notification.project_autonomy_lost", Urgent
		}
	} else if e.Type == "project.mode_changed" || e.Type == "project.taken_over" {
		var data struct {
			ID         string `json:"mode_change_id"`
			TakeoverID string `json:"takeover_id"`
			Trigger    string `json:"trigger"`
		}
		if json.Unmarshal(e.Data, &data) != nil {
			return ErrInvalid
		}
		if e.Type == "project.mode_changed" && data.Trigger == "TAKEOVER" {
			return nil
		}
		if e.Type == "project.taken_over" {
			data.ID = data.TakeoverID
		}
		subject, err = guid.RequireID(data.ID)
		if err != nil {
			return ErrInvalid
		}
		if m.projectControls.ModeOn == nil {
			return ErrUnavailable
		}
		f, found, err = m.projectControls.ModeOn(ctx, tx, e.TenantID, subject)
		template, severity = "notification.project_mode_changed", Action
	} else {
		if m.projectControls.AndonOn == nil {
			return ErrUnavailable
		}
		f, found, err = m.projectControls.AndonOn(ctx, tx, e.TenantID, subject)
	}
	if err != nil || !found || f.ProjectID != e.ProjectID {
		return err
	}
	params := map[string]string{"subject_id": subject.String(), "occurrence_id": f.OccurrenceID.String(), "mode": f.ToMode}
	if autonomyVersion > 0 {
		params["autonomy_version"] = strconv.FormatInt(autonomyVersion, 10)
	}
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
		for _, actor := range ids {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: e.Type, Permission: "project_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: e.Type, Severity: severity, Permission: "project_read", TemplateKey: template, Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/project", DedupeKey: e.Type + ":" + f.OccurrenceID.String()}, f.OccurrenceID, f.OccurrenceID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
