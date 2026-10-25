package notification

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestExecutionAttentionRoutesActualExecutorAndClosesCurrentState(t *testing.T) {
	for _, kind := range []string{"HUMAN", "AGENT"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			state := "INTERRUPTED"
			workState := "IN_PROGRESS"
			epoch := int64(1)
			workVersion := 4
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, State: state, DispatchEpoch: epoch}, nil
			}
			f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
				return delivery.ExecutionAttentionFact{ProjectID: 81, WorkItemID: 91, ExecutionID: 92, ExecutorID: 12, ExecutorKind: kind, State: workState, Version: workVersion}, true, nil
			}
			f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: r.PrincipalID == 11, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10", SourceType: "entity_binding", ScopeID: r.EntityID.String()}}}, nil
			}
			f.m.attentionExecutions.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]execution.AttentionExecution, error) {
				if state != "INTERRUPTED" {
					return nil, nil
				}
				return []execution.AttentionExecution{{ID: 92, ProjectID: 81, WorkItemID: 91, CreatedAt: f.m.now()}}, nil
			}
			f.m.decisionFacts.AttentionWorksOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if workState == "EXECUTION_FAILED" {
					return []guid.ID{91}, nil
				}
				return nil, nil
			}
			actor := guid.ID(12)
			if kind == "AGENT" {
				actor = 11
			}
			ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: actor})
			req := governance.InboxRequest{TenantID: 7, PrincipalID: actor, Kind: "EXECUTION_ATTENTION", Limit: 10}
			inbox, err := f.m.attentionInbox(ctx, req)
			require.NoError(t, err)
			require.Len(t, inbox, 1)
			require.Equal(t, "INTERRUPTED", inbox[0].Reason)
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 92}
			for range 2 {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectAttentionOn(t.Context(), tx, e, "INTERRUPTED") }))
			}
			q := query.BindNotifications(f.db, f.m.names)
			rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(executionAttentionType)).Find()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, actor, rows[0].RecipientPrincipalID)
			epoch++
			yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], actor)
			require.NoError(t, err)
			require.False(t, yes, "old interrupted episode suppressed")
			state = "FAILED"
			workState = "EXECUTION_FAILED"
			inbox, err = f.m.attentionInbox(ctx, req)
			require.NoError(t, err)
			require.Len(t, inbox, 1)
			require.Equal(t, "EXECUTION_FAILED", inbox[0].Reason)
			failedParams := map[string]string{"subject_id": "91", "subkind": "EXECUTION_FAILED", "episode": "4"}
			failedRaw, _ := json.Marshal(failedParams)
			failedRow := *rows[0]
			failedRow.Params = string(failedRaw)
			workVersion = 6
			yes, err = f.m.visibleOn(t.Context(), f.db, &failedRow, actor)
			require.NoError(t, err)
			require.False(t, yes, "old failed episode cannot revive")
			workState = "READY"
			inbox, err = f.m.attentionInbox(ctx, req)
			require.NoError(t, err)
			require.Empty(t, inbox)
			var params map[string]string
			require.NoError(t, json.Unmarshal([]byte(rows[0].Params), &params))
			require.Equal(t, "INTERRUPTED", params["subkind"])
		})
	}
}

