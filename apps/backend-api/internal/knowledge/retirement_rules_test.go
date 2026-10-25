package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementRulesReadCurrentAuthorizedActiveReferences(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "RULE", Title: "Verified sources", Body: "Private rule body", Provenance: "Human proposal"})
	q := query.BindEntries(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).UpdateSimple(q.State.Value("ACTIVE"))
	require.NoError(t, err)
	s := module.NewServices()
	require.NoError(t, m.provideRetirementRules(s))
	require.NoError(t, s.Seal())
	reader, err := module.Resolve[RetirementRules](s)
	require.NoError(t, err)
	read := func(actor guid.ID) ([]RetirementRule, error) {
		var out []RetirementRule
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = reader.ReadOn(knowledgeContext(t, actor), tx, 1, 7)
			return e
		})
		return out, err
	}
	rows, err := read(2)
	require.NoError(t, err)
	require.Equal(t, []RetirementRule{{ID: entry.ID, Title: entry.Title, SHA256: entry.ContentHash, Version: entry.Version}}, rows)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return reader.CheckOn(knowledgeContext(t, 2), tx, 1, 7, entry.ID) }))
	_, err = read(7)
	require.Error(t, err, "Agent cannot export private retirement memories as a Human")
	denied := errors.New("current policy unavailable")
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, denied
	})
	_, err = read(2)
	require.ErrorIs(t, err, denied)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return reader.CheckOn(knowledgeContext(t, 2), tx, 1, 7, entry.ID) }))
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})
	expired := time.Now().UTC().Add(-time.Hour)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).Update(q.ExpiresAt, expired)
	require.NoError(t, err)
	rows, err = read(2)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).UpdateSimple(q.State.Value("REVOKED"))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return reader.CheckOn(knowledgeContext(t, 2), tx, 1, 7, entry.ID) }), "source history remains readable after freezing/revocation if current read grants permit it")
	rows, err = read(2)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = reader.ReadOn(knowledgeContext(t, 2), db, 1, 7)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestRetirementRuleReferencesFailClosedOnCurrentProjectAndStorage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7), ProjectID: ptr(3)}, Kind: "RULE", Title: "Scoped rule", Body: "Retain scoped source evidence.", Provenance: "Human proposal"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independent review of scoped evidence"})
		return err
	}))
	read := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.retirementRulesOn(knowledgeContext(t, 2), tx, 1, 7); return err })
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.retirementRulesOn(knowledgeContext(t, 2), tx, 1, 7)
		require.Empty(t, rows)
		return err
	}))
	failure := errors.New("project rule authority unavailable")
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	require.ErrorIs(t, read(), failure)
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("retirement-rule-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Entries.String() {
			tx.AddError(failure)
		}
	}))
	require.ErrorIs(t, read(), failure)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.checkRetirementRuleOn(knowledgeContext(t, 2), tx, 1, 7, entry.ID) }), failure)
	require.NoError(t, db.Callback().Query().Remove("retirement-rule-failure"))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.checkRetirementRuleOn(knowledgeContext(t, 2), tx, 1, 7, entry.ID+1) }), gorm.ErrRecordNotFound)
}
