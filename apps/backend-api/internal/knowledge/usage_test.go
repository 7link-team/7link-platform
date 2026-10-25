package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUsageRequiresVerifiedRuntimeFactsDeduplicatesAndSurvivesPurge(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	chunk := readKnowledgeChunks(t, m, db, source).Items[0]
	q := query.BindEntries(db, m.names)
	entry, e := q.WithContext(ctx).Where(q.ID.Eq(chunk.EntryID)).First()
	require.NoError(t, e)
	fact := UsageFact{EntryID: entry.ID, ExecutionID: 88, EvidenceID: 90, ContentHash: entry.ContentHash, Kind: "INCLUSION"}
	record := func(in UsageFact) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.recordUsageOn(ctx, tx, 1, in) })
	}
	require.ErrorIs(t, record(fact), ErrUnavailable)
	var absent SourceUsage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		absent, err = m.sourceUsageOn(ctx, tx, 1, source.ID)
		return err
	}))
	require.False(t, absent.Available)
	require.Nil(t, absent.Inclusions)
	require.Nil(t, absent.Citations)
	// The source-owner verifier reads an actual immutable fact from its own store.
	require.NoError(t, db.Exec("CREATE TABLE runtime_fact_probe (id BIGINT PRIMARY KEY, tenant_id BIGINT, execution_id BIGINT, entry_id BIGINT, kind TEXT, content_hash TEXT)").Error)
	evidence := UsageEvidence{VerifyOn: func(_ context.Context, tx *gorm.DB, tenant guid.ID, in UsageFact) error {
		var count int64
		if err := tx.Table("runtime_fact_probe").Where("id = ? AND tenant_id = ? AND execution_id = ? AND entry_id = ? AND kind = ? AND content_hash = ?", in.EvidenceID, tenant, in.ExecutionID, in.EntryID, in.Kind, in.ContentHash).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		return nil
	}}
	services := module.NewServices()
	require.NoError(t, m.provideUsage(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[UsageEvidenceConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(UsageEvidence{}), ErrUnavailable)
	require.NoError(t, connector.Bind(evidence))
	require.ErrorIs(t, connector.Bind(evidence), ErrConflict)
	require.ErrorIs(t, record(fact), ErrNotFound)
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 90, 1, 88, entry.ID, "INCLUSION", entry.ContentHash).Error)
	require.NoError(t, record(fact))
	require.NoError(t, record(fact))
	citation := fact
	citation.Kind = "CITATION"
	citation.EvidenceID = 91
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 91, 1, 88, entry.ID, "CITATION", entry.ContentHash).Error)
	require.NoError(t, record(citation))
	require.NoError(t, record(citation))
	var usage SourceUsage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		usage, err = m.sourceUsageOn(ctx, tx, 1, source.ID)
		return err
	}))
	require.True(t, usage.Available)
	require.EqualValues(t, 1, *usage.Inclusions)
	require.EqualValues(t, 1, *usage.Citations)

	// The source-owner verifier reads the persisted immutable inclusion/citation
	// fact in every attempted transaction, including storage failure attempts.
	storageFailure := errors.New("usage receipt storage unavailable")
	rollback := errors.New("rollback usage receipt diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(storageFailure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("usage-receipt-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("usage-receipt-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("usage-receipt-failure", probe))
	for _, kind := range []string{"INCLUSION", "CITATION"} {
		candidate := fact
		candidate.Kind = kind
		candidate.EvidenceID = 95
		require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 95, 1, 88, entry.ID, kind, entry.ContentHash).Error)
		run := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if err := m.recordUsageOn(ctx, tx, 1, candidate); err != nil {
					return err
				}
				return rollback
			})
		}
		calls, failAt = 0, 0
		require.ErrorIs(t, run(), rollback)
		stages := calls
		for i := 1; i <= stages; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(), storageFailure, "usage storage stage %d for %s", i, kind)
		}
		calls, failAt = 0, 0
		require.NoError(t, db.Exec("DELETE FROM runtime_fact_probe WHERE id = ?", 95).Error)
	}
	require.NoError(t, db.Callback().Query().Remove("usage-receipt-failure"))
	require.NoError(t, db.Callback().Create().Remove("usage-receipt-failure"))
	require.NoError(t, db.Callback().Update().Remove("usage-receipt-failure"))
	require.ErrorIs(t, m.recordUsageOn(ctx, db, 1, fact), ErrInvalid)
	require.ErrorIs(t, m.recordUsageOn(ctx, nil, 1, fact), ErrInvalid)
	invalid := fact
	invalid.Kind = "UNKNOWN"
	require.ErrorIs(t, record(invalid), ErrInvalid)
	noInclusion := citation
	noInclusion.ExecutionID = 89
	noInclusion.EvidenceID = 96
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 96, 1, 89, entry.ID, "CITATION", entry.ContentHash).Error)
	require.ErrorIs(t, record(noInclusion), ErrConflict)
	missing := fact
	missing.EntryID = 999999
	missing.EvidenceID = 97
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 97, 1, 88, missing.EntryID, "INCLUSION", entry.ContentHash).Error)
	require.ErrorIs(t, record(missing), ErrNotFound)

	// Audit failure rolls the new receipt back, rather than increasing counters.
	appender := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	fact.EvidenceID = 92
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 92, 1, 88, entry.ID, "INCLUSION", entry.ContentHash).Error)
	require.ErrorContains(t, record(fact), "audit unavailable")
	var count int64
	require.NoError(t, query.BindUsageReceipts(db, m.names).Count(&count).Error)
	require.EqualValues(t, 2, count)
	m.audit = appender
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.invalidateSourceOn(ctx, tx, 1, source.ID, source.Version)
		return err
	}))
	now := m.now()
	m.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	require.NoError(t, m.purgeChunksJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		usage, err = m.sourceUsageOn(ctx, tx, 1, source.ID)
		return err
	}))
	require.EqualValues(t, 1, *usage.Inclusions)
	require.EqualValues(t, 1, *usage.Citations)
	// Historical output may arrive after source retirement and chunk purge. It
	// cites the already delivered hash, never the replacement's current content.
	citation.EvidenceID = 93
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 93, 1, 88, entry.ID, "CITATION", entry.ContentHash).Error)
	require.NoError(t, record(citation))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		usage, err = m.sourceUsageOn(ctx, tx, 1, source.ID)
		return err
	}))
	require.EqualValues(t, 2, *usage.Citations)
	fact.EvidenceID = 94
	require.NoError(t, db.Exec("INSERT INTO runtime_fact_probe VALUES (?,?,?,?,?,?)", 94, 1, 88, entry.ID, "INCLUSION", entry.ContentHash).Error)
	require.ErrorIs(t, record(fact), ErrConflict, "retirement still prevents fresh inclusion")
}
