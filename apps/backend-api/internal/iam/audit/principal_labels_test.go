package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrincipalLabelsBatchReadsAndExportsPreserveAuditFacts(t *testing.T) {
	h := newAuditHarness(t, "labels_")
	first, err := h.service.Append(t.Context(), EventInput{TenantID: 7, PrincipalID: 11, EventType: "login", TargetType: "principal", TargetID: 12, Outcome: "success", Detail: map[string]any{"viewed_principal_id": "13"}})
	require.NoError(t, err)
	_, err = h.service.Append(t.Context(), EventInput{TenantID: 7, PrincipalID: 11, EventType: "other", TargetType: "oauth_client", TargetID: 99, Outcome: "success"})
	require.NoError(t, err)
	calls := 0
	name := "Current nickname"
	h.service.principalLabels = func(_ context.Context, tenant guid.ID, ids []guid.ID) (map[guid.ID]PrincipalLabel, error) {
		calls++
		assert.Equal(t, guid.ID(7), tenant)
		assert.ElementsMatch(t, []guid.ID{11, 12, 13}, ids)
		return map[guid.ID]PrincipalLabel{11: {DisplayName: &name}, 12: {Anonymized: true}, 13: {DisplayName: &name}}, nil
	}
	events, total, err := h.service.List(t.Context(), Filter{TenantID: 7, Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, 1, calls, "one identity lookup for the whole page")
	for _, event := range events {
		require.NotNil(t, event.Actor)
		assert.Equal(t, name, *event.Actor.DisplayName)
		if event.TargetType == "oauth_client" {
			assert.Nil(t, event.TargetPrincipal, "other resource IDs must never be resolved as principal IDs")
		}
	}
	got, err := h.service.Get(t.Context(), 7, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first.EventHash, got.EventHash)
	assert.Equal(t, first.Detail, got.Detail)
	require.NotNil(t, got.TargetPrincipal)
	assert.True(t, got.TargetPrincipal.Anonymized)
	require.NotNil(t, got.ViewedPrincipal)
	assert.Equal(t, 2, calls)

	snapshot, err := h.service.PrepareExport(t.Context(), Filter{TenantID: 7})
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, h.service.WriteExport(t.Context(), snapshot, &output))
	assert.Equal(t, 3, calls, "one identity lookup for the export batch; none for chain verification")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 4)
	var exported exportEvent
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &exported))
	assert.Equal(t, first.EventHash, exported.Event.EventHash)
	assert.Equal(t, "login", exported.Event.EventType)
	assert.Equal(t, guid.ID(11), exported.Event.PrincipalID)
	require.NotNil(t, exported.Event.Actor)
	assert.Equal(t, name, *exported.Event.Actor.DisplayName)
	integrity, err := h.service.Verify(t.Context(), 7)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
	assert.Equal(t, 3, calls)
	stream, err := h.service.ReadStream(t.Context(), 7, 0, 10)
	require.NoError(t, err)
	serialized, err := json.Marshal(stream)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), name, "privacy-safe event streams remain unchanged")
}

func TestPrincipalLabelsFailClosedAndSkipAbsentReferences(t *testing.T) {
	errLookup := errors.New("identity lookup unavailable")
	s := &Service{principalLabels: func(context.Context, guid.ID, []guid.ID) (map[guid.ID]PrincipalLabel, error) { return nil, errLookup }}
	require.NoError(t, s.labelEvents(t.Context(), 7, nil))
	require.NoError(t, s.labelEvents(t.Context(), 7, []Event{{TargetType: "oauth_client", TargetID: 2, Detail: json.RawMessage(`{"viewed_principal_id":"invalid"}`)}}))
	assert.ErrorIs(t, s.labelEvents(t.Context(), 7, []Event{{PrincipalID: 1}}), errLookup)
	assert.Zero(t, viewedPrincipalID(Event{Detail: json.RawMessage(`{"viewed_principal_id":4}`)}))
}
