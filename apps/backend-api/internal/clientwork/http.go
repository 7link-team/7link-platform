package clientwork

import (
	ci "7link.com/platform/internal/clientwork/i18n"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sheetsPath = "/entities/{entity_id}/requirement-sheets"

type ListInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor   string `query:"cursor" maxLength:"512"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type ObjectInput struct {
	ReplyCursor string `query:"reply_cursor" maxLength:"512"`
	EntityID    string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID          string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Revision    int    `query:"revision" minimum:"0" default:"0"`
}
type CreateInput[T any] struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     T
}
type ClientworkPublishBody struct{}
type PackagePublishBody struct {
	ReauthProof string `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}

type EditInput[T any] struct {
	Cookie   string `json:"-" header:"Cookie" hidden:"true"`
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch  string `header:"If-Match" required:"true" doc:"Quoted current concurrency version."`
	Body     T
}
type Receipt struct {
	Bundle         *PackageBundleView    `json:"bundle,omitempty"`
	RequestedAt    *time.Time            `json:"requested_at,omitempty"`
	Coalesced      bool                  `json:"coalesced,omitempty"`
	TestCaseImport *TestCaseImportReport `json:"test_case_import,omitempty"`
	ID             guid.ID               `json:"id"`
	Version        int                   `json:"version"`
	Revision       int                   `json:"revision,omitempty"`
	State          string                `json:"state"`
}
type CommandOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[Receipt]
}
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

