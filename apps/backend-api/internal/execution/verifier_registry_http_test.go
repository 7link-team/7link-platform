package execution

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"bytes"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"net/http/httptest"
	"strconv"
	"testing"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/pkg/extension/authn"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerifierListFiltersBeforePaginationAndRejectsMalformedQueries(t *testing.T) {
	f := verifierRegistryFixture(t)
	first := registryDefinition()
	first.Key = "first"
	a := putRegistry(t, f, 0, 0, first)
	second := registryDefinition()
	second.Key = "second"
	b := putRegistry(t, f, 0, 0, second)
	for _, tc := range []struct {
		name   string
		in     verifierListInput
		status int
		count  int
	}{
		{"cursor", verifierListInput{Cursor: "bad", Limit: 1}, 422, 0},
		{"project", verifierListInput{ProjectID: "bad", Limit: 1}, 422, 0},
		{"limit", verifierListInput{Limit: 0}, 422, 0},
		{"type", verifierListInput{WorkItemType: "INVALID", Limit: 1}, 422, 0},
		{"risk", verifierListInput{RiskLevel: "INVALID", Limit: 1}, 422, 0},
		{"foreign_project", verifierListInput{ProjectID: "10", Limit: 1}, 200, 0},
		{"other_type", verifierListInput{WorkItemType: "FEATURE", Limit: 1}, 200, 0},
		{"other_risk", verifierListInput{RiskLevel: "LOW", Limit: 1}, 200, 0},
		{"active", verifierListInput{State: "ACTIVE", Limit: 1}, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, e := f.m.listVerifiersHTTP(f.human, &tc.in)
			if tc.status != 200 {
				var status huma.StatusError
				require.ErrorAs(t, e, &status)
				require.Equal(t, tc.status, status.GetStatus())
				return
			}
			require.NoError(t, e)
			require.Len(t, out.Body.Data.Items, tc.count)
			if tc.count > 0 {
				require.Equal(t, a.ID, out.Body.Data.Items[0].ID)
				require.True(t, out.Body.Data.HasMore)
				require.Equal(t, a.ID.String(), out.Body.Data.NextCursor)
			}
		})
	}
	out, e := f.m.listVerifiersHTTP(f.human, &verifierListInput{Cursor: a.ID.String(), Limit: 1})
	require.NoError(t, e)
	require.Len(t, out.Body.Data.Items, 1)
	require.Equal(t, b.ID, out.Body.Data.Items[0].ID)
	require.False(t, out.Body.Data.HasMore)
}

func TestVerifierRegistryHTTPReplayCurrentAuthorityPartialPatchAndHistory(t *testing.T) {
	f := verifierRegistryFixture(t)
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.registerVerifierRegistryHTTP(api))
	call := func(method, url, key string, version int, body any) *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, url, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if version > 0 {
			req.Header.Set("If-Match", `"`+strconv.Itoa(version)+`"`)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	in := registryDefinition()
	created := call("POST", "/tenants/7/verifiers", "create", 0, in)
	first := executionRouteData[VerifierView](t, created, 201)
	require.Equal(t, "private, no-store", created.Header().Get("Cache-Control"))
	replay := call("POST", "/tenants/7/verifiers", "create", 0, in)
	again := executionRouteData[VerifierView](t, replay, 201)
	require.Equal(t, first, again)
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 409, call("POST", "/tenants/7/verifiers", "duplicate-stable-key", 0, in).Code)
	path := "/verifiers/" + first.ID.String()
	require.Equal(t, 422, call("POST", "/tenants/7/verifiers", "create", 0, map[string]any{"name": "Different"}).Code)
	changed := executionRouteData[VerifierView](t, call("PATCH", path, "rename", 1, map[string]any{"name": "New label"}), 200)
	require.Equal(t, 1, changed.CurrentVersionNo)
	require.Equal(t, first.Current, changed.Current)
	update := map[string]any{"timeout_ms": 1000, "change_note": "Short bounded timeout"}
	latest := executionRouteData[VerifierView](t, call("PATCH", path, "timeout", 2, update), 200)
	require.Equal(t, 2, latest.CurrentVersionNo)
	require.Equal(t, 409, call("PATCH", path, "stale", 1, map[string]any{"name": "Stale"}).Code)
	history := executionRouteData[VerifierVersionPage](t, call("GET", path+"/versions?limit=1", "", 0, nil), 200)
	require.True(t, history.HasMore)
	require.Len(t, history.Items, 1)
	require.Equal(t, first.Current, history.Items[0])
	next := executionRouteData[VerifierVersionPage](t, call("GET", path+"/versions?limit=1&cursor="+history.NextCursor, "", 0, nil), 200)
	require.Len(t, next.Items, 1)
	require.False(t, next.HasMore)
	require.Equal(t, 2, next.Items[0].VersionNo)
	for _, filter := range []string{"work_item_type=FEATURE", "risk_level=LOW", "project_id=88"} {
		page := executionRouteData[VerifierPage](t, call("GET", "/tenants/7/verifiers?"+filter, "", 0, nil), 200)
		require.Empty(t, page.Items)
	}
	page := executionRouteData[VerifierPage](t, call("GET", "/tenants/7/verifiers?work_item_type=BUG&risk_level=HIGH&project_id=9", "", 0, nil), 200)
	require.Len(t, page.Items, 1)
	f.authorized = false
	require.Equal(t, 403, call("POST", "/tenants/7/verifiers", "create", 0, in).Code)
	require.Equal(t, 403, call("PATCH", path, "timeout", 2, update).Code)
	require.Equal(t, 403, call("GET", path, "", 0, nil).Code)
	f.authorized = true
	require.Equal(t, 404, call("GET", "/verifiers/999", "", 0, nil).Code)
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.putVerifierOn(f.human, tx, 7, 0, 0, in); return err }))
	var rows int64
	require.NoError(t, f.db.Table(f.m.names.VerifierVersions.String()).Count(&rows).Error)
	require.EqualValues(t, 2, rows)
	// Definitions are management facts, never evidence/verdicts.
	var tasks []model.VerificationTask
	require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Find(&tasks).Error)
	require.Empty(t, tasks)
}

