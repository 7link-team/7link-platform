package governance

import (
	"7link.com/platform/pkg/extension/authn"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
)

func TestInboxHTTPCountDetailAndRecipientBoundary(t *testing.T) {
	m, _, handler, authority := governanceHTTPHarness(t)
	item := inboxFixture(7, "WORK_ITEM_REVIEW", "HIGH")
	require.NoError(t, m.inbox.Register(inboxFixtureSource(item.Kind, []InboxItem{item})))
	w := governanceRequest(t, handler, http.MethodGet, "/tenants/1/inbox-counts", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"total":1`)
	path := "/tenants/1/inbox-items/WORK_ITEM_REVIEW:7"
	w = governanceRequest(t, handler, http.MethodGet, path, nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"source_revision"`)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	w = governanceRequest(t, handler, http.MethodPost, path+"/acknowledgements", map[string]any{"revision": inboxRevision(item)}, "")
	require.Equal(t, 400, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodGet, "/tenants/1/inbox-items/WORK_ITEM_REVIEW:8", nil, "")
	require.Equal(t, 404, w.Code, w.Body.String())
	authority.member = false
	for _, p := range []string{path, "/tenants/1/inbox-counts"} {
		w = governanceRequest(t, handler, http.MethodGet, p, nil, "")
		require.Equal(t, 403, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), item.Title)
	}
}

func TestInboxHTTPAcknowledgementPersistsReplaysAndRollsBack(t *testing.T) {
	m, db, handler, authority := governanceHTTPHarness(t)
	item := inboxFixture(7, "CLARIFICATION_REPLY", "HIGH")
	require.NoError(t, m.inbox.Register(inboxFixtureSource(item.Kind, []InboxItem{item})))
	path := "/tenants/1/inbox-items/CLARIFICATION_REPLY:7/acknowledgements"
	post := func(revision string) int {
		w := governanceRequest(t, handler, http.MethodPost, path, map[string]any{"revision": revision}, "")
		return w.Code
	}
	require.Equal(t, 409, post(strings.Repeat("0", 64)))
	table := m.seed.names.InboxAcknowledgements.String()
	require.Zero(t, seedCount(t, db, table))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:http-ack", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(ErrUnavailable)
		}
	}))
	require.Equal(t, 503, post(inboxRevision(item)))
	require.NoError(t, db.Callback().Create().Remove("test:http-ack"))
	require.Zero(t, seedCount(t, db, table))
	for range 2 {
		require.Equal(t, 200, post(inboxRevision(item)))
	}
	require.EqualValues(t, 1, seedCount(t, db, table))
	authority.member = false
	require.Equal(t, 403, post(inboxRevision(item)))
	require.EqualValues(t, 1, seedCount(t, db, table))
}

func TestInboxHTTPRejectsInvalidSourceIdentityAndMissingWriter(t *testing.T) {
	m, _, _, _ := governanceHTTPHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	for _, identity := range []inboxItemInput{{TenantID: "bad", ID: "CLARIFICATION_REPLY:7"}, {TenantID: "1", ID: "missing-separator"}, {TenantID: "1", ID: "CLARIFICATION_REPLY:bad"}} {
		out, err := m.inboxItemHTTP(ctx, &identity)
		require.Error(t, err)
		require.Nil(t, out)
		ack := &inboxAckInput{TenantID: identity.TenantID, ID: identity.ID}
		response, err := m.inboxAckHTTP(ctx, ack)
		require.Error(t, err)
		require.Nil(t, response)
	}
	_, err := (&Module{}).inboxAckHTTP(ctx, &inboxAckInput{TenantID: "1", ID: "CLARIFICATION_REPLY:7"})
	require.Error(t, err)
	for _, input := range []inboxCountsInput{{TenantID: "1", ProjectID: "bad"}, {TenantID: "2"}} {
		out, err := m.inboxCountsHTTP(ctx, &input)
		require.Error(t, err)
		require.Nil(t, out)
	}
}
