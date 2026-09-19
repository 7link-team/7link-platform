package audit

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type archiveListInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// Platform routes never derive their scope from a caller-controlled tenant
// header. Reject it rather than silently accepting an ambiguous client request.
func archiveTenant(platform bool, raw string) (guid.ID, error) {
	if platform {
		if raw != "" {
			return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
		}
		return 0, nil
	}
	id, err := parseAuditID(raw)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func registerArchiveOperation[I, O any](api huma.API, registrar *authz.Registrar, platform bool, verb string, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	op.Tags = []string{"audit"}
	op.Errors = []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, http.StatusInternalServerError}
	if platform {
		op.OperationID = strings.Replace(op.OperationID, "audit-", "audit-platform-", 1)
		op.Path = "/platform" + op.Path
		op.Summary = "Platform: " + op.Summary
		authz.RegisterPlatform(registrar, api, op, authz.Guard{Resource: "platform", Verb: "administer"}, handler)
		documentPlatformScopeWithoutTenantHeader(api, op.Method, op.Path)
		return
	}
	authz.Register(registrar, api, op, authz.Guard{Resource: "audit_event", Verb: verb}, handler)
}

// documentPlatformScopeWithoutTenantHeader drops the tenant selector the shared
// input type declares for both scopes. The platform handler still refuses a
// caller-supplied tenant, so publishing the parameter would describe a request
// that can only ever be answered with 422.
func documentPlatformScopeWithoutTenantHeader(api huma.API, method, path string) {
	item, ok := api.OpenAPI().Paths[path]
	if !ok || item == nil {
		return
	}
	var operation *huma.Operation
	switch method {
	case http.MethodGet:
		operation = item.Get
	case http.MethodPut:
		operation = item.Put
	case http.MethodPost:
		operation = item.Post
	case http.MethodPatch:
		operation = item.Patch
	case http.MethodDelete:
		operation = item.Delete
	}
	if operation == nil {
		return
	}
	kept := make([]*huma.Param, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		if parameter.In == "header" && parameter.Name == authz.TenantHeader {
			continue
		}
		kept = append(kept, parameter)
	}
	operation.Parameters = kept
}

func registerArchiveHTTP(api huma.API, service *Service, registrar *authz.Registrar, platform bool) {
	registerArchiveOperation(api, registrar, platform, "view", huma.Operation{
		OperationID: "audit-archive-status", Method: http.MethodGet, Path: "/audit/archive-status", Summary: "Read audit archive execution status",
	}, func(ctx context.Context, in *tenantInput) (*respx.Body[ArchiveStatus], error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		status, err := service.archiveStatus(ctx, tenant)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, status), nil
	})
	registerArchiveOperation(api, registrar, platform, "view", huma.Operation{
		OperationID: "audit-list-archives", Method: http.MethodGet, Path: "/audit/archives", Summary: "List durable audit archive jobs",
	}, func(ctx context.Context, in *archiveListInput) (*respx.Body[[]ArchiveJob], error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		jobs, err := service.listArchiveJobs(ctx, tenant, in.Offset, in.Limit)
		if err != nil {
			return nil, auditAPIError(err)
		}
		repo, err := service.pinnedRepository()
		if err != nil {
			return nil, auditAPIError(err)
		}
		total, err := repo.archiveJobCount(ctx, tenant)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.List(ctx, jobs, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(jobs), Total: total}), nil
	})
	registerArchiveOperation(api, registrar, platform, "export", huma.Operation{
		OperationID: "audit-request-archive", Method: http.MethodPost, Path: "/audit/archives", Summary: "Queue the next eligible audit archive segment",
	}, func(ctx context.Context, in *tenantInput) (*respx.Body[ArchiveJob], error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		job, err := service.requestArchive(ctx, tenant)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, job), nil
	})
	registerArchiveOperation(api, registrar, platform, "export", huma.Operation{
		OperationID: "audit-retry-archive", Method: http.MethodPost, Path: "/audit/archives/{archive_id}/attempts", Summary: "Create a new attempt for a failed or expired audit archive job",
	}, func(ctx context.Context, in *archiveInput) (*respx.Body[ArchiveJob], error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		id, err := parseAuditID(in.ID)
		if err != nil {
			return nil, err
		}
		job, err := service.retryArchive(ctx, tenant, id)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, job), nil
	})
	registerArchiveOperation(api, registrar, platform, "view", huma.Operation{
		OperationID: "audit-verify-archive", Method: http.MethodPost, Path: "/audit/archives/{archive_id}/verifications", Summary: "Create an archived-object verification",
	}, func(ctx context.Context, in *archiveInput) (*respx.Body[ArchiveObject], error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		id, err := parseAuditID(in.ID)
		if err != nil {
			return nil, err
		}
		object, err := service.verifyArchiveJob(ctx, tenant, id)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return respx.OK(ctx, object), nil
	})
	download := exportOperation()
	download.OperationID, download.Path, download.Summary = "audit-download-archive", "/audit/archives/{archive_id}/content", "Download the verified immutable audit archive content"
	registerArchiveOperation(api, registrar, platform, "export", download, func(ctx context.Context, in *archiveInput) (*huma.StreamResponse, error) {
		tenant, err := archiveTenant(platform, in.TenantID)
		if err != nil {
			return nil, err
		}
		id, err := parseAuditID(in.ID)
		if err != nil {
			return nil, err
		}
		object, err := service.requestArchiveDownload(ctx, tenant, id)
		if err != nil {
			return nil, auditAPIError(err)
		}
		return &huma.StreamResponse{Body: func(stream huma.Context) {
			stream.SetHeader("Content-Type", "application/x-ndjson")
			stream.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-archive-%s.ndjson"`, id.String()))
			stream.SetHeader("Cache-Control", "no-store")
			stream.SetHeader("Content-Length", strconv.FormatInt(object.Size, 10))
			if err := service.writeArchiveDownload(stream.Context(), object, stream.BodyWriter()); err != nil {
				slog.ErrorContext(stream.Context(), "audit archive download failed", "job_id", id.String(), "error_code", archiveFailureCode(err))
			}
		}}, nil
	})
	if platform {
		registerArchiveOperation(api, registrar, true, "manage", huma.Operation{
			OperationID: "audit-set-retention", Method: http.MethodPut, Path: "/audit/retention-policy", Summary: "Configure platform audit retention policy",
		}, func(ctx context.Context, in *retentionInput) (*respx.Body[RetentionPolicy], error) {
			tenant, err := archiveTenant(true, in.TenantID)
			if err != nil {
				return nil, err
			}
			policy, err := service.setRetentionPolicy(ctx, tenant, in.Body.MinDays, in.Body.ArchiveAfterDays)
			if err != nil {
				return nil, auditAPIError(err)
			}
			return respx.OK(ctx, policy), nil
		})
	}
}
