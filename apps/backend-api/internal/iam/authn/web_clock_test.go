package authn

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebClockWritesUTCInEveryLocalZone freezes the invariant that every instant
// the authentication service stores is UTC, whatever zone the host runs in.
//
// It guards a class rather than one column. The drivers serialize a time.Time
// with the offset it carries and the engines order those values as text, so a
// value written with a non-UTC offset sorts by its wall clock instead of its
// instant. In UTC+8 a session expired an hour ago therefore sorted seven hours
// into the future and kept authenticating, because "…17:21+08:00" is greater
// than "…10:21+00:00" as a string. The service now normalizes at its clock, the
// way internal/iam/core/repository.go already does, rather than relying on each
// of its callers to remember .UTC().
//
// It escaped notice because the defect is invisible on a UTC host: there
// time.Local is UTC and every write is already normalized. So the test pins
// time.Local to a non-UTC zone itself instead of trusting the machine it runs
// on, and asserts over the whole column set so a time column added later is
// covered without anyone remembering to extend this test.
func TestWebClockWritesUTCInEveryLocalZone(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })
	time.Local = time.FixedZone("UTC+8", 8*60*60)

	db, web, _ := newAuthenticationService(t)

	require.Equal(t, time.UTC, web.now().Location(),
		"the service clock must hand out UTC so no caller can store a local instant")

	_, err := web.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)

	var columns []struct {
		Name string
		Type string
	}
	require.NoError(t, db.Raw("SELECT name, type FROM pragma_table_info('iam_sessions')").Scan(&columns).Error)
	require.NotEmpty(t, columns, "the session table must be readable for this invariant to mean anything")

	var checked int
	for _, column := range columns {
		if !strings.Contains(strings.ToUpper(column.Type), "DATETIME") {
			continue
		}
		var stored []string
		require.NoError(t, db.Raw(fmt.Sprintf( //nolint:gosec // the column name comes from the schema, not from input
			"SELECT CAST(%s AS TEXT) FROM iam_sessions WHERE %s IS NOT NULL", column.Name, column.Name)).
			Scan(&stored).Error)
		for _, value := range stored {
			checked++
			assert.True(t, strings.HasSuffix(value, "+00:00"),
				"iam_sessions.%s stored %q; a non-UTC offset makes the engine order it by wall clock", column.Name, value)
		}
	}
	require.Positive(t, checked, "the login must have written at least one instant for this to assert anything")
}

// TestWebExpiryRejectsASessionExpiredInALocalZone is the behavioral half of the
// invariant above: normalizing the clock is only worth anything if an expired
// session actually stops authenticating.
//
// It has to cross the HTTP boundary to mean anything. Resolving the session
// directly would compare the stored instant against the same clock that wrote
// it, and two values carrying the same offset order correctly however wrong
// that offset is — a first draft of this test did exactly that and survived the
// mutation. The defect lives in the mismatch: a caller that stores the service
// clock raw against a service that normalizes internally. Only a request
// exercises both halves.
func TestWebExpiryRejectsASessionExpiredInALocalZone(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })
	time.Local = time.FixedZone("UTC+8", 8*60*60)

	db, web, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login, err := web.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	cookie := "Cookie: " + strings.Split(web.SessionCookie(login.SessionID), ";")[0]

	expired, err := web.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	expiredID := sessionPublicID(t, web, expired.SessionID)
	require.Equal(t, http.StatusOK, api.Get("/sessions/"+expiredID, cookie).Code,
		"the session must be reachable first, or its disappearance proves nothing")

	result := db.Exec("UPDATE iam_sessions SET expires_at = ? WHERE id_hash = ?",
		web.now().Add(-time.Hour), tokenHash(expired.SessionID))
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected, "the expiry must reach the session row, or the rest asserts nothing")

	assert.Equal(t, http.StatusNotFound, api.Get("/sessions/"+expiredID, cookie).Code,
		"a session past its expiry must read as absent")
	assert.Equal(t, http.StatusUnauthorized,
		api.Get("/sessions/current", "Cookie: "+strings.Split(web.SessionCookie(expired.SessionID), ";")[0]).Code,
		"a session past its expiry must stop authenticating")
}
