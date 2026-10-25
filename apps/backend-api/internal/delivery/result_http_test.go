package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type resultHTTPGuard struct{ ctx context.Context }

func (g resultHTTPGuard) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	claims, _ := authn.FromContext(g.ctx)
	return claims, nil
}
func (resultHTTPGuard) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (resultHTTPGuard) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}
func (resultHTTPGuard) CheckEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (resultHTTPGuard) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func (h *resultFixture) mutation(key string) ResultMutationInput {
	return ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IfMatch: fmtETag(h.stored().Version), IdempotencyKey: key}
}
func TestResultSubmissionStaleContextIsAtomicAndSameKeyRefreshes(t *testing.T) {
	h := newResultFixture(t)
	seen := h.stored().ActivitySeq
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.recordWorkActivityOn(h.ctx, tx, 1, h.work, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 88, Title: "Changed context"})
		return err
	}))
	in := &resultSubmitInput{ResultMutationInput: h.mutation("context-submit"), Body: h.input}
	in.Body.SeenSeq = &seen
	before := h.snapshot()
	_, err := h.m.submitResultHTTP(h.actor(11), in)
	assertWorkStatus(t, err, 409)
	require.Equal(t, before, h.snapshot(), "interruption must not pin files or create results, activity, audit or outbox")
	seen++
	first, err := h.m.submitResultHTTP(h.actor(11), in)
	require.NoError(t, err)
	again, err := h.m.submitResultHTTP(h.actor(11), in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
}

func (h *resultFixture) versionInput(result guid.ID, key string) ResultVersionInput {
	return ResultVersionInput{ResultMutationInput: h.mutation(key), ResultID: result.String()}
}

func TestResultHTTPReplayAndCurrentPermissions(t *testing.T) {
	h := newResultFixture(t)
	submit := &resultSubmitInput{ResultMutationInput: h.mutation("submit-once"), Body: h.input}
	first, err := h.m.submitResultHTTP(h.ctx, submit)
	require.NoError(t, err)
	require.Equal(t, 201, first.Status)
	again, err := h.m.submitResultHTTP(h.ctx, submit)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	require.Len(t, h.files.pins, 1)
	check := &resultCheckInput{ResultVersionInput: h.versionInput(first.Body.Data.ID, "check-once"), Body: resultCheckBody{Verdict: "PASSED", Reason: "Actual independent review"}}
	checked, err := h.m.checkResultHTTP(h.actor(12), check)
	require.NoError(t, err)
	replayed, err := h.m.checkResultHTTP(h.actor(12), check)
	require.NoError(t, err)
	require.Equal(t, checked.Body.Data, replayed.Body.Data)
	accept := &resultAcceptInput{ResultVersionInput: h.versionInput(first.Body.Data.ID, "accept-once"), Body: resultAcceptBody{Reason: "All criteria verified"}}
	accepted, err := h.m.acceptResultHTTP(h.actor(13), accept)
	require.NoError(t, err)
	replayed, err = h.m.acceptResultHTTP(h.actor(13), accept)
	require.NoError(t, err)
	require.Equal(t, accepted.Body.Data, replayed.Body.Data)
	handoff := &resultHandoffInput{ResultVersionInput: h.versionInput(first.Body.Data.ID, "handoff-once"), Body: resultHandoffBody{Summary: "Deliver immutable bytes"}}
	sent, err := h.m.handoffResultHTTP(h.actor(12), handoff)
	require.NoError(t, err)
	replayed, err = h.m.handoffResultHTTP(h.actor(12), handoff)
	require.NoError(t, err)
	require.Equal(t, sent.Body.Data, replayed.Body.Data)
	page, err := h.m.listResultsHTTP(h.actor(13), &resultListInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Len(t, page.Body.Data[0].Handoffs, 1)
	receipt := &resultReceiptInput{ResultVersionInput: h.versionInput(first.Body.Data.ID, "receipt-once"), HandoffID: page.Body.Data[0].Handoffs[0].ID.String(), Body: resultReceiptBody{Accepted: true, Reason: "Received exact package"}}
	received, err := h.m.receiveResultHTTP(h.actor(13), receipt)
	require.NoError(t, err)
	replayed, err = h.m.receiveResultHTTP(h.actor(13), receipt)
	require.NoError(t, err)
	require.Equal(t, received.Body.Data, replayed.Body.Data)
	h.deniedAuthority = true
	_, err = h.m.acceptResultHTTP(h.actor(13), accept)
	assertWorkStatus(t, err, 403)
	_, err = h.m.handoffResultHTTP(h.actor(12), handoff)
	assertWorkStatus(t, err, 403)
	_, err = h.m.receiveResultHTTP(h.actor(13), receipt)
	assertWorkStatus(t, err, 403)
	h.deniedAuthority = false
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "work_item_read"}, nil
	}
	_, err = h.m.submitResultHTTP(h.ctx, submit)
	assertWorkStatus(t, err, 404)
	_, err = h.m.checkResultHTTP(h.actor(12), check)
	assertWorkStatus(t, err, 404)
}

