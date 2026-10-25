package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files"
	governance "7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This exercises the composed production JWT verifier, IAM membership, current
// contact classification, project provisioning and external transport boundary.
func TestDeliveryClientProjectionComposedIAMTransportAndRevocation(t *testing.T) {
	a := bootstrapComposedAppConfigured(t, func(config *platformapp.AppConfig) { config.Files.LocalRoot = t.TempDir() })
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "projection-test", "name": "Projection test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	for _, id := range []int{301, 302, 303} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("projection-%d", id), "display_name": "Projection actor", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		return err
	}))
	for _, id := range []int{302, 303} {
		require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": id, "status": "active", "created_at": now, "updated_at": now}).Error)
		require.NoError(t, db.Table("intake_contacts").Create(map[string]any{"id": id + 99, "tenant_id": 201, "kind": "PERSON", "display_name": "External contact", "principal_id": id, "state": "ACTIVE", "version": 1, "created_at": now, "updated_at": now}).Error)
	}
	owner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(owner, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token := func(id guid.ID) string {
		value, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), id, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, err)
		return value
	}
	ownerToken, clientToken, otherToken := token(301), token(302), token(303)
	router := moduleRouter(t, a)
	call := func(method, path, bearer, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		keyHash := sha256.Sum256([]byte(method + ":" + path))
		req.Header.Set("Idempotency-Key", hex.EncodeToString(keyHash[:]))
		req.Header.Set("If-None-Match", `"private-cache"`)
		req.Header.Set("Range", "bytes=0-99")
		req.Header.Set("Accept", "text/event-stream")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	created := call("POST", "/tenants/201/projects", ownerToken, `{"name":"Projection project","code":"PROJ-1","contacts":[{"intake_contact_id":"401","receives_deliveries":true,"can_accept":true}],"members":[{"principal_id":"302","role_key":"engineer"}]}`)
	require.Equal(t, 201, created.Code, created.Body.String())
	var project struct {
		Data struct {
			Entity struct {
				ID guid.ID `json:"id"`
			} `json:"entity"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &project))
	p := project.Data.Entity.ID.String()
	require.NotEqual(t, "0", p)
	shared := call("POST", "/entities/"+p+"/channels", ownerToken, `{"name":"Client shared","visibility":"CLIENT_SHARED"}`)
	require.Equal(t, 201, shared.Code, shared.Body.String())
	var sharedChannel struct {
		Data struct {
			ID guid.ID `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(shared.Body.Bytes(), &sharedChannel))
	for _, principal := range []string{"302", "303"} {
		invited := call("PUT", "/channels/"+sharedChannel.Data.ID.String()+"/members/"+principal, ownerToken, `{"external":true}`)
		require.Equal(t, 200, invited.Code, invited.Body.String())
	}
	discovery := call("GET", "/tenants/201/client-access?limit=1", clientToken, "")
	require.Equal(t, 200, discovery.Code, discovery.Body.String())
	require.Contains(t, discovery.Body.String(), `"external":true`)
	require.Contains(t, discovery.Body.String(), p)
	require.Contains(t, discovery.Body.String(), `"has_more":false`)
	for _, path := range []string{
		"/entities/" + p + "/project", "/entities/" + p + "/work-items/44", "/entities/" + p + "/executions/45",
		"/spaces/8/files/9/content", "/spaces/8/files/9/thumbnail", "/files/9/content",
		"/entities/" + p + "/artifact-previews/9/thumbnail", "/entities/" + p + "/delivery-packages/9/export",
		"/entities/" + p + "/client-issues/9/internal", "/entities/" + p + "/artifact-comments/9/attachments",
		"/tenants/201/search?q=private", "/entities/" + p + "/search?q=private", "/tenants/201/events/stream",
		"/entities/" + p + "/events/stream", "/tenants/201/export", "/entities/" + p + "/work-items/count",
		"/entities/" + p + "/delivery-packages/9/artifacts/40/thumbnail", "/entities/" + p + "/artifact-previews/count",
	} {
		t.Run(path, func(t *testing.T) {
			out := call("GET", path, clientToken, "")
			require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
			require.Equal(t, "private, no-store", out.Header().Get("Cache-Control"))
			require.Contains(t, out.Header().Values("Vary"), "Authorization")
			require.Contains(t, out.Header().Values("Vary"), "Cookie")
			require.NotContains(t, out.Body.String(), "Projection project")
		})
	}
	// A different active external contact has no project discovery or access.
	other := call("GET", "/tenants/201/client-access?limit=1", otherToken, "")
	require.Equal(t, 200, other.Code, other.Body.String())
	require.Contains(t, other.Body.String(), `"items":[]`)
	// An allowed projection route still applies the shared IAM guard: a caller
	// without any entity grant receives the established 403 before domain lookup.
	otherPackages := call("GET", "/entities/"+p+"/delivery-packages", otherToken, "")
	require.Equal(t, 403, otherPackages.Code)
	require.NotContains(t, otherPackages.Body.String(), "Projection project")
	require.NotContains(t, otherPackages.Body.String(), `"items"`)
	t.Run("pinned-file-comment-and-real-receipt", func(t *testing.T) {
		roots := call("GET", "/entities/"+p+"/files", ownerToken, "")
		require.Equal(t, 200, roots.Code, roots.Body.String())
		var rootPage struct {
			Data struct {
				Items []struct {
					ID guid.ID `json:"id"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(roots.Body.Bytes(), &rootPage))
		require.NotEmpty(t, rootPage.Data.Items)
		var upload bytes.Buffer
		form := multipart.NewWriter(&upload)
		require.NoError(t, form.WriteField("kind", "file"))
		require.NoError(t, form.WriteField("parent_id", rootPage.Data.Items[0].ID.String()))
		part, err := form.CreateFormFile("file", "source.txt")
		require.NoError(t, err)
		_, err = part.Write([]byte("first line\nRejected immutable source\n"))
		require.NoError(t, err)
		require.NoError(t, form.Close())
		req := httptest.NewRequest(http.MethodPost, "/entities/"+p+"/files", &upload)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, 201, response.Code, response.Body.String())
		var uploaded struct {
			Data struct {
				ID guid.ID `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &uploaded))
		resources, err := module.Resolve[files.ClientCollaborationResources](a.Modules().Services())
		require.NoError(t, err)
		ownerProject := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, EntityID: project.Data.Entity.ID, SubjectType: authn.SubjectTypePrincipal})
		packageHash := strings.Repeat("b", 64)
		require.NoError(t, db.Table("clientwork_packages").Create(map[string]any{"tenant_id": 201, "id": 901, "project_id": project.Data.Entity.ID, "title": "Frozen delivery", "release_notes": "Delivered bytes", "manifest": "{}", "content_hash": packageHash, "state": "PUBLISHED", "recipient_id": 302, "actor_id": 301, "version": 2, "created_at": now, "updated_at": now}).Error)
		var pin files.PinnedResource
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			candidate, err := resources.InspectOn(ownerProject, tx, uploaded.Data.ID)
			if err != nil {
				return err
			}
			pin, err = resources.PinOn(ownerProject, tx, files.ResourceBindingInput{ID: 902, SpaceID: candidate.SpaceID, FileID: candidate.FileID, ExpectedFileVersion: candidate.NodeVersion, VersionID: &candidate.VersionID, Purpose: "clientwork.package", ResourceKind: "client_package", ResourceID: 901, Slot: "artifact." + candidate.VersionID.String(), ActorID: 301})
			return err
		}))
		// This is a published-package fixture over real uploaded/pinned file bytes.
		// It intentionally asserts no execution/verification/acceptance provenance.
		manifest, err := json.Marshal(map[string]any{"bindings": []any{map[string]any{"version_id": pin.VersionID, "binding_id": pin.BindingID}}, "content": map[string]any{"title": "Frozen delivery", "release_notes": "Delivered bytes", "artifacts": []any{map[string]any{"name": pin.Name, "version_id": pin.VersionID, "sha256": pin.SHA256, "coverage": "NOT_APPLICABLE", "verification_overrides": nil}}}, "refs": []any{map[string]any{"work_item_id": "990", "result_id": "991", "evidence_id": "992"}}})
		require.NoError(t, err)
		require.NoError(t, db.Table("clientwork_packages").Where("tenant_id=201 AND id=901").Update("manifest", string(manifest)).Error)

		t.Run("actual-shared-channel-delivery", func(t *testing.T) {
			// Reuse the published fixture over actual uploaded/pinned bytes above. This
			// asserts transport/current IAM, not fabricated publication provenance.
			path := "/entities/" + p + "/delivery-packages/901/sends"
			body := `{"channel_id":"` + sharedChannel.Data.ID.String() + `"}`
			send := func(key string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+ownerToken)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", key)
				req.Header.Set("If-Match", `"2"`)
				out := httptest.NewRecorder()
				router.ServeHTTP(out, req)
				return out
			}
			first := send("actual-package-send")
			require.Equal(t, 200, first.Code, first.Body.String())
			require.Contains(t, first.Body.String(), `"state":"SUCCEEDED"`)
			replay := send("actual-package-send")
			require.Equal(t, 200, replay.Code, replay.Body.String())
			require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
			require.Equal(t, first.Body.String(), replay.Body.String())
			ledger := call("GET", path, ownerToken, "")
			require.Equal(t, 200, ledger.Code, ledger.Body.String())
			require.Contains(t, ledger.Body.String(), `"attempt_no":1`)
			require.Contains(t, ledger.Body.String(), `"recipient_id":"302"`)
			var message guid.ID
			require.NoError(t, db.Table("clientwork_package_sends").Where("tenant_id=201 AND package_id=901").Select("message_id").Scan(&message).Error)
			require.Positive(t, message)
			var fact struct {
				Body       string
				Visibility string
				AuthorID   guid.ID
			}
			require.NoError(t, db.Table("collab_messages").Where("tenant_id=201 AND id=?", message).Take(&fact).Error)
			require.Contains(t, fact.Body, packageHash)
			require.Equal(t, "CHANNEL", fact.Visibility)
			require.Equal(t, guid.ID(301), fact.AuthorID)
			require.NoError(t, db.Table("collab_channel_members").Where("tenant_id=201 AND channel_id=? AND principal_id=302", sharedChannel.Data.ID).Update("left_at", now).Error)
			revoked := send("actual-package-send")
			require.Equal(t, 404, revoked.Code, revoked.Body.String())
			require.NotContains(t, revoked.Body.String(), packageHash)
			var n int64
			require.NoError(t, db.Table("clientwork_package_sends").Where("tenant_id=201 AND package_id=901").Count(&n).Error)
			require.EqualValues(t, 1, n)
			require.NoError(t, db.Table("collab_channel_members").Where("tenant_id=201 AND channel_id=? AND principal_id=302", sharedChannel.Data.ID).Update("left_at", nil).Error)
			require.NoError(t, db.Table("delivery_project_contacts").Where("tenant_id=201 AND project_id=? AND intake_contact_id=401", project.Data.Entity.ID).Update("receives_deliveries", false).Error)
			unconfigured := send("actual-package-send")
			require.Equal(t, 404, unconfigured.Code, unconfigured.Body.String())
			require.NoError(t, db.Table("delivery_project_contacts").Where("tenant_id=201 AND project_id=? AND intake_contact_id=401", project.Data.Entity.ID).Update("receives_deliveries", true).Error)
			require.NoError(t, db.Table("intake_contacts").Where("tenant_id=201 AND id=401").Update("state", "BLOCKED").Error)
			blocked := send("actual-package-send")
			require.Equal(t, 404, blocked.Code, blocked.Body.String())
			require.NoError(t, db.Table("intake_contacts").Where("tenant_id=201 AND id=401").Update("state", "ACTIVE").Error)

		})
		contentPath := "/entities/" + p + "/delivery-packages/901/artifacts/" + pin.VersionID.String() + "/content"
		content := call("GET", contentPath, clientToken, "")
		require.Equal(t, 200, content.Code, content.Body.String())
		require.Equal(t, "first line\nRejected immutable source\n", content.Body.String())
		require.Equal(t, "private, no-store", content.Header().Get("Cache-Control"))
		for _, direct := range []string{"/files/" + pin.FileID.String(), "/files/" + pin.FileID.String() + "/content", "/files/" + pin.FileID.String() + "/thumbnail", "/spaces/" + pin.SpaceID.String() + "/files/" + pin.FileID.String() + "/content", "/entities/" + p + "/delivery-packages/901/artifacts/" + pin.VersionID.String() + "/thumbnail"} {
			denied := call("GET", direct, clientToken, "")
			require.Equal(t, 404, denied.Code, denied.Body.String())
			require.NotContains(t, denied.Body.String(), "Rejected immutable source")
			require.Equal(t, "private, no-store", denied.Header().Get("Cache-Control"))
		}
		digest := sha256.Sum256([]byte("901:" + packageHash + ":0"))
		item := hex.EncodeToString(digest[:])
		path := "/entities/" + p + "/delivery-packages/901/items/" + item + "/comments"
		made := call("POST", path, clientToken, `{"start_line":2,"end_line":2,"body":"This frozen line requires correction"}`)
		require.Equal(t, 201, made.Code, made.Body.String())
		var source struct {
			ThreadID       guid.ID
			AnchorSnapshot string
			TargetID       *guid.ID
		}
		require.NoError(t, db.Table("execution_client_comment_links").Where("tenant_id=201 AND delivery_id=901").Take(&source).Error)
		require.Contains(t, source.AnchorSnapshot, "Rejected immutable source")
		require.Nil(t, source.TargetID)
		listed := call("GET", path, clientToken, "")
		require.Equal(t, 200, listed.Code, listed.Body.String())
		require.Contains(t, listed.Body.String(), source.ThreadID.String())
		require.Contains(t, listed.Body.String(), "Rejected immutable source")
		otherComments := call("GET", path, otherToken, "")
		require.Equal(t, 403, otherComments.Code)
		require.NotContains(t, otherComments.Body.String(), "Rejected immutable source")
		require.NotContains(t, otherComments.Body.String(), source.ThreadID.String())
		receiptBody := fmt.Sprintf(`{"signer_name":"Client signer","confirmed":true,"content_hash":%q,"reason":"Reviewed the fixed source","items":[{"item_id":%q,"result":"REJECTED","feedback_category":"FUNCTIONAL_DEFECT","comment":"The source contains a reproducible defect","comment_thread_ids":[%q]}]}`, packageHash, item, source.ThreadID.String())
		sendReceipt := func(key, submitted string) *httptest.ResponseRecorder {
			receiptReq := httptest.NewRequest(http.MethodPost, "/entities/"+p+"/delivery-packages/901/receipts", strings.NewReader(submitted))
			receiptReq.Header.Set("Authorization", "Bearer "+clientToken)
			receiptReq.Header.Set("Content-Type", "application/json")
			receiptReq.Header.Set("Idempotency-Key", key)
			receiptReq.Header.Set("If-Match", `"2"`)
			received := httptest.NewRecorder()
			router.ServeHTTP(received, receiptReq)
			return received
		}
		wrongSource := sendReceipt("source-wrong-thread", strings.Replace(receiptBody, `"comment_thread_ids":["`+source.ThreadID.String()+`"]`, `"comment_thread_ids":["999"]`, 1))
		require.Equal(t, 422, wrongSource.Code, wrongSource.Body.String())
		require.Contains(t, wrongSource.Body.String(), "127057")
		require.Contains(t, wrongSource.Body.String(), "NOT_THIS_ITEM")
		require.NoError(t, db.Exec("CREATE TRIGGER reject_receipt_probe BEFORE INSERT ON clientwork_package_receipts BEGIN SELECT RAISE(ABORT,'receipt persistence failed'); END").Error)
		failed := sendReceipt("source-receipt-failure", receiptBody)
		require.Equal(t, 500, failed.Code, failed.Body.String())
		var rolledBack struct{ TargetID *guid.ID }
		require.NoError(t, db.Table("execution_client_comment_links").Where("thread_id=?", source.ThreadID).Take(&rolledBack).Error)
		require.Nil(t, rolledBack.TargetID)
		var receipts int64
		require.NoError(t, db.Table("clientwork_package_receipts").Where("tenant_id=201 AND package_id=901").Count(&receipts).Error)
		require.Zero(t, receipts)
		require.NoError(t, db.Exec("DROP TRIGGER reject_receipt_probe").Error)
		received := sendReceipt("source-receipt", receiptBody)
		require.Equal(t, 200, received.Code, received.Body.String())
		var receipt struct {
			ItemOutcomes  *string
			ReceiptJSON   *string
			ReceiptSHA256 *string
		}
		require.NoError(t, db.Table("clientwork_package_receipts").Where("tenant_id=201 AND package_id=901").Take(&receipt).Error)
		require.NotNil(t, receipt.ItemOutcomes)
		require.Contains(t, *receipt.ItemOutcomes, "Rejected immutable source")
		require.Contains(t, *receipt.ItemOutcomes, source.ThreadID.String())
		require.NotNil(t, receipt.ReceiptJSON)
		require.NotContains(t, *receipt.ReceiptJSON, "anchors")
		require.NotNil(t, receipt.ReceiptSHA256)
		var converted struct {
			TargetID                   *guid.ID
			TargetKind, AnchorSnapshot string
		}
		require.NoError(t, db.Table("execution_client_comment_links").Where("thread_id=?", source.ThreadID).Take(&converted).Error)
		require.NotNil(t, converted.TargetID)
		require.Equal(t, "REJECTION_ITEM", converted.TargetKind)
		require.Equal(t, source.AnchorSnapshot, converted.AnchorSnapshot)
	})
	require.NoError(t, db.Table("delivery_project_contacts").Where("tenant_id=201 AND project_id=? AND intake_contact_id=401", project.Data.Entity.ID).Delete(map[string]any{}).Error)
	require.NoError(t, db.Table("collab_channel_members").Where("tenant_id=201 AND channel_id=? AND principal_id=302", sharedChannel.Data.ID).Update("left_at", time.Now().UTC()).Error)
	revoked := call("GET", "/tenants/201/client-access?limit=1", clientToken, "")
	require.Equal(t, 200, revoked.Code, revoked.Body.String())
	require.Contains(t, revoked.Body.String(), `"items":[]`)
	require.Contains(t, revoked.Body.String(), `"has_more":false`)
	require.Equal(t, 404, call("GET", "/entities/"+p+"/delivery-packages", clientToken, "").Code)
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=302").Update("status", "disabled").Error)
	require.Equal(t, 403, call("GET", "/tenants/201/client-access", clientToken, "").Code)
}
