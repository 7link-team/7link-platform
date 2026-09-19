package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const auditRouteManifest = `
audit-anchor-current|POST|/audit/anchors
audit-export-events|GET|/audit/exports
audit-get-event|GET|/audit/events/{event_id}
audit-get-governance|GET|/audit/governance-policy
audit-list-events|GET|/audit/events
audit-set-retention|PUT|/audit/retention-policy
audit-sign-root|POST|/audit/root-signatures
audit-verify-integrity|GET|/audit/integrity-verifications
audit-archive-status|GET|/audit/archive-status
audit-list-archives|GET|/audit/archives
audit-request-archive|POST|/audit/archives
audit-retry-archive|POST|/audit/archives/{archive_id}/retry
audit-verify-archive|POST|/audit/archives/{archive_id}/verify
audit-download-archive|GET|/audit/archives/{archive_id}/download
audit-platform-archive-status|GET|/platform/audit/archive-status
audit-platform-list-archives|GET|/platform/audit/archives
audit-platform-request-archive|POST|/platform/audit/archives
audit-platform-retry-archive|POST|/platform/audit/archives/{archive_id}/retry
audit-platform-verify-archive|POST|/platform/audit/archives/{archive_id}/verify
audit-platform-download-archive|GET|/platform/audit/archives/{archive_id}/download
audit-platform-set-retention|PUT|/platform/audit/retention`

var auditGuards = map[string]string{
	"audit-anchor-current":            "audit_event_anchor",
	"audit-export-events":             "audit_event_export",
	"audit-get-event":                 "audit_event_view",
	"audit-get-governance":            "audit_event_view",
	"audit-list-events":               "audit_event_view",
	"audit-set-retention":             "audit_event_manage",
	"audit-sign-root":                 "audit_event_manage",
	"audit-verify-integrity":          "audit_event_view",
	"audit-archive-status":            "audit_event_view",
	"audit-list-archives":             "audit_event_view",
	"audit-request-archive":           "audit_event_export",
	"audit-retry-archive":             "audit_event_export",
	"audit-verify-archive":            "audit_event_view",
	"audit-download-archive":          "audit_event_export",
	"audit-platform-archive-status":   "platform_administer",
	"audit-platform-list-archives":    "platform_administer",
	"audit-platform-request-archive":  "platform_administer",
	"audit-platform-retry-archive":    "platform_administer",
	"audit-platform-verify-archive":   "platform_administer",
	"audit-platform-download-archive": "platform_administer",
	"audit-platform-set-retention":    "platform_administer",
}

func auditPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	}
}

func auditOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 21)
	for path, item := range spec.Paths {
		for method, operation := range auditPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func auditDeclarationAPI(t *testing.T) huma.API {
	t.Helper()
	respx.Install()
	_, api := humatest.New(t)
	RegisterHTTP(api, nil, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	return api
}

func TestAuditRouteManifestAndOpenAPIContract(t *testing.T) {
	api := auditDeclarationAPI(t)
	want, got := strings.Fields(auditRouteManifest), auditOperationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 21)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))

	for path, item := range api.OpenAPI().Paths {
		for method, operation := range auditPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			assert.Equal(t, []string{"audit"}, operation.Tags, location)
			assert.Equal(t, auditGuards[operation.OperationID], operation.Extensions[authz.GuardExtensionKey], location)
			assert.NotEmpty(t, operation.Security, location)
			var tenantHeaders int
			for _, parameter := range operation.Parameters {
				if parameter.Name == authz.TenantHeader && parameter.In == "header" {
					tenantHeaders++
				}
			}
			// Platform routes never derive their scope from a caller-controlled
			// tenant header, so the document must not offer them one either: a
			// platform archive request that sends it is answered with 422.
			wantTenantHeaders := 1
			if strings.HasPrefix(path, "/platform/") {
				wantTenantHeaders = 0
			}
			assert.Equal(t, wantTenantHeaders, tenantHeaders, location)
		}
	}
	export := api.OpenAPI().Paths["/audit/exports"].Get
	assert.Contains(t, export.Responses["200"].Content, "application/x-ndjson")
	assert.Contains(t, export.Responses, "409")
	assert.Contains(t, export.Responses, "422")
	assert.Contains(t, export.Responses, "500")
	assert.Contains(t, api.OpenAPI().Paths["/audit/events/{event_id}"].Get.Responses, "404")
}

