package governance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func claimEvaluationHarness(t *testing.T) (*Module, *gorm.DB, events.Envelope, *governanceTestPublisher) {
	t.Helper()
	m, db, _, pub := settingsHarness(t)
	m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
		confidence := 59
		return ClaimEvaluationFacts{ClaimID: 77, WorkItemID: 88, SelfConfidence: &confidence}, true, nil
	}
	return m, db, events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: 88}, Data: json.RawMessage(`{"to":"NEEDS_HUMAN_REVIEW","result_id":"77"}`)}, pub
}

func TestClaimEvaluationPersistsIncidentsAndExactReceiptAtomically(t *testing.T) {
	m, db, event, pub := claimEvaluationHarness(t)
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.evaluateClaimEvent(t.Context(), tx, event) })
	}
	pub.Fail = true
	require.Error(t, run())
	require.Zero(t, seedCount(t, db, m.seed.names.ClaimEvaluations.String()))
	require.Zero(t, seedCount(t, db, m.seed.names.EscalationIncidents.String()))
	require.Zero(t, seedCount(t, db, "audit_probe"))
	pub.Fail = false
	require.NoError(t, run())
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.ClaimEvaluations.String()))
	require.EqualValues(t, 2, seedCount(t, db, m.seed.names.EscalationIncidents.String()))
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"))
	q := query.BindClaimEvaluations(db, m.seed.names)
	receipt, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.Equal(t, "TRIGGERED", receipt.LowConfidenceResult)
	require.Equal(t, "UNAVAILABLE", receipt.SensitivePathResult)
	require.Equal(t, event.EventID, receipt.TriggerEventID)
	require.NoError(t, run())
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"), "replay cannot append duplicate incidents/events")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		req := AuthorityRequest{TenantID: 1, ProjectID: 99, Resource: ResourceRef{ID: 88}, Facts: &WorkItemFacts{ClaimID: 77}}
		ok, e := m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.True(t, ok)
		req.Facts.ClaimID = 78
		ok, e = m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, ok)
		req.Facts.ClaimID = 77
		req.TenantID = 2
		ok, e = m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, ok)
		req.TenantID = 1
		req.ProjectID = 98
		ok, e = m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, ok)
		req.ProjectID = 99
		req.Resource.ID = 89
		ok, e = m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, ok)
		req.Facts = nil
		ok, e = m.claimEvaluatedOn(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, ok)
		return nil
	}))
}

func TestClaimEvaluationConfidenceBoundaryAndDisabledRules(t *testing.T) {
	for _, score := range []*int{nil, ptrInt(59), ptrInt(60), ptrInt(100)} {
		t.Run(func() string {
			if score == nil {
				return "absent"
			}
			return string(rune(*score + 65))
		}(), func(t *testing.T) {
			m, db, event, _ := claimEvaluationHarness(t)
			m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
				return ClaimEvaluationFacts{ClaimID: 77, WorkItemID: 88, SelfConfidence: score}, true, nil
			}
			rule := defaultRule("SENSITIVE_PATH")
			rule.Enabled = false
			value, err := json.Marshal(rule.EscalationRuleValue)
			require.NoError(t, err)
			require.NoError(t, query.BindEscalationRules(db, m.seed.names).WithContext(t.Context()).Create(&model.EscalationRule{ID: 500, TenantID: 1, Kind: rule.Kind, ValueJSON: string(value), Version: 1}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.evaluateClaimEvent(t.Context(), tx, event) }))
			row, err := query.BindClaimEvaluations(db, m.seed.names).WithContext(t.Context()).First()
			require.NoError(t, err)
			require.Equal(t, "SKIPPED", row.SensitivePathResult)
			want := "PASS"
			if score == nil {
				want = "SKIPPED"
			} else if *score < 60 {
				want = "TRIGGERED"
			}
			require.Equal(t, want, row.LowConfidenceResult)
			count := int64(0)
			if want == "TRIGGERED" {
				count = 1
			}
			require.Equal(t, count, seedCount(t, db, m.seed.names.EscalationIncidents.String()))
		})
	}
}