func httpError(ctx context.Context, e error) error {
	var interrupted *collab.Interrupted
	if errors.As(e, &interrupted) {
		return respx.ErrStatus(ctx, 409, 124409, ci.Conflict, interrupted)
	}
	if errors.Is(e, collab.ErrSeenSeqRequired) {
		return respx.ErrStatus(ctx, 422, 124422, ci.Invalid, nil)
	}
	var anchorErr *execution.ClientAnchorError
	if errors.As(e, &anchorErr) {
		code := 130476
		if anchorErr.Unavailable {
			code = 130480
		}
		return respx.ErrStatus(ctx, 422, code, ci.Invalid, map[string]any{"field": anchorErr.Field})
	}
	var commentErr *execution.ClientThreadError
	if errors.As(e, &commentErr) {
		return respx.ErrStatus(ctx, 422, 127057, ci.Invalid, map[string]any{"reason": commentErr.Reason, "target_id": commentErr.TargetID})
	}
	switch {
	case errors.Is(e, ErrBundleStale):
		return respx.ErrStatus(ctx, 422, 128008, ci.BundleStale, nil)
	case errors.Is(e, ErrBundleTooLarge), errors.Is(e, ErrBundleScan):
		return respx.ErrStatus(ctx, 422, 128021, ci.BundleFailed, nil)
	case errors.Is(e, delivery.ErrRequirementStage), errors.Is(e, delivery.ErrRequirementLimit), errors.Is(e, delivery.ErrProjectLimit), errors.Is(e, delivery.ErrParentNotClient), errors.Is(e, delivery.ErrContactNotFound), errors.Is(e, delivery.ErrIntakeNotFound), errors.Is(e, intake.ErrInvalidArgument), errors.Is(e, collab.ErrInvalidArgument), errors.Is(e, ErrInvalid), errors.Is(e, execution.ErrInvalidArgument), errors.Is(e, idempotency.ErrInvalidKey), errors.Is(e, delivery.ErrInvalidArgument), errors.Is(e, files.ErrPinnedInvalid):
		return respx.ErrStatus(ctx, 422, 124422, ci.Invalid, nil)
	case errors.Is(e, ErrDispatchExpired):
		return respx.ErrStatus(ctx, 409, 124412, ci.DispatchExpired, nil)
	case errors.Is(e, ErrShareEnded):
		return respx.ErrStatus(ctx, 410, 124411, ci.ShareEnded, nil)
	case errors.Is(e, ErrOpenException):
		return respx.ErrStatus(ctx, 409, 124410, ci.OpenException, nil)
	case errors.Is(e, core.ErrEntityConflict), errors.Is(e, core.ErrAuthorizationChanged), errors.Is(e, delivery.ErrProjectCodeTaken), errors.Is(e, intake.ErrConflict), errors.Is(e, ErrConflict), errors.Is(e, execution.ErrConflict), errors.Is(e, idempotency.ErrKeyReused), errors.Is(e, idempotency.ErrInProgress), errors.Is(e, delivery.ErrRequirementVersion), errors.Is(e, delivery.ErrWorkVersion), errors.Is(e, delivery.ErrRequirementFrozen), errors.Is(e, delivery.ErrResultCoverage), errors.Is(e, delivery.ErrResultState), errors.Is(e, delivery.ErrWorkProjectReadOnly), errors.Is(e, files.ErrPinnedConflict):
		return respx.ErrStatus(ctx, 409, 124409, ci.Conflict, nil)
	case errors.Is(e, core.ErrEntityNotFound), errors.Is(e, ErrNotFound), errors.Is(e, execution.ErrNotFound), errors.Is(e, execution.ErrForbidden), errors.Is(e, gorm.ErrRecordNotFound), errors.Is(e, delivery.ErrProjectNotFound), errors.Is(e, delivery.ErrWorkForbidden), errors.Is(e, delivery.ErrInvalidMember), errors.Is(e, collab.ErrForbidden), errors.Is(e, collab.ErrNotFound), errors.Is(e, files.ErrPinnedForbidden), errors.Is(e, files.ErrNotFound):
		return respx.ErrStatus(ctx, 404, 124404, ci.NotFound, nil)
	case errors.Is(e, delivery.ErrProjectForbidden), errors.Is(e, delivery.ErrProjectNonDelegable), errors.Is(e, delivery.ErrResultIndependent), errors.Is(e, intake.ErrForbidden), errors.Is(e, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, nil)
	case errors.Is(e, authn.ErrInvalidCredentials):
		return respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "project_cancel"})
	case errors.Is(e, intake.ErrUnavailable), errors.Is(e, ErrUnavailable), errors.Is(e, execution.ErrUnavailable), errors.Is(e, delivery.ErrUnavailable), errors.Is(e, collab.ErrUnavailable), errors.Is(e, files.ErrPinnedUnavailable):
		return respx.ErrStatus(ctx, 503, 124503, ci.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 124500, ci.Internal, nil)
	}
}
func version(value string) (int, error) {
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, ErrInvalid
	}
	v, e := strconv.Atoi(value[1 : len(value)-1])
	if e != nil || v < 1 {
		return 0, ErrInvalid
	}
	return v, nil
}
func (m *Module) command(ctx context.Context, project guid.ID, op, method, path, key string, status int, request any, permission string, client bool, check func(*gorm.DB) error, run func(*gorm.DB) (Receipt, error), lockBeforeProject ...func(*gorm.DB) error) (*CommandOutput, error) {
	if m.writer() == nil || m.replay == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	hashRequest := request
	if in, ok := request.(*EditInput[PackagePublishBody]); ok {
		hashRequest = &EditInput[ClientworkPublishBody]{EntityID: in.EntityID, ID: in.ID, Key: in.Key, IfMatch: in.IfMatch}
	}
	if in, ok := request.(*AgentMergeInput); ok {
		// Flatten only this request's body so the canonical top-level seen_seq
		// retry cursor is excluded while source/version and business data stay bound.
		hashRequest = struct {
			ID      string
			IfMatch string
			intake.SourceDisposition
		}{in.ID, in.IfMatch, in.Body}
	}
	if in, ok := request.(*RecordedAcceptanceInput); ok {
		public := *in
		public.Cookie = ""
		public.Body.ReauthProof = ""
		hashRequest = public
	}
	if in, ok := request.(*ExceptionDecisionInput); ok {
		// Proofs authenticate the first effect; renewed session credentials must
		// not turn a retry of the same business decision into a new command.
		public := *in
		public.Cookie, public.Body.ReauthProof = "", ""
		hashRequest = public
	}
	if in, ok := request.(*IntakeConversionInput); ok {
		public := *in
		public.Cookie, public.Body.ReauthProof = "", ""
		hashRequest = public
	}
	if in, ok := request.(*ReworkDispositionInput); ok {
		public := *in
		public.Cookie = ""
		if public.Body.Append != nil {
			body := *public.Body.Append
			body.ReauthProof = ""
			public.Body.Append = &body
		}
		hashRequest = public
	}
	if in, ok := request.(*IntakeProjectConversionInput); ok {
		public := *in
		public.Cookie, public.Body.ReauthProof = "", ""
		hashRequest = public
	}
	hash, e := idempotency.HashRequest(method, path, map[string]string{"entity_id": project.String()}, hashRequest)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	k := idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: op, Value: key, RequestHash: hash}
	if e = k.Validate(); e != nil {
		return nil, httpError(ctx, e)
	}
	var result idempotency.Result
	var replayed bool
	if in, ok := request.(*ExceptionDecisionInput); ok && in.Body.Decision == "KILL" {
		permission = "exception_decide_replay"
	}
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, lock := range lockBeforeProject {
			if err := lock(tx); err != nil {
				return err
			}
		}
		_, questionDraft := request.(*ClarificationDraftInput)
		_, releaseWriter := request.(*ReleaseNoteWriteInput)
		if releaseWriter {
			if m.releaseDrafters.WriterOn == nil {
				return ErrUnavailable
			}
			if err := m.releaseDrafters.WriterOn(ctx, tx, authn.TenantIDFromContext(ctx), project); err != nil {
				return err
			}
		}
		_, suggestion := request.(*IntakeSuggestionInput)
		if questionDraft || suggestion {
			if m.questionDrafts.AuthorizeOn == nil {
				return ErrUnavailable
			}
			if _, err := m.questionDrafts.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), project); err != nil {
				return err
			}
		}
		if in, ok := request.(*DispatchAutoInput); ok {
			if m.dispatch.AutoLockOn == nil || m.dispatchFleet.PrincipalsOn == nil {
				return ErrUnavailable
			}
			ids, err := m.dispatchFleet.PrincipalsOn(ctx, tx, authn.TenantIDFromContext(ctx))
			if err != nil {
				return err
			}
			state, _ := ctx.Value(dispatchAutoKey{}).(*dispatchAutoState)
			if state == nil {
				return ErrForbidden
			}
			state.principals = ids
			if err := m.dispatch.AutoLockOn(ctx, tx, authn.TenantIDFromContext(ctx), project, in.Body.WorkItemID, ids); err != nil {
				return err
			}
		}
		if in, ok := request.(*DispatchStopInput); ok {
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return ErrInvalid
			}
			q := query.BindDispatchProposal(tx, m.names)
			row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
			if err != nil {
				return err
			}
			if m.dispatch.LockStopOn == nil {
				return ErrUnavailable
			}
			if err = m.dispatch.LockStopOn(ctx, tx, row.TenantID, project, row.WorkItemID, in.Body.ExecutionID); err != nil {
				return err
			}
		}
		if in, ok := request.(*ExceptionDecisionInput); ok && in.Body.Decision == "KILL" {
			c, ok := m.projects.(delivery.ProjectCancellationAccess)
			if !ok {
				return ErrUnavailable
			}
			if e := c.LockCancellationOn(ctx, tx, authn.TenantIDFromContext(ctx), project); e != nil {
				return e
			}
		}
		_, delegated := request.(*DelegatedNewInput)
		_, delegatedMerge := request.(*AgentMergeInput)
		if in, ok := request.(*AgentMergeInput); ok {
			if m.agentMerge.AuthorizeOn == nil {
				return ErrUnavailable
			}
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return ErrInvalid
			}
			if err = m.agentMerge.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id); err != nil {
				return err
			}
		}
		_, triageRequest := request.(*TriageRequestInput)
		if in, ok := request.(*TriageRequestInput); ok {
			if m.triageTarget.AuthorizeRequestOn == nil {
				return ErrUnavailable
			}
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return ErrInvalid
			}
			if err = m.triageTarget.AuthorizeRequestOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id); err != nil {
				return err
			}
		}
		if in, ok := request.(*DelegatedNewInput); ok {
			if m.delegatedConversion.AuthorizeOn == nil {
				return ErrUnavailable
			}
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return ErrInvalid
			}
			if err = m.delegatedConversion.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id); err != nil {
				return err
			}
		}
		_, draft := request.(*ClarificationDraftInput)
		if _, auto := request.(*DispatchAutoInput); !auto && !draft && !releaseWriter && !suggestion && !delegated && !delegatedMerge && !triageRequest {
			if _, e := m.authorize(ctx, tx, project, permission, client); e != nil {
				return e
			}
		}
		if check != nil {
			if e := check(tx); e != nil {
				return e
			}
		}
		var e error
		result, replayed, e = m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			r, e := run(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			data, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, r).Body)
			responseStatus := status
			if op == "clientwork.package.bundle_build" && r.State == "BUILT" {
				responseStatus = 200
			}
			if triageRequest && r.Coalesced {
				responseStatus = 202
			}
			return idempotency.Result{StatusCode: responseStatus, Body: data, ResourceKind: Name, ResourceID: r.ID}, e
		})
		return e
	})
	if e != nil {
		if errors.Is(e, authn.ErrInvalidCredentials) {
			switch in := request.(type) {
			case *EditInput[PackagePublishBody]:
				return nil, respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "delivery_send", "target": "package:" + in.ID})
			case *RecordedAcceptanceInput:
				return nil, respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "acceptance_record", "target": "package:" + in.ID})
			case *IntakeProjectConversionInput:
				return nil, respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "intake_convert_privileged", "target": "intake:" + in.ID})
			case *ReworkDispositionInput:
				if in.Body.Append != nil {
					return nil, respx.ErrStatus(ctx, 403, 124403, ci.Forbidden, map[string]any{"reauthentication_required": true, "purpose": "scope_change", "target": "requirement:" + in.Body.RequirementID.String()})
				}
			}
		}
		return nil, httpError(ctx, e)
	}
	out := &CommandOutput{Status: result.StatusCode}
	if e = json.Unmarshal(result.Body, &out.Body); e != nil {
		return nil, httpError(ctx, e)
	}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