func TestVerifierRegistryPinsMatchingImmutablePolicyVersionInActualQueue(t *testing.T) {
	f, request := verificationFixture(t)
	f.m.verifierRegistry.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: f.authorized}, nil
	}
	f.m.verifierRegistry.work = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) (string, string, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(9), project)
		require.Equal(t, guid.ID(3), work)
		return "BUG", "HIGH", nil
	}
	in := registryDefinition()
	in.Spec.Argv = request.Argv
	in.TimeoutMS = int64(request.TimeoutSec) * 1000
	if request.Method == "TEST" {
		in.Spec.ReportFormat = "JUNIT_XML"
		in.Spec.ReportGlobs = []string{request.ReportPath}
	}
	v := putRegistry(t, f, 0, 0, in)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = f.m.queueVerificationOn(f.human, tx, request)
		return err
	}))
	in.TimeoutMS += 1000
	putRegistry(t, f, v.ID, 1, in)
	var row model.VerificationTask
	require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id = ?", id).First(&row).Error)
	var p VerificationPlan
	require.NoError(t, json.Unmarshal([]byte(row.PlanJSON), &p))
	require.Equal(t, hash([]byte(row.PlanJSON)), row.PlanHash)
	require.Len(t, p.RegistryVerifiers, 1)
	require.Equal(t, v.Current.ID, p.RegistryVerifiers[0].VersionID)
	require.Equal(t, 1, p.RegistryVerifiers[0].VersionNo)
	require.Equal(t, v.ID, p.RegistryVerifiers[0].VerifierID)
	require.Len(t, p.RegistryVerifiers[0].DefinitionHash, 64)
}

func TestVerifierRegistryHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("getVerifierHTTP"+id, func(t *testing.T) {
			_, err := f.m.getVerifierHTTP(f.human, &VerifierIDInput{VerifierID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("verifierVersionsHTTP"+id, func(t *testing.T) {
			_, err := f.m.verifierVersionsHTTP(f.human, &verifierVersionsInput{VerifierIDInput: VerifierIDInput{VerifierID: id}, Limit: 10})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("patchVerifierHTTP"+id, func(t *testing.T) {
			_, err := f.m.patchVerifierHTTP(f.human, &verifierPatchInput{VerifierIDInput: VerifierIDInput{VerifierID: id}, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestVerifierHTTPReadAndMutationStorageFailuresNeverExposePartialDefinitions(t *testing.T) {
	for _, command := range []string{"create", "patch", "get", "list", "versions"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := verifierRegistryFixture(t)
				var v VerifierView
				if command != "create" {
					v = putRegistry(t, f, 0, 0, registryDefinition())
				}
				return f, func(*gorm.DB) error {
					switch command {
					case "create":
						_, e := f.m.createVerifierHTTP(f.human, &verifierCreateInput{IdempotencyKey: "definition", Body: registryDefinition()})
						return e
					case "patch":
						name := "Changed label"
						_, e := f.m.patchVerifierHTTP(f.human, &verifierPatchInput{VerifierIDInput: VerifierIDInput{VerifierID: v.ID.String()}, IfMatch: "1", IdempotencyKey: "rename", Body: VerifierPatch{Name: &name}})
						return e
					case "get":
						out, e := f.m.getVerifierHTTP(f.human, &VerifierIDInput{VerifierID: v.ID.String()})
						if e == nil {
							require.Equal(t, v, out.Body.Data)
						}
						return e
					case "list":
						out, e := f.m.listVerifiersHTTP(f.human, &verifierListInput{Limit: 1, State: "ACTIVE"})
						if e == nil {
							require.Len(t, out.Body.Data.Items, 1)
						}
						return e
					default:
						out, e := f.m.verifierVersionsHTTP(f.human, &verifierVersionsInput{VerifierIDInput: VerifierIDInput{VerifierID: v.ID.String()}, Limit: 1})
						if e == nil {
							require.Equal(t, []VerifierVersionView{v.Current}, out.Body.Data.Items)
						}
						return e
					}
				}
			}, true)
		})
	}
}

func TestVerifierOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.verifierOwner(f.human, 999)
		if missingWriter {
			require.ErrorIs(t, e, ErrUnavailable)
		} else {
			require.NoError(t, e)
		}
		require.False(t, found)
	}
}
