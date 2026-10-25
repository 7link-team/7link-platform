package governance

import (
	"context"
	"net/http"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type businessAuditInput struct {
	TenantID      string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID     string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Module        string `query:"module" pattern:"^[a-z][a-z0-9_]*$" maxLength:"64"`
	EventType     string `query:"event_type" maxLength:"3200" doc:"Comma-separated exact event types (at most 50)."`
	ActorID       string `query:"actor_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ActorKind     string `query:"actor_kind" enum:"HUMAN,AGENT,MACHINE,CLIENT,SYSTEM"`
	AuthorityKind string `query:"authority_kind" maxLength:"32"`
	AuthorityRef  string `query:"authority_ref" maxLength:"128"`
	TargetType    string `query:"target_type" maxLength:"64"`
	TargetID      string `query:"target_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Outcome       string `query:"outcome" enum:"success,denied,failure"`
	From          string `query:"occurred_at_gte" maxLength:"40"`
	To            string `query:"occurred_at_lt" maxLength:"40"`
	Origin        string `query:"origin" enum:"HUMAN_CREATED,AGENT_SPAWNED"`
	SpawnGrantID  string `query:"spawn_grant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

// Extend the common envelope metadata locally; the projection is synchronous
// over committed chain facts, so it has no asynchronous index lag.
type businessAuditMeta struct {
	respx.Meta
	IndexLagSeconds int `json:"index_lag_seconds"`
}
type businessAuditOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Code    int                   `json:"code"`
		Message string                `json:"message"`
		Meta    businessAuditMeta     `json:"meta"`
		Data    []audit.BusinessEvent `json:"data"`
	}
}

func (m *Module) registerBusinessAuditHTTP(api huma.API) {
	op := huma.Operation{OperationID: "governance.business-audit.list", Method: http.MethodGet, Path: "/business-audit-events", Summary: "List current-authority business audit projections", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}
	op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
		tenant, _ := guid.Parse(ctx.Param("tenant_id"))
		next(huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), tenant, "audit_read")))
	})
	authz.RegisterTenantMember(m.registrar, api, op, m.businessAuditHTTP)
}

func (in *businessAuditInput) query() (audit.BusinessQuery, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return audit.BusinessQuery{}, ErrInvalidArgument
	}
	q := audit.BusinessQuery{Filter: audit.Filter{TenantID: tenant, Module: in.Module, ActorKind: in.ActorKind, TargetType: in.TargetType, Outcome: in.Outcome, Offset: in.Offset, Limit: in.Limit}, AuthorityKind: in.AuthorityKind, AuthorityRef: in.AuthorityRef, Origin: in.Origin}
	for _, f := range []struct {
		value string
		dest  *guid.ID
	}{{in.ProjectID, &q.ProjectID}, {in.ActorID, &q.PrincipalID}, {in.TargetID, &q.TargetID}, {in.SpawnGrantID, &q.SpawnGrantID}} {
		*f.dest, err = guid.OptionalID(f.value)
		if err != nil {
			return q, ErrInvalidArgument
		}
	}
	for _, f := range []struct {
		value string
		dest  *time.Time
	}{{in.From, &q.From}, {in.To, &q.To}} {
		if f.value == "" {
			continue
		}
		*f.dest, err = time.Parse(time.RFC3339Nano, f.value)
		if err != nil {
			return q, ErrInvalidArgument
		}
	}
	if in.EventType != "" {
		q.EventTypes = strings.Split(in.EventType, ",")
		for _, typ := range q.EventTypes {
			if strings.TrimSpace(typ) != typ || typ == "" || len(typ) > 64 {
				return q, ErrInvalidArgument
			}
		}
	}
	if q.Limit < 1 || q.Limit > 200 || q.Offset < 0 || len(q.EventTypes) > 50 || (!q.To.IsZero() && !q.From.IsZero() && !q.To.After(q.From)) {
		return q, ErrInvalidArgument
	}
	return q, nil
}

func (m *Module) businessAuditHTTP(ctx context.Context, in *businessAuditInput) (*businessAuditOutput, error) {
	q, err := in.query()
	if err != nil {
		return nil, escapeHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, escapeHTTPError(ctx, err)
	}
	var rows []audit.BusinessEvent
	var total int64
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Locks the current principal and checks active tenant membership plus
		// tenant audit_read, even on an empty list or a replayed URL.
		if err := m.escapeReadAuthorityOn(ctx, tx, q.TenantID, 0); err != nil {
			return err
		}
		if m.businessAudit == nil {
			return ErrUnavailable
		}
		actor := authn.PrincipalIDFromContext(ctx)
		all, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: q.TenantID, PrincipalID: actor, Permission: "audit_event_view"})
		if err != nil {
			return err
		}
		projects := map[guid.ID]bool{}
		rows, total, err = m.businessAudit(ctx, tx, q, func(e audit.BusinessEvent) (bool, error) {
			if all.Allowed {
				return true, nil
			}
			if e.ProjectID == 0 {
				return e.Actor.ID == actor, nil
			}
			if allowed, exists := projects[e.ProjectID]; exists {
				return allowed, nil
			}
			x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: q.TenantID, EntityID: e.ProjectID, PrincipalID: actor, Permission: "project_read"})
			if err != nil {
				return false, err
			}
			projects[e.ProjectID] = x.Allowed
			return x.Allowed, nil
		})
		return err
	})
	if err != nil {
		return nil, escapeHTTPError(ctx, err)
	}
	envelope := respx.List(ctx, rows, respx.Page{Offset: q.Offset, Limit: q.Limit, Count: len(rows), Total: total}).Body
	out := &businessAuditOutput{CacheControl: "no-store"}
	out.Body.Code, out.Body.Message, out.Body.Meta.Meta, out.Body.Data = envelope.Code, envelope.Message, envelope.Meta, envelope.Data
	return out, nil
}
