package delivery

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gorm"
)

var ErrProjectNotFound = errors.New("delivery: project not found")

type projectClientView struct {
	ID   guid.ID `json:"id"`
	Name string  `json:"name"`
}

type projectDetailView struct {
	ProjectView
	Name   string             `json:"name"`
	Client *projectClientView `json:"client"`
}

type projectGetInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type projectGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[projectDetailView]
}

func (m *Module) getProjectHTTP(ctx context.Context, in *projectGetInput) (*projectGetOutput, error) {
	id, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, projectHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, projectHTTPError(ctx, ErrUnavailable)
	}
	view, err := m.service.read(ctx, m.runtime.Database().Write(), authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, projectHTTPError(ctx, err)
	}
	return &projectGetOutput{ETag: `"` + strconv.Itoa(view.Version) + `"`, Body: respx.OK(ctx, view).Body}, nil
}

func (s *projectService) read(ctx context.Context, db *gorm.DB, tenant, id guid.ID) (projectDetailView, error) {
	var view projectDetailView
	if s == nil || db == nil || s.names.Projects.IsZero() || s.entities.GetOn == nil {
		return view, ErrUnavailable
	}
	if tenant <= 0 || id <= 0 {
		return view, ErrInvalidArgument
	}
	// The IAM name/client and delivery version must describe one snapshot.
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindProjects(tx, s.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if err != nil {
			return err
		}
		entity, err := s.entities.GetOn(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if entity.ID != id || entity.TenantID != tenant || entity.Type != "project" {
			return ErrProjectNotFound
		}
		view.ProjectView, err = projectRecordView(*row)
		if err != nil {
			return err
		}
		view.Name = entity.Name
		if row.ClientEntityID != nil {
			client, err := s.entities.GetOn(ctx, tx, tenant, *row.ClientEntityID)
			if err != nil {
				return err
			}
			if client.ID != *row.ClientEntityID || client.TenantID != tenant || client.Type != "client" {
				return ErrProjectNotFound
			}
			view.Client = &projectClientView{ID: client.ID, Name: client.Name}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return projectDetailView{}, err
	}
	return view, nil
}
