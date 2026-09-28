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
audit-anchor-current|POST|/tenants/{tenant_id}/audit/anchors
audit-export-events|GET|/tenants/{tenant_id}/audit/exports
audit-get-event|GET|/tenants/{tenant_id}/audit/events/{event_id}
audit-get-governance|GET|/tenants/{tenant_id}/audit/governance-policy
audit-list-events|GET|/tenants/{tenant_id}/audit/events
audit-set-retention|PUT|/tenants/{tenant_id}/audit/retention-policy
audit-sign-root|POST|/tenants/{tenant_id}/audit/root-signatures
audit-verify-integrity|GET|/tenants/{tenant_id}/audit/integrity-verifications
audit-archive-status|GET|/tenants/{tenant_id}/audit/archive-status
audit-list-archives|GET|/tenants/{tenant_id}/audit/archives
audit-request-archive|POST|/tenants/{tenant_id}/audit/archives
audit-retry-archive|POST|/tenants/{tenant_id}/audit/archives/{archive_id}/attempts
audit-verify-archive|POST|/tenants/{tenant_id}/audit/archives/{archive_id}/verifications
audit-download-archive|GET|/tenants/{tenant_id}/audit/archives/{archive_id}/content
audit-platform-archive-status|GET|/system/audit/archive-status
audit-platform-list-archives|GET|/system/audit/archives
audit-platform-request-archive|POST|/system/audit/archives
audit-platform-retry-archive|POST|/system/audit/archives/{archive_id}/attempts
audit-platform-verify-archive|POST|/system/audit/archives/{archive_id}/verifications
audit-platform-download-archive|GET|/system/audit/archives/{archive_id}/content
audit-platform-set-retention|PUT|/system/audit/retention-policy
audit-system-list-events|GET|/system/audit/events
audit-system-get-event|GET|/system/audit/events/{event_id}
audit-system-export-events|GET|/system/audit/exports
audit-system-verify-integrity|GET|/system/audit/integrity-verifications`

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
	"audit-system-list-events":        "system_audit_event_view",
	"audit-system-get-event":          "system_audit_event_view",
	"audit-system-export-events":      "system_audit_event_export",
	"audit-system-verify-integrity":   "system_audit_event_view",
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
	manifest := make([]string, 0, 25)
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
	require.Len(t, got, 25)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))

	for path, item := range api.OpenAPI().Paths {
		for method, operation := range auditPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			// The platform copy reads the same record at a wider scope, so it
			// belongs beside the tenant one rather than in a section named
			// after who may call it.
			assert.Equal(t, []string{"audit"}, operation.Tags, location)
			assert.Equal(t, auditGuards[operation.OperationID], operation.Extensions[authz.GuardExtensionKey], location)
			assert.NotEmpty(t, operation.Security, location)
			var tenantPaths int
			for _, parameter := range operation.Parameters {
				if parameter.Name == "tenant_id" && parameter.In == "path" {
					tenantPaths++
				}
			}
			// Platform routes have no tenant path parameter; tenant routes require
			// exactly one so their authorization context is explicit in the URL.
			wantTenantPaths := 1
			if strings.HasPrefix(path, "/system/") {
				wantTenantPaths = 0
			}
			assert.Equal(t, wantTenantPaths, tenantPaths, location)
		}
	}
	export := api.OpenAPI().Paths["/tenants/{tenant_id}/audit/exports"].Get
	assert.Contains(t, export.Responses["200"].Content, "application/x-ndjson")
	assert.Contains(t, export.Responses, "409")
	assert.Contains(t, export.Responses, "422")
	assert.Contains(t, export.Responses, "500")
	assert.Contains(t, api.OpenAPI().Paths["/tenants/{tenant_id}/audit/events/{event_id}"].Get.Responses, "404")
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
	headerA := "/tenants/" + tenantA.String()
	headerB := "/tenants/" + tenantB.String()

	response := api.Get(headerA + "/audit/events?event_type=client_created&limit=25")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var list struct {
		Data []Event `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Data, 1)
	assert.Equal(t, event.ID, list.Data[0].ID)
	assert.Equal(t, http.StatusOK, api.Get(headerA+"/audit/events/"+event.ID.String()).Code)
	assert.Equal(t, http.StatusNotFound, api.Get(headerB+"/audit/events/"+event.ID.String()).Code)
	assert.Equal(t, http.StatusOK, api.Get(headerA+"/audit/integrity-verifications").Code)
	assert.Equal(t, http.StatusOK, api.Get(headerB+"/audit/integrity-verifications").Code)

	exported := api.Get(headerA + "/audit/exports?event_type=client_created")
	require.Equal(t, http.StatusOK, exported.Code, exported.Body.String())
	assert.Equal(t, "application/x-ndjson", exported.Header().Get("Content-Type"))
	assert.Regexp(t, `^attachment; filename="audit-[0-9]{8}T[0-9]{6}Z.ndjson"$`, exported.Header().Get("Content-Disposition"))
	assert.Equal(t, "no-store", exported.Header().Get("Cache-Control"))
	assert.Contains(t, exported.Body.String(), "client_created")
	assert.Equal(t, int64(2), h.count(t, "iam_audit_events", tenantA))
	assert.Zero(t, h.count(t, "iam_audit_events", tenantB))

	retention := api.Put(headerA+"/audit/retention-policy", map[string]any{
		"min_days": 90, "archive_after_days": 180,
	})
	require.Equal(t, http.StatusOK, retention.Code, retention.Body.String())
	assert.Contains(t, retention.Body.String(), `"min_days":90`)
	governanceA := api.Get(headerA + "/audit/governance-policy")
	require.Equal(t, http.StatusOK, governanceA.Code, governanceA.Body.String())
	assert.Contains(t, governanceA.Body.String(), `"min_days":90`)
	governanceB := api.Get(headerB + "/audit/governance-policy")
	require.Equal(t, http.StatusOK, governanceB.Code, governanceB.Body.String())
	assert.Contains(t, governanceB.Body.String(), `"min_days":365`)
	assert.Zero(t, h.count(t, "iam_audit_retention_policies", tenantB))

	assert.Equal(t, http.StatusServiceUnavailable, api.Post(headerA+"/audit/anchors").Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post(headerA+"/audit/root-signatures").Code)
}

