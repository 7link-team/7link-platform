package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

const requirementPath = "/entities/{entity_id}/requirements"

type requirementBaselineReceipt struct {
	SuiteHash   string `json:"suite_hash"`
	VersionNo   int    `json:"version_no"`
	ContentHash string `json:"content_hash"`
}
type requirementBaselineView struct {
	Requirement RequirementView            `json:"requirement"`
	Version     requirementBaselineReceipt `json:"version"`
}
type requirementCreateInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           requirementCreateBody
}

// RequirementGetInput is also embedded by requirement code-link operations.
type RequirementGetInput struct {
	EntityID      string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type requirementBaselineInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID  string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted concurrency version from the requirement ETag."`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           struct{}
}
type requirementListInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Title    string `query:"title" doc:"like:TEXT title filter."`
	Status   string `query:"status" doc:"eq:STATUS or in:STATUS,STATUS."`
	Priority string `query:"priority" doc:"eq:N, in:N,N or lte:N."`
	Sort     string `query:"sort" doc:"number, priority, created_at, updated_at; prefix - for descending."`
	RawQuery url.Values
}

func (in *requirementListInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	in.RawQuery = u.Query()
	return nil
}

type deliveryCommandOutput[T any] struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[T]
}
type requirementGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[requirementDetailView]
}

