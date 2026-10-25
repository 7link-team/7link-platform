package execution

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type visualReviewInput struct {
	ExecutionInput
	Body VisualReview
}

type visualReviewsInput struct {
	ExecutionInput
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

func (m *Module) registerVisualReviewHTTP(api huma.API) error {
	path := "/executions/{execution_id}/visual-reviews"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "execution.visual.reviews", Method: http.MethodGet, Path: path, Summary: "Read captured visual evidence and recorded reviewer decisions", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, m.visualReviewsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "execution.visual.review", Method: http.MethodPost, Path: path, Summary: "Record an authorized judgement of captured visual evidence", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "execution_id", m.owner, authz.Guard{Resource: "verification", Verb: "run", FilterData: true}, m.visualReviewHTTP)
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) visualReviewsHTTP(ctx context.Context, in *visualReviewsInput) (*respx.Body[VisualReviewsPage], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var cursor guid.ID
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out VisualReviewsPage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.listVisualReviewsOn(ctx, tx, authn.TenantIDFromContext(ctx), id, cursor, in.Limit)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) visualReviewHTTP(ctx context.Context, in *visualReviewInput) (*respx.Body[VisualReviewDecision], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out VisualReviewDecision
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.RecordVisualReviewOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
