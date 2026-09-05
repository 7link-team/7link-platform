package keyringx

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RewrapMode says whether a pass over a sealed store writes what it finds.
type RewrapMode int

const (
	// SurveyOnly counts what each key still holds and writes nothing. It is
	// what answers "may the retired key come out yet", which is otherwise a
	// question an operator can only guess at.
	SurveyOnly RewrapMode = iota
	// RewrapStale reseals every value an older key sealed, so that the answer
	// to that question can become yes.
	RewrapStale
)

// SealedValue is one stored ciphertext together with what binds it to its row.
type SealedValue struct {
	// Ref names the row the way an operator would search for it. It appears in
	// reports and errors only; nothing looks a row up by it.
	Ref string
	// AAD is the associated data the value was sealed with. A store that seals
	// without any leaves this nil.
	AAD []byte
	// Ciphertext is the stored envelope.
	Ciphertext string
}

// SealedStore is one column of sealed values, described in the terms a rewrap
// needs and no more: how to read what is there, and how to put one value back.
// Both are closures so this package keeps no storage dependency -- the module
// that owns the table goes on owning its queries.
type SealedStore struct {
	// Name identifies the store in the report, in the operator's terms rather
	// than the code's: "iam_credentials.totp_secret", not "the TOTP secret".
	Name string
	// Purpose is the derivation label the store's values were sealed under.
	Purpose string
	// Values reads every row that carries a sealed value.
	Values func(ctx context.Context) ([]SealedValue, error)
	// Write stores one resealed value and reports whether it landed. False
	// means the row changed underneath the pass, which is a skip rather than a
	// failure: whatever changed it sealed under the active key already.
	Write func(ctx context.Context, value SealedValue, resealed string) (bool, error)
}

// StoreReport is what one pass found in one sealed store. The four counts
// before Rewrapped partition Total, so a report that does not add up is a bug
// rather than something to interpret.
type StoreReport struct {
	Store string `json:"store"`
	Total int64  `json:"total"`
	// Current is sealed by the active key and needs nothing.
	Current int64 `json:"current"`
	// Retired is sealed by a key the ring still carries but no longer seals
	// with. This is what a rewrap converts. It counts what the pass found, so a
	// rewrap reports it alongside Rewrapped rather than instead of it: the two
	// together say what was wrong and what was done about it.
	Retired int64 `json:"retired"`
	// Legacy was sealed before envelopes recorded which key produced them, so
	// its key cannot be named, only tried. A rewrap converts these too, and
	// doing so is what gives the store an answer instead of a maybe.
	Legacy int64 `json:"legacy"`
	// Unreadable is sealed by a key this deployment no longer holds, or is not
	// an envelope this build understands. A rewrap cannot repair these. Putting
	// the missing key back into the configuration can, and that is the first
	// thing to try, because the alternative reading -- the stored bytes are
	// damaged -- is both rarer and worse.
	Unreadable int64 `json:"unreadable"`
	// Rewrapped is what this pass wrote. A survey leaves it zero.
	Rewrapped int64 `json:"rewrapped"`
	// Skipped is what changed underneath the pass and was left alone.
	Skipped int64 `json:"skipped"`
}

// Pending is how many values still depend on a key other than the active one
// once this pass has ended. A survey converts nothing, so it is everything the
// older keys hold; a rewrap converts them, so it falls to zero.
//
// Skipped counts against it for the same reason Rewrapped does: a row that
// changed underneath the pass was changed by something that seals with the
// active key, so it no longer needs a retired one either.
//
// Unreadable is deliberately absent. Those values do not depend on a retired
// key the deployment still holds; they depend on one it has already lost, and
// folding the two together would let a survey that found nothing recoverable
// read as a survey that found nothing wrong.
func (r StoreReport) Pending() int64 {
	pending := r.Retired + r.Legacy - r.Rewrapped - r.Skipped
	if pending < 0 {
		return 0
	}
	return pending
}

