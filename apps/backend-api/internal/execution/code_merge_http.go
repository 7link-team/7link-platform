package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const mergesPath = "/executions/{execution_id}/merges"

type mergeInput struct {
	ExecutionInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           MergeRequest
}
type mergeOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[MergeView]
}
type mergeReadInput struct {
	MergeID string `path:"merge_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type mergeReceiptInput struct {
	MergeID string `path:"merge_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body    struct {
		Fence       RunnerFence      `json:"fence"`
		Nonce       string           `json:"nonce" minLength:"64" maxLength:"64"`
		Observation MergeObservation `json:"observation"`
	}
}

func (m *Module) registerCodeMergeHTTP(api huma.API) error {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	create := op("execution.merge.create", http.MethodPost, mergesPath, "Request guarded integration of an accepted verified commit")
	create.DefaultStatus = 201
	authz.RegisterOwned(m.registrar, api, create, "execution_id", m.owner, authz.Guard{Resource: "code", Verb: "merge", FilterData: true}, m.requestMergeHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.merge.list", http.MethodGet, mergesPath, "Read immutable merge attempts"), "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, m.listMergesHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.merge.get", http.MethodGet, "/merges/{merge_id}", "Read the current merge outcome"), "merge_id", m.mergeOwner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, m.getMergeHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.merge.pull", http.MethodPost, "/runner/merge-pulls", "Lease a fenced guarded code merge"), m.pullMergeHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.merge.receipt", http.MethodPost, "/runner/merges/{merge_id}/receipts", "Record actual fenced merge outcome"), m.completeMergeHTTP)
	for _, path := range []string{mergesPath, "/runner/merges/{merge_id}/receipts"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) requestMergeHTTP(ctx context.Context, in *mergeInput) (*mergeOutput, error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	digest, err := idempotency.HashRequest(http.MethodPost, mergesPath, map[string]string{"execution_id": id.String()}, in.Body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "execution.merge.create", Value: in.IdempotencyKey, RequestHash: digest}
	if err = key.Validate(); err != nil {
		return nil, httpError(ctx, err)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, e := m.authorizeMergeOn(ctx, tx, tenant, id); e != nil {
			return e
		}
		var e error
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := m.RequestMergeOn(ctx, tx, tenant, id, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, v).Body)
			return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "merge", ResourceID: v.ID}, e
		})
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := &mergeOutput{Status: 201}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) mergeOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	var r model.Merge
	err = m.mergeTable(db).WithContext(ctx).Where("id = ?", id).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: r.TenantID, EntityID: r.ProjectID}, true, nil
}
func (m *Module) getMergeHTTP(ctx context.Context, in *mergeReadInput) (*respx.Body[MergeView], error) {
	id, err := guid.RequireID(in.MergeID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out MergeView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.Merge
		if e := m.mergeTable(tx).Where("tenant_id = ? AND id = ?", authn.TenantIDFromContext(ctx), id).First(&r).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return e
		}
		if _, e := m.commentAuthority(ctx, tx, r.TenantID, r.ProjectID, "execution_read"); e != nil {
			return e
		}
		var e error
		out, e = mergeView(&r)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listMergesHTTP(ctx context.Context, in *ExecutionInput) (*respx.Body[[]MergeView], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := []MergeView{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.rowOn(ctx, tx, authn.TenantIDFromContext(ctx), id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, r.TenantID, r.ProjectID, "execution_read"); e != nil {
			return e
		}
		var rows []model.Merge
		if e = m.mergeTable(tx).Where("tenant_id = ? AND execution_id = ?", r.TenantID, id).Order("attempt DESC").Limit(100).Find(&rows).Error; e != nil {
			return e
		}
		for _, r := range rows {
			v, e := mergeView(&r)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) pullMergeHTTP(ctx context.Context, in *pullInput) (*respx.Body[[]MergePlan], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	ctx, err := m.machines.BindRunnerContext(ctx, in.Body.MachineID)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := []MergePlan{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.pullMergeOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) completeMergeHTTP(ctx context.Context, in *mergeReceiptInput) (*respx.Body[bool], error) {
	id, err := guid.RequireID(in.MergeID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	ctx, err = m.machines.BindRunnerContext(ctx, in.Body.Fence.MachineID)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.completeMergeOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Nonce, in.Body.Observation)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}
