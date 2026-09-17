package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type listInput struct {
	TenantID    string `header:"X-Tenant-Id" maxLength:"128"`
	PrincipalID string `query:"principal_id" maxLength:"64"`
	EventType   string `query:"event_type" maxLength:"64"`
	Outcome     string `query:"outcome" enum:"success,denied,failure"`
	TargetType  string `query:"target_type" maxLength:"64"`
	TargetID    string `query:"target_id" maxLength:"128"`
	From        string `query:"from" maxLength:"40" doc:"inclusive RFC3339 timestamp"`
	To          string `query:"to" maxLength:"40" doc:"exclusive RFC3339 timestamp"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Limit       int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type eventInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	ID       string `path:"id" maxLength:"64"`
}

type tenantInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
}

type exportInput struct {
	TenantID    string `header:"X-Tenant-Id" maxLength:"128"`
	PrincipalID string `query:"principal_id" maxLength:"64"`
	EventType   string `query:"event_type" maxLength:"64"`
	Outcome     string `query:"outcome" enum:"success,denied,failure"`
	TargetType  string `query:"target_type" maxLength:"64"`
	TargetID    string `query:"target_id" maxLength:"128"`
	From        string `query:"from" maxLength:"40" doc:"inclusive RFC3339 timestamp"`
	To          string `query:"to" maxLength:"40" doc:"exclusive RFC3339 timestamp"`
}

// governanceInput bounds the anchor chain the governance report carries. The
// chain gains an entry on every anchoring run and an omitted limit used to
// return all of it; the rest of the report is a fixed-size summary.
type governanceInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type retentionInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	Body     struct {
		MinDays          int `json:"min_days" minimum:"1" maximum:"36500" example:"365" doc:"minimum days audit events must be retained"`
		ArchiveAfterDays int `json:"archive_after_days" minimum:"1" maximum:"36500" example:"730" doc:"days after which events become archive-eligible; must not be earlier than min_days"`
	}
}

func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	registerArchiveHTTP(api, service, registrar, false)
	registerArchiveHTTP(api, service, registrar, true)
	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-list-events", Method: http.MethodGet, Path: "/iam/audit-events",
		Summary: "List tenant audit events", Tags: []string{"audit"},
		Errors: []int{http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "view"}, func(ctx context.Context, in *listInput) (*respx.Body[[]Event], error) {
		filter, err := listFilter(in)
		if err != nil {
			return nil, err
		}
		events, total, err := service.List(ctx, filter)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.List(ctx, events, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(events), Total: total}), nil
	})

	authz.Register(registrar, api, exportOperation(), authz.Guard{Resource: "audit_event", Verb: "export"}, func(ctx context.Context, in *exportInput) (*huma.StreamResponse, error) {
		filter, err := exportFilter(in)
		if err != nil || validateFilter(filter, false) != nil {
			return nil, huma.Error422UnprocessableEntity(auditi18n.InvalidTime)
		}
		snapshot, err := service.RequestExport(ctx, filter, authnext.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, auditAPIError(err)
		}
		filename := fmt.Sprintf("audit-%s.ndjson", snapshot.GeneratedAt.Format("20060102T150405Z"))
		return &huma.StreamResponse{Body: func(stream huma.Context) {
			stream.SetHeader("Content-Type", "application/x-ndjson")
			stream.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
			stream.SetHeader("Cache-Control", "no-store")
			if err := service.WriteExport(stream.Context(), snapshot, stream.BodyWriter()); err != nil {
				slog.Error("audit export stream failed", "tenant_id", in.TenantID, "error", err)
			}
		}}, nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-get-event", Method: http.MethodGet, Path: "/iam/audit-events/{id}",
		Summary: "Get a tenant audit event", Tags: []string{"audit"},
		Errors: []int{http.StatusNotFound, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "view"}, func(ctx context.Context, in *eventInput) (*respx.Body[Event], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		id, err := parseAuditID(in.ID)
		if err != nil {
			return nil, err
		}
		event, err := service.Get(ctx, tenantID, id)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, event), nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-verify-integrity", Method: http.MethodGet, Path: "/iam/audit-integrity",
		Summary: "Verify the tenant audit hash chain", Tags: []string{"audit"},
		Errors: []int{http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "view"}, func(ctx context.Context, in *tenantInput) (*respx.Body[Integrity], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		result, err := service.Verify(ctx, tenantID)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-anchor-current", Method: http.MethodPost, Path: "/iam/audit-anchor",
		Summary: "Anchor the current verified audit head into the WORM store", Tags: []string{"audit"},
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "anchor"}, func(ctx context.Context, in *tenantInput) (*respx.Body[Anchor], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		anchor, err := service.Anchor(ctx, tenantID)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, anchor), nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-get-governance", Method: http.MethodGet, Path: "/iam/audit/governance",
		Summary: "Get WORM governance status: retention policy, chain integrity and anchor chain", Tags: []string{"audit"},
		Errors: []int{http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "view"}, func(ctx context.Context, in *governanceInput) (*respx.Body[Governance], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		report, total, err := service.GovernancePage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.List(ctx, report, respx.Page{
			Offset: in.Offset, Limit: in.Limit, Count: len(report.Anchors), Total: total,
		}), nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-set-retention", Method: http.MethodPut, Path: "/iam/audit/retention",
		Summary: "Configure the tenant audit retention policy", Tags: []string{"audit"},
		Errors: []int{http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "manage"}, func(ctx context.Context, in *retentionInput) (*respx.Body[RetentionPolicy], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		policy, err := service.SetRetentionPolicy(ctx, tenantID, in.Body.MinDays, in.Body.ArchiveAfterDays)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, policy), nil
	})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "audit-sign-root", Method: http.MethodPost, Path: "/iam/audit/roots/sign",
		Summary: "Anchor and sign the current verified audit head as a WORM root commitment", Tags: []string{"audit"},
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, authz.Guard{Resource: "audit_event", Verb: "manage"}, func(ctx context.Context, in *tenantInput) (*respx.Body[Anchor], error) {
		tenantID, err := parseAuditID(in.TenantID)
		if err != nil {
			return nil, err
		}
		anchor, err := service.SignRoot(ctx, tenantID)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, anchor), nil
	})
}

func parseAuditID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func parseOptionalAuditID(value string) (guid.ID, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return parseAuditID(value)
}

func listFilter(in *listInput) (Filter, error) {
	filter, err := filterValues(in.TenantID, in.PrincipalID, in.EventType, in.Outcome, in.TargetType, in.TargetID, in.From, in.To)
	if err != nil {
		return Filter{}, err
	}
	filter.Offset, filter.Limit = in.Offset, in.Limit
	return filter, nil
}

func exportFilter(in *exportInput) (Filter, error) {
	return filterValues(in.TenantID, in.PrincipalID, in.EventType, in.Outcome, in.TargetType, in.TargetID, in.From, in.To)
}

func filterValues(tenant, principal, eventType, outcome, targetType, target, from, to string) (Filter, error) {
	tenantID, err := parseAuditID(tenant)
	if err != nil {
		return Filter{}, err
	}
	principalID, err := parseOptionalAuditID(principal)
	if err != nil {
		return Filter{}, err
	}
	targetID, err := parseOptionalAuditID(target)
	if err != nil {
		return Filter{}, err
	}
	filter := Filter{TenantID: tenantID, PrincipalID: principalID, EventType: eventType, Outcome: outcome, TargetType: targetType, TargetID: targetID}
	if filter.From, err = optionalTime(from); err != nil {
		return Filter{}, huma.Error422UnprocessableEntity(auditi18n.InvalidTime)
	}
	if filter.To, err = optionalTime(to); err != nil {
		return Filter{}, huma.Error422UnprocessableEntity(auditi18n.InvalidTime)
	}
	return filter, nil
}

func optionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func exportOperation() huma.Operation {
	return huma.Operation{
		OperationID: "audit-export-events", Method: http.MethodGet, Path: "/iam/audit-events/export",
		Summary: "Export a verified tenant audit snapshot", Tags: []string{"audit"},
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "A newline-delimited JSON audit snapshot ending with a completion record",
				Content: map[string]*huma.MediaType{
					"application/x-ndjson": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
				},
			},
		},
	}
}

func auditAPIError(err error) error {
	switch {
	case errors.Is(err, ErrArchiveNotFound):
		return huma.Error404NotFound(auditi18n.ArchiveNotFound)
	case errors.Is(err, ErrArchiveInvalid):
		return huma.Error422UnprocessableEntity(auditi18n.InvalidQuery)
	case errors.Is(err, ErrArchiveDisabled):
		return huma.Error503ServiceUnavailable(auditi18n.ArchiveDisabled)
	case errors.Is(err, ErrArchiveEmpty):
		return huma.Error422UnprocessableEntity(auditi18n.ArchiveEmpty)
	case errors.Is(err, ErrArchiveBusy):
		return huma.Error409Conflict(auditi18n.ArchiveBusy)
	case errors.Is(err, ErrArchiveIntegrity):
		return huma.Error409Conflict(auditi18n.ArchiveIntegrity)
	case errors.Is(err, ErrArchiveUnavailable):
		return huma.Error503ServiceUnavailable(auditi18n.ArchiveUnavailable)
	case errors.Is(err, gorm.ErrRecordNotFound):
		return huma.Error404NotFound(auditi18n.EventNotFound)
	case errors.Is(err, ErrInvalidEvent), errors.Is(err, ErrInvalidFilter):
		return huma.Error422UnprocessableEntity(auditi18n.InvalidQuery)
	case errors.Is(err, ErrIntegrity):
		return huma.Error409Conflict(auditi18n.IntegrityFailed)
	case errors.Is(err, ErrAnchorDisabled):
		return huma.Error503ServiceUnavailable(auditi18n.AnchorNotEnabled)
	case errors.Is(err, ErrAnchorEmpty):
		return huma.Error422UnprocessableEntity(auditi18n.AnchorEmpty)
	case errors.Is(err, ErrAnchorAlreadyExists):
		return huma.Error409Conflict(auditi18n.AnchorAlreadyExists)
	case errors.Is(err, ErrAnchorUnavailable):
		return huma.Error503ServiceUnavailable(auditi18n.AnchorUnavailable)
	case errors.Is(err, ErrRetentionInvalid):
		return huma.Error422UnprocessableEntity(auditi18n.RetentionInvalid)
	case errors.Is(err, ErrRootSigningDisabled):
		return huma.Error422UnprocessableEntity(auditi18n.RootSigningDisabled)
	default:
		return huma.Error500InternalServerError(auditi18n.Unavailable)
	}
}
