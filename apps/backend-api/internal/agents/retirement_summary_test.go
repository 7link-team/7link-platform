package agents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementSummaryUsesActualReferencesAndDisclosesSizeOmissions(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	m.retirementRules.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	r := &model.Retirement{ID: 88, TenantID: 1, AgentPrincipalID: 7}
	for i := 0; i < 1000; i++ {
		r.Precheck.WorkItems = append(r.Precheck.WorkItems, model.RetirementWorkItem{WorkItemID: guid.ID(i + 1), Title: strings.Repeat("界", 200), State: "IN_PROGRESS"})
	}
	m.retirementWork.DecisionsOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID, since time.Time) ([]delivery.RetirementDecision, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 7, agent)
		require.Equal(t, now.Add(-30*24*time.Hour), since)
		return []delivery.RetirementDecision{{ID: 81, Kind: "RETURN", Reason: "actual rejection"}}, nil
	}
	m.retirementRules.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]knowledge.RetirementRule, error) {
		return []knowledge.RetirementRule{{ID: 82, Title: "actual rule reference", SHA256: "source hash"}}, nil
	}
	var content string
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		content, err = m.retirementSummaryOn(agentContext(t, 2), tx, r)
		return err
	}))
	require.Less(t, len(content), 19000, "leave room for the producer instruction under 20KB")
	require.Contains(t, content, "Omitted")
	require.Contains(t, content, "actual rejection")
	require.Contains(t, content, "actual rule reference")
	require.Contains(t, content, "no canonical thread-closed state")
	require.NotContains(t, content, "## 给接手人的注意事项", "SYSTEM cannot invent Agent advice")
	denied := errors.New("source authority revoked")
	m.retirementWork.DecisionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]delivery.RetirementDecision, error) {
		return nil, denied
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.retirementSummaryOn(agentContext(t, 2), tx, r); return err }), denied)
}

func TestRetirementFrozenSourcesRecheckEveryCurrentReadBeforeCopyOrDisclosure(t *testing.T) {
	for _, source := range []string{"work", "execution", "decision", "rule"} {
		t.Run(source, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			retirementWorkFixture(m)
			r := &model.Retirement{TenantID: 1, AgentPrincipalID: 7, Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{}}}
			switch source {
			case "work":
				r.Precheck.WorkItems = []model.RetirementWorkItem{{ProjectID: 30, WorkItemID: 40, Title: "Private work"}}
			case "execution":
				r.Precheck.Executions = []model.RetirementExecution{{ProjectID: 30, WorkItemID: 40}}
			case "decision":
				r.Precheck.Progress.SourceWorkItems = []model.RetirementSourceWork{{ProjectID: 30, WorkItemID: 40}}
			case "rule":
				r.Precheck.Progress.SourceRuleIDs = []guid.ID{50}
			}
			allowed, checks := true, 0
			m.retirementWork.CheckReadOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
				checks++
				require.Equal(t, []guid.ID{1, 30, 40}, []guid.ID{tenant, project, work})
				if !allowed {
					return delivery.ErrWorkForbidden
				}
				return nil
			}
			m.retirementRules.CheckOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID) error {
				checks++
				require.Equal(t, []guid.ID{1, 7, 50}, []guid.ID{tenant, agent, id})
				if !allowed {
					return knowledge.ErrForbidden
				}
				return nil
			}
			check := func() error {
				return db.Transaction(func(tx *gorm.DB) error { return m.checkRetirementSourceReadsOn(agentContext(t, 2), tx, r) })
			}
			require.NoError(t, check())
			require.Equal(t, 1, checks)
			allowed = false
			require.Error(t, check(), "retaining agent_retire cannot lend a revoked source grant")
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.retirementSummaryOn(agentContext(t, 2), tx, r); return err }))
		})
	}
}

func TestRetirementSummaryBoundsPendingInboxAndPreservesExplicitUnavailableSources(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.lockState(ctx, tx, 1, 7); return e }))
	require.NoError(t, db.Exec("UPDATE k_agents_live_states SET head=1001,ack=0 WHERE agent_principal_id=7").Error)
	rows := make([]*model.InboxEvent, 1001)
	for i := range rows {
		kind := "STATE"
		if i%2 == 0 {
			kind = "MESSAGE"
		}
		rows[i] = &model.InboxEvent{ID: guid.ID(10000 + i), TenantID: 1, AgentPrincipalID: 7, Seq: int64(i + 1), Kind: kind, SubjectID: 55, SourceEventID: guid.ID(i + 1).String(), CreatedAt: m.now()}
	}
	require.NoError(t, query.BindInboxEvent(db, m.names).WithContext(ctx).CreateInBatches(rows, 100))
	r := &model.Retirement{ID: 88, TenantID: 1, AgentPrincipalID: 7}
	m.retirementWork.DecisionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]delivery.RetirementDecision, error) {
		return nil, delivery.ErrUnavailable
	}
	m.retirementRules.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]knowledge.RetirementRule, error) {
		return nil, knowledge.ErrForbidden
	}
	content, err := m.retirementSummaryOn(ctx, db, r)
	require.NoError(t, err)
	require.Contains(t, content, "Additional pending inbox references omitted after 1000")
	require.Contains(t, content, "Source capability unavailable")
	require.NotContains(t, content, `"seq":1001`)
	fault := errors.New("rule source failed")
	m.retirementRules.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]knowledge.RetirementRule, error) {
		return nil, fault
	}
	content, err = m.retirementSummaryOn(ctx, db, r)
	require.ErrorIs(t, err, fault)
	require.Empty(t, content)
}
