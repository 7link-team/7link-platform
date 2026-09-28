package audit

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type SystemAuditFilterInput struct {
	TenantSelector string `query:"tenant_id" hidden:"true"`
	PrincipalID    string `query:"principal_id" maxLength:"64" doc:"acting operator: under an account view, the operator who opened it, not the viewed account (detail.viewed_principal_id)"`
	EventType      string `query:"event_type" maxLength:"64"`
	Outcome        string `query:"outcome" enum:"success,denied,failure"`
	TargetType     string `query:"target_type" maxLength:"64"`
	TargetID       string `query:"target_id" maxLength:"128"`
	From           string `query:"from" maxLength:"40"`
	To             string `query:"to" maxLength:"40"`
}

type systemAuditListInput struct {
	SystemAuditFilterInput
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type systemAuditEventInput struct {
	ID             string `path:"event_id" maxLength:"64"`
	TenantSelector string `query:"tenant_id" hidden:"true"`
}

type systemAuditIntegrityInput struct {
	TenantSelector string `query:"tenant_id" hidden:"true"`
}

func rejectSystemAuditTenant(selector string) error {
	if selector != "" {
		return huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return nil
}

func systemAuditFilter(in *SystemAuditFilterInput) (Filter, error) {
	if err := rejectSystemAuditTenant(in.TenantSelector); err != nil {
		return Filter{}, err
	}
	return filterFields(0, in.PrincipalID, in.EventType, in.Outcome, in.TargetType, in.TargetID, in.From, in.To)
}

func registerSystemAuditHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "audit-system-list-events", Method: http.MethodGet, Path: "/system/audit/events",
		Summary: "List system audit events", Tags: []string{"audit"}, Errors: []int{422, 500},
	}, authz.Guard{Resource: "system_audit_event", Verb: "view"}, func(ctx context.Context, in *systemAuditListInput) (*respx.Body[[]Event], error) {
		filter, err := systemAuditFilter(&in.SystemAuditFilterInput)
		if err != nil {
			return nil, err
		}
		filter.Offset, filter.Limit = in.Offset, in.Limit
		events, total, err := service.List(ctx, filter)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.List(ctx, events, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(events), Total: total}), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "audit-system-get-event", Method: http.MethodGet, Path: "/system/audit/events/{event_id}",
		Summary: "Get a system audit event", Tags: []string{"audit"}, Errors: []int{404, 422, 500},
	}, authz.Guard{Resource: "system_audit_event", Verb: "view"}, func(ctx context.Context, in *systemAuditEventInput) (*respx.Body[Event], error) {
		if err := rejectSystemAuditTenant(in.TenantSelector); err != nil {
			return nil, err
		}
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		event, err := service.Get(ctx, 0, id)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, event), nil
	})
	operation := exportOperation()
	operation.OperationID, operation.Path, operation.Summary = "audit-system-export-events", "/system/audit/exports", "Export a verified system audit snapshot"
	authz.RegisterPlatform(registrar, api, operation, authz.Guard{Resource: "system_audit_event", Verb: "export"}, func(ctx context.Context, in *SystemAuditFilterInput) (*huma.StreamResponse, error) {
		filter, err := systemAuditFilter(in)
		if err != nil {
			return nil, err
		}
		snapshot, err := service.RequestExport(ctx, filter, authnext.ActorIDFromContext(ctx))
		if err != nil {
			return nil, auditAPIError(err)
		}
		filename := fmt.Sprintf("audit-%s.ndjson", snapshot.GeneratedAt.Format("20060102T150405Z"))
		return &huma.StreamResponse{Body: func(stream huma.Context) {
			stream.SetHeader("Content-Type", "application/x-ndjson")
			stream.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
			stream.SetHeader("Cache-Control", "no-store")
			if err := service.WriteExport(stream.Context(), snapshot, stream.BodyWriter()); err != nil {
				slog.Error("system audit export stream failed", "error", err)
			}
		}}, nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "audit-system-verify-integrity", Method: http.MethodGet, Path: "/system/audit/integrity-verifications",
		Summary: "Verify the system audit hash chain", Tags: []string{"audit"}, Errors: []int{422, 500},
	}, authz.Guard{Resource: "system_audit_event", Verb: "view"}, func(ctx context.Context, in *systemAuditIntegrityInput) (*respx.Body[Integrity], error) {
		if err := rejectSystemAuditTenant(in.TenantSelector); err != nil {
			return nil, err
		}
		integrity, err := service.Verify(ctx, 0)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, integrity), nil
	})
}