func TestAuditHTTPValidationIntegrityAndStorageFailures(t *testing.T) {
	h, api := newAuditHTTPHarness(t)
	header := "/tenants/801"
	invalidTenant := "/tenants/invalid"
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(invalidTenant+"/audit/events").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(invalidTenant+"/audit/events/1").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(invalidTenant+"/audit/integrity-verifications").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post(invalidTenant+"/audit/anchors").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(invalidTenant+"/audit/governance-policy").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Put(invalidTenant+"/audit/retention-policy", map[string]any{
		"min_days": 90, "archive_after_days": 180,
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post(invalidTenant+"/audit/root-signatures").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(header+"/audit/events?from=invalid").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(header+"/audit/events?to=invalid").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(header+"/audit/events/not-an-id").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(header+"/audit/events?principal_id=bad").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(header+"/audit/events?target_id=bad").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get(
		header+"/audit/exports?from=2026-09-03T20:00:00Z&to=2026-09-02T20:00:00Z",
	).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Put(header+"/audit/retention-policy", map[string]any{
		"min_days": 90, "archive_after_days": 89,
	}).Code)

	event, err := h.service.Append(t.Context(), EventInput{TenantID: 801, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("DROP TRIGGER "+h.prefix+"trg_iam_audit_events_no_update").Error)
	require.NoError(t, h.db.Exec(
		"UPDATE "+h.table("iam_audit_events")+" SET event_hash = 'tampered' WHERE id = ?", event.ID,
	).Error)
	broken := api.Get(header + "/audit/exports")
	assert.Equal(t, http.StatusConflict, broken.Code, broken.Body.String())
	assert.Contains(t, broken.Body.String(), auditi18n.IntegrityFailed)

	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_audit_events")).Error)
	assert.Equal(t, http.StatusInternalServerError, api.Get(header+"/audit/events").Code)
	assert.Equal(t, http.StatusInternalServerError, api.Get(header+"/audit/events/1").Code)
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

	id, err := guid.RequireID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)
	id, err = guid.OptionalID(" ")
	require.NoError(t, err)
	assert.Zero(t, id)
	_, err = guid.RequireID("bad")
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
	header := "/tenants/1201"
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post(header+"/audit/anchors").Code)
	_, err := service.Append(t.Context(), EventInput{TenantID: 1201, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	anchored := api.Post(header + "/audit/anchors")
	require.Equal(t, http.StatusOK, anchored.Code, anchored.Body.String())
	assert.Contains(t, anchored.Body.String(), `"head_sequence":1`)
	signed := api.Post(header + "/audit/root-signatures")
	require.Equal(t, http.StatusOK, signed.Code, signed.Body.String())
	assert.Contains(t, signed.Body.String(), `"root_signature"`)
}

// TestErrorMappersKeepChosenStatus is this package's half of the set-wide rule in
// tests/architecture (domainErrorMappers): an error that already chose its
// status, headers included, leaves every mapper unchanged instead of becoming 500.
func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.ErrorWithHeaders(huma.Error422UnprocessableEntity(respx.InvalidID), http.Header{"Retry-After": {"1"}})
	for name, mapper := range map[string]func(error) error{
		"auditAPIError": auditAPIError,
	} {
		mapped := mapper(chosen)
		var status huma.StatusError
		require.ErrorAs(t, mapped, &status, name)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(), name)
		var headers huma.HeadersError
		require.ErrorAs(t, mapped, &headers, name)
		assert.Equal(t, "1", headers.GetHeaders().Get("Retry-After"), name)
	}
}