func TestResultHTTPHistoryPagingIsolationAndOldVersions(t *testing.T) {
	h := newResultFixture(t)
	first, err := h.submit()
	require.NoError(t, err)
	second, err := h.submit()
	require.NoError(t, err)
	input := ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}
	page, err := h.m.listResultsHTTP(h.actor(12), &resultListInput{ResultWorkInput: input, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Body.Meta.Page.Total)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, second.ID, page.Body.Data[0].ID)
	page, err = h.m.listResultsHTTP(h.actor(12), &resultListInput{ResultWorkInput: input, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, first.ID, page.Body.Data[0].ID)
	page, err = h.m.listResultsHTTP(h.actor(12), &resultListInput{ResultWorkInput: input, Limit: 1, Offset: 2})
	require.NoError(t, err)
	require.Empty(t, page.Body.Data)
	_, err = h.m.checkResultHTTP(h.actor(12), &resultCheckInput{ResultVersionInput: h.versionInput(first.ID, ""), Body: resultCheckBody{Verdict: "PASSED", Reason: "Old version"}})
	assertWorkStatus(t, err, 409)
	for _, result := range []guid.ID{guid.ID(1), guid.ID(999)} {
		_, err = h.m.acceptResultHTTP(h.actor(12), &resultAcceptInput{ResultVersionInput: h.versionInput(result, ""), Body: resultAcceptBody{Reason: "Foreign result"}})
		assertWorkStatus(t, err, 404)
	}
	for _, claims := range []*authn.Claims{{TenantID: 2, EntityID: 99, PrincipalID: 12}, {TenantID: 1, EntityID: 98, PrincipalID: 12}} {
		ctx := authn.WithClaims(h.ctx, claims)
		foreign := input
		foreign.EntityID = claims.EntityID.String()
		_, err = h.m.listResultsHTTP(ctx, &resultListInput{ResultWorkInput: foreign, Limit: 50})
		require.Error(t, err)
	}
}

func TestResultHTTPRegistrationPublishesOwnedContracts(t *testing.T) {
	h := newResultFixture(t)
	registry, err := authz.NewRegistry(
		authz.Action{Resource: "verification", Verb: "run", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "decision", Verb: "force_valid", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "work_item", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "work_item", Verb: "execute", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "requirement", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "scope", Verb: "change", Scope: authz.ActionScopeTenant, DataScoped: true},
		authz.Action{Resource: "artifact_comment", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true},
	)
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: h.ctx}
	h.m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	_, api := humatest.New(t)
	require.NoError(t, h.m.registerResultHTTP(api))
	h.m.entityOwner = func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: id}, true, nil
	}
	require.NoError(t, h.m.registerChangeHTTP(api))
	require.NoError(t, h.m.registerFeedbackHTTP(api))
	require.NoError(t, h.m.registerDefectHTTP(api))
	base := api.OpenAPI().Paths[workResultsPath]
	require.NotNil(t, base)
	require.Equal(t, "result.submit", base.Post.OperationID)
	require.Equal(t, "work_item_execute", base.Post.Extensions[authz.GuardExtensionKey])
	require.Equal(t, "work_item_read", base.Get.Extensions[authz.GuardExtensionKey])
	parameters := map[string]bool{}
	for _, parameter := range base.Post.Parameters {
		parameters[parameter.In+":"+parameter.Name] = parameter.Required
	}
	require.True(t, parameters["path:entity_id"])
	require.True(t, parameters["path:work_item_id"])
	require.True(t, parameters["header:If-Match"])
	for _, path := range []string{workResultPath + "/checks", workResultPath + "/acceptance", workResultPath + "/handoffs", workResultPath + "/handoffs/{handoff_id}/receipt", workResultPath + "/evidence/{evidence_id}/content", workPath + "/{work_item_id}/evidence-candidates/{file_id}"} {
		require.NotNil(t, api.OpenAPI().Paths[path], path)
	}
	seen := map[string]bool{}
	count := 0
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put} {
			if operation == nil {
				continue
			}
			count++
			require.False(t, seen[operation.OperationID], operation.OperationID)
			seen[operation.OperationID] = true
			require.NotEmpty(t, operation.Security, operation.OperationID)
			require.NotEmpty(t, operation.Extensions[authz.GuardExtensionKey], operation.OperationID)
			require.Contains(t, operation.Responses, "404", operation.OperationID)
			for _, parameter := range operation.Parameters {
				if parameter.In == "path" {
					require.True(t, parameter.Required)
					require.Contains(t, path, "{"+parameter.Name+"}")
					require.Equal(t, "string", parameter.Schema.Type)
					require.Equal(t, "^[1-9][0-9]*$", parameter.Schema.Pattern)
				}
				if parameter.In == "query" && parameter.Name == "limit" {
					require.EqualValues(t, 50, parameter.Schema.Default)
					require.InDelta(t, 200, *parameter.Schema.Maximum, 1e-9)
					require.InDelta(t, 1, *parameter.Schema.Minimum, 1e-9)
				}
			}
			if method == "POST" || method == "PUT" {
				status := "201"
				if operation.OperationID == "work_item.defect-stage" || operation.OperationID == "work_item.resume" || operation.OperationID == "work_item.baseline" {
					status = "200"
				}
				require.Contains(t, operation.Responses, status, operation.OperationID)
			}
			t.Logf("%s %s %s", method, path, operation.OperationID)
		}
	}
	require.Equal(t, 20, count)
}

