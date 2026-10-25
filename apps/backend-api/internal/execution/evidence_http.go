package execution

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const evidenceCreatePath = "/executions/{execution_id}/evidence"
const evidenceUpdatePath = "/evidence/{evidence_id}"

type evidenceCreateInput struct {
	ExecutionInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EvidenceCreate
}
type evidenceUpdateInput struct {
	EvidenceID     string `path:"evidence_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EvidenceUpdate
}
type evidenceOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[EvidenceView]
}

func (m *Module) evidenceOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindEvidence(db, m.names)
	evidence, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return m.artifactCommentExecutionOwner(ctx, evidence.ExecutionID)
}
func (m *Module) registerEvidenceHTTP(api huma.API) error {
	guard := authz.Guard{Resource: "evidence", Verb: "submit", FilterData: true}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "evidence.create", Method: "POST", Path: evidenceCreatePath, Summary: "Submit a manual observation or pinned artifact evidence draft", Tags: []string{"executions"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, "execution_id", m.artifactCommentExecutionOwner, guard, m.createEvidenceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "evidence.update", Method: "PATCH", Path: evidenceUpdatePath, Summary: "Edit or withdraw unattached evidence", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "evidence_id", m.evidenceOwner, guard, m.updateEvidenceHTTP)
	if err := authz.RegisterRequestContract(api, evidenceCreatePath); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, evidenceUpdatePath)
}
func (m *Module) createEvidenceHTTP(ctx context.Context, in *evidenceCreateInput) (*evidenceOutput, error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.evidenceCommand(ctx, id, 0, "evidence.create", "POST", evidenceCreatePath, in.IdempotencyKey, in.Body, 201, func(tx *gorm.DB) (EvidenceView, error) {
		return m.CreateEvidenceOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body)
	})
}
func (m *Module) updateEvidenceHTTP(ctx context.Context, in *evidenceUpdateInput) (*evidenceOutput, error) {
	id, err := guid.RequireID(in.EvidenceID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.evidenceCommand(ctx, 0, id, "evidence.update", "PATCH", evidenceUpdatePath, in.IdempotencyKey, in.Body, 200, func(tx *gorm.DB) (EvidenceView, error) {
		return m.UpdateEvidenceOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body)
	})
}
func (m *Module) evidenceCommand(ctx context.Context, execution, evidence guid.ID, operation, method, path, value string, input any, status int, run func(*gorm.DB) (EvidenceView, error)) (*evidenceOutput, error) {
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrCapabilityUnavailable)
	}
	paths := map[string]string{"execution_id": execution.String()}
	if evidence > 0 {
		paths = map[string]string{"evidence_id": evidence.String()}
	}
	digest, err := idempotency.HashRequest(method, path, paths, input)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: value, RequestHash: digest}
	if err = key.Validate(); err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if evidence > 0 {
			q := query.BindEvidence(tx, m.names)
			source, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(evidence)).First()
			if e != nil {
				return e
			}
			execution = source.ExecutionID
		}
		row, e := m.rowOn(ctx, tx, tenant, execution, false)
		if e != nil {
			return e
		}
		// Authorization is current even on replay. Cursor validation belongs inside
		// the uncached command, so a successful immutable response remains replayable.
		if _, e = m.commentAuthority(ctx, tx, tenant, row.ProjectID, "evidence_submit"); e != nil {
			return e
		}
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			view, e := run(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, view).Body)
			return idempotency.Result{StatusCode: status, Body: body, ResourceKind: "evidence", ResourceID: view.ID}, e
		})
		return e
	})
	if err != nil {
		return nil, writeContextHTTPError(ctx, err, operation)
	}
	out := &evidenceOutput{Status: status}
	if replay {
		out.Replayed = "true"
	}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	return out, nil
}
