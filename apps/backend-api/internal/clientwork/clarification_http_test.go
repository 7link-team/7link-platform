package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClarificationPagesBindRecipientAndRejectCorruptOrUnavailableRows(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	internal, client := testContext(t, 2), testContext(t, 4)
	opened, err := m.requestClarificationHTTP(internal, clarificationRequest())
	require.NoError(t, err)
	q := query.BindClarificationRound(db, m.names)
	original, err := q.WithContext(t.Context()).Where(q.ID.Eq(opened.Body.Data.ID)).Take()
	require.NoError(t, err)
	for i := 1; i <= 2; i++ {
		row := *original
		row.ID += guid.ID(i)
		row.State = "CANCELLED"                                                                    //nolint:misspell // Preserve the published domain or serialized contract spelling.
		closedAt, closeReason, closeNote := now(), "CANCELLED_BY_TEAM", "Superseded clarification" //nolint:misspell // Preserve the published domain or serialized contract spelling.
		row.ClosedAt, row.CloseReason, row.CloseNote = &closedAt, &closeReason, &closeNote
		require.NoError(t, q.WithContext(t.Context()).Create(&row))
	}
	first, err := m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.True(t, first.Body.Data.HasMore)
	require.Len(t, first.Body.Data.Items, 1)
	second, err := m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.True(t, second.Body.Data.HasMore)
	require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
	last, err := m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1, Cursor: second.Body.Data.NextCursor})
	require.NoError(t, err)
	require.False(t, last.Body.Data.HasMore)
	require.Equal(t, original.ID, last.Body.Data.Items[0].ID)
	_, err = m.listClarificationHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, err)
	projects.deny = true
	_, err = m.listClarificationHTTP(internal, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
	projects.deny = false
	failure := errors.New("private clarification read failure")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_clarification_list", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.ClarificationRound.String() {
			tx.AddError(failure)
		}
	}))
	out, err := m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
	require.Nil(t, out)
	require.NoError(t, db.Callback().Query().Remove("regression_clarification_list"))
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", first.Body.Data.Items[0].ID).Update("questions", "{").Error)
	out, err = m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
	require.Nil(t, out, "corrupt first row cannot produce a partial page")
	m.runtime = nil
	_, err = m.listClarificationHTTP(client, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
	_, err = m.getClarificationHTTP(client, &ObjectInput{EntityID: "3", ID: original.ID.String()})
	require.Error(t, err)
	_, err = m.cancelClarificationHTTP(internal, &EditInput[ClarificationCancellation]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.answerClarificationHTTP(client, &EditInput[ClarificationAnswer]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.requestClarificationHTTP(internal, &CreateInput[ClarificationRequest]{EntityID: "bad"})
	require.Error(t, err)
}

func TestClarificationHTTPPaginationAndBoundary(t *testing.T) {
	m, _, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	page, err := m.listClarificationHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, opened.Body.Data.ID, page.Body.Data.Items[0].ID)
	hidden, err := m.listClarificationHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.Empty(t, hidden.Body.Data.Items)
	for _, in := range []*ListInput{{EntityID: "bad", Limit: 1}, {EntityID: "3", Limit: 0}, {EntityID: "3", Limit: 101}, {EntityID: "3", Limit: 1, Cursor: "invalid"}, {EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(testContext(t, 2), 3, "clarifications:false"), opened.Body.Data.ID)}} {
		_, err = m.listClarificationHTTP(testContext(t, 4), in)
		require.Error(t, err)
	}
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "6", ID: opened.Body.Data.ID.String()}} {
		_, err = m.getClarificationHTTP(testContext(t, 4), in)
		require.Error(t, err)
	}
	require.True(t, externalRouteAllowed("GET", []string{"entities", "3", "client-clarifications"}, 4))
	require.True(t, externalRouteAllowed("POST", []string{"entities", "3", "client-clarifications", "1", "answers"}, 4))
	require.False(t, externalRouteAllowed("POST", []string{"entities", "3", "client-clarifications"}, 4))
	require.False(t, externalRouteAllowed("POST", []string{"entities", "3", "client-clarifications", "1", "cancellation"}, 4))
	require.False(t, externalRouteAllowed("GET", []string{"entities", "3", "intakes", "50"}, 4))
}
