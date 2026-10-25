package notification

import (
	"context"
	"encoding/json"
	"slices"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) clarificationAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, recipient guid.ID) (bool, error) {
	if m.clarificationFacts == nil || m.intakeSource == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	fact, found, err := m.clarificationFacts(ctx, tx, row.TenantID, id)
	if err != nil || !found || fact.ProjectID != row.ProjectID {
		return false, err
	}
	if row.Type == "intake.clarification_answered" && !fact.Answered {
		return false, nil
	}
	if row.Type == "intake.client_silent" && (fact.State != "EXPIRED" || fact.LateAnswer) {
		return false, nil
	}
	project, channel, found, err := m.intakeSource(ctx, tx, row.TenantID, fact.IntakeID)
	if err != nil || !found || project != row.ProjectID {
		return false, err
	}
	if row.Type == "intake.clarification_answered" && m.clarificationReplySource != nil && m.clarificationHandled != nil {
		reply, found, err := m.clarificationReplySource(ctx, tx, row.TenantID, id, false)
		if err != nil || !found || reply.ProjectID != row.ProjectID {
			return false, err
		}
		handled, err := m.clarificationHandled(ctx, tx, row.TenantID, row.ProjectID, id, reply.ReplyVersion)
		if err != nil || handled {
			return false, err
		}
	}
	if fact.RequesterID == recipient {
		return true, nil
	}
	if row.Type != "intake.client_silent" {
		return false, nil
	}
	if m.intakeTargets == nil {
		return false, ErrUnavailable
	}
	ids, err := m.intakeTargets(ctx, tx, row.TenantID, project, channel)
	return slices.Contains(ids, recipient), err
}

func (m *Module) projectClarificationOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.SubjectID <= 0 || e.ProjectID <= 0 {
		return ErrInvalid
	}
	if m.candidates == nil || m.clarificationFacts == nil {
		return ErrUnavailable
	}
	fact, found, err := m.clarificationFacts(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || fact.ProjectID != e.ProjectID {
		return err
	}
	typ, severity := "intake.clarification_answered", Info
	if e.Type == "clientwork.clarification_expired" {
		typ, severity = "intake.client_silent", Action
	}
	params := map[string]string{"subject_id": e.SubjectID.String()}
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
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Type: typ, Permission: "intake_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			occurrence, group := e.ID, fact.IntakeID
			if typ == "intake.client_silent" {
				occurrence, group = e.SubjectID, e.SubjectID
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: severity, TemplateKey: "notification." + map[string]string{"intake.clarification_answered": "clarification_answered", "intake.client_silent": "client_silent"}[typ], Permission: "intake_read", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/intake", DedupeKey: typ + ":" + group.String()}, occurrence, e.SubjectID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
