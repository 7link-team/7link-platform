package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
)

const clarificationPath = entityBase + "/client-clarifications"

func (m *Module) registerClarificationHTTP(api huma.API) error {
	if err := m.registerClarificationDraftHTTP(api); err != nil {
		return err
	}
	owner := authz.ResourceOwnerResolver(m.owner)
	read := authz.Guard{Resource: "channel", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.list", Method: "GET", Path: clarificationPath, Summary: "List available project clarification rounds", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, read, m.listClarificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.get", Method: "GET", Path: clarificationPath + "/{id}", Summary: "Read client-safe clarification questions and answers", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, read, m.getClarificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.attachment", Method: "GET", Path: clarificationPath + "/{id}/answers/{question_no}/attachments/{attachment_id}/content", Summary: "Download an exact attachment with current clarification reader authority", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, read, m.clarificationAttachmentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.request", Method: "POST", Path: clarificationPath, Summary: "Make Intake questions available to a project client member", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "scope", Verb: "change"}, m.requestClarificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.answer", Method: "POST", Path: clarificationPath + "/{id}/answers", Summary: "Answer one outstanding clarification question as its current recipient", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, read, m.answerClarificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.clarification.cancel", Method: "POST", Path: clarificationPath + "/{id}/cancellation", Summary: "Cancel an open clarification round and withdraw outstanding questions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "scope", Verb: "change"}, m.cancelClarificationHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if err := authz.RegisterRequestContract(api, clarificationPath); err != nil {
		return err
	}
	if err := authz.RegisterRequestContract(api, clarificationPath+"/{id}/answers"); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, clarificationPath+"/{id}/cancellation")
}

func (m *Module) cancelClarificationHTTP(ctx context.Context, in *EditInput[ClarificationCancellation]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.clarification.cancel", "POST", clarificationPath+"/"+id.String()+"/cancellation", in.Key, 200, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.cancelClarificationOn(ctx, tx, p, id, v, in.Body.Reason) })
}

func (m *Module) requestClarificationHTTP(ctx context.Context, in *CreateInput[ClarificationRequest]) (*CommandOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.clarification.request", "POST", clarificationPath, in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.requestClarificationOn(ctx, tx, p, in.Body) })
}

func (m *Module) answerClarificationHTTP(ctx context.Context, in *EditInput[ClarificationAnswer]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	check := func(tx *gorm.DB) error {
		external, err := m.authorize(ctx, tx, p, "channel_read", true)
		if err != nil {
			return err
		}
		if !external {
			return ErrForbidden
		}
		_, err = m.loadClarificationOn(ctx, tx, p, id, true)
		return err
	}
	return m.command(ctx, p, "clientwork.clarification.answer", "POST", clarificationPath+"/"+id.String()+"/answers", in.Key, 201, in, "channel_read", true, check, func(tx *gorm.DB) (Receipt, error) { return m.answerClarificationOn(ctx, tx, p, id, v, in.Body) })
}

func (m *Module) listClarificationHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ClarificationView]], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[ClarificationView]{Items: []ClarificationView{}}
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, err := m.authorize(ctx, tx, p, "channel_read", true)
		if err != nil {
			return err
		}
		scope := cursorScope(ctx, p, "clarifications:"+strconv.FormatBool(external))
		before, err := position(in.Cursor, scope)
		if err != nil {
			return err
		}
		q := query.BindClarificationRound(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if external {
			r = r.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)))
		}
		if before > 0 {
			r = r.Where(q.ID.Lt(before))
		}
		rows, err := r.Order(q.ID.Desc()).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		if len(rows) > in.Limit {
			rows = rows[:in.Limit]
			out.HasMore = true
			out.NextCursor = cursor(scope, rows[len(rows)-1].ID)
		}
		for _, row := range rows {
			view, err := clarificationView(row)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, view)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) getClarificationHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[ClarificationView], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ClarificationView
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, err := m.authorize(ctx, tx, p, "channel_read", true)
		if err != nil {
			return err
		}
		row, err := m.loadClarificationOn(ctx, tx, p, id, external)
		if err != nil {
			return err
		}
		out, err = clarificationView(row)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
