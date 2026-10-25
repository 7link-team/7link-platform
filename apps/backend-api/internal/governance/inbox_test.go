package governance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func inboxFixture(id guid.ID, kind, priority string) InboxItem {
	return InboxItem{InboxKey: InboxKey{ID: id, Kind: kind, Priority: priority, CreatedAt: time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)}, TenantID: 1, RecipientID: 2, ProjectID: 3, Title: "Pending source decision", ObjectID: id, ObjectType: "work_item"}
}

func inboxFixtureSource(kind string, items []InboxItem) InboxSource {
	return InboxSource{Kind: kind, Read: func(_ context.Context, req InboxRequest) ([]InboxItem, error) {
		rows := []InboxItem{}
		for _, row := range items {
			if req.After == nil || CompareInboxKeys(row.InboxKey, *req.After) > 0 {
				rows = append(rows, row)
				if len(rows) == req.Limit {
					break
				}
			}
		}
		return rows, nil
	}}
}

func TestInboxCatalogAndContribution(t *testing.T) {
	kinds := InboxKinds()
	require.Len(t, kinds, 26)
	require.Len(t, slices.Compact(slices.Sorted(slices.Values(kinds))), 26)
	kinds[0] = "changed"
	require.Equal(t, "WORK_ITEM_REVIEW", InboxKinds()[0])
	var nilRegistry *inboxRegistry
	require.ErrorIs(t, nilRegistry.Register(InboxSource{}), ErrUnavailable)
	r := &inboxRegistry{}
	require.ErrorIs(t, r.Register(InboxSource{Kind: "internal_qa.failed", Read: func(context.Context, InboxRequest) ([]InboxItem, error) { return nil, nil }}), ErrInvalidArgument)
	require.ErrorIs(t, r.Register(InboxSource{Kind: "WORK_ITEM_REVIEW"}), ErrInvalidArgument)
	for _, kind := range InboxKinds() {
		require.NoError(t, r.Register(inboxFixtureSource(kind, nil)))
	}
	require.ErrorIs(t, r.Register(inboxFixtureSource("WORK_ITEM_REVIEW", nil)), ErrInvalidArgument)
}

func TestInboxCurrentSourcesMergeStableCursorAndCapabilities(t *testing.T) {
	r := &inboxRegistry{}
	a := inboxFixture(10, "WORK_ITEM_REVIEW", "HIGH")
	b := inboxFixture(11, "WORK_ITEM_REVIEW", "NORMAL")
	c := inboxFixture(12, "INTERACTION", "CRITICAL")
	require.NoError(t, r.Register(inboxFixtureSource(a.Kind, []InboxItem{a, b})))
	require.NoError(t, r.Register(inboxFixtureSource(c.Kind, []InboxItem{c})))
	req := InboxRequest{TenantID: 1, PrincipalID: 2, Limit: 1}
	first, err := r.read(t.Context(), req, "")
	require.NoError(t, err)
	require.Equal(t, []InboxItem{c}, first.Items)
	require.True(t, first.HasMore)
	require.Len(t, first.Capabilities, 26)
	require.Equal(t, "AVAILABLE", first.Capabilities[0].Status)
	require.Equal(t, "UNAVAILABLE", first.Capabilities[2].Status)
	second, err := r.read(t.Context(), req, first.NextCursor)
	require.NoError(t, err)
	require.Equal(t, []InboxItem{a}, second.Items)
	last, err := r.read(t.Context(), req, second.NextCursor)
	require.NoError(t, err)
	require.Equal(t, []InboxItem{b}, last.Items)
	require.False(t, last.HasMore)
	require.Empty(t, last.NextCursor)
	// A new decision is read from current state; no persisted stale projection.
	r.sources[a.Kind] = inboxFixtureSource(a.Kind, nil)
	current, err := r.read(t.Context(), req, "")
	require.NoError(t, err)
	require.Equal(t, []InboxItem{c}, current.Items)
	require.False(t, current.HasMore)
	for _, change := range []func(*InboxRequest){func(r *InboxRequest) { r.PrincipalID = 9 }, func(r *InboxRequest) { r.TenantID = 9 }, func(r *InboxRequest) { r.ProjectID = 3 }, func(r *InboxRequest) { r.Kind = "INTERACTION" }} {
		other := req
		change(&other)
		_, err := r.read(t.Context(), other, first.NextCursor)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}

func TestInboxFailClosedSourceIsolationAndInternalQA(t *testing.T) {
	base := inboxFixture(10, "WORK_ITEM_REVIEW", "HIGH")
	for name, mutate := range map[string]func(*InboxItem){
		"tenant": func(i *InboxItem) { i.TenantID = 9 }, "recipient": func(i *InboxItem) { i.RecipientID = 9 }, "project": func(i *InboxItem) { i.ProjectID = 9 },
		"internal QA": func(i *InboxItem) { i.Visibility = "INTERNAL" }, "unregistered kind": func(i *InboxItem) { i.Kind = "verification.completed" },
		"other kind": func(i *InboxItem) { i.Kind = "INTERACTION" }, "object": func(i *InboxItem) { i.ObjectID = 0 }, "title": func(i *InboxItem) { i.Title = "" },
		"priority": func(i *InboxItem) { i.Priority = "LOW" }, "id": func(i *InboxItem) { i.ID = 0 }, "created": func(i *InboxItem) { i.CreatedAt = time.Time{} }, "due": func(i *InboxItem) { i.DueAt = &time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			item := base
			mutate(&item)
			r := &inboxRegistry{}
			require.NoError(t, r.Register(inboxFixtureSource(base.Kind, []InboxItem{item})))
			page, err := r.read(t.Context(), InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Limit: 2}, "")
			require.ErrorIs(t, err, ErrInvalidArgument)
			require.Empty(t, page.Items)
		})
	}
	for _, rows := range [][]InboxItem{{base, base}, {inboxFixture(11, base.Kind, "NORMAL"), base}, {base, base, base}} {
		r := &inboxRegistry{}
		require.NoError(t, r.Register(InboxSource{Kind: base.Kind, Read: func(context.Context, InboxRequest) ([]InboxItem, error) { return rows, nil }}))
		_, err := r.read(t.Context(), InboxRequest{TenantID: 1, PrincipalID: 2, Limit: 1}, "")
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}