func TestResultHTTPEvidenceStreamsExactBindingAndCandidates(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	evidenceInput := resultEvidenceInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, ResultID: r.ID.String(), EvidenceID: r.Evidence[0].ID.String()}
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "evidence-stream-test", Method: http.MethodGet, Path: workResultPath + "/evidence/{evidence_id}/content"}, func(_ context.Context, in *resultEvidenceInput) (*huma.StreamResponse, error) {
		return h.m.downloadResultEvidenceHTTP(h.actor(12), in)
	})
	path := "/entities/99/work-items/" + h.work.String() + "/results/" + r.ID.String() + "/evidence/" + r.Evidence[0].ID.String() + "/content"
	response := api.Get(path)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "exact", response.Body.String())
	require.Equal(t, "text/plain", response.Header().Get("Content-Type"))
	require.Equal(t, "5", response.Header().Get("Content-Length"))
	require.Equal(t, "attachment; filename=evidence.txt", response.Header().Get("Content-Disposition"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, 1, h.files.closed)
	require.Equal(t, []guid.ID{r.Evidence[0].BindingID}, h.files.opened)
	bad := evidenceInput
	bad.ResultID = "999"
	_, err = h.m.downloadResultEvidenceHTTP(h.actor(12), &bad)
	assertWorkStatus(t, err, 404)
	bad = evidenceInput
	bad.EvidenceID = "999"
	_, err = h.m.downloadResultEvidenceHTTP(h.actor(12), &bad)
	assertWorkStatus(t, err, 404)
	second, err := h.submit()
	require.NoError(t, err)
	bad = evidenceInput
	bad.ResultID = second.ID.String()
	_, err = h.m.downloadResultEvidenceHTTP(h.actor(12), &bad)
	assertWorkStatus(t, err, 404)
	response = api.Get(path)
	require.Equal(t, 200, response.Code)
	require.Equal(t, r.Evidence[0].BindingID, h.files.opened[len(h.files.opened)-1])
	candidate, err := h.m.resultEvidenceCandidateHTTP(h.ctx, &resultCandidateInput{ResultWorkInput: evidenceInput.ResultWorkInput, FileID: "501"})
	require.NoError(t, err)
	require.Equal(t, guid.ID(500), candidate.Body.Data.SpaceID)
	require.Equal(t, guid.ID(502), candidate.Body.Data.VersionID)
	require.Equal(t, int64(3), candidate.Body.Data.FileVersion)
	h.files.fail = files.ErrPinnedForbidden
	_, err = h.m.resultEvidenceCandidateHTTP(h.ctx, &resultCandidateInput{ResultWorkInput: evidenceInput.ResultWorkInput, FileID: "501"})
	assertWorkStatus(t, err, 403)
	_, err = h.m.downloadResultEvidenceHTTP(h.actor(12), &evidenceInput)
	assertWorkStatus(t, err, 403)
	h.files.fail = nil
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	before := len(h.files.inspected)
	_, err = h.m.resultEvidenceCandidateHTTP(h.ctx, &resultCandidateInput{ResultWorkInput: evidenceInput.ResultWorkInput, FileID: "501"})
	require.Error(t, err)
	require.Len(t, h.files.inspected, before)
}

