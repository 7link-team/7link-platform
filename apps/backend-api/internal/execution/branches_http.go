package execution

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

type strategyBranchInput struct {
	ExecutionInput
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           StrategyBranch
}
type strategyBranchOutput struct {
	Status       int
	Replayed     string `header:"Idempotent-Replayed"`
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[ExecutionView]
}

func (m *Module) registerBranchesHTTP(api huma.API) error {
	op := func(id, method, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: "/executions/{execution_id}/branches", Summary: summary, Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}
	}
	authz.RegisterOwned(m.registrar, api, op("execution.branch.list", "GET", "Read paged immutable work-item branch history"), "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, m.branchHistoryHTTP)
	create := op("execution.branch.strategy", "POST", "Create a strategy branch from the frozen root baseline")
	create.DefaultStatus = 201
	authz.RegisterOwned(m.registrar, api, create, "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "start", FilterData: true}, m.strategyBranchHTTP)
	return authz.RegisterRequestContract(api, "/executions/{execution_id}/branches")
}
func (m *Module) branchHistoryHTTP(ctx context.Context, in *executionTraceInput) (*respx.Body[ExecutionPage], error) {
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
	var out ExecutionPage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.BranchHistoryOn(ctx, tx, authn.TenantIDFromContext(ctx), id, cursor, in.Limit)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) strategyBranchHTTP(ctx context.Context, in *strategyBranchInput) (*strategyBranchOutput, error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	tenant := authn.TenantIDFromContext(ctx)
	digest, err := idempotency.HashRequest("POST", "/executions/{execution_id}/branches", map[string]string{"execution_id": id.String(), "version": strconv.Itoa(version)}, in.Body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "execution.branch.strategy", Value: in.IdempotencyKey, RequestHash: digest}
	if key.Validate() != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Include the requested machine before the source authority acquires project locks.
		if in.Body.MachineID > 0 {
			r, e := m.rowOn(ctx, tx, tenant, id, false)
			if e != nil {
				return e
			}
			if m.machines.ByIDOn == nil {
				return ErrUnavailable
			}
			machine, e := m.machines.ByIDOn(ctx, tx, tenant, in.Body.MachineID)
			if e != nil {
				return e
			}
			source, e := m.machines.ByIDOn(ctx, tx, tenant, r.MachineID)
			if e != nil {
				return e
			}
			if e = m.workPrincipalLocks(ctx, tx, link(r, authn.PrincipalIDFromContext(ctx)), authn.PrincipalIDFromContext(ctx), r.InitiatorID, machine.PrincipalID, source.PrincipalID); e != nil {
				return e
			}
		}
		if _, e := m.strategyAuthorityOn(ctx, tx, tenant, id); e != nil {
			return e
		}
		var e error
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := m.BranchStrategyOn(ctx, tx, tenant, id, version, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			if e = m.authorizeKnowledgeViewOn(ctx, tx, tenant, v); e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, v).Body)
			return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "execution", ResourceID: v.ID}, e
		})
		if e != nil {
			return e
		}
		// Cached plans contain knowledge pins: repeat their current read authority too.
		var envelope respx.Envelope[ExecutionView]
		if e = json.Unmarshal(result.Body, &envelope); e != nil {
			return e
		}
		row, e := m.rowOn(ctx, tx, tenant, envelope.Data.ID, false)
		if e != nil {
			return e
		}
		if m.work.AuthorizeOn == nil {
			return ErrUnavailable
		}
		if e = m.work.AuthorizeOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), "DISPATCH"); e != nil {
			return e
		}
		return m.authorizeKnowledgeViewOn(ctx, tx, tenant, envelope.Data)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := &strategyBranchOutput{Status: result.StatusCode, CacheControl: "private, no-store"}
	if replay {
		out.Replayed = "true"
	}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	return out, nil
}