func TestInboxUnavailableValidationAndCancellation(t *testing.T) {
	req := InboxRequest{TenantID: 1, PrincipalID: 2, Limit: 2}
	var missing *inboxRegistry
	_, err := missing.read(t.Context(), req, "")
	require.ErrorIs(t, err, ErrUnavailable)
	r := &inboxRegistry{}
	for _, change := range []func(*InboxRequest){func(r *InboxRequest) { r.TenantID = 0 }, func(r *InboxRequest) { r.PrincipalID = 0 }, func(r *InboxRequest) { r.ProjectID = -1 }, func(r *InboxRequest) { r.Limit = 0 }, func(r *InboxRequest) { r.Limit = 101 }, func(r *InboxRequest) { r.Kind = "unknown" }, func(r *InboxRequest) { r.After = &InboxKey{} }} {
		invalid := req
		change(&invalid)
		_, err := r.read(t.Context(), invalid, "")
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, cursor := range []string{"%", strings.Repeat("x", 2049), base64.RawURLEncoding.EncodeToString([]byte(`{`)), base64.RawURLEncoding.EncodeToString([]byte(`{}`))} {
		_, err := r.read(t.Context(), req, cursor)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	key := inboxFixture(10, "WORK_ITEM_REVIEW", "HIGH").InboxKey
	encoded, _ := json.Marshal(inboxCursor{Scope: inboxScope(req), Key: key})
	cursor := base64.RawURLEncoding.EncodeToString(encoded)
	require.NoError(t, r.Register(InboxSource{Kind: key.Kind, Read: func(context.Context, InboxRequest) ([]InboxItem, error) { return nil, ErrUnavailable }}))
	page, err := r.read(t.Context(), req, cursor)
	require.NoError(t, err)
	require.Equal(t, "UNAVAILABLE", page.Capabilities[0].Status)
	r.sources[key.Kind] = inboxFixtureSource(key.Kind, []InboxItem{inboxFixture(10, key.Kind, key.Priority)})
	// A source ignoring the keyset must not duplicate a delivered decision.
	r.sources[key.Kind] = InboxSource{Kind: key.Kind, Read: func(context.Context, InboxRequest) ([]InboxItem, error) {
		return []InboxItem{inboxFixture(10, key.Kind, key.Priority)}, nil
	}}
	_, err = r.read(t.Context(), req, cursor)
	require.ErrorIs(t, err, ErrInvalidArgument)
	failure := errors.New("database failure")
	r.sources[key.Kind] = InboxSource{Kind: key.Kind, Read: func(context.Context, InboxRequest) ([]InboxItem, error) { return nil, failure }}
	_, err = r.read(t.Context(), req, "")
	require.ErrorIs(t, err, failure)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = r.read(ctx, req, "")
	require.ErrorIs(t, err, context.Canceled)
	ctx, cancel = context.WithCancel(t.Context())
	r.sources[key.Kind] = InboxSource{Kind: key.Kind, Read: func(context.Context, InboxRequest) ([]InboxItem, error) { cancel(); return nil, nil }}
	_, err = r.read(ctx, req, "")
	require.ErrorIs(t, err, context.Canceled)
	req.Kind = "INTERACTION"
	page, err = r.read(t.Context(), req, "")
	require.NoError(t, err)
	require.Len(t, page.Capabilities, 1)
}

func TestInboxKeyOrderAllTiesAndNullableDueDate(t *testing.T) {
	base := inboxFixture(10, "WORK_ITEM_REVIEW", "HIGH").InboxKey
	due := base.CreatedAt.Add(time.Hour)
	for name, change := range map[string]func(*InboxKey){
		"priority": func(k *InboxKey) { k.Priority = "CRITICAL" }, "due": func(k *InboxKey) { k.DueAt = &due }, "created": func(k *InboxKey) { k.CreatedAt = k.CreatedAt.Add(-time.Hour) }, "kind": func(k *InboxKey) { k.Kind = "ANDON" }, "id": func(k *InboxKey) { k.ID = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			a := base
			change(&a)
			require.Negative(t, CompareInboxKeys(a, base))
			require.Positive(t, CompareInboxKeys(base, a))
		})
	}
	a, b := base, base
	a.DueAt = &due
	b.DueAt = &due
	require.Zero(t, CompareInboxKeys(a, b))
	later := due.Add(time.Hour)
	b.DueAt = &later
	require.Negative(t, CompareInboxKeys(a, b))
	require.Zero(t, CompareInboxKeys(base, base))
	require.Zero(t, priorityRank("INVALID"))
}