func TestResultHTTPActualValidationContracts(t *testing.T) {
	h := newResultFixture(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "result-submit-contract-test", Method: http.MethodPost, Path: workResultsPath, DefaultStatus: 201}, func(_ context.Context, in *resultSubmitInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
		if in.Body.Summary == h.input.Summary {
			require.Equal(t, "99", in.EntityID)
			require.Equal(t, h.work.String(), in.WorkItemID)
			require.Equal(t, h.input, in.Body)
		}
		return h.m.submitResultHTTP(h.ctx, in)
	})
	huma.Register(api, huma.Operation{OperationID: "result-list-contract-test", Method: http.MethodGet, Path: workResultsPath}, func(_ context.Context, in *resultListInput) (*respx.Body[[]ResultView], error) {
		return h.m.listResultsHTTP(h.ctx, in)
	})
	path := "/entities/99/work-items/" + h.work.String() + "/results"
	raw, err := json.Marshal(h.input)
	require.NoError(t, err)
	require.Equal(t, 422, api.Post(path, strings.NewReader(string(raw))).Code)
	require.Equal(t, 422, api.Post(path, "If-Match: 1", strings.NewReader(string(raw))).Code)
	for _, query := range []string{"?limit=0", "?limit=201", "?offset=-1"} {
		require.Equal(t, 422, api.Get(path+query).Code)
	}
	for _, body := range []resultSubmitBody{{Summary: "", Evidence: h.input.Evidence}, {Summary: "ok", Evidence: nil}, {Summary: "ok", Evidence: []resultEvidenceBody{{SpaceID: 500, FileID: 501, FileVersion: 3, VersionID: 502, Coverage: "UNKNOWN"}}}} {
		encoded, e := json.Marshal(body)
		require.NoError(t, e)
		require.Equal(t, 422, api.Post(path, "If-Match: "+fmtETag(h.stored().Version), strings.NewReader(string(encoded))).Code)
	}
	response := api.Post(path, "If-Match: "+fmtETag(h.stored().Version), "Idempotency-Key: actual-result", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
	response = api.Post(path, "If-Match: \"1\"", strings.NewReader(string(raw)))
	require.Equal(t, 409, response.Code, response.Body.String())
}

func TestResultHTTPInvalidIDsAndUnavailable(t *testing.T) {
	m := &Module{}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11})
	for _, id := range []string{"bad", "1"} {
		want := 422
		if id == "1" {
			want = 503
		}
		base := ResultWorkInput{EntityID: id, WorkItemID: id}
		mutation := ResultMutationInput{ResultWorkInput: base, IfMatch: "\"1\""}
		version := ResultVersionInput{ResultMutationInput: mutation, ResultID: id}
		_, err := m.submitResultHTTP(ctx, &resultSubmitInput{ResultMutationInput: mutation})
		assertWorkStatus(t, err, want)
		_, err = m.checkResultHTTP(ctx, &resultCheckInput{ResultVersionInput: version})
		assertWorkStatus(t, err, want)
		_, err = m.acceptResultHTTP(ctx, &resultAcceptInput{ResultVersionInput: version})
		assertWorkStatus(t, err, want)
		_, err = m.handoffResultHTTP(ctx, &resultHandoffInput{ResultVersionInput: version})
		assertWorkStatus(t, err, want)
		_, err = m.receiveResultHTTP(ctx, &resultReceiptInput{ResultVersionInput: version, HandoffID: id})
		assertWorkStatus(t, err, want)
		_, err = m.listResultsHTTP(ctx, &resultListInput{ResultWorkInput: base, Limit: 50})
		assertWorkStatus(t, err, want)
		_, err = m.resultEvidenceCandidateHTTP(ctx, &resultCandidateInput{ResultWorkInput: base, FileID: id})
		assertWorkStatus(t, err, want)
		_, err = m.downloadResultEvidenceHTTP(ctx, &resultEvidenceInput{ResultWorkInput: base, ResultID: id, EvidenceID: id})
		assertWorkStatus(t, err, want)
	}
	for _, in := range []resultListInput{{ResultWorkInput: ResultWorkInput{EntityID: "1", WorkItemID: "1"}, Limit: 0}, {ResultWorkInput: ResultWorkInput{EntityID: "1", WorkItemID: "1"}, Limit: 201}, {ResultWorkInput: ResultWorkInput{EntityID: "1", WorkItemID: "1"}, Limit: 50, Offset: -1}} {
		_, err := m.listResultsHTTP(ctx, &in)
		assertWorkStatus(t, err, 422)
	}
	for _, test := range []struct {
		err    error
		status int
	}{{ErrResultState, 409}, {ErrResultIndependent, 403}, {ErrResultCoverage, 409}, {ErrInvalidArgument, 422}, {files.ErrPinnedForbidden, 403}, {files.ErrPinnedConflict, 409}, {files.ErrPinnedUnavailable, 503}, {files.ErrPinnedInvalid, 422}, {gorm.ErrRecordNotFound, 404}, {errors.New("unexpected"), 500}} {
		assertWorkStatus(t, resultHTTPError(ctx, test.err), test.status)
	}
	encoded, err := respx.MarshalEnvelope(ctx, resultHTTPError(ctx, files.ErrPinnedConflict))
	require.NoError(t, err)
	var envelope struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	require.Equal(t, 120419, envelope.Code)
}
