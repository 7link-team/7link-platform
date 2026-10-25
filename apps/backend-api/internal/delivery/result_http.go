package delivery

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const workResultsPath = workPath + "/{work_item_id}/results"
const workResultPath = workResultsPath + "/{result_id}"

type ResultWorkInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type resultListInput struct {
	ResultWorkInput
	Offset int `query:"offset" default:"0" minimum:"0"`
	Limit  int `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type ResultMutationInput struct {
	ResultWorkInput
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted work item concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key"`
}
type ResultVersionInput struct {
	ResultMutationInput
	ResultID string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type resultSubmitInput struct {
	ResultMutationInput
	Body resultSubmitBody
}

// Mutation responses stay bounded so their original receipt can be stored and
// replayed exactly. Full evidence, actions and packages are read through GET.
type resultCommandReceipt struct {
	ID         guid.ID `json:"id"`
	ProjectID  guid.ID `json:"project_id"`
	WorkItemID guid.ID `json:"work_item_id"`
	Sequence   int     `json:"sequence"`
}
type resultCheckInput struct {
	ResultVersionInput
	Body resultCheckBody
}
type resultAcceptInput struct {
	ResultVersionInput
	Body resultAcceptBody
}
type resultHandoffInput struct {
	ResultVersionInput
	Body resultHandoffBody
}
type resultReceiptInput struct {
	ResultVersionInput
	HandoffID string `path:"handoff_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body      resultReceiptBody
}
type resultEvidenceInput struct {
	ResultWorkInput
	ResultID   string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	EvidenceID string `path:"evidence_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type resultCandidateInput struct {
	ResultWorkInput
	FileID string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type resultCandidateView struct {
	SpaceID     guid.ID `json:"space_id"`
	FileID      guid.ID `json:"file_id"`
	VersionID   guid.ID `json:"version_id"`
	FileVersion int64   `json:"file_version"`
	Name        string  `json:"name"`
	SHA256      string  `json:"sha256"`
	MIME        string  `json:"mime"`
	Size        int64   `json:"size"`
}

func resultHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrDefectAgentAccept) {
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.ResultIndependent, map[string]any{"reason_code": "AGENT_CANNOT_ACCEPT"})
	}
	var conversion *commentDefectError
	if errors.As(err, &conversion) {
		return commentFeedbackHTTPError(ctx, conversion)
	}
	switch {
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAdditionalVerification), errors.Is(err, authn.ErrInvalidToken), errors.Is(err, authn.ErrExpiredToken):
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.HumanOnly, map[string]any{"reauthentication_required": true, "purpose": "verification_override"})
	case errors.Is(err, files.ErrPinnedForbidden):
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.ResultEvidenceForbidden, nil)
	case errors.Is(err, files.ErrPinnedConflict):
		return workHTTPError(ctx, ErrWorkVersion)
	case errors.Is(err, files.ErrPinnedInvalid):
		return workHTTPError(ctx, ErrInvalidArgument)
	case errors.Is(err, files.ErrPinnedUnavailable):
		return respx.ErrStatus(ctx, 503, 120503, deliveryi18n.ProvisioningUnavailable, nil)
	case errors.Is(err, files.ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return workHTTPError(ctx, ErrProjectNotFound)
	case errors.Is(err, ErrUpstreamStale):
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.ResultState, map[string]string{"reason_code": "UPSTREAM_STALE"})
	case errors.Is(err, ErrAndonBlocked):
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.ResultState, map[string]string{"reason_code": "ANDON_UPSTREAM"})
	case errors.Is(err, ErrResultState):
		return respx.ErrStatus(ctx, 409, 120420, deliveryi18n.ResultState, nil)
	case errors.Is(err, ErrResultIndependent):
		return respx.ErrStatus(ctx, 403, 120403, deliveryi18n.ResultIndependent, nil)
	case errors.Is(err, ErrVerificationPending):
		return respx.ErrStatus(ctx, 409, 120438, deliveryi18n.VerificationPending, nil)
	case errors.Is(err, ErrResultCoverage):
		return respx.ErrStatus(ctx, 409, 120462, deliveryi18n.ResultCoverage, nil)
	default:
		return assignmentHTTPError(ctx, err)
	}
}

