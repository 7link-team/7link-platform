package federation

import (
	"sync"
	"time"
)

// replayRegistry claims protocol identifiers until their validity window
// expires. It fails closed at capacity so load cannot evict a live claim and
// make an already consumed request usable again.
type replayRegistry struct {
	mu      sync.Mutex
	limit   int
	claimed map[string]time.Time
}

func newReplayRegistry(limit int) *replayRegistry {
	if limit < 1 {
		limit = 1
	}
	return &replayRegistry{limit: limit, claimed: make(map[string]time.Time)}
}

func (r *replayRegistry) claim(key string, expiresAt, now time.Time) bool {
	if r == nil || key == "" || !expiresAt.After(now) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for existing, expiry := range r.claimed {
		if !expiry.After(now) {
			delete(r.claimed, existing)
		}
	}
	if _, exists := r.claimed[key]; exists || len(r.claimed) >= r.limit {
		return false
	}
	r.claimed[key] = expiresAt
	return true
}
