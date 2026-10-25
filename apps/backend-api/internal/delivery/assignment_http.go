package delivery

import (
	"context"
	"errors"
	"net/http"
	"slices"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const workAssignmentsPath = "/work-items/{work_item_id}/assignments"
const assignmentPath = "/assignments/{assignment_id}"

type assignmentCreateInput struct {
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted work item concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           assignmentCreateBody
}
type assignmentGetInput struct {
	AssignmentID string `path:"assignment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type assignmentReleaseInput struct {
	AssignmentID   string `path:"assignment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted assignment concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           *assignmentReleaseBody
}
type assignmentReleaseBody struct {
	SeenSeq *int64 `json:"seen_seq,omitempty" minimum:"0"`
}
type assignmentLeaseInput struct {
	AssignmentID   string `path:"assignment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted assignment concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           assignmentLeaseBody
}
type assignmentListInput struct {
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset     int    `query:"offset" default:"0" minimum:"0"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Status     string `query:"status" enum:"ACTIVE,RELEASED,EXPIRED,REVOKED,COMPLETED"`
	Role       string `query:"role" enum:"EXECUTOR,REVIEWER"`
}
type assignmentGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[AssignmentView]
}

func assignmentHTTPError(ctx context.Context, err error) error {
	var interrupted *WorkWriteInterrupted
	if errors.As(err, &interrupted) {
		return respx.ErrStatus(ctx, 409, 120469, "delivery.write_interrupted", interrupted)
	}
	switch {
	case errors.Is(err, ErrWorkSeenSeqRequired):
		return respx.ErrStatus(ctx, 422, 120470, "delivery.seen_seq_required", nil)
	case errors.Is(err, ErrAssignmentState):
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.AssignmentState, nil)
	case errors.Is(err, ErrAssignmentHeld):
		return respx.ErrStatus(ctx, 409, 120426, deliveryi18n.AssignmentHeld, nil)
	case errors.Is(err, ErrAssignmentHolder):
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.AssignmentHolder, map[string]string{"reason_code": "NOT_LEASE_HOLDER"})
	case errors.Is(err, ErrAssignmentReadiness):
		return respx.ErrStatus(ctx, 409, 120461, deliveryi18n.AssignmentReadiness, nil)
	default:
		return workHTTPError(ctx, err)
	}
}