func (m *Module) registerResultHTTP(api huma.API) error {
	if err := m.registerVerificationHTTP(api); err != nil {
		return err
	}
	owner := authz.ResourceOwnerResolver(m.workOwner)
	read := authz.Guard{Resource: "work_item", Verb: "read"}
	execute := authz.Guard{Resource: "work_item", Verb: "execute"}
	write := authz.Guard{Resource: "work_item", Verb: "write"}
	// Governance performs the decision-specific, direct-Human authority checks
	// inside the command transaction, in addition to the route's work visibility.
	errors := []int{400, 401, 403, 404, 409, 422, 500, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.list", Method: http.MethodGet, Path: workResultsPath, Summary: "List immutable Human result versions", Tags: []string{"work-items"}, Errors: errors}, "work_item_id", owner, read, m.listResultsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.submit", Method: http.MethodPost, Path: workResultsPath, Summary: "Submit a fixed Human result with exact-version evidence", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, execute, m.submitResultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.check", Method: http.MethodPost, Path: workResultPath + "/checks", Summary: "Record an independent Human check of one exact result", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, write, m.checkResultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.accept", Method: http.MethodPost, Path: workResultPath + "/acceptance", Summary: "Accept the current exact result version", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, read, m.acceptResultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.handoff", Method: http.MethodPost, Path: workResultPath + "/handoffs", Summary: "Freeze a handoff package for the accepted exact result", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, read, m.handoffResultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.receipt", Method: http.MethodPost, Path: workResultPath + "/handoffs/{handoff_id}/receipt", Summary: "Record a Human receipt or reasoned return of a handoff", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, read, m.receiveResultHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.evidence.download", Method: http.MethodGet, Path: workResultPath + "/evidence/{evidence_id}/content", Summary: "Download authorized exact pinned result evidence", Tags: []string{"work-items"}, Errors: errors}, "work_item_id", owner, read, m.downloadResultEvidenceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.evidence.candidate", Method: http.MethodGet, Path: workPath + "/{work_item_id}/evidence-candidates/{file_id}", Summary: "Resolve authorized file version metadata before attachment", Tags: []string{"work-items"}, Errors: errors}, "work_item_id", owner, execute, m.resultEvidenceCandidateHTTP)
	for _, path := range []string{workResultsPath, workResultPath + "/checks", workResultPath + "/acceptance", workResultPath + "/handoffs", workResultPath + "/handoffs/{handoff_id}/receipt"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) resultCommandHTTP(ctx context.Context, in ResultMutationInput, result, handoff, operation, suffix, permission, authority string, body any, command func(*gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, int) (ResultView, error)) (*deliveryCommandOutput[resultCommandReceipt], error) {
	project, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	// The cursor controls freshness, not the logical action identity. Keep
	// business payloads nested without removing arbitrary nested fields.
	hashBody := body
	switch input := body.(type) {
	case resultSubmitBody:
		input.SeenSeq = nil
		hashBody = input
	case resultAcceptBody:
		input.SeenSeq = nil
		hashBody = input
	case scopeReworkBody:
		input.SeenSeq = nil
		hashBody = input
	}
	request := struct {
		Version int    `json:"version"`
		Result  string `json:"result"`
		Handoff string `json:"handoff"`
		Body    any    `json:"body"`
	}{version, result, handoff, hashBody}
	params := map[string]string{"entity_id": project.String(), "work_item_id": id.String()}
	if result != "" {
		params["result_id"] = result
	}
	if handoff != "" {
		params["handoff_id"] = handoff
	}
	path := workResultsPath
	if result != "" {
		path = workResultPath + suffix
	}
	return deliveryCommand(ctx, m, "result", resultHTTPError, operation, http.MethodPost, path, in.IdempotencyKey, 201, request, params, func(tx *gorm.DB) (resultCommandReceipt, guid.ID, error) {
		v, e := command(tx, tenant, project, id, actor, owner, version)
		return resultCommandReceipt{ID: v.ID, ProjectID: v.ProjectID, WorkItemID: v.WorkItemID, Sequence: v.Sequence}, v.ID, e
	}, func(tx *gorm.DB) error {
		// Verification reviewers introduce additional principal/IAM fences; acquire
		// them before the common replay authorization takes the project lock.
		var reviewerOwners []string
		reviewerScoped := false
		if operation == "result.verification.collect" {
			rid, e := guid.RequireID(result)
			if e != nil {
				return ErrInvalidArgument
			}
			if e = m.prelockQualityOn(ctx, tx, tenant, project, id, rid, actor, owner); e != nil {
				return e
			}
		}
		if operation == "result.verification.start" {
			input, ok := body.(verificationStartBody)
			if !ok {
				return ErrInvalidArgument
			}
			rid, e := guid.RequireID(result)
			if e != nil {
				return ErrInvalidArgument
			}
			if reviewerOwners, reviewerScoped, e = m.verificationParticipantScopeOn(ctx, tx, tenant, project, id, rid, actor, owner, input); e != nil {
				return e
			}
		}
		_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, permission, operation == "result.accept")
		if e != nil {
			return e
		}
		if reviewerScoped && (work.CreatedBy == nil || !slices.Contains(reviewerOwners, work.CreatedBy.String())) {
			return ErrWorkForbidden
		}
		if result != "" {
			rid, e := guid.RequireID(result)
			if e != nil {
				return ErrInvalidArgument
			}
			row, e := m.resultOn(ctx, tx, tenant, project, id, rid)
			if e != nil {
				return e
			}
			if authority != "" {
				if operation == "result.accept" {
					_, e = m.resultAcceptanceAuthorityOn(ctx, tx, work, row, actor)
					return e
				}
				if authority == "delivery_send" {
					h := query.BindResultHandoffs(tx, m.service.names)
					base := h.WithContext(ctx).Where(h.TenantID.Eq(tenant), h.ProjectID.Eq(project), h.WorkItemID.Eq(id), h.ResultID.Eq(rid))
					if handoff != "" {
						hid, e := guid.RequireID(handoff)
						if e != nil {
							return ErrInvalidArgument
						}
						base = base.Where(h.ID.Eq(hid))
					}
					packageRow, e := base.Order(h.ID.Desc()).First()
					if errors.Is(e, gorm.ErrRecordNotFound) && operation == "result.handoff" {
						// First creation has no Delivery yet. Its command creates the actual
						// row, checks direct Human authority against it, then commits together.
						return nil
					}
					if e != nil {
						return e
					}
					_, e = m.resultAuthorityOn(ctx, tx, work, row, actor, authority, packageRow.DeliveryID)
					return e
				}
				_, e = m.resultAuthorityOn(ctx, tx, work, row, actor, authority)
				return e
			}
		}
		return nil
	})
}

