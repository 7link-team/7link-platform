package keyringx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rewrapPurpose = "encryption:v1"

// sealedRow is one row of a stand-in table. It carries the associated data
// alongside the ciphertext because a rewrap that used different associated data
// than the sealing path would report every row unreadable, and the point of
// these tests is that it does not.
type sealedRow struct {
	ref  string
	aad  []byte
	text string
	// stale marks a row whose write reports that it changed underneath the
	// pass, which is how a real store signals a lost race rather than an error.
	stale bool
}

// storeFixture is a table without a database: enough state to answer what a
// rewrap asks of a store, and to record what it did in reply.
type storeFixture struct {
	name     string
	rows     []sealedRow
	readErr  error
	writeErr error
	writes   int
}

func (f *storeFixture) store() SealedStore {
	return SealedStore{
		Name:    f.name,
		Purpose: rewrapPurpose,
		Values: func(context.Context) ([]SealedValue, error) {
			if f.readErr != nil {
				return nil, f.readErr
			}
			values := make([]SealedValue, 0, len(f.rows))
			for _, row := range f.rows {
				values = append(values, SealedValue{Ref: row.ref, AAD: row.aad, Ciphertext: row.text})
			}
			return values, nil
		},
		Write: func(_ context.Context, value SealedValue, resealed string) (bool, error) {
			if f.writeErr != nil {
				return false, f.writeErr
			}
			f.writes++
			for index := range f.rows {
				if f.rows[index].ref != value.Ref {
					continue
				}
				if f.rows[index].stale {
					return false, nil
				}
				f.rows[index].text = resealed
				return true, nil
			}
			return false, nil
		},
	}
}

func (f *storeFixture) row(t *testing.T, ref string) sealedRow {
	t.Helper()
	for _, row := range f.rows {
		if row.ref == ref {
			return row
		}
	}
	require.FailNow(t, "no such row", ref)
	return sealedRow{}
}

// newRotationFixture builds the table a deployment actually has partway through
// a rotation: something already resealed, something still on the retired key,
// something from before keys had identities, and two rows nothing in the ring
// can open. Every category matters separately, because each one calls for a
// different response from the operator reading the report.
func newRotationFixture(t *testing.T) (*Keyring, *storeFixture) {
	t.Helper()
	// The active key seals; the first key is retired but still listed, which is
	// the only configuration in which a rewrap has anything to convert.
	ring := newTestRing(t, secondKey, firstKey)
	retired := newTestRing(t, firstKey)
	stranger := newTestRing(t, thirdKey)

	current, err := ring.Seal(rewrapPurpose, []byte("current"), []byte("still-not-a-real-secret"))
	require.NoError(t, err)
	stale, err := retired.Seal(rewrapPurpose, []byte("stale"), []byte("also-not-a-real-secret"))
	require.NoError(t, err)
	// A store that seals without associated data passes none, and a rewrap has
	// to carry that absence through rather than substituting the row reference.
	unbound, err := retired.Seal(rewrapPurpose, nil, []byte("unbound-not-a-real-secret"))
	require.NoError(t, err)
	foreign, err := stranger.Seal(rewrapPurpose, []byte("foreign"), []byte("foreign-not-a-real-secret"))
	require.NoError(t, err)

	fixture := &storeFixture{
		name: "iam_example.secret_ciphertext",
		rows: []sealedRow{
			{ref: "current", aad: []byte("current"), text: current},
			{ref: "stale", aad: []byte("stale"), text: stale},
			{ref: "unbound", text: unbound},
			{ref: "legacy", aad: []byte("legacy"), text: sealLegacy(
				t, testDomain, firstKey, rewrapPurpose, []byte("legacy"), []byte("legacy-not-a-real-secret"),
			)},
			{ref: "foreign", aad: []byte("foreign"), text: foreign},
			{ref: "damaged", aad: []byte("damaged"), text: "v2.no-such-key.not-an-envelope"},
		},
	}
	return ring, fixture
}

