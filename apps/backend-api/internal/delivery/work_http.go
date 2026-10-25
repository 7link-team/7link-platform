package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformquery "7link.com/platform/pkg/query"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const workPath = "/entities/{entity_id}/work-items"

// Keep creation receipts bounded below the idempotency store's response limit.
// Criteria and the immutable snapshot are fetched from the detail resource.
type workCreatedView struct {
	ID                guid.ID `json:"id"`
	ProjectID         guid.ID `json:"project_id"`
	Number            int     `json:"number"`
	State             string  `json:"state"`
	Version           int     `json:"version"`
	ContractVersionNo int     `json:"contract_version_no"`
	ContentHash       string  `json:"content_hash"`
}

type workCreateInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" doc:"Optional request key for safe retry."`
	Body           workCreateBody
}
type workCreateOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[workCreatedView]
}
type workListInput struct {
	EntityID               string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset                 int    `query:"offset" default:"0" minimum:"0"`
	Limit                  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Title                  string `query:"title" doc:"like:TEXT title filter."`
	Type                   string `query:"type" doc:"eq:TYPE work item type."`
	State                  string `query:"state" doc:"eq:STATE current state."`
	Severity               string `query:"severity" doc:"eq:SEVERITY or in:SEVERITY,SEVERITY for defects."`
	AccountablePrincipalID string `query:"accountable_principal_id" doc:"eq:ID or in:ID,ID current accountable Human."`
	CreatedBy              string `query:"created_by" doc:"eq:ID or in:ID,ID original submitting principal."`
	Sort                   string `query:"sort" doc:"number, title, created_at or updated_at; prefix - for descending."`
	RawQuery               url.Values
}

func (in *workListInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	in.RawQuery = u.Query()
	return nil
}

type workGetInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerWorkHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.create", Method: http.MethodPost, Path: workPath, Summary: "Create and confirm a work item", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.createWorkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.list", Method: http.MethodGet, Path: workPath, Summary: "List work items", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listWorkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.get", Method: http.MethodGet, Path: workPath + "/{work_item_id}", Summary: "Get a work item and confirmed contract", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.getWorkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.prepare", Method: http.MethodPatch, Path: workPath + "/{work_item_id}", Summary: "Update work preparation and check readiness", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.prepareWorkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.check_readiness", Method: http.MethodPost, Path: workPath + "/{work_item_id}/readiness-checks", Summary: "Record a work readiness check", DefaultStatus: 201, Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.checkWorkReadinessHTTP)
	if err := authz.RegisterRequestContract(api, workPath+"/{work_item_id}"); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, workPath)
}

func workHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrWorkVersion) {
		return respx.ErrStatus(ctx, 409, 120419, deliveryi18n.WorkVersionConflict, nil)
	}
	if errors.Is(err, ErrWorkPreparationState) {
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.WorkPreparationState, nil)
	}
	var qe platformquery.Error
	if errors.Is(err, ErrWorkProjectReadOnly) {
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.WorkProjectReadOnly, nil)
	}
	if errors.Is(err, ErrWorkForbidden) {
		return respx.ErrStatus(ctx, 403, 120403, respx.Forbidden, nil)
	}
	if errors.As(err, &qe) {
		return projectHTTPError(ctx, ErrInvalidArgument)
	}
	return projectHTTPError(ctx, err)
}