func newAuditHTTPHarness(t *testing.T) (*auditHarness, humatest.TestAPI) {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, auditi18n.Register())
	h := newAuditHarness(t, "sl_")
	_, api := humatest.New(t)
	RegisterHTTP(api, h.service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	return h, api
}

func TestAuditHTTPTenantIsolationAndLifecycle(t *testing.T) {
	h, api := newAuditHTTPHarness(t)
	tenantA, tenantB := guid.ID(701), guid.ID(702)
	event, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantA, PrincipalID: 703, EventType: "client_created",
		TargetType: "oauth_client", TargetID: 704, Outcome: "success",
	})
	require.NoError(t, err)
	headerA := authz.TenantHeader + ": " + tenantA.String()
	headerB := authz.TenantHeader + ": " + tenantB.String()

	response := api.Get("/audit/events?event_type=client_created&limit=25", headerA)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var list struct {
		Data []Event `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Data, 1)
	assert.Equal(t, event.ID, list.Data[0].ID)
	assert.Equal(t, http.StatusOK, api.Get("/audit/events/"+event.ID.String(), headerA).Code)
	assert.Equal(t, http.StatusNotFound, api.Get("/audit/events/"+event.ID.String(), headerB).Code)
	assert.Equal(t, http.StatusOK, api.Get("/audit/integrity-verifications", headerA).Code)
	assert.Equal(t, http.StatusOK, api.Get("/audit/integrity-verifications", headerB).Code)

	exported := api.Get("/audit/exports?event_type=client_created", headerA)
	require.Equal(t, http.StatusOK, exported.Code, exported.Body.String())
	assert.Equal(t, "application/x-ndjson", exported.Header().Get("Content-Type"))
	assert.Regexp(t, `^attachment; filename="audit-[0-9]{8}T[0-9]{6}Z.ndjson"$`, exported.Header().Get("Content-Disposition"))
	assert.Equal(t, "no-store", exported.Header().Get("Cache-Control"))
	assert.Contains(t, exported.Body.String(), "client_created")
	assert.Equal(t, int64(2), h.count(t, "iam_audit_events", tenantA))
	assert.Zero(t, h.count(t, "iam_audit_events", tenantB))

	retention := api.Put("/audit/retention-policy", headerA, map[string]any{
		"min_days": 90, "archive_after_days": 180,
	})
	require.Equal(t, http.StatusOK, retention.Code, retention.Body.String())
	assert.Contains(t, retention.Body.String(), `"min_days":90`)
	governanceA := api.Get("/audit/governance-policy", headerA)
	require.Equal(t, http.StatusOK, governanceA.Code, governanceA.Body.String())
	assert.Contains(t, governanceA.Body.String(), `"min_days":90`)
	governanceB := api.Get("/audit/governance-policy", headerB)
	require.Equal(t, http.StatusOK, governanceB.Code, governanceB.Body.String())
	assert.Contains(t, governanceB.Body.String(), `"min_days":365`)
	assert.Zero(t, h.count(t, "iam_audit_retention_policies", tenantB))

	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/audit/anchors", headerA).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/audit/root-signatures", headerA).Code)
}

func TestAuditHTTPValidationIntegrityAndStorageFailures(t *testing.T) {
	h, api := newAuditHTTPHarness(t)
	header := authz.TenantHeader + ": 801"
	invalidTenant := authz.TenantHeader + ": invalid"
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events/1", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/integrity-verifications", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/audit/anchors", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/governance-policy", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Put("/audit/retention-policy", invalidTenant, map[string]any{
		"min_days": 90, "archive_after_days": 180,
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/audit/root-signatures", invalidTenant).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events?from=invalid", header).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events?to=invalid", header).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events/not-an-id", header).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events?principal_id=bad", header).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/audit/events?target_id=bad", header).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(
		"/audit/exports?from=2026-09-03T20:00:00Z&to=2026-09-02T20:00:00Z", header,
	).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Put("/audit/retention-policy", header, map[string]any{
		"min_days": 90, "archive_after_days": 89,
	}).Code)

	event, err := h.service.Append(t.Context(), EventInput{TenantID: 801, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("DROP TRIGGER "+h.prefix+"trg_iam_audit_events_no_update").Error)
	require.NoError(t, h.db.Exec(
		"UPDATE "+h.table("iam_audit_events")+" SET event_hash = 'tampered' WHERE id = ?", event.ID,
	).Error)
	broken := api.Get("/audit/exports", header)
	assert.Equal(t, http.StatusConflict, broken.Code, broken.Body.String())
	assert.Contains(t, broken.Body.String(), auditi18n.IntegrityFailed)

	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_audit_events")).Error)
	assert.Equal(t, http.StatusInternalServerError, api.Get("/audit/events", header).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Get("/audit/events/1", header).Code)
}

func TestAuditAPIErrorMappingsAndFilterHelpers(t *testing.T) {
	tests := []struct {
		err    error
		status int
		key    string
	}{
		{gorm.ErrRecordNotFound, 404, auditi18n.EventNotFound},
		{ErrInvalidEvent, 422, auditi18n.InvalidQuery},
		{ErrInvalidFilter, 422, auditi18n.InvalidQuery},
		{ErrIntegrity, 409, auditi18n.IntegrityFailed},
		{ErrAnchorDisabled, 503, auditi18n.AnchorNotEnabled},
		{ErrAnchorEmpty, 422, auditi18n.AnchorEmpty},
		{ErrAnchorAlreadyExists, 409, auditi18n.AnchorAlreadyExists},
		{ErrAnchorUnavailable, 503, auditi18n.AnchorUnavailable},
		{ErrRetentionInvalid, 422, auditi18n.RetentionInvalid},
		{ErrRootSigningDisabled, 422, auditi18n.RootSigningDisabled},
		{ErrArchiveDisabled, 503, auditi18n.ArchiveDisabled},
		{ErrArchiveEmpty, 422, auditi18n.ArchiveEmpty},
		{ErrArchiveNotFound, 404, auditi18n.ArchiveNotFound},
		{ErrArchiveBusy, 409, auditi18n.ArchiveBusy},
		{ErrArchiveInvalid, 422, auditi18n.InvalidQuery},
		{ErrArchiveIntegrity, 409, auditi18n.ArchiveIntegrity},
		{ErrArchiveUnavailable, 503, auditi18n.ArchiveUnavailable},
		{errors.New("storage"), 500, auditi18n.Unavailable},
	}
	for _, test := range tests {
		var status huma.StatusError
		require.ErrorAs(t, auditAPIError(fmt.Errorf("wrapped: %w", test.err)), &status)
		assert.Equal(t, test.status, status.GetStatus())
		assert.Contains(t, status.Error(), test.key)
	}

	id, err := parseAuditID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)
	id, err = parseOptionalAuditID(" ")
	require.NoError(t, err)
	assert.Zero(t, id)
	_, err = parseAuditID("bad")
	assert.Error(t, err)
	parsed, err := optionalTime("2026-09-03T20:00:00Z")
	require.NoError(t, err)
	assert.False(t, parsed.IsZero())
	parsed, err = optionalTime("")
	require.NoError(t, err)
	assert.True(t, parsed.IsZero())
	_, err = optionalTime("bad")
	assert.Error(t, err)

	_, err = listFilter(&listInput{TenantID: "1", PrincipalID: "bad", Limit: 50})
	assert.Error(t, err)
	_, err = exportFilter(&exportInput{TenantID: "1", TargetID: "bad"})
	assert.Error(t, err)
}

func TestAuditHTTPAnchorAndSignedRootSuccess(t *testing.T) {
	h, _ := newAuditHTTPHarness(t)
	fixture := newFakeS3()
	store, _ := fakeAnchorStore(t, fixture, "audit-http")
	service := NewServiceWithAnchorAndSigner(h.runtime, store, auditTestSigner(t), newAuditTestIDs(12000))
	service.now = h.service.now
	_, api := humatest.New(t)
	RegisterHTTP(api, service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	header := authz.TenantHeader + ": 1201"
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/audit/anchors", header).Code)
	_, err := service.Append(t.Context(), EventInput{TenantID: 1201, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	anchored := api.Post("/audit/anchors", header)
	require.Equal(t, http.StatusOK, anchored.Code, anchored.Body.String())
	assert.Contains(t, anchored.Body.String(), `"head_sequence":1`)
	signed := api.Post("/audit/root-signatures", header)
	require.Equal(t, http.StatusOK, signed.Code, signed.Body.String())
	assert.Contains(t, signed.Body.String(), `"root_signature"`)
}