// TestASurveyCountsWhatEachKeyHoldsAndWritesNothing covers the question an
// operator has before a rotation, which they otherwise have to guess at: how
// much of this table would break if the retired key came out.
func TestASurveyCountsWhatEachKeyHoldsAndWritesNothing(t *testing.T) {
	ring, fixture := newRotationFixture(t)
	before := fixture.row(t, "stale").text

	reports, err := Rewrap(t.Context(), ring, SurveyOnly, fixture.store())
	require.NoError(t, err)
	require.Len(t, reports, 1)
	report := reports[0]

	assert.Equal(t, "iam_example.secret_ciphertext", report.Store)
	assert.Equal(t, int64(6), report.Total)
	assert.Equal(t, int64(1), report.Current)
	assert.Equal(t, int64(2), report.Retired)
	assert.Equal(t, int64(1), report.Legacy)
	assert.Equal(t, int64(2), report.Unreadable)
	assert.Equal(t, int64(0), report.Rewrapped)
	assert.Equal(t, int64(3), report.Pending(),
		"a survey has to say how much work is outstanding, not just that some is")
	assert.Equal(t, report.Total,
		report.Current+report.Retired+report.Legacy+report.Unreadable,
		"the counts partition the table; a report that does not add up cannot be acted on")
	assert.Equal(t, 0, fixture.writes, "a survey that writes is not a survey")
	assert.Equal(t, before, fixture.row(t, "stale").text)
}

// TestARewrapConvertsEverythingAnOlderKeySealed is the change that lets the
// answer become yes. The second pass is the part that matters: it is what an
// operator runs to confirm the first one finished.
func TestARewrapConvertsEverythingAnOlderKeySealed(t *testing.T) {
	ring, fixture := newRotationFixture(t)

	reports, err := Rewrap(t.Context(), ring, RewrapStale, fixture.store())
	require.NoError(t, err)
	assert.Equal(t, int64(3), reports[0].Rewrapped)
	assert.Equal(t, int64(0), reports[0].Skipped)
	assert.Equal(t, int64(2), reports[0].Unreadable)
	assert.Equal(t, int64(0), reports[0].Pending(),
		"a pass that converted everything it found has to say so in the same breath")

	// The values have to survive the conversion, or the rewrap has replaced an
	// outage that has not happened yet with one that has.
	opened, err := ring.Open(rewrapPurpose, []byte("stale"), fixture.row(t, "stale").text)
	require.NoError(t, err)
	assert.Equal(t, "also-not-a-real-secret", string(opened))
	opened, err = ring.Open(rewrapPurpose, nil, fixture.row(t, "unbound").text)
	require.NoError(t, err)
	assert.Equal(t, "unbound-not-a-real-secret", string(opened))
	opened, err = ring.Open(rewrapPurpose, []byte("legacy"), fixture.row(t, "legacy").text)
	require.NoError(t, err)
	assert.Equal(t, "legacy-not-a-real-secret", string(opened))

	reports, err = Rewrap(t.Context(), ring, SurveyOnly, fixture.store())
	require.NoError(t, err)
	assert.Equal(t, int64(4), reports[0].Current)
	assert.Equal(t, int64(0), reports[0].Pending())
	assert.Equal(t, int64(2), reports[0].Unreadable,
		"a missing key is not repaired by resealing the rows that key never sealed")
}