func (m *Module) createWorkHTTP(ctx context.Context, in *workCreateInput) (*workCreateOutput, error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.idempotency == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	hash, err := idempotency.HashRequest(http.MethodPost, workPath, map[string]string{"entity_id": entity.String()}, in.Body)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: actor, Operation: "work_item.create", Value: in.IdempotencyKey, RequestHash: hash}
	if key.Value != "" {
		if err = key.Validate(); err != nil {
			return nil, workHTTPError(ctx, err)
		}
	}
	execute := func(tx *gorm.DB) (idempotency.Result, error) {
		created, e := m.createWorkOn(ctx, tx, tenant, entity, actor, in.Body)
		if e != nil {
			mapped := workHTTPError(ctx, e)
			var se huma.StatusError
			if !errors.As(mapped, &se) {
				return idempotency.Result{}, mapped
			}
			encoded, encodeErr := respx.MarshalEnvelope(ctx, mapped)
			return idempotency.Result{StatusCode: se.GetStatus(), Body: encoded}, encodeErr
		}
		body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, workCreatedView{ID: created.ID, ProjectID: created.ProjectID, Number: created.Number, State: created.State, Version: created.Version, ContractVersionNo: created.Contract.VersionNo, ContentHash: created.Contract.ContentHash}).Body)
		return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "work_item", ResourceID: created.ID}, e
	}
	var result idempotency.Result
	var replayed bool
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		if key.Value != "" {
			result, replayed, e = m.idempotency.Do(ctx, tx, key, execute)
			return e
		}
		result, e = execute(tx)
		if e != nil {
			return e
		}
		if result.StatusCode >= 400 {
			return rollbackProjectResponse
		}
		return nil
	})
	if err != nil && !errors.Is(err, rollbackProjectResponse) {
		return nil, workHTTPError(ctx, err)
	}
	if result.StatusCode >= 400 {
		failure, e := respx.RestoreError(result.StatusCode, result.Body)
		if e != nil {
			return nil, workHTTPError(ctx, e)
		}
		if replayed {
			return nil, huma.ErrorWithHeaders(failure, http.Header{"Idempotent-Replayed": {"true"}})
		}
		return nil, failure
	}
	var envelope respx.Envelope[workCreatedView]
	if err = json.Unmarshal(result.Body, &envelope); err != nil {
		return nil, workHTTPError(ctx, err)
	}
	out := &workCreateOutput{Status: result.StatusCode, Body: envelope}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}

