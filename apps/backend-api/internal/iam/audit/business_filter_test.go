package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/require"
)

func TestBusinessAuditFilterListExportExactFacts(t *testing.T) {
	h, api := newAuditHTTPHarness(t)
	var wanted []Event
	for i := range 10 {
		detail := map[string]any{"project_id": "3", "actor_kind": "AGENT", "authority_source": "POLICY:dispatch_auto"}
		input := EventInput{TenantID: 1, PrincipalID: 8, EventType: "dispatch_auto.applied", Outcome: "success", Detail: detail}
		switch i {
		case 1:
			detail["project_id"] = 3 // Old numeric and current string IDs agree.
		case 2:
			detail["project_id"] = "33"
		case 3:
			detail["project_id"] = nil
		case 4:
			detail["actor_kind"] = "HUMAN"
		case 5:
			detail["authority_source"] = "POLICY:dispatch_auto_other"
		case 6:
			input.EventType = "dispatchXauto.applied" // '_' is literal, never LIKE.
		case 7:
			input.TenantID = 2
		case 8:
			delete(detail, "actor_kind")
		case 9:
			input.EventType = "dispatch_auto_extra.applied"
		}
		event, err := h.service.Append(t.Context(), input)
		require.NoError(t, err)
		if i < 2 {
			wanted = append(wanted, event)
		}
	}
	query := url.Values{"project_id": {"3"}, "module": {"dispatch_auto"}, "actor_kind": {"AGENT"}, "authority_source": {"POLICY:dispatch_auto"}}.Encode()
	response := api.Get("/tenants/1/audit/events?" + query + "&limit=1")
	require.Equal(t, 200, response.Code, response.Body.String())
	var page respx.Envelope[[]Event]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	require.EqualValues(t, 2, page.Meta.Page.Total, "count uses the exact same filters before pagination")
	require.Equal(t, wanted[1].ID, page.Data[0].ID)
	response = api.Get("/tenants/1/audit/events?" + query + "&limit=1&offset=1")
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Equal(t, wanted[0].ID, page.Data[0].ID)

	response = api.Get("/tenants/1/audit/exports?" + query)
	require.Equal(t, 200, response.Code, response.Body.String())
	lines := bytes.Split(bytes.TrimSpace(response.Body.Bytes()), []byte("\n"))
	require.Len(t, lines, 4, "manifest, two actual matching events, completion")
	var manifest exportManifest
	require.NoError(t, json.Unmarshal(lines[0], &manifest))
	require.EqualValues(t, 3, manifest.Filter.ProjectID)
	require.Equal(t, "dispatch_auto", manifest.Filter.Module)
	require.Equal(t, "AGENT", manifest.Filter.ActorKind)
	require.Equal(t, "POLICY:dispatch_auto", manifest.Filter.AuthoritySource)
	for i := range 2 {
		var entry exportEvent
		require.NoError(t, json.Unmarshal(lines[i+1], &entry))
		require.Equal(t, wanted[i].ID, entry.Event.ID, "export uses the list predicates in chain order")
		require.Equal(t, wanted[i].EventHash, entry.Event.EventHash)
	}
	var complete exportComplete
	require.NoError(t, json.Unmarshal(lines[3], &complete))
	require.EqualValues(t, 2, complete.ExportedEvents)
	digest := sha256.Sum256(append(append(append([]byte{}, lines[1]...), '\n'), append(lines[2], '\n')...))
	require.Equal(t, hex.EncodeToString(digest[:]), complete.ContentSHA256)
	chain, err := h.service.Verify(t.Context(), 1)
	require.NoError(t, err)
	require.True(t, chain.Valid)
	require.Equal(t, chain.HeadHash, manifest.HeadHash)
}

func TestBusinessAuditFilterValidationAndOpenAPISymmetry(t *testing.T) {
	_, api := newAuditHTTPHarness(t)
	for _, query := range []string{"project_id=bad", "project_id=0", "module=a%25", "module=a.b", "actor_kind=UNKNOWN", "authority_source=ROLE", "authority_source=UNKNOWN:one"} {
		for _, path := range []string{"events", "exports"} {
			response := api.Get("/tenants/1/audit/" + path + "?" + query)
			require.Equal(t, 422, response.Code, response.Body.String())
		}
	}
	for _, filter := range []Filter{{ProjectID: -1}, {Module: "a%b"}, {Module: strings.Repeat("a", 65)}, {ActorKind: "UNKNOWN"}, {AuthoritySource: "ROLE"}} {
		require.ErrorIs(t, validateFilter(filter, false), ErrInvalidFilter)
	}
	spec := auditDeclarationAPI(t).OpenAPI()
	for _, path := range []string{"/tenants/{tenant_id}/audit/events", "/tenants/{tenant_id}/audit/exports"} {
		parameters := map[string]bool{}
		for _, parameter := range spec.Paths[path].Get.Parameters {
			parameters[parameter.Name] = true
		}
		for _, name := range []string{"project_id", "module", "actor_kind", "authority_source"} {
			require.True(t, parameters[name], "%s %s", path, name)
		}
	}
}