func TestExecutionAttentionAudienceRejectsMalformedAndUnavailableEpisodes(t *testing.T) {
	for _, mode := range []string{"params", "id", "missing", "execution_missing", "execution", "notfound", "work", "readable", "unknown_executor", "mismatched_execution", "subkind"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				if mode == "execution" {
					return execution.NotificationFacts{}, ErrUnavailable
				}
				if mode == "notfound" {
					return execution.NotificationFacts{}, execution.ErrNotFound
				}
				return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, State: "INTERRUPTED", DispatchEpoch: 1}, nil
			}
			f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
				if mode == "work" {
					return delivery.ExecutionAttentionFact{}, false, ErrUnavailable
				}
				kind := "HUMAN"
				if mode == "unknown_executor" {
					kind = "SERVICE"
				}
				id := guid.ID(92)
				if mode == "mismatched_execution" {
					id = 99
				}
				return delivery.ExecutionAttentionFact{ProjectID: 81, WorkItemID: 91, ExecutionID: id, ExecutorKind: kind, ExecutorID: 11}, true, nil
			}
			f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
				if mode == "readable" {
					return false, ErrUnavailable
				}
				return true, nil
			}
			row := &model.Notification{TenantID: 7, ProjectID: 81, Type: executionAttentionType, Params: `{"subject_id":"92","subkind":"INTERRUPTED","episode":"1"}`}
			switch mode {
			case "params":
				row.Params = "["
			case "id":
				row.Params = `{"subject_id":"bad"}`
			case "missing":
				f.m.decisionFacts.AttentionOn = nil
			case "execution_missing":
				f.m.executionFacts = nil
			case "subkind":
				row.Params = `{"subject_id":"92","subkind":"UNKNOWN"}`
			}
			yes, e := f.m.attentionAudienceOn(t.Context(), f.db, row, 11)
			require.False(t, yes)
			if mode == "notfound" || mode == "unknown_executor" || mode == "mismatched_execution" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}

func TestAttentionProjectionFailuresCannotInventEpisode(t *testing.T) {
	for _, mode := range []string{"missing", "execution", "notfound", "stale", "work", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				if mode == "execution" {
					return execution.NotificationFacts{}, ErrUnavailable
				}
				if mode == "notfound" {
					return execution.NotificationFacts{}, execution.ErrNotFound
				}
				state := "INTERRUPTED"
				if mode == "stale" {
					state = "RUNNING"
				}
				return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, State: state, DispatchEpoch: 1}, nil
			}
			f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
				if mode == "work" {
					return delivery.ExecutionAttentionFact{}, false, ErrUnavailable
				}
				return delivery.ExecutionAttentionFact{ProjectID: 81, ExecutionID: 92, ExecutorKind: "HUMAN", ExecutorID: 11, State: "EXECUTION_FAILED", Version: 1}, true, nil
			}
			f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			switch mode {
			case "missing":
				f.m.candidates = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			kind := "INTERRUPTED"
			if mode == "work" {
				kind = "EXECUTION_FAILED"
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.projectAttentionOn(t.Context(), tx, events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 92}, kind)
			})
			if mode == "notfound" || mode == "stale" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestAttentionInboxRejectsIdentityLimitsAndCurrentSourceFailures(t *testing.T) {
	for _, mode := range []string{"identity", "limit", "kind", "missing", "database", "executions", "works", "fact", "params", "visibility"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
			req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "EXECUTION_ATTENTION", Limit: 10}
			f.m.attentionExecutions.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]execution.AttentionExecution, error) {
				if mode == "executions" {
					return nil, ErrUnavailable
				}
				return nil, nil
			}
			f.m.decisionFacts.AttentionWorksOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if mode == "works" {
					return nil, ErrUnavailable
				}
				return []guid.ID{91}, nil
			}
			calls := 0
			f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
				calls++
				if mode == "fact" || mode == "params" && calls > 1 {
					return delivery.ExecutionAttentionFact{}, false, ErrUnavailable
				}
				return delivery.ExecutionAttentionFact{ProjectID: 81, State: "EXECUTION_FAILED", ExecutorKind: "HUMAN", ExecutorID: 11, Version: 1}, true, nil
			}
			f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			switch mode {
			case "identity":
				req.PrincipalID = 12
			case "limit":
				req.Limit = 0
			case "kind":
				req.Kind = "UNKNOWN"
			case "missing":
				f.m.attentionExecutions.ListOn = nil
			case "database":
				f.m.runtime = nil
			case "visibility":
				f.err = ErrUnavailable
			}
			rows, err := f.m.attentionInbox(ctx, req)
			require.Error(t, err)
			require.Empty(t, rows)
		})
	}
}
