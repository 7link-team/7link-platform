package delivery

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRequirementCodeLinkHTTPParsesEmbeddedPathParameters(t *testing.T) {
	_, api := humatest.New(t)
	var created *requirementCodeLinkInput
	var evidence *requirementCodeEvidenceInput
	huma.Register(api, huma.Operation{OperationID: "requirement-code-link-input", Method: "POST", Path: requirementCodeLinksPath}, func(_ context.Context, in *requirementCodeLinkInput) (*struct{ Body string }, error) {
		created = in
		return &struct{ Body string }{Body: "ok"}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "requirement-code-evidence-input", Method: "GET", Path: requirementCodeLinksPath + "/{code_link_id}/evidence"}, func(_ context.Context, in *requirementCodeEvidenceInput) (*struct{ Body string }, error) {
		evidence = in
		return &struct{ Body string }{Body: "ok"}, nil
	})
	body := `{"kind":"BRANCH","repository_id":"456","ref":"main"}`
	response := api.Post("/entities/99/requirements/123/code-links", "Idempotency-Key: reference", strings.NewReader(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotNil(t, created)
	require.Equal(t, "99", created.EntityID)
	require.Equal(t, "123", created.RequirementID)
	require.Equal(t, "reference", created.Key)
	require.Equal(t, "main", created.Body.Ref)
	response = api.Get("/entities/99/requirements/123/code-links/456/evidence")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotNil(t, evidence)
	require.Equal(t, "99", evidence.EntityID)
	require.Equal(t, "123", evidence.RequirementID)
	require.Equal(t, "456", evidence.CodeLinkID)
	for _, path := range []string{"/entities/invalid/requirements/123/code-links", "/entities/99/requirements/0/code-links"} {
		created, evidence = nil, nil
		response = api.Post(path, "Idempotency-Key: reference", strings.NewReader(body))
		require.Equal(t, 422, response.Code, response.Body.String())
		response = api.Get(path + "/456/evidence")
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Nil(t, created)
		require.Nil(t, evidence)
	}
}

func TestCodeLinksHTTPRejectsInvalidIdentityAndUnavailableRuntime(t *testing.T) {
	m := &Module{}
	_, err := m.listCodeLinksHTTP(t.Context(), &ResultWorkInput{EntityID: "bad", WorkItemID: "1"})
	require.Error(t, err)
	_, err = m.createCodeLinkHTTP(t.Context(), &codeLinkCreateInput{ResultWorkInput: ResultWorkInput{EntityID: "1", WorkItemID: "2"}})
	require.Error(t, err)
	_, err = m.codeLinkEvidenceHTTP(t.Context(), &codeLinkEvidenceInput{ResultWorkInput: ResultWorkInput{EntityID: "1", WorkItemID: "2"}, CodeLinkID: "3"})
	require.Error(t, err)
}
