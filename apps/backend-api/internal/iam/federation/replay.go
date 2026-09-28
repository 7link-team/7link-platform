package federation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// replaySweepInterval bounds how often one process asks the database to drop
// claims whose window has closed. The sweep is housekeeping, not correctness --
// an expired row refuses a key that is itself already expired -- so running it
// on every callback would spend a range delete to buy nothing.
const replaySweepInterval = time.Minute

// errReplayUnavailable is returned when there is nowhere to record a claim.
// It is deliberately not the same as "this identifier was already used": one
// says the request is a replay, the other says we cannot tell, and a federated
// login must be refused either way but only the second is an operator problem.
var errReplayUnavailable = errors.New("federation replay store unavailable")

// replayRegistry claims protocol identifiers -- OIDC and SAML state nonces,
// SAML assertion IDs, SAML logout request IDs -- until their validity window
// closes, so each one is honored exactly once.
//
// The claims live in the database rather than in this process. Single-use is a
// property of the deployment, not of a replica: with the claim held in memory,
// a callback consumed by one replica is still unclaimed on every other, and
// replaying it there succeeds. That failure appears the day a deployment adds
// its second replica, with nothing in the code changing to announce it, and
// there is no distributed cache here to hold the claim instead.
//
// The database also supplies the part a map cannot: the primary key makes the
// claim atomic between replicas. Two callbacks racing with the same nonce both
// attempt the insert and exactly one of them wins, which a read-then-write
// could not guarantee no matter where the state was kept.
type replayRegistry struct {
	repo *Repository

	mu      sync.Mutex
	sweptAt time.Time
}

func newReplayRegistry(repo *Repository) *replayRegistry {
	return &replayRegistry{repo: repo}
}

// claim records one identifier as consumed. It reports false when the
// identifier was already claimed or is already expired, and an error when the
// store could not be reached -- callers refuse the request in both cases, but
// only the second means something is wrong with the deployment rather than
// with the request.
func (r *replayRegistry) claim(ctx context.Context, key string, expiresAt, now time.Time) (bool, error) {
	if r == nil || r.repo == nil {
		return false, errReplayUnavailable
	}
	if key == "" || !expiresAt.After(now) {
		return false, nil
	}
	r.sweep(ctx, now)
	return r.repo.claimReplay(ctx, key, expiresAt, now)
}

// sweep drops claims whose window has closed, at most once per interval per
// process. A failure here is logged rather than returned: the claim that
// follows is still correct on a table that was not swept, and refusing a valid
// login because housekeeping failed would turn a growing table into an outage.
func (r *replayRegistry) sweep(ctx context.Context, now time.Time) {
	r.mu.Lock()
	if now.Sub(r.sweptAt) < replaySweepInterval {
		r.mu.Unlock()
		return
	}
	r.sweptAt = now
	r.mu.Unlock()

	if err := r.repo.sweepReplayClaims(ctx); err != nil {
		slog.WarnContext(ctx, "federation replay claim sweep failed", "error", err)
	}
}
