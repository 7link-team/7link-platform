package governance

import (
	"context"
	"errors"
	"sync"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoliciesEvaluateInOrderAndIsolateFacts(t *testing.T) {
	var catalog policyRegistry
	request := AuthorityRequest{Code: "user_view", Facts: &WorkItemFacts{Risk: "LOW"}, Spawn: &SpawnRequestFacts{ProjectIDs: []guid.ID{1}}}
	calls := []string{}
	codes := []string{"user_view"}
	require.NoError(t, catalog.Register(PolicyRule{ID: "first", Codes: codes, Evaluate: func(_ context.Context, req AuthorityRequest) (bool, error) {
		calls = append(calls, "first")
		req.Facts.Risk = "HIGH"
		req.Spawn.ProjectIDs[0] = 2
		return false, nil
	}, Explain: func(_ context.Context, req AuthorityRequest) string {
		t.Fatal("S8 explanation executed before a later S7 match")
		return "NO_AUTHORITY"
	}}))
	codes[0] = "role_view"
	require.NoError(t, catalog.Register(PolicyRule{ID: "unrelated", Codes: []string{"role_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) {
		t.Fatal("unrelated policy executed")
		return false, nil
	}}))
	require.NoError(t, catalog.Register(PolicyRule{ID: "second", Codes: []string{"user_view"}, Evaluate: func(_ context.Context, req AuthorityRequest) (bool, error) {
		calls = append(calls, "second")
		require.Equal(t, "LOW", req.Facts.Risk)
		require.Equal(t, guid.ID(1), req.Spawn.ProjectIDs[0])
		return true, nil
	}}))
	require.NoError(t, catalog.Register(PolicyRule{ID: "third", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) {
		t.Fatal("evaluation continued after match")
		return false, nil
	}}))
	_, err := catalog.evaluate(t.Context(), request)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Empty(t, calls)
	require.NoError(t, catalog.seal(authz.DefaultRegistry()))
	require.NoError(t, catalog.seal(authz.DefaultRegistry()))
	outcome, err := catalog.evaluate(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, policyOutcome{source: "POLICY:second"}, outcome)
	require.Equal(t, []string{"first", "second"}, calls)
	require.Equal(t, "LOW", request.Facts.Risk)
	require.Equal(t, guid.ID(1), request.Spawn.ProjectIDs[0])
	require.Error(t, catalog.Register(PolicyRule{ID: "late", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) { return true, nil }}))
}

