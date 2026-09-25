package files

import (
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/require"
)

// TestPersonalRoutesDoNotDependOnTheSelectedTenant pins the property that a
// person's own files are reachable whichever tenant the session has selected.
//
// The account collection refuses claims that carry a tenant, which is correct:
// the space belongs to nobody's tenant. What was wrong was the caller handing
// it a tenant anyway, so listing your own roots failed for the whole time a
// tenant was selected -- which is most of the time, and which is why a second
// personal area had to exist inside each tenant.
func TestPersonalRoutesDoNotDependOnTheSelectedTenant(t *testing.T) {
	withTenant := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70, TenantID: 90})

	personal := personalContext(withTenant)
	claims, ok := authn.FromContext(personal)
	require.True(t, ok)
	require.Equal(t, int64(70), int64(claims.PrincipalID), "the person is unchanged")
	require.Zero(t, claims.TenantID, "the selected tenant is not part of a question about the person")
	require.Zero(t, claims.EntityID)

	// The original context keeps its claims: this copies, it does not mutate.
	original, _ := authn.FromContext(withTenant)
	require.Equal(t, int64(90), int64(original.TenantID))

	// Claims that already carry no tenant are handed through untouched.
	accountOnly := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})
	require.Equal(t, accountOnly, personalContext(accountOnly))
}

// TestAccountCollectionAnswersWhileATenantIsSelected is the behavior the unit
// above exists to produce, exercised through the service.
func TestAccountCollectionAnswersWhileATenantIsSelected(t *testing.T) {
	h := newOperationServiceHarness(t)
	accountContext := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})

	root, err := h.service.ensureOwnedRoot(accountContext, "account")
	require.NoError(t, err)

	collection := fileCollection{scope: ownerAccount, ownerID: 70}
	baseline, err := h.service.listRoots(accountContext, collection, browserPageRequest{})
	require.NoError(t, err)
	require.Equal(t, []fileEntryView{root}, baseline.Items)

	// h.ctx is the same person with tenant 90 selected. Without dropping that
	// tenant the account collection is refused outright.
	selected, err := h.service.listRoots(personalContext(h.ctx), collection, browserPageRequest{})
	require.NoError(t, err, "a person's own roots do not depend on the selected tenant")
	require.Equal(t, []fileEntryView{root}, selected.Items)
}

// TestMyFilesRouteAnswersWhileATenantIsSelected exercises the wiring rather
// than the helper. Calling personalContext from a test proves the helper
// copies claims; it proves nothing about whether the route uses it, and the
// route is where the bug was.
func TestMyFilesRouteAnswersWhileATenantIsSelected(t *testing.T) {
	h := newOperationServiceHarness(t)
	accountContext := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})
	root, err := h.service.ensureOwnedRoot(accountContext, "account")
	require.NoError(t, err)

	api := newFileHTTPAPI(t, h.service, 90)
	response := api.Get("/me/files?limit=10", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var envelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Items, 1)
	require.Equal(t, root.ID, envelope.Data.Items[0].ID)
	require.Equal(t, "account", envelope.Data.Items[0].Scope)
}
