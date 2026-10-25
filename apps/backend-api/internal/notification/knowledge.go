package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
)

func (m *Module) projectKnowledgeEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	typ := "knowledge.source_failed"
	if e.Type == "knowledge.memory_reviewed" {
		var action string
		_ = json.Unmarshal(data["action"], &action)
		if action != "APPROVED" && action != "REJECTED" {
			return nil
		}
		typ = "memory.reviewed"
	}
	if m.knowledgeAudience == nil || m.candidates == nil {
		return ErrUnavailable
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
			project, yes, err := m.knowledgeAudience(ctx, tx, e.TenantID, e.SubjectID, id, typ)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			template := "notification.memory_reviewed"
			root := "/admin/" + e.TenantID.String()
			if project > 0 {
				root += "/entities/" + project.String()
			}
			path := root + "/governance/memory-review?entry_id=" + e.SubjectID.String()
			if typ == "knowledge.source_failed" {
				template = "notification.knowledge_source_failed"
				path = root + "/knowledge/sources/" + e.SubjectID.String()
			}
			if m.knowledgeLinks != nil {
				path, err = m.knowledgeLinks(ctx, tx, e.TenantID, e.SubjectID, typ)
				if err != nil {
					return err
				}
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: project, Recipients: []guid.ID{id}, Type: typ, Severity: Info, TemplateKey: template, Permission: "knowledge_read", Params: map[string]string{"subject_id": e.SubjectID.String()}, LinkPath: path, DedupeKey: "knowledge:" + e.ID.String()})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
func (m *Module) knowledgeAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, recipient guid.ID) (bool, error) {
	if m.knowledgeAudience == nil {
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
	project, yes, err := m.knowledgeAudience(ctx, tx, row.TenantID, id, recipient, row.Type)
	return yes && project == row.ProjectID, err
}
