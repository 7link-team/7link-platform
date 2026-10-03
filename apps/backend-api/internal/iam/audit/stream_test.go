package audit

import (
	"encoding/json"
	"reflect"
	"testing"

	"7link.com/platform/internal/infra/guid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appendStreamEvent records one event the way a module does, with detail, an
// address and an agent string, so the projection has something to withhold.
func (h *auditHarness) appendStreamEvent(t *testing.T, tenantID guid.ID, eventType string) {
	t.Helper()
	tx := h.db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, h.service.DomainAppender()(t.Context(), tx, DomainEvent{
		TenantID: tenantID, PrincipalID: 51, EventType: eventType,
		TargetType: "principal", TargetID: 77,
		Detail: map[string]any{"email": "ada@example.com", "display_name": "Ada Lovelace"},
	}))
	require.NoError(t, tx.Commit().Error)
}

// TestStreamEventHasNoFieldForPersonalData is the structural half of the promise.
// A consumer outside this module cannot forward the detail map, the address or
// the agent string because the type it receives has nowhere to put them, and a
// field added later would fail here rather than at somebody else's endpoint.
func TestStreamEventHasNoFieldForPersonalData(t *testing.T) {
	fields := map[string]struct{}{}
	structure := reflect.TypeOf(StreamEvent{})
	for index := 0; index < structure.NumField(); index++ {
		fields[structure.Field(index).Name] = struct{}{}
	}

	assert.Equal(t, map[string]struct{}{
		"ID": {}, "TenantID": {}, "Sequence": {}, "EventType": {},
		"TargetType": {}, "TargetID": {}, "PrincipalID": {}, "Outcome": {}, "OccurredAt": {},
	}, fields, "a field added here reaches every webhook receiver, so the set is frozen on purpose")

	// The serialized form is what a receiver actually reads.
	encoded, err := json.Marshal(StreamEvent{})
	require.NoError(t, err)
	for _, forbidden := range []string{"detail", "ip_address", "user_agent", "email", "display_name"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
}

func TestReadStreamHandsOutEventsInSequenceOrder(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	tenantID := guid.ID(900)
	for _, eventType := range []string{"first", "second", "third"} {
		h.appendStreamEvent(t, tenantID, eventType)
	}

	events, err := h.service.ReadStream(t.Context(), tenantID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 3)
	for index, event := range events {
		assert.EqualValues(t, index+1, event.Sequence, "the chain is read in order")
		assert.Equal(t, tenantID, event.TenantID)
		assert.Equal(t, "success", event.Outcome)
		assert.Equal(t, guid.ID(51), event.PrincipalID)
		assert.Equal(t, guid.ID(77), event.TargetID)
		assert.Equal(t, "principal", event.TargetType)
		assert.False(t, event.OccurredAt.IsZero())
		assert.NotZero(t, event.ID)
	}
	assert.Equal(t, "first", events[0].EventType)
	assert.Equal(t, "third", events[2].EventType)

	// A cursor past some of them returns only what follows, which is what makes
	// "deliver each event once" a single stored number.
	rest, err := h.service.ReadStream(t.Context(), tenantID, 1, 10)
	require.NoError(t, err)
	require.Len(t, rest, 2)
	assert.EqualValues(t, 2, rest[0].Sequence)

	// At the end a reader returns nothing rather than repeating the last event.
	none, err := h.service.ReadStream(t.Context(), tenantID, 3, 10)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestReadStreamNeverCrossesTenants(t *testing.T) {
	h := newAuditHarness(t, "")
	h.appendStreamEvent(t, guid.ID(901), "mine")
	h.appendStreamEvent(t, guid.ID(902), "theirs")

	events, err := h.service.ReadStream(t.Context(), guid.ID(901), 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "mine", events[0].EventType)
	assert.Equal(t, guid.ID(901), events[0].TenantID)
}

func TestReadStreamBoundsWhatOneReadCosts(t *testing.T) {
	// A follower that has fallen far behind must not be able to pull the whole
	// chain into memory in one statement.
	h := newAuditHarness(t, "")
	tenantID := guid.ID(903)
	for index := 0; index < 5; index++ {
		h.appendStreamEvent(t, tenantID, "event")
	}

	limited, err := h.service.ReadStream(t.Context(), tenantID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, limited, 2, "the caller's limit is honored")

	// A limit beyond the cap, or none at all, is capped rather than refused.
	capped, err := h.service.ReadStream(t.Context(), tenantID, 0, maxStreamBatch*10)
	require.NoError(t, err)
	assert.Len(t, capped, 5)

	defaulted, err := h.service.ReadStream(t.Context(), tenantID, 0, 0)
	require.NoError(t, err)
	assert.Len(t, defaulted, 5)

	// A negative cursor reads from the beginning rather than underflowing.
	fromStart, err := h.service.ReadStream(t.Context(), tenantID, -5, 10)
	require.NoError(t, err)
	assert.Len(t, fromStart, 5)
}

func TestReadStreamRequiresATenantAndAService(t *testing.T) {
	h := newAuditHarness(t, "")

	_, err := h.service.ReadStream(t.Context(), 0, 0, 10)
	require.ErrorIs(t, err, ErrInvalidEvent, "the chain is per tenant, so reading without one is meaningless")

	var absent *Service
	_, err = absent.ReadStream(t.Context(), guid.ID(1), 0, 10)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestModuleProvidesTheStreamReader(t *testing.T) {
	// A follower resolves a function, not this module, so the capability has to be
	// reachable through the adapter even before the service exists.
	instance := &Module{}
	_, err := instance.readStream(t.Context(), guid.ID(1), 0, 10)
	require.ErrorIs(t, err, ErrUnavailable)

	h := newAuditHarness(t, "")
	h.appendStreamEvent(t, guid.ID(904), "through_the_module")
	wired := &Module{service: h.service}
	events, err := wired.readStream(t.Context(), guid.ID(904), 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "through_the_module", events[0].EventType)
}

func TestReadStreamReportsAMissingDatasource(t *testing.T) {
	repo := &Repository{}
	_, err := repo.readStream(t.Context(), guid.ID(1), 0, 10)
	require.ErrorIs(t, err, errRepositoryUnavailable)
}

func TestReadStreamReportsAFailedRead(t *testing.T) {
	// A chain that cannot be read is not an empty chain: a follower that treated
	// the two the same would advance its cursor past events nobody has seen.
	h := newAuditHarness(t, "")
	tenantID := guid.ID(905)
	h.appendStreamEvent(t, tenantID, "before_the_table_went_away")
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_audit_events")).Error)

	_, err := h.service.ReadStream(t.Context(), tenantID, 0, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read audit event stream")
}