func (m *Module) submitResultHTTP(ctx context.Context, in *resultSubmitInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, "", "", "result.submit", "", "work_item_execute", "", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.submitResultOn(ctx, tx, t, p, w, a, o, v, in.Body)
	})
}
func (m *Module) checkResultHTTP(ctx context.Context, in *resultCheckInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, e := guid.RequireID(in.ResultID)
	if e != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.check", "/checks", "work_item_write", "", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.checkResultOn(ctx, tx, t, p, w, id, a, o, v, in.Body)
	})
}
func (m *Module) acceptResultHTTP(ctx context.Context, in *resultAcceptInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, e := guid.RequireID(in.ResultID)
	if e != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.accept", "/acceptance", "work_item_read", "decision_approve", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.acceptResultOn(ctx, tx, t, p, w, id, a, o, v, in.Body)
	})
}
func (m *Module) handoffResultHTTP(ctx context.Context, in *resultHandoffInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, e := guid.RequireID(in.ResultID)
	if e != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.handoff", "/handoffs", "work_item_read", "delivery_send", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.handoffResultOn(ctx, tx, t, p, w, id, a, o, v, in.Body)
	})
}
func (m *Module) receiveResultHTTP(ctx context.Context, in *resultReceiptInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, e := guid.RequireID(in.ResultID)
	hid, he := guid.RequireID(in.HandoffID)
	if e != nil || he != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	code := "delivery_send"
	if !in.Body.Accepted {
		code = "decision_request_changes"
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, in.HandoffID, "result.receipt", "/handoffs/{handoff_id}/receipt", "work_item_read", code, in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.receiveResultOn(ctx, tx, t, p, w, id, hid, a, o, v, in.Body)
	})
}