func (m *Module) listWorkHTTP(ctx context.Context, in *workListInput) (*respx.Body[[]WorkView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	rows := make([]WorkView, 0)
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, e := m.workPolicy(ctx, tx, tenant, entity, "work_item_read", actor)
		if e != nil {
			return e
		}
		w := query.BindWorkItems(tx, m.service.names)
		contract := platformquery.New(
			platformquery.String("title", w.Title).Ops(platformquery.Like),
			platformquery.String("type", w.Type).Ops(platformquery.Eq),
			platformquery.String("state", w.State).Ops(platformquery.Eq),
			platformquery.Enum("severity", w.Severity, []string{"FATAL", "CRITICAL", "NORMAL", "MINOR"}).Ops(platformquery.Eq, platformquery.In),
			platformquery.ID("accountable_principal_id", w.AccountablePrincipalID, projectFilterID).Ops(platformquery.Eq, platformquery.In),
			platformquery.ID("created_by", w.CreatedBy, projectFilterID).Ops(platformquery.Eq, platformquery.In),
		).Sortable(map[string]platformquery.SortKey{"number": {Column: w.Number}, "title": {Column: w.Title}, "created_at": {Column: w.CreatedAt}, "updated_at": {Column: w.UpdatedAt}}).Reserve("offset", "limit", "sort", "lang")
		predicates, e := contract.Parse(in.RawQuery)
		if e != nil {
			return e
		}
		sort := in.Sort
		if sort == "" {
			sort = "-number"
		}
		order, e := contract.Ordering(sort, 3, w.ID.Desc())
		if e != nil {
			return e
		}
		scope, e := workScope(c, entity, w.CreatedBy)
		if e != nil {
			return e
		}
		base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		for _, p := range predicates {
			base = base.Where(p)
		}
		total, e = base.Count()
		if e != nil {
			return e
		}
		found, e := base.Order(order...).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range found {
			rows = append(rows, workRecord(row))
		}
		return nil
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}

func (m *Module) getWorkHTTP(ctx context.Context, in *workGetInput) (*workGetOutput, error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.WorkItemID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	view, err := m.readWork(ctx, m.runtime.Database().Write(), authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return &workGetOutput{ETag: fmtETag(view.Version), Body: respx.OK(ctx, view).Body}, nil
}

func (m *Module) readWork(ctx context.Context, db *gorm.DB, tenant, entity, id, actor guid.ID) (workDetailView, error) {
	var view workDetailView
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, e := m.workPolicy(ctx, tx, tenant, entity, "work_item_read", actor)
		if e != nil {
			return e
		}
		w := query.BindWorkItems(tx, m.service.names)
		scope, e := workScope(c, entity, w.CreatedBy)
		if e != nil {
			return e
		}
		base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.Eq(id), w.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		row, e := base.First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if e != nil {
			return e
		}
		view.WorkView = workRecord(row)
		view.StateKey, e = m.workDisplayKeyOn(ctx, tx, row)
		if e != nil {
			return e
		}
		view.Preparation, e = preparationRecord(row)
		if e != nil {
			return e
		}
		checks := query.BindReadinessChecks(tx, m.service.names)
		latest, e := checks.WithContext(ctx).Where(checks.TenantID.Eq(tenant), checks.ProjectID.Eq(entity), checks.WorkItemID.Eq(id)).Order(checks.CreatedAt.Desc(), checks.ID.Desc()).First()
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil {
			var issues []workReadinessIssue
			if e = json.Unmarshal([]byte(latest.Issues), &issues); e != nil {
				return e
			}
			view.Preparation.LastCheck = &workCheckView{ID: latest.ID, WorkItemID: id, WorkItemVersion: latest.WorkItemVersion, Trigger: latest.Trigger, OK: latest.OK, Issues: issues, CheckedBy: latest.CheckedBy, CreatedAt: latest.CreatedAt}
		}
		view.Criteria = make([]workCriterionView, 0)
		cq := query.BindWorkCriteria(tx, m.service.names)
		criteria, e := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.WorkItemID.Eq(id)).Order(cq.Position).Find()
		if e != nil {
			return e
		}
		for _, criterion := range criteria {
			view.Criteria = append(view.Criteria, workCriterionView{ID: criterion.ID, Position: criterion.Position, Text: criterion.Text, VerificationMethod: criterion.VerificationMethod})
		}
		vq := query.BindWorkContracts(tx, m.service.names)
		contract, e := vq.WithContext(ctx).Where(vq.TenantID.Eq(tenant), vq.ProjectID.Eq(entity), vq.WorkItemID.Eq(id)).Order(vq.VersionNo.Desc()).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		var snapshot workContractSnapshot
		if e = json.Unmarshal([]byte(contract.Snapshot), &snapshot); e != nil {
			return e
		}
		view.Contract = &workContractView{VersionNo: contract.VersionNo, Snapshot: snapshot, ContentHash: contract.ContentHash, ConfirmedAt: contract.ConfirmedAt, ConfirmedBy: contract.ConfirmedBy}
		return nil
	})
	if err != nil {
		return workDetailView{}, err
	}
	return view, nil
}

type workGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[workDetailView]
}
type workPrepareInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted work concurrency version from ETag."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           WorkPreparationBody
}
type workCheckInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted work concurrency version from ETag."`
	IdempotencyKey string `header:"Idempotency-Key"`
}

func (m *Module) prepareWorkHTTP(ctx context.Context, in *workPrepareInput) (*deliveryCommandOutput[workPreparationReceipt], error) {
	entity, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), authn.TenantIDFromContext(ctx), entity, id)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", workHTTPError, "work_item.prepare", http.MethodPatch, workPath+"/{work_item_id}", in.IdempotencyKey, 200, struct {
		Version int                 `json:"version"`
		Body    WorkPreparationBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": entity.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (workPreparationReceipt, guid.ID, error) {
		v, e := m.prepareWorkOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), owner, version, &in.Body)
		return v, id, e
	})
}
func (m *Module) checkWorkReadinessHTTP(ctx context.Context, in *workCheckInput) (*deliveryCommandOutput[workPreparationReceipt], error) {
	entity, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), authn.TenantIDFromContext(ctx), entity, id)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", workHTTPError, "work_item.check_readiness", http.MethodPost, workPath+"/{work_item_id}/readiness-checks", in.IdempotencyKey, 201, struct {
		Version int `json:"version"`
	}{version}, map[string]string{"entity_id": entity.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (workPreparationReceipt, guid.ID, error) {
		v, e := m.prepareWorkOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), owner, version, nil)
		return v, id, e
	})
}