// DerivedStoreReport describes a store a rotation cannot rewrap. Its values are
// one-way digests of secrets the deployment does not keep a copy of, so there
// is nothing to reseal: they can only be waited out or reissued. Reporting them
// is what stops "the retired key may come out now" from being read off a
// rewrap that never covered them in the first place.
type DerivedStoreReport struct {
	Store string `json:"store"`
	// Outstanding is how many values are still live -- neither used nor
	// expired -- and therefore still depend on the key that derived them.
	Outstanding int64 `json:"outstanding"`
	// Until is when the last of them expires. A zero time means the store has
	// no expiry at all, so waiting will never empty it.
	Until time.Time `json:"until,omitzero"`
	// Reissue says, in an operator's words, what clears the store when waiting
	// cannot. It belongs to the module that owns the store, because only that
	// module knows what reissuing one of its values actually costs a person.
	Reissue string `json:"reissue,omitempty"`
}

// Rewrap walks each store once and reports what it found. In RewrapStale mode
// it also reseals everything an older key sealed.
//
// A value no key can open is counted, not returned as an error: it is the
// finding the survey exists to surface, and one such row must not hide the
// state of every row after it. A failure to seal or to write is returned,
// because that is the pass itself malfunctioning.
func Rewrap(
	ctx context.Context,
	keys *Keyring,
	mode RewrapMode,
	stores ...SealedStore,
) ([]StoreReport, error) {
	if keys == nil {
		return nil, errors.New("keyringx: rewrap requires a key ring")
	}
	reports := make([]StoreReport, 0, len(stores))
	for _, store := range stores {
		report, err := rewrapStore(ctx, keys, mode, store)
		if err != nil {
			return nil, fmt.Errorf("rewrap %s: %w", store.Name, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func rewrapStore(
	ctx context.Context,
	keys *Keyring,
	mode RewrapMode,
	store SealedStore,
) (StoreReport, error) {
	if store.Values == nil || store.Write == nil {
		return StoreReport{}, errors.New("store must be able to read and write its values")
	}
	values, err := store.Values(ctx)
	if err != nil {
		return StoreReport{}, err
	}
	report := StoreReport{Store: store.Name, Total: int64(len(values))}
	for _, value := range values {
		id, recognized := keys.SealedBy(value.Ciphertext)
		if !recognized {
			report.Unreadable++
			continue
		}
		if id == keys.ActiveID() {
			report.Current++
			continue
		}
		// Everything past here is sealed by something other than the active
		// key, so it has to be opened -- both to learn whether the ring can
		// still open it at all, and to have anything to reseal.
		plain, err := keys.Open(store.Purpose, value.AAD, value.Ciphertext)
		if err != nil {
			report.Unreadable++
			continue
		}
		if id == "" {
			report.Legacy++
		} else {
			report.Retired++
		}
		if mode == SurveyOnly {
			clear(plain)
			continue
		}
		resealed, err := keys.Seal(store.Purpose, value.AAD, plain)
		clear(plain)
		if err != nil {
			return StoreReport{}, fmt.Errorf("reseal %s: %w", value.Ref, err)
		}
		wrote, err := store.Write(ctx, value, resealed)
		if err != nil {
			return StoreReport{}, fmt.Errorf("store %s: %w", value.Ref, err)
		}
		if wrote {
			report.Rewrapped++
		} else {
			report.Skipped++
		}
	}
	return report, nil
}

// RewrapResult is everything one module can say about its own dependence on
// retired keys: the stores a rewrap converts, and the stores it cannot.
//
// The two are kept apart on purpose. A rewrap that reports only what it
// converted reads as "nothing is left", and a deployment that acts on that
// takes out a key that unused recovery codes and unexpired links still need.
type RewrapResult struct {
	Sealed  []StoreReport        `json:"sealed,omitempty"`
	Derived []DerivedStoreReport `json:"derived,omitempty"`
}

// Pending is how many sealed values still depend on a key other than the
// active one once the pass has ended. It deliberately says nothing about the
// derived stores, which no rewrap can bring to zero.
func (r RewrapResult) Pending() int64 {
	var pending int64
	for _, report := range r.Sealed {
		pending += report.Pending()
	}
	return pending
}

// Unreadable is how many sealed values no key in the ring opens.
func (r RewrapResult) Unreadable() int64 {
	var unreadable int64
	for _, report := range r.Sealed {
		unreadable += report.Unreadable
	}
	return unreadable
}

// Outstanding is how many derived values are still live. While it is above
// zero, retiring the keys that produced them silently invalidates them.
func (r RewrapResult) Outstanding() int64 {
	var outstanding int64
	for _, report := range r.Derived {
		outstanding += report.Outstanding
	}
	return outstanding
}