func TestPolicyDenialReasonsErrorsAndCancellation(t *testing.T) {
	for _, reason := range []string{"", "NO_AUTHORITY", "SPAWN_QUOTA_EXCEEDED"} {
		var catalog policyRegistry
		require.NoError(t, catalog.Register(PolicyRule{ID: "first", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) { return false, nil }, Explain: func(context.Context, AuthorityRequest) string { return reason }}))
		require.NoError(t, catalog.Register(PolicyRule{ID: "second", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) { return false, nil }, Explain: func(context.Context, AuthorityRequest) string { return "NO_AUTHORITY" }}))
		require.NoError(t, catalog.seal(authz.DefaultRegistry()))
		result, err := catalog.evaluate(t.Context(), AuthorityRequest{Code: "user_view"})
		require.NoError(t, err)
		require.Empty(t, result.source)
		if reason == "" {
			require.Equal(t, "NO_AUTHORITY", result.reason)
		} else {
			require.Equal(t, reason, result.reason)
		}
		result, err = catalog.evaluate(t.Context(), AuthorityRequest{Code: "unknown"})
		require.NoError(t, err)
		require.Equal(t, "NO_AUTHORITY", result.reason)
	}
	failure := errors.New("dependency unavailable")
	for _, mode := range []string{"error", "canceled before", "canceled during", "canceled explanation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var catalog policyRegistry
			require.NoError(t, catalog.Register(PolicyRule{ID: "test", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) {
				if mode == "canceled before" {
					t.Fatal("canceled request evaluated")
				}
				if mode == "error" {
					return false, failure
				}
				if mode == "canceled during" {
					cancel()
					return true, nil
				}
				return false, nil
			}, Explain: func(context.Context, AuthorityRequest) string { cancel(); return "NO_AUTHORITY" }}))
			require.NoError(t, catalog.seal(authz.DefaultRegistry()))
			if mode == "canceled before" {
				cancel()
			}
			result, err := catalog.evaluate(ctx, AuthorityRequest{Code: "user_view"})
			require.Zero(t, result)
			if mode == "error" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestPolicyRegistrationRejectsForbiddenAndUnknownActions(t *testing.T) {
	evaluate := func(context.Context, AuthorityRequest) (bool, error) { return true, nil }
	for _, rule := range []PolicyRule{
		{}, {ID: "bad id", Codes: []string{"user_view"}, Evaluate: evaluate},
		{ID: "test", Evaluate: evaluate}, {ID: "test", Codes: []string{"user_view"}},
		{ID: "test", Codes: []string{""}, Evaluate: evaluate},
		{ID: "test", Codes: []string{"user_view", "user_view"}, Evaluate: evaluate},
		{ID: "test", Codes: []string{"user_view", "project_create"}, Evaluate: evaluate},
		{ID: "test", Codes: []string{"decision_pause"}, Evaluate: evaluate},
		{ID: "test", Codes: []string{"decision_approve"}, Evaluate: evaluate},
	} {
		var catalog policyRegistry
		require.ErrorIs(t, catalog.Register(rule), ErrInvalidArgument)
		require.Empty(t, catalog.rules)
	}
	var catalog policyRegistry
	rule := PolicyRule{ID: "test", Codes: []string{"plan_approve"}, Evaluate: evaluate}
	require.NoError(t, catalog.Register(rule))
	require.Error(t, catalog.Register(rule))
	registry := authz.DefaultRegistry()
	require.Error(t, catalog.seal(registry))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "plan", Verb: "approve", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, catalog.seal(registry))
	result, err := catalog.evaluate(t.Context(), AuthorityRequest{Code: "plan_approve"})
	require.NoError(t, err)
	require.Equal(t, "POLICY:test", result.source)
	var system policyRegistry
	require.NoError(t, system.Register(PolicyRule{ID: "test", Codes: []string{"platform_administer"}, Evaluate: evaluate}))
	require.Error(t, system.seal(registry))
	require.ErrorIs(t, system.seal(nil), ErrUnavailable)
	require.ErrorIs(t, (*policyRegistry)(nil).Register(rule), ErrUnavailable)
	require.ErrorIs(t, (*policyRegistry)(nil).seal(registry), ErrUnavailable)
	_, err = (*policyRegistry)(nil).evaluate(t.Context(), AuthorityRequest{})
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestPolicyEvaluationIsConcurrentAndDoesNotHoldRegistryLock(t *testing.T) {
	var catalog policyRegistry
	late := PolicyRule{ID: "late", Codes: []string{"user_view"}, Evaluate: func(context.Context, AuthorityRequest) (bool, error) { return true, nil }}
	require.NoError(t, catalog.Register(PolicyRule{ID: "read", Codes: []string{"user_view"}, Evaluate: func(_ context.Context, req AuthorityRequest) (bool, error) {
		assert.Error(t, catalog.Register(late), "callbacks run outside registry locks; sealed registration is still rejected")
		assert.Equal(t, "LOW", req.Facts.Risk)
		req.Facts.Risk = "HIGH"
		return true, nil
	}}))
	require.NoError(t, catalog.seal(authz.DefaultRegistry()))
	request := AuthorityRequest{Code: "user_view", Facts: &WorkItemFacts{Risk: "LOW"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				result, err := catalog.evaluate(t.Context(), request)
				assert.NoError(t, err)
				assert.Equal(t, "POLICY:read", result.source)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, "LOW", request.Facts.Risk)
}