func (m *Module) registerAssignmentHTTP(api huma.API) error {
	workOwner := authz.ResourceOwnerResolver(m.workOwner)
	owner := authz.ResourceOwnerResolver(m.assignmentOwner)
	read := authz.Guard{Resource: "work_item", Verb: "read"}
	execute := authz.Guard{Resource: "work_item", Verb: "execute"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "assignment.create", Method: http.MethodPost, Path: workAssignmentsPath, Summary: "Claim a work item as the current Human", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "work_item_id", workOwner, execute, m.createAssignmentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "assignment.list", Method: http.MethodGet, Path: workAssignmentsPath, Summary: "List work assignment history", Tags: []string{"work-items"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "work_item_id", workOwner, read, m.listAssignmentsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "assignment.get", Method: http.MethodGet, Path: assignmentPath, Summary: "Read an assignment and lease", Tags: []string{"assignments"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "assignment_id", owner, read, m.getAssignmentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "assignment.release", Method: http.MethodPost, Path: assignmentPath + "/release", Summary: "Release the current Human executor assignment", Tags: []string{"assignments"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "assignment_id", owner, execute, m.releaseAssignmentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "assignment.renew", Method: http.MethodPut, Path: assignmentPath + "/lease", Summary: "Renew the current Human executor lease", Tags: []string{"assignments"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "assignment_id", owner, execute, m.renewAssignmentHTTP)
	for _, path := range []string{workAssignmentsPath, assignmentPath + "/lease", assignmentPath + "/release"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) workOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if m == nil || m.service == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	if id <= 0 {
		return authz.ResourceOwner{}, false, ErrInvalidArgument
	}
	q := query.BindWorkItems(m.runtime.Database().Write(), m.service.names)
	row, err := q.WithContext(ctx).Select(q.TenantID, q.ProjectID).Where(q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}
func (m *Module) assignmentOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if m == nil || m.service == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	if id <= 0 {
		return authz.ResourceOwner{}, false, ErrInvalidArgument
	}
	q := query.BindAssignments(m.runtime.Database().Write(), m.service.names)
	row, err := q.WithContext(ctx).Select(q.TenantID, q.ProjectID).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}

func (m *Module) createAssignmentHTTP(ctx context.Context, in *assignmentCreateInput) (*deliveryCommandOutput[assignmentReceipt], error) {
	entity, id, version, err := deliveryEditIDs(authn.EntityIDFromContext(ctx).String(), in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil, assignmentHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, entity, id)
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	request := struct {
		Version int                  `json:"version"`
		Body    assignmentCreateBody `json:"body"`
	}{version, in.Body}
	request.Body.SeenSeq = nil
	return deliveryCommand(ctx, m, "assignment", assignmentHTTPError, "assignment.create", http.MethodPost, workAssignmentsPath, in.IdempotencyKey, 201, request, map[string]string{"work_item_id": id.String()}, func(tx *gorm.DB) (assignmentReceipt, guid.ID, error) {
		v, e := m.claimHumanWorkOn(ctx, tx, tenant, entity, id, actor, owner, version, in.Body)
		return v, v.Assignment.ID, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, entity, id, actor, owner, "work_item_execute")
		return e
	})
}
func (m *Module) releaseAssignmentHTTP(ctx context.Context, in *assignmentReleaseInput) (*deliveryCommandOutput[assignmentReceipt], error) {
	var seen *int64
	if in.Body != nil {
		seen = in.Body.SeenSeq
	}
	return m.assignmentChangeHTTP(ctx, in.AssignmentID, in.IfMatch, in.IdempotencyKey, nil, seen)
}
func (m *Module) renewAssignmentHTTP(ctx context.Context, in *assignmentLeaseInput) (*deliveryCommandOutput[assignmentReceipt], error) {
	return m.assignmentChangeHTTP(ctx, in.AssignmentID, in.IfMatch, in.IdempotencyKey, &in.Body, nil)
}
func (m *Module) assignmentChangeHTTP(ctx context.Context, raw, match, key string, lease *assignmentLeaseBody, seen *int64) (*deliveryCommandOutput[assignmentReceipt], error) {
	entity, id, version, err := deliveryEditIDs(authn.EntityIDFromContext(ctx).String(), raw, match)
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, assignmentHTTPError(ctx, ErrUnavailable)
	}
	db := m.runtime.Database().Write()
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	q := query.BindAssignments(db, m.service.names)
	row, err := q.WithContext(ctx).Select(q.WorkItemID).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrProjectNotFound
	}
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	owner, err := m.preparationOwner(ctx, db, tenant, entity, row.WorkItemID)
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	action, method, suffix := "assignment.release", http.MethodPost, "/release"
	if lease != nil {
		action, method, suffix = "assignment.renew", http.MethodPut, "/lease"
	}
	request := struct {
		Version int                  `json:"version"`
		Lease   *assignmentLeaseBody `json:"lease,omitempty"`
		SeenSeq *int64               `json:"seen_seq,omitempty"`
	}{version, lease, seen}
	return deliveryCommand(ctx, m, "assignment", assignmentHTTPError, action, method, assignmentPath+suffix, key, 200, request, map[string]string{"assignment_id": id.String()}, func(tx *gorm.DB) (assignmentReceipt, guid.ID, error) {
		v, e := m.changeHumanAssignmentOn(ctx, tx, tenant, entity, row.WorkItemID, id, actor, owner, version, lease, seen)
		return v, id, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, entity, row.WorkItemID, actor, owner, "work_item_execute")
		return e
	})
}
func (m *Module) getAssignmentHTTP(ctx context.Context, in *assignmentGetInput) (*assignmentGetOutput, error) {
	id, err := guid.RequireID(in.AssignmentID)
	if err != nil {
		return nil, assignmentHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, assignmentHTTPError(ctx, ErrUnavailable)
	}
	tenant, entity, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	var view AssignmentView
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindAssignments(tx, m.service.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if e != nil {
			return e
		}
		if _, e = m.readWork(ctx, tx, tenant, entity, row.WorkItemID, actor); e != nil {
			return e
		}
		view = assignmentRecord(row)
		return nil
	})
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	return &assignmentGetOutput{ETag: fmtETag(view.Version), Body: respx.OK(ctx, view).Body}, nil
}
func (m *Module) listAssignmentsHTTP(ctx context.Context, in *assignmentListInput) (*respx.Body[[]AssignmentView], error) {
	id, err := guid.RequireID(in.WorkItemID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 100 || (in.Status != "" && !slices.Contains([]string{"ACTIVE", "RELEASED", "EXPIRED", "REVOKED", "COMPLETED"}, in.Status)) || (in.Role != "" && !slices.Contains([]string{"EXECUTOR", "REVIEWER"}, in.Role)) {
		return nil, assignmentHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, assignmentHTTPError(ctx, ErrUnavailable)
	}
	tenant, entity, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	items := make([]AssignmentView, 0)
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.readWork(ctx, tx, tenant, entity, id, actor); e != nil {
			return e
		}
		q := query.BindAssignments(tx, m.service.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.WorkItemID.Eq(id))
		if in.Status != "" {
			base = base.Where(q.Status.Eq(in.Status))
		}
		if in.Role != "" {
			base = base.Where(q.Role.Eq(in.Role))
		}
		var e error
		total, e = base.Count()
		if e != nil {
			return e
		}
		rows, e := base.Order(q.CreatedAt.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			items = append(items, assignmentRecord(row))
		}
		return nil
	})
	if err != nil {
		return nil, assignmentHTTPError(ctx, err)
	}
	return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
}