type requirementEditInput[T any] struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID  string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           T
}
type criterionPatchInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID  string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	CriterionID    string `path:"criterion_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           criterionPatchBody
}

func (m *Module) registerRequirementHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.create", Method: http.MethodPost, Path: requirementPath, Summary: "Create a draft requirement", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.createRequirementHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.list", Method: http.MethodGet, Path: requirementPath, Summary: "List instance requirements", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.listRequirementsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.get", Method: http.MethodGet, Path: requirementPath + "/{requirement_id}", Summary: "Get requirement criteria and immutable baselines", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.getRequirementHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.baseline", Method: http.MethodPost, Path: requirementPath + "/{requirement_id}/versions", Summary: "Confirm the initial immutable requirement baseline", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.baselineRequirementHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement.update", Method: http.MethodPatch, Path: requirementPath + "/{requirement_id}", Summary: "Edit draft requirement metadata", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.patchRequirementHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_criterion.create", Method: http.MethodPost, Path: requirementPath + "/{requirement_id}/acceptance-criteria", Summary: "Add a draft acceptance criterion", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.createCriterionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_criterion.update", Method: http.MethodPatch, Path: requirementPath + "/{requirement_id}/acceptance-criteria/{criterion_id}", Summary: "Edit or archive a draft acceptance criterion", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "write"}, m.patchCriterionHTTP)
	for _, path := range []string{requirementPath, requirementPath + "/{requirement_id}", requirementPath + "/{requirement_id}/acceptance-criteria", requirementPath + "/{requirement_id}/acceptance-criteria/{criterion_id}"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func requirementHTTPError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrRequirementSuiteMissing):
		return respx.ErrStatus(ctx, 422, 120439, deliveryi18n.RequirementSuiteMissing, nil)
	case errors.Is(err, ErrRequirementVersion):
		return respx.ErrStatus(ctx, 409, 120409, deliveryi18n.RequirementVersionConflict, nil)
	case errors.Is(err, ErrRequirementFrozen):
		return respx.ErrStatus(ctx, 422, 120439, deliveryi18n.RequirementFrozen, nil)
	case errors.Is(err, ErrRequirementStage):
		return respx.ErrStatus(ctx, 422, 120420, deliveryi18n.RequirementStage, nil)
	case errors.Is(err, ErrRequirementLimit):
		return respx.ErrStatus(ctx, 422, 120449, deliveryi18n.RequirementLimit, map[string]any{"limit": 500})
	case errors.Is(err, ErrCriterionLimit):
		return respx.ErrStatus(ctx, 422, 120449, deliveryi18n.CriterionLimit, map[string]any{"limit": 50})
	default:
		return workHTTPError(ctx, err)
	}
}

// Both commands store bounded receipts. Their full criteria/snapshots are read
// through the detail resource, never truncated to fit the replay store.
func deliveryCommand[T any](ctx context.Context, m *Module, resource string, mapError func(context.Context, error) error, operation, method, path, keyValue string, status int, request any, params map[string]string, run func(*gorm.DB) (T, guid.ID, error), authorize ...func(*gorm.DB) error) (*deliveryCommandOutput[T], error) {
	mapDomainError := mapError
	mapError = func(ctx context.Context, err error) error {
		var interrupted *WorkWriteInterrupted
		if errors.As(err, &interrupted) || errors.Is(err, ErrWorkSeenSeqRequired) {
			ctx = respx.WithNext(ctx, respx.NextStep{Operation: "inbox_events.list", Reason: "READ_NEWER_EVENTS"}, respx.NextStep{Operation: operation, Reason: "RETRY_WITH_SAME_IDEMPOTENCY_KEY"})
		}
		return mapDomainError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.idempotency == nil {
		return nil, mapError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(method, path, params, request)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	key := idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: keyValue, RequestHash: hash}
	if key.Value != "" {
		if err = key.Validate(); err != nil {
			return nil, mapError(ctx, err)
		}
	}
	execute := func(tx *gorm.DB) (idempotency.Result, error) {
		view, id, e := run(tx)
		if e != nil {
			var interrupted *WorkWriteInterrupted
			if errors.As(e, &interrupted) || errors.Is(e, ErrWorkSeenSeqRequired) {
				return idempotency.Result{}, e
			}
			mapped := mapError(ctx, e)
			var se huma.StatusError
			if !errors.As(mapped, &se) {
				return idempotency.Result{}, mapped
			}
			encoded, encodeErr := respx.MarshalEnvelope(ctx, mapped)
			return idempotency.Result{StatusCode: se.GetStatus(), Body: encoded}, encodeErr
		}
		body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, view).Body)
		return idempotency.Result{StatusCode: status, Body: body, ResourceKind: resource, ResourceID: id}, e
	}
	var result idempotency.Result
	var replayed bool
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		for _, check := range authorize {
			if e = check(tx); e != nil {
				return e
			}
		}
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
		return nil, mapError(ctx, err)
	}
	if result.StatusCode >= 400 {
		failure, e := respx.RestoreError(result.StatusCode, result.Body)
		if e != nil {
			return nil, mapError(ctx, e)
		}
		if replayed {
			return nil, huma.ErrorWithHeaders(failure, http.Header{"Idempotent-Replayed": {"true"}})
		}
		return nil, failure
	}
	var envelope respx.Envelope[T]
	if err = json.Unmarshal(result.Body, &envelope); err != nil {
		return nil, mapError(ctx, err)
	}
	out := &deliveryCommandOutput[T]{Status: result.StatusCode, Body: envelope}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createRequirementHTTP(ctx context.Context, in *requirementCreateInput) (*deliveryCommandOutput[RequirementView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement.create", http.MethodPost, requirementPath, in.IdempotencyKey, 201, in.Body, map[string]string{"entity_id": entity.String()}, func(tx *gorm.DB) (RequirementView, guid.ID, error) {
		v, e := m.createRequirementOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, authn.PrincipalIDFromContext(ctx), in.Body)
		return v, v.ID, e
	})
}
func (m *Module) baselineRequirementHTTP(ctx context.Context, in *requirementBaselineInput) (*deliveryCommandOutput[requirementBaselineView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.RequirementID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	expected, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || expected < 1 || in.IfMatch != fmtETag(expected) {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement.baseline", http.MethodPost, requirementPath+"/{requirement_id}/versions", in.IdempotencyKey, 201, struct {
		Version int `json:"version"`
	}{expected}, map[string]string{"entity_id": entity.String(), "requirement_id": id.String()}, func(tx *gorm.DB) (requirementBaselineView, guid.ID, error) {
		v, e := m.baselineRequirementOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), expected)
		return v, id, e
	})
}
func fmtETag(version int) string { return "\"" + strconv.Itoa(version) + "\"" }

func deliveryEditIDs(entityRaw, idRaw, match string) (guid.ID, guid.ID, int, error) {
	entity, err := guid.RequireID(entityRaw)
	if err != nil {
		return 0, 0, 0, ErrInvalidArgument
	}
	id, err := guid.RequireID(idRaw)
	if err != nil {
		return 0, 0, 0, ErrInvalidArgument
	}
	version, err := strconv.Atoi(strings.Trim(match, "\""))
	if err != nil || version < 1 || match != fmtETag(version) {
		return 0, 0, 0, ErrInvalidArgument
	}
	return entity, id, version, nil
}
func (m *Module) patchRequirementHTTP(ctx context.Context, in *requirementEditInput[requirementPatchBody]) (*deliveryCommandOutput[RequirementView], error) {
	entity, id, version, err := deliveryEditIDs(in.EntityID, in.RequirementID, in.IfMatch)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement.update", http.MethodPatch, requirementPath+"/{requirement_id}", in.IdempotencyKey, 200, struct {
		Version int                  `json:"version"`
		Body    requirementPatchBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": entity.String(), "requirement_id": id.String()}, func(tx *gorm.DB) (RequirementView, guid.ID, error) {
		v, e := m.patchRequirementOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), version, in.Body)
		return v, id, e
	})
}
func (m *Module) createCriterionHTTP(ctx context.Context, in *requirementEditInput[criterionCreateBody]) (*deliveryCommandOutput[RequirementView], error) {
	entity, id, version, err := deliveryEditIDs(in.EntityID, in.RequirementID, in.IfMatch)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement_criterion.create", http.MethodPost, requirementPath+"/{requirement_id}/acceptance-criteria", in.IdempotencyKey, 201, struct {
		Version int                 `json:"version"`
		Body    criterionCreateBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": entity.String(), "requirement_id": id.String()}, func(tx *gorm.DB) (RequirementView, guid.ID, error) {
		v, e := m.createCriterionOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx), version, in.Body)
		return v, id, e
	})
}
func (m *Module) patchCriterionHTTP(ctx context.Context, in *criterionPatchInput) (*deliveryCommandOutput[RequirementView], error) {
	entity, id, version, err := deliveryEditIDs(in.EntityID, in.RequirementID, in.IfMatch)
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	cid, err := guid.RequireID(in.CriterionID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "requirement", requirementHTTPError, "requirement_criterion.update", http.MethodPatch, requirementPath+"/{requirement_id}/acceptance-criteria/{criterion_id}", in.IdempotencyKey, 200, struct {
		Version int                `json:"version"`
		Body    criterionPatchBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": entity.String(), "requirement_id": id.String(), "criterion_id": cid.String()}, func(tx *gorm.DB) (RequirementView, guid.ID, error) {
		v, e := m.patchCriterionOn(ctx, tx, authn.TenantIDFromContext(ctx), entity, id, cid, authn.PrincipalIDFromContext(ctx), version, in.Body)
		return v, id, e
	})
}

func (m *Module) listRequirementsHTTP(ctx context.Context, in *requirementListInput) (*respx.Body[[]RequirementView], error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	rows := make([]RequirementView, 0)
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, e := m.workPolicy(ctx, tx, tenant, entity, "requirement_read", actor)
		if e != nil {
			return e
		}
		r := query.BindRequirements(tx, m.service.names)
		contract := platformquery.New(platformquery.String("title", r.Title).Ops(platformquery.Like), platformquery.String("status", r.Status).Ops(platformquery.Eq, platformquery.In), platformquery.Int("priority", r.Priority).Ops(platformquery.Eq, platformquery.In, platformquery.Lte)).Sortable(map[string]platformquery.SortKey{"number": {Column: r.Number}, "priority": {Column: r.Priority}, "created_at": {Column: r.CreatedAt}, "updated_at": {Column: r.UpdatedAt}}).Reserve("offset", "limit", "sort", "lang")
		predicates, e := contract.Parse(in.RawQuery)
		if e != nil {
			return e
		}
		sort := in.Sort
		if sort == "" {
			sort = "priority,number"
		}
		order, e := contract.Ordering(sort, 3, r.ID)
		if e != nil {
			return e
		}
		scope, e := workScope(c, entity, r.CreatedBy)
		if e != nil {
			return e
		}
		base := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ArchivedAt.IsNull())
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
			rows = append(rows, requirementRecord(row))
		}
		return nil
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}
func (m *Module) getRequirementHTTP(ctx context.Context, in *RequirementGetInput) (*requirementGetOutput, error) {
	entity, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.RequirementID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	view, err := m.readRequirement(ctx, m.runtime.Database().Write(), authn.TenantIDFromContext(ctx), entity, id, authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return &requirementGetOutput{ETag: fmtETag(view.Version), Body: respx.OK(ctx, view).Body}, nil
}
func (m *Module) readRequirement(ctx context.Context, db *gorm.DB, tenant, entity, id, actor guid.ID) (requirementDetailView, error) {
	var view requirementDetailView
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, e := m.workPolicy(ctx, tx, tenant, entity, "requirement_read", actor)
		if e != nil {
			return e
		}
		r := query.BindRequirements(tx, m.service.names)
		scope, e := workScope(c, entity, r.CreatedBy)
		if e != nil {
			return e
		}
		base := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id), r.ArchivedAt.IsNull())
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
		sheets := query.BindReviewSheets(tx, m.service.names)
		latest, e := sheets.WithContext(ctx).Where(sheets.TenantID.Eq(tenant), sheets.ProjectID.Eq(entity), sheets.RequirementID.Eq(id), sheets.WorkItemID.IsNull()).Order(sheets.ID.Desc()).Find()
		if e != nil {
			return e
		}
		for _, sheet := range latest {
			v, err := sheetView(sheet)
			if err != nil {
				return err
			}
			if v.Document.Decision != nil {
				view.Decisions = append(view.Decisions, *v.Document.Decision)
			}
			if view.Review == nil && (v.Document.RequirementVersion == row.Version || v.Document.Decision != nil && v.Document.Decision.RequirementVersion == row.Version) {
				view.Review = &requirementReviewSummary{SheetID: sheet.ID, State: sheet.State}
			}
		}
		view.RequirementView = requirementRecord(row)
		if view.Decisions == nil {
			view.Decisions = []RequirementReviewDecision{}
		}
		view.Description = row.Description
		view.Criteria = make([]requirementCriterionView, 0)
		view.Versions = make([]requirementVersionView, 0)
		cq := query.BindCriteria(tx, m.service.names)
		criteria, e := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ProjectID.Eq(entity), cq.RequirementID.Eq(id), cq.ArchivedAt.IsNull(), cq.RetiredInVersion.IsNull()).Order(cq.Position, cq.ID).Find()
		if e != nil {
			return e
		}
		for _, criterion := range criteria {
			view.Criteria = append(view.Criteria, requirementCriterionRecord(criterion))
		}
		vq := query.BindRequirementVersions(tx, m.service.names)
		versions, e := vq.WithContext(ctx).Where(vq.TenantID.Eq(tenant), vq.ProjectID.Eq(entity), vq.RequirementID.Eq(id)).Order(vq.VersionNo.Desc()).Find()
		if e != nil {
			return e
		}
		for _, version := range versions {
			snapshot := requirementSnapshot{Title: version.Title, Description: version.Description, Priority: version.Priority}
			if e = json.Unmarshal([]byte(version.Criteria), &snapshot.Criteria); e != nil {
				return e
			}
			caseCount, err := frozenSuiteCaseCount(version)
			if err != nil {
				return err
			}
			view.Versions = append(view.Versions, requirementVersionView{SuiteHash: version.SuiteHash, SuiteCaseCount: caseCount, VersionNo: version.VersionNo, Snapshot: snapshot, ContentHash: version.ContentHash, CreatedAt: version.CreatedAt, CreatedBy: version.CreatedBy})
		}
		return nil
	})
	return view, err
}