// TestARowChangedUnderneathIsSkippedRatherThanOverwritten covers the race a
// rewrap runs into on a live database. Losing it is harmless -- whatever
// changed the row sealed under the active key already -- so it is counted
// rather than treated as a failure.
func TestARowChangedUnderneathIsSkippedRatherThanOverwritten(t *testing.T) {
	ring, fixture := newRotationFixture(t)
	for index := range fixture.rows {
		if fixture.rows[index].ref == "stale" {
			fixture.rows[index].stale = true
		}
	}
	before := fixture.row(t, "stale").text

	reports, err := Rewrap(t.Context(), ring, RewrapStale, fixture.store())
	require.NoError(t, err)
	assert.Equal(t, int64(1), reports[0].Skipped)
	assert.Equal(t, int64(2), reports[0].Rewrapped)
	assert.Equal(t, before, fixture.row(t, "stale").text,
		"the value the other writer stored has to survive the pass")
	assert.Equal(t, int64(0), reports[0].Pending(),
		"the row that changed underneath was changed by something sealing with the active key")
}

// TestAPassThatCannotFinishSaysSoRatherThanReportingProgress separates the two
// kinds of trouble. A value no key opens is a finding; a store that cannot be
// read or written is the pass itself malfunctioning, and reporting a partial
// count for it would invite an operator to act on numbers that mean nothing.
func TestAPassThatCannotFinishSaysSoRatherThanReportingProgress(t *testing.T) {
	ring, fixture := newRotationFixture(t)
	fixture.writeErr = errors.New("database is read only")

	_, err := Rewrap(t.Context(), ring, RewrapStale, fixture.store())
	require.Error(t, err)
	assert.ErrorContains(t, err, "iam_example.secret_ciphertext")
	assert.ErrorContains(t, err, "database is read only")

	_, unreadable := newRotationFixture(t)
	unreadable.readErr = errors.New("no such table")
	_, err = Rewrap(t.Context(), ring, SurveyOnly, unreadable.store())
	assert.ErrorContains(t, err, "no such table")
}

// TestRewrapRefusesWhatItCannotOperateOn keeps a half-described store from
// looking like an empty one. A store with no reader reports zero rows, and zero
// pending rows is exactly the answer that ends with a key being deleted.
func TestRewrapRefusesWhatItCannotOperateOn(t *testing.T) {
	ring := newTestRing(t, secondKey, firstKey)

	_, err := Rewrap(t.Context(), nil, SurveyOnly)
	assert.ErrorContains(t, err, "key ring")

	_, err = Rewrap(t.Context(), ring, SurveyOnly, SealedStore{Name: "iam_example.column"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "iam_example.column")
	assert.ErrorContains(t, err, "read and write")

	_, err = Rewrap(t.Context(), ring, SurveyOnly, SealedStore{
		Name:   "iam_example.column",
		Values: func(context.Context) ([]SealedValue, error) { return nil, nil },
	})
	assert.ErrorContains(t, err, "read and write")
}

// TestAResultKeepsWhatCanBeResealedApartFromWhatCannot is the distinction the
// whole report exists to preserve. Sealed values can be converted; derived ones
// are one-way digests of secrets the deployment does not keep, so they can only
// be waited out or reissued -- and a total that mixed them would read as
// permission to delete a key that recovery codes still depend on.
func TestAResultKeepsWhatCanBeResealedApartFromWhatCannot(t *testing.T) {
	expiry := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	result := RewrapResult{
		Sealed: []StoreReport{
			{Store: "iam_credentials.totp_secret", Total: 4, Current: 2, Retired: 1, Legacy: 1},
			{Store: "iam_passkeys.credential_ciphertext", Total: 2, Current: 1, Unreadable: 1},
		},
		Derived: []DerivedStoreReport{
			{Store: "iam_recovery_codes.code_hash", Outstanding: 7, Reissue: "generate a new set"},
			{Store: "iam_email_verification_tokens.token_hmac", Outstanding: 2, Until: expiry},
		},
	}

	assert.Equal(t, int64(2), result.Pending())
	assert.Equal(t, int64(1), result.Unreadable())
	assert.Equal(t, int64(9), result.Outstanding())

	empty := RewrapResult{}
	assert.Equal(t, int64(0), empty.Pending())
	assert.Equal(t, int64(0), empty.Unreadable())
	assert.Equal(t, int64(0), empty.Outstanding())
}
