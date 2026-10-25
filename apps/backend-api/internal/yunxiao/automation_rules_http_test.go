package yunxiao

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestAutomationCatalogTransportBindsProjectScopeAndPreservesErrors(t *testing.T) {
	m, _, _ := workHarness(t)
	a := testAuth{}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), a, a, a)
	_, api := humatest.New(t)
	require.NoError(t, m.registerAutomationRulesHTTP(api))
	path := "/tenants/1/automation-rules?project_id=99"
	response := api.Get(path)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"items":[]`)
	require.Equal(t, 422, api.Get("/tenants/1/automation-rules").Code)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	require.Equal(t, 403, api.Get(path).Code)
}
func TestAutomationCatalogHTTPPersistsVersionsPaginatesAndPreviewsWithoutExecution(t *testing.T) {
	m, db, ctx := workHarness(t)
	scope := AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: []byte(`{"state":"NEEDS_HUMAN_REVIEW"}`)}}, nil
	}
	var ids []guid.ID
	for _, key := range []string{"catalog-first", "catalog-second"} {
		in := &automationCreateInput{AutomationScope: scope, WriteHeaders: WriteHeaders{IdempotencyKey: key, IfMatch: `"0"`}, Body: automationDefinitionFixture()}
		created, err := m.createAutomationRuleHTTP(ctx, in)
		require.NoError(t, err)
		ids = append(ids, created.Body.Data.ID)
		replay, err := m.createAutomationRuleHTTP(ctx, in)
		require.NoError(t, err)
		require.Equal(t, "true", replay.Replayed)
		require.Equal(t, created.Body.Data, replay.Body.Data)
	}
	list, err := m.listAutomationRulesHTTP(ctx, &automationListInput{AutomationScope: scope, Limit: 1, TestEventID: "700"})
	require.NoError(t, err)
	require.True(t, list.Body.Data.Rules.HasMore)
	require.Len(t, list.Body.Data.Rules.Items, 1)
	require.Len(t, list.Body.Data.Previews, 1)
	require.True(t, list.Body.Data.Previews[0].Matches)
	require.False(t, list.Body.Data.Previews[0].Executed)
	next, err := m.listAutomationRulesHTTP(ctx, &automationListInput{AutomationScope: scope, Limit: 1, Cursor: list.Body.Data.Rules.NextCursor})
	require.NoError(t, err)
	require.False(t, next.Body.Data.Rules.HasMore)
	require.Equal(t, ids[1], next.Body.Data.Rules.Items[0].ID)
	input := automationDefinitionFixture()
	input.Name = "Updated rule"
	updated, err := m.updateAutomationRuleHTTP(ctx, &automationUpdateInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: ids[0].String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "catalog-update", IfMatch: `"1"`}, Body: input})
	require.NoError(t, err)
	require.Equal(t, 2, updated.Body.Data.Version)
	require.Equal(t, input.Name, updated.Body.Data.Name)
	for _, event := range []guid.ID{701, 702} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.executeAutomationOn(ctx, tx, 1, ids[0], event, 88, func(context.Context, *gorm.DB) error { return ErrUnavailable })
			return err
		}))
	}
	runs, err := m.listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: ids[0].String()}, Limit: 1})
	require.NoError(t, err)
	require.True(t, runs.Body.Data.HasMore)
	require.Len(t, runs.Body.Data.Items, 1)
	require.Equal(t, "FAILED", runs.Body.Data.Items[0].Outcome)
	more, err := m.listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: ids[0].String()}, Cursor: runs.Body.Data.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.Len(t, more.Body.Data.Items, 1)
	require.False(t, more.Body.Data.HasMore)
	archive := &automationArchiveInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: ids[0].String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "catalog-archive", IfMatch: `"2"`}}
	archived, err := m.archiveAutomationRuleHTTP(ctx, archive)
	require.NoError(t, err)
	require.False(t, archived.Body.Data.Enabled)
	require.Equal(t, 3, archived.Body.Data.Version)
	replay, err := m.archiveAutomationRuleHTTP(ctx, archive)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	q := query.BindAutomationRules(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(ids[0])).First()
	require.NoError(t, err)
	require.NotNil(t, stored.ArchivedAt)
	require.False(t, stored.Enabled)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	_, err = m.createAutomationRuleHTTP(ctx, &automationCreateInput{AutomationScope: scope, WriteHeaders: WriteHeaders{IdempotencyKey: "catalog-first", IfMatch: `"0"`}, Body: automationDefinitionFixture()})
	require.Error(t, err, "replay rechecks authority")
}

func TestAutomationCatalogHTTPRejectsMalformedScopeCursorAndCurrentSource(t *testing.T) {
	m, _, ctx := workHarness(t)
	scope := AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	for _, in := range []*automationListInput{{AutomationScope: AutomationScope{ProjectID: "bad"}}, {AutomationScope: scope, Cursor: "bad"}} {
		_, err := m.listAutomationRulesHTTP(ctx, in)
		require.Error(t, err)
	}
	_, err := m.updateAutomationRuleHTTP(ctx, &automationUpdateInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: "bad"}, WriteHeaders: WriteHeaders{IfMatch: `"1"`}})
	require.Error(t, err)
	_, err = m.archiveAutomationRuleHTTP(ctx, &automationArchiveInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: "bad"}, WriteHeaders: WriteHeaders{IfMatch: `"1"`}})
	require.Error(t, err)
	_, err = m.listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: "91"}, Cursor: "bad"})
	require.Error(t, err)
}

func TestAutomationRulesHTTPUnavailableAndInvalidIdentity(t *testing.T) {
	m := &Module{}
	_, e := m.listAutomationRulesHTTP(t.Context(), &automationListInput{AutomationScope: AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}})
	require.Error(t, e)
	_, e = m.listAutomationRunsHTTP(t.Context(), &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "bad"}, RuleID: "7"}})
	require.Error(t, e)
}

func TestAutomationCatalogReadFailuresDoNotExposePartialOrHiddenRuns(t *testing.T) {
	for _, mode := range []string{"rule_query", "definition", "preview_id", "preview_source", "runs_query", "hidden_work", "unbound_work", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, _ := automationDispatchHarness(t)
			scope := AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
			m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.executeAutomationOn(ctx, tx, 1, rule.ID, 700, 88, func(context.Context, *gorm.DB) error { return nil })
				return e
			}))
			switch mode {
			case "rule_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("catalog_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRules.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("catalog_storage")
			case "definition":
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", rule.ID).Update("definition", "[").Error)
			case "preview_source":
				m.automationEvents = nil
			case "runs_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("catalog_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("catalog_storage")
			case "hidden_work":
				m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			case "unbound_work":
				m.workItemOn = nil
			case "revoked":
				m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
			}
			if mode == "runs_query" || mode == "hidden_work" || mode == "unbound_work" {
				_, e := m.listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: rule.ID.String()}})
				require.Error(t, e)
			} else {
				in := &automationListInput{AutomationScope: scope}
				if mode == "preview_id" {
					in.TestEventID = "bad"
				}
				if mode == "preview_source" {
					in.TestEventID = "700"
				}
				_, e := m.listAutomationRulesHTTP(ctx, in)
				require.Error(t, e)
			}
		})
	}
	m, _, ctx := workHarness(t)
	scope := AutomationScope{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	_, e := m.createAutomationRuleHTTP(ctx, &automationCreateInput{AutomationScope: AutomationScope{}})
	require.Error(t, e)
	_, e = (&Module{}).listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: "7"}})
	require.Error(t, e)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	_, e = m.listAutomationRunsHTTP(ctx, &automationRunListInput{AutomationRulePath: AutomationRulePath{AutomationScope: scope, RuleID: "7"}})
	require.Error(t, e)
}
