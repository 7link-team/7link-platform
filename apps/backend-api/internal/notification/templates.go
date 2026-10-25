package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
)

func (m *Module) projectTemplatePublicationOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if m.templatePublications == nil || m.candidates == nil {
		return ErrUnavailable
	}
	projects, err := m.templatePublications(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil {
		return err
	}
	for _, project := range projects {
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
				lead, err := m.leadOn(ctx, tx, e.TenantID, project, id)
				if err != nil {
					return err
				}
				if !lead {
					continue
				}
				_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: project, Recipients: []guid.ID{id}, Type: "template.version_published", Severity: Info, TemplateKey: "notification.template_version_published", Permission: "project_read", Params: map[string]string{"subject_id": e.SubjectID.String()}, LinkPath: "/admin/" + e.TenantID.String() + "/templates/" + e.SubjectID.String() + "/workflow", DedupeKey: "template-published:" + project.String() + ":" + e.SubjectID.String()})
				if err != nil {
					return err
				}
			}
			if len(ids) < 100 {
				break
			}
		}
	}
	return nil
}
func (m *Module) templatePublicationAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, recipient guid.ID) (bool, error) {
	if m.templatePublications == nil {
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
	projects, err := m.templatePublications(ctx, tx, row.TenantID, id)
	if err != nil || !slices.Contains(projects, row.ProjectID) {
		return false, err
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, recipient)
}
