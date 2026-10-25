package clientwork

import (
	"context"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gorm"
)

type ProgressSourceInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	FileID     string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type ProgressSourceCandidate struct {
	delivery.ClientSourceRef
	Name string `json:"name"`
}

func (m *Module) progressSourceHTTP(ctx context.Context, in *ProgressSourceInput) (*respx.Body[ProgressSourceCandidate], error) {
	project, pe := guid.RequireID(in.EntityID)
	work, we := guid.RequireID(in.WorkItemID)
	fileID, fe := guid.RequireID(in.FileID)
	if pe != nil || we != nil || fe != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.files.InspectOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ProgressSourceCandidate
	err := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, project, "project_manage", false); e != nil {
			return e
		}
		file, e := m.files.InspectOn(ctx, tx, fileID)
		if e != nil {
			return e
		}
		ref := delivery.ClientSourceRef{WorkItemID: work, FileID: fileID, VersionID: file.VersionID, SHA256: file.SHA256}
		artifact, _, e := m.projects.SourceArtifactOn(ctx, tx, authn.TenantIDFromContext(ctx), project, ref)
		if e != nil {
			return e
		}
		out = ProgressSourceCandidate{ClientSourceRef: ref, Name: artifact.Name}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