func (m *Module) listResultsHTTP(ctx context.Context, in *resultListInput) (*respx.Body[[]ResultView], error) {
	project, pe := guid.RequireID(in.EntityID)
	id, we := guid.RequireID(in.WorkItemID)
	if pe != nil || we != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	items := []ResultView{}
	var count int64
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if _, e := m.readWork(ctx, tx, tenant, project, id, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		r := query.BindResultVersions(tx, m.service.names)
		base := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.Eq(id))
		var e error
		count, e = base.Count()
		if e != nil {
			return e
		}
		rows, e := base.Order(r.Sequence.Desc(), r.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, row := range rows {
			v, e := m.resultViewOn(ctx, tx, row)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: count}), nil
}

func (m *Module) resultEvidenceCandidateHTTP(ctx context.Context, in *resultCandidateInput) (*respx.Body[resultCandidateView], error) {
	project, pe := guid.RequireID(in.EntityID)
	work, we := guid.RequireID(in.WorkItemID)
	file, fe := guid.RequireID(in.FileID)
	if pe != nil || we != nil || fe != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.resultFiles == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	var v files.PinnedCandidate
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.readWork(ctx, tx, authn.TenantIDFromContext(ctx), project, work, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		var e error
		v, e = m.resultFiles.InspectOn(ctx, tx, file)
		return e
	})
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return respx.OK(ctx, resultCandidateView{SpaceID: v.SpaceID, FileID: v.FileID, VersionID: v.VersionID, FileVersion: v.NodeVersion, Name: v.Name, SHA256: v.SHA256, MIME: v.MIME, Size: v.Size}), nil
}

func (m *Module) downloadResultEvidenceHTTP(ctx context.Context, in *resultEvidenceInput) (*huma.StreamResponse, error) {
	project, pe := guid.RequireID(in.EntityID)
	work, we := guid.RequireID(in.WorkItemID)
	result, re := guid.RequireID(in.ResultID)
	evidence, ee := guid.RequireID(in.EvidenceID)
	if pe != nil || we != nil || re != nil || ee != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.resultFiles == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	var download files.PinnedResourceRead
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if _, e := m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		if _, e := m.resultOn(ctx, tx, tenant, project, work, result); e != nil {
			return e
		}
		q := query.BindResultEvidence(tx, m.service.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work), q.ResultID.Eq(result), q.ID.Eq(evidence)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrProjectNotFound
		}
		if e != nil {
			return e
		}
		download, e = m.resultFiles.OpenOn(ctx, tx, row.BindingID)
		return e
	})
	if err != nil {
		if download.Reader != nil {
			_ = download.Reader.Close()
		}
		return nil, resultHTTPError(ctx, err)
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() { _ = download.Reader.Close() }()
		stream.SetHeader("Content-Type", download.MIME)
		stream.SetHeader("Content-Length", strconv.FormatInt(download.Size, 10))
		stream.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Name}))
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(stream.BodyWriter(), download.Reader)
	}}, nil
}