func TestClaimEvaluationAppliesProjectConfidenceFloor(t *testing.T) {
	m, db, event, _ := claimEvaluationHarness(t)
	m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
		confidence := 65 // Tenant's 60 passes, project's 70 must escalate.
		return ClaimEvaluationFacts{ClaimID: 77, WorkItemID: 88, SelfConfidence: &confidence}, true, nil
	}
	value := defaultRule("LOW_SELF_CONFIDENCE").EscalationRuleValue
	value.Params = json.RawMessage(`{"min_confidence":70}`)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	q := query.BindProjectEscalationRules(db, m.seed.names)
	now := m.seed.now()
	require.NoError(t, q.WithContext(t.Context()).Create(&model.ProjectEscalationRule{ID: 501, TenantID: 1, ProjectID: 99, Kind: "LOW_SELF_CONFIDENCE", ValueJSON: string(encoded), Version: 1, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.evaluateClaimEvent(t.Context(), tx, event) }))
	row, err := query.BindClaimEvaluations(db, m.seed.names).WithContext(t.Context()).First()
	require.NoError(t, err)
	require.Equal(t, "TRIGGERED", row.LowConfidenceResult)
}

func TestClaimEvaluationRejectsInvalidStaleOrUnavailableSource(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Module, *events.Envelope)
		wantError bool
	}{
		{"wrong event", func(_ *Module, e *events.Envelope) { e.EventType = "execution.created" }, true},
		{"bad JSON", func(_ *Module, e *events.Envelope) { e.Data = []byte("{") }, true},
		{"irrelevant state", func(_ *Module, e *events.Envelope) { e.Data = []byte(`{"to":"IN_PROGRESS"}`) }, false},
		{"missing claim", func(_ *Module, e *events.Envelope) { e.Data = []byte(`{"to":"NEEDS_HUMAN_REVIEW"}`) }, true},
		{"no source", func(m *Module, _ *events.Envelope) { m.claimSource = ClaimEvaluationSource{} }, true},
		{"source failure", func(m *Module, _ *events.Envelope) {
			m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
				return ClaimEvaluationFacts{}, false, errors.New("source offline")
			}
		}, true},
		{"superseded", func(m *Module, _ *events.Envelope) {
			m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
				return ClaimEvaluationFacts{}, false, nil
			}
		}, false},
		{"wrong identity", func(m *Module, _ *events.Envelope) {
			m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
				return ClaimEvaluationFacts{ClaimID: 78, WorkItemID: 88}, true, nil
			}
		}, true},
		{"invalid confidence", func(m *Module, _ *events.Envelope) {
			m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
				return ClaimEvaluationFacts{ClaimID: 77, WorkItemID: 88, SelfConfidence: ptrInt(101)}, true, nil
			}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, db, e, _ := claimEvaluationHarness(t)
			tc.mutate(m, &e)
			err := db.Transaction(func(tx *gorm.DB) error { return m.evaluateClaimEvent(t.Context(), tx, e) })
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, seedCount(t, db, m.seed.names.ClaimEvaluations.String()))
			require.Zero(t, seedCount(t, db, m.seed.names.EscalationIncidents.String()))
		})
	}
	m, db, event, _ := claimEvaluationHarness(t)
	require.ErrorIs(t, m.evaluateClaimEvent(t.Context(), db, event), ErrInvalidArgument)
	require.ErrorIs(t, m.evaluateClaimEvent(t.Context(), nil, event), ErrInvalidArgument)
	require.ErrorIs(t, m.bindClaimEvaluationSource(ClaimEvaluationSource{}), ErrInvalidArgument)
	require.ErrorIs(t, m.bindClaimEvaluationSource(m.claimSource), ErrInvalidArgument)
	m.claimSource = ClaimEvaluationSource{}
	require.NoError(t, m.bindClaimEvaluationSource(ClaimEvaluationSource{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
		return ClaimEvaluationFacts{}, false, nil
	}}))
}

func ptrInt(v int) *int { return &v }