func receipt(v SheetView) Receipt {
	return Receipt{ID: v.ID, Version: v.Version, Revision: v.Revision, State: v.State}
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil || m.owner == nil {
		return ErrUnavailable
	}
	m.registerRecordedAcceptanceHTTP(api)
	if e := m.registerReleaseNoteHTTP(api); e != nil {
		return e
	}
	if e := m.registerAutomationBridgeHTTP(api); e != nil {
		return e
	}
	if e := m.registerReworkDispositionHTTP(api); e != nil {
		return e
	}
	if e := m.registerIntakeSuggestionHTTP(api); e != nil {
		return e
	}
	if e := m.registerIntakeThreadHTTP(api); e != nil {
		return e
	}
	if e := m.registerNewIntakeHTTP(api); e != nil {
		return e
	}
	if e := m.registerIntakeProjectHTTP(api); e != nil {
		return e
	}
	if e := m.registerClarificationHTTP(api); e != nil {
		return e
	}
	if e := m.registerIntakeHTTP(api); e != nil {
		return e
	}
	m.registerPendingHTTP(api)
	if e := m.registerDispatchHTTP(api); e != nil {
		return e
	}
	if e := m.registerPackageSendsHTTP(api); e != nil {
		return e
	}
	m.registerPackageTimelineHTTP(api)
	m.registerTestReportsHTTP(api)
	m.registerAcceptanceRemindersHTTP(api)
	if e := m.registerShareHTTP(api); e != nil {
		return e
	}
	if e := m.registerToleranceHTTP(api); e != nil {
		return e
	}
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "channel", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.list", Method: http.MethodGet, Path: sheetsPath, Summary: "List authorized requirement confirmation sheets", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.listSheetsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.get", Method: http.MethodGet, Path: sheetsPath + "/{id}", Summary: "Read an exact confirmation sheet revision", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getSheetHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.create", Method: http.MethodPost, Path: sheetsPath, Summary: "Draft a confirmation sheet from canonical requirements", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.createSheetHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.version", Method: http.MethodPost, Path: sheetsPath + "/{id}/versions", Summary: "Append an immutable sheet revision", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.reviseSheetHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.publish", Method: http.MethodPost, Path: sheetsPath + "/{id}/publish", Summary: "Publish the exact revision for client signoff", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.publishSheetHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "requirement_sheet.signoff", Method: http.MethodPost, Path: sheetsPath + "/{id}/signoffs", Summary: "Sign or reject an exact published sheet version and hash", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.signSheetHTTP)
	if e := m.registerChangeApplicationHTTP(api); e != nil {
		return e
	}
	m.registerAccessHTTP(api)
	if e := m.registerPackageBundlesHTTP(api); e != nil {
		return e
	}
	m.registerContentHTTP(api)
	if e := m.registerDomainHTTP(api); e != nil {
		return e
	}
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	for _, path := range []string{sheetsPath, sheetsPath + "/{id}/versions", sheetsPath + "/{id}/publish", sheetsPath + "/{id}/signoffs"} {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) createSheetHTTP(ctx context.Context, in *CreateInput[SheetDraft]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "requirement_sheet.create", "POST", sheetsPath, in.Key, 201, in.Body, "requirement_write", false, nil, func(tx *gorm.DB) (Receipt, error) {
		v, e := m.draftSheetOn(ctx, tx, p, 0, 0, in.Body)
		return receipt(v), e
	})
}
func (m *Module) reviseSheetHTTP(ctx context.Context, in *EditInput[SheetDraft]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "requirement_sheet.version", "POST", sheetsPath+"/"+id.String()+"/versions", in.Key, 201, in, "requirement_write", false, nil, func(tx *gorm.DB) (Receipt, error) {
		r, e := m.draftSheetOn(ctx, tx, p, id, v, in.Body)
		return receipt(r), e
	})
}
func editIDs(project, id, tag string) (guid.ID, guid.ID, int, error) {
	p, e := guid.RequireID(project)
	if e != nil {
		return 0, 0, 0, ErrInvalid
	}
	i, e := guid.RequireID(id)
	if e != nil {
		return 0, 0, 0, ErrInvalid
	}
	v, e := version(tag)
	return p, i, v, e
}
func (m *Module) publishSheetHTTP(ctx context.Context, in *EditInput[ClientworkPublishBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "requirement_sheet.publish", "POST", sheetsPath+"/"+id.String()+"/publish", in.Key, 200, in, "requirement_write", false, nil, func(tx *gorm.DB) (Receipt, error) { r, e := m.publishSheetOn(ctx, tx, p, id, v); return receipt(r), e })
}
func (m *Module) signSheetHTTP(ctx context.Context, in *EditInput[SignoffBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	check := func(tx *gorm.DB) error {
		if _, err := m.clientSignerOn(ctx, tx, p); err != nil {
			return err
		}
		external, e := m.authorize(ctx, tx, p, "requirement_read", true)
		if e != nil {
			return e
		}
		if !external {
			return ErrForbidden
		}
		row, e := m.loadSheet(ctx, tx, p, id)
		if e != nil {
			return e
		}
		r, e := m.sheetView(ctx, tx, row, true, in.Body.Revision)
		if e != nil {
			return e
		}
		if r.SignerID != authn.PrincipalIDFromContext(ctx) {
			return ErrNotFound
		}
		return nil
	}
	return m.command(ctx, p, "requirement_sheet.signoff", "POST", sheetsPath+"/"+id.String()+"/signoffs", in.Key, 201, in, "requirement_read", true, check, func(tx *gorm.DB) (Receipt, error) {
		r, e := m.signSheetOn(ctx, tx, p, id, v, in.Body)
		return receipt(r), e
	})
}
func (m *Module) getSheetHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[SheetView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out SheetView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, e := m.authorize(ctx, tx, p, "requirement_read", true)
		if e != nil {
			return e
		}
		row, e := m.loadSheet(ctx, tx, p, id)
		if e != nil {
			return e
		}
		out, e = m.sheetView(ctx, tx, row, external, in.Revision)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func cursorScope(ctx context.Context, p guid.ID, kind string) string {
	return fmt.Sprintf("%s:%s:%s:%s", authn.TenantIDFromContext(ctx), p, authn.PrincipalIDFromContext(ctx), kind)
}
func cursor(scope string, id guid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(scope + ":" + id.String()))
}
func position(raw, scope string) (guid.ID, error) {
	if raw == "" {
		return 0, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || !strings.HasPrefix(string(b), scope+":") {
		return 0, ErrInvalid
	}
	v, e := guid.RequireID(strings.TrimPrefix(string(b), scope+":"))
	if e != nil {
		return 0, ErrInvalid
	}
	return v, nil
}
func (m *Module) listSheetsHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[SheetView]], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[SheetView]{Items: []SheetView{}}
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, e := m.authorize(ctx, tx, p, "requirement_read", true)
		if e != nil {
			return e
		}
		scope := cursorScope(ctx, p, fmt.Sprintf("sheets:%t", external))
		last, e := position(in.Cursor, scope)
		if e != nil {
			return e
		}
		q := query.BindSheets(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p))
		if external {
			r = r.Where(q.PublishedRevision.Gt(0))
		}
		if last > 0 {
			r = r.Where(q.ID.Lt(last))
		}
		rows, e := r.Order(q.ID.Desc()).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = cursor(scope, rows[len(rows)-1].ID)
		}
		for _, row := range rows {
			v, e := m.sheetView(ctx, tx, row, external, 0)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

var _ module.HTTPRegistrar = (*Module)(nil)
