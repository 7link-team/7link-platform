package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEventIngestionTransactionalReceiptPreventsDuplicateEffects(t *testing.T) {
	m, db, services := knowledgeHarness(t)
	bus := events.NewModule(m.runtime)
	require.NoError(t, bus.InjectServices(services))
	require.NoError(t, bus.AddTypes("delivery", events.TypeSpec{Name: "requirement.versioned", SchemaVersion: 1, SubjectKind: "requirement", Realtime: events.RealtimeNone}))
	calls := 0
	m.ingestion.readers = []EventSourceReader{{ReadOn: func(context.Context, *gorm.DB, events.Envelope) ([]EventSourceDocument, error) {
		calls++
		return []EventSourceDocument{{Kind: "REQUIREMENT", Origin: "requirement", OriginID: 101, Version: 1, Title: "Canonical baseline", Content: "# Canonical baseline\n\nSigned baseline bytes."}}, nil
	}}}
	require.NoError(t, bus.AddSubscribers(Name, events.Subscriber{Name: "knowledge.source_ingest", Mode: events.Transactional, StartFrom: events.Earliest, EventTypes: []string{"requirement.versioned"}, Handle: m.ingestEventOn}))
	require.NoError(t, bus.Initialize(t.Context()))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bus.Publish(t.Context(), tx, events.Event{TenantID: 1, ProjectID: 99, Type: "requirement.versioned", Subject: events.Subject{Kind: "requirement", ID: 101}, SubjectVersion: 1, Actor: events.Actor{Kind: "HUMAN", ID: 2, AuthoritySource: "IAM:requirement_write"}, Data: map[string]any{"version_no": 1}})
		return err
	}))
	_, err := bus.Sequence(t.Context())
	require.NoError(t, err)
	_, err = bus.DispatchPartition(t.Context(), "knowledge.source_ingest", 0)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	var receipts, sources, chunks int64
	require.NoError(t, db.Table("k_domain_event_receipts").Where("consumer = ?", "knowledge.source_ingest").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	require.NoError(t, db.Table("k_knowledge_sources").Count(&sources).Error)
	require.NoError(t, db.Table("k_knowledge_chunks").Count(&chunks).Error)
	require.EqualValues(t, 1, sources)
	require.Positive(t, chunks)
	// Durable cursor rewind exercises receipt replay, rather than only a second
	// handler call that would bypass the event platform's transaction contract.
	require.NoError(t, db.Table("k_domain_event_cursors").Where("consumer = ?", "knowledge.source_ingest").Update("last_seq", 0).Error)
	_, err = bus.DispatchPartition(t.Context(), "knowledge.source_ingest", 0)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	var after int64
	require.NoError(t, db.Table("k_knowledge_chunks").Count(&after).Error)
	require.Equal(t, chunks, after)
}

func TestEventBaselineSupersessionPreservesUnchangedChunksAndRollsBackAuditFailure(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	event := events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}}
	doc := EventSourceDocument{Kind: "REQUIREMENT", Origin: "requirement", OriginID: 200, Version: 1, Title: "Baseline", Content: "# Baseline\n\nStable description.\n\n## Criterion A\n\nOld exclusive phrase.\n\n## Criterion B\n\nStable criterion."}
	apply := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) })
	}
	require.NoError(t, apply())
	s := query.BindSources(db, m.names)
	source, err := s.WithContext(t.Context()).First()
	require.NoError(t, err)
	c := query.BindChunks(db, m.names)
	before, err := c.WithContext(t.Context()).Order(c.ID).Find()
	require.NoError(t, err)
	doc.Version = 2
	doc.Title = "Current baseline"
	doc.Content = "# Baseline\n\nStable description.\n\n## Criterion A\n\nNew exclusive phrase.\n\n## Criterion B\n\nStable criterion."
	require.NoError(t, apply())
	after, err := c.WithContext(t.Context()).Where(c.InvalidatedAt.IsNull()).Find()
	require.NoError(t, err)
	unchanged := 0
	for _, old := range before {
		for _, live := range after {
			if old.ContentHash == live.ContentHash {
				require.Equal(t, old.ID, live.ID)
				unchanged++
			}
		}
	}
	require.Positive(t, unchanged)
	e := query.BindEntries(db, m.names)
	liveEntries, err := e.WithContext(t.Context()).Where(e.State.Eq("ACTIVE")).Find()
	require.NoError(t, err)
	for _, entry := range liveEntries {
		require.Equal(t, "Current baseline", entry.Title)
		require.Equal(t, sourceHash(entry.Title+"\n"+entry.Body), entry.ContentHash)
	}
	old, err := e.WithContext(t.Context()).Where(e.Body.Like("%Old exclusive phrase%")).First()
	require.NoError(t, err)
	require.Equal(t, "REVOKED", old.State)
	// An older baseline cannot overwrite the current projection.
	doc.Version = 1
	doc.Content = "Stale replay"
	require.NoError(t, apply())
	current, err := s.WithContext(t.Context()).Where(s.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, 2, current.OriginVersion)
	failure := errors.New("audit unavailable")
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	doc.Version = 3
	doc.Content = "Replacement that must roll back"
	require.ErrorIs(t, apply(), failure)
	current, err = s.WithContext(t.Context()).Where(s.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, 2, current.OriginVersion)
}

func TestRetirementInvalidatesAgentSourceAndEntryInSameConsumerTransaction(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := sourceDraft()
	agent := guid.ID(7)
	input.Scope = Scope{Scope: "AGENT", AgentPrincipalID: &agent}
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, input), nil, false)
	payload, err := json.Marshal(map[string]any{"principal_id": agent})
	require.NoError(t, err)
	event := events.Envelope{EventID: 301, EventType: "agent.retired", TenantID: 1, Subject: events.Subject{Kind: "agent", ID: agent}, Data: payload, OccurredAt: time.Now()}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }))
	s := query.BindSources(db, m.names)
	row, err := s.WithContext(t.Context()).Where(s.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "INVALIDATED", row.State)
	c := query.BindChunks(db, m.names)
	count, err := c.WithContext(t.Context()).Where(c.SourceID.Eq(source.ID), c.InvalidatedAt.IsNull()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	entries := query.BindEntries(db, m.names)
	count, err = entries.WithContext(t.Context()).Where(entries.AgentPrincipalID.Eq(agent), entries.State.Eq("ACTIVE")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRetirementFencesAgentSourceBeforeEventDispatchAndPreservesMemoryPolicy(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := sourceDraft()
	agent := guid.ID(7)
	input.Scope = Scope{Scope: "AGENT", AgentPrincipalID: &agent}
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, input), nil, false)
	state := "ACTIVE"
	m.agents.ByPrincipalOn = func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		require.NotNil(t, tx)
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: principal, OwnerPrincipalID: 2, State: state}, principal == agent, nil
	}
	read := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.sourceOn(knowledgeContext(t, 2), tx, 1, source.ID, "knowledge_read", false)
			return err
		})
	}
	require.NoError(t, read())
	state = "RETIRED"
	require.ErrorIs(t, read(), ErrNotFound)
	// No retirement event has been dispatched: the stored source is still indexed.
	s := query.BindSources(db, m.names)
	row, err := s.WithContext(t.Context()).Where(s.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "INDEXED", row.State)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.authorize(knowledgeContext(t, 2), tx, 1, input.Scope, "knowledge_read", false)
		return err
	}))
}

func TestClientAcceptedBonusRequiresAuthoritativeAcceptanceReaderAndScopedSource(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	event := events.Envelope{EventID: 501, TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}}
	work := guid.ID(700)
	doc := EventSourceDocument{Kind: "DELIVERY", Origin: "work_item_delivery", OriginID: work, Version: 1, WorkItemID: &work, Title: "Delivery", Content: "# Delivery\n\nImmutable accepted declaration summary."}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }))
	event.EventType = "clientwork.package_decided"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }), ErrUnavailable)
	m.ingestion.accepted.ReadOn = func(context.Context, *gorm.DB, events.Envelope) ([]guid.ID, error) { return []guid.ID{work}, nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }))
	s := query.BindSources(db, m.names)
	row, err := s.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.True(t, row.ClientAccepted)
	version := row.Version
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }))
	row, err = s.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.Equal(t, version, row.Version)
	// Provenance does not bypass current permission checks on source reads.
	m.policy = core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.sourceOn(knowledgeContext(t, 2), tx, 1, row.ID, "knowledge_read", false)
		return e
	}))
	event.ProjectID = 98
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }))
}

func TestCanonicalSourceReplacementStorageFailuresPreservePriorProjection(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	event := events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}}
	doc := EventSourceDocument{Kind: "REQUIREMENT", Origin: "requirement", OriginID: 200, Version: 1, Title: "Canonical baseline", Content: "# Original requirement\n\nOriginal authoritative bytes."}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }))
	q := query.BindSources(db, m.names)
	before, e := q.WithContext(t.Context()).First()
	require.NoError(t, e)
	counts := map[string]int64{}
	for _, table := range []string{m.names.Sources.String(), m.names.Chunks.String(), m.names.Entries.String(), "audit_probe", "k_domain_events"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		counts[table] = n
	}
	doc.Version = 2
	doc.Title = "Revised canonical requirement"
	doc.Content = "# Revised requirement\n\nReplacement authoritative bytes."
	failure := errors.New("canonical source storage unavailable")
	rollback := errors.New("rollback source diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("canonical-source-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("canonical-source-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("canonical-source-failure", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if e := m.upsertEventSourceOn(t.Context(), tx, event, doc); e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.GreaterOrEqual(t, total, 10)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "canonical replacement stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("canonical-source-failure"))
	require.NoError(t, db.Callback().Create().Remove("canonical-source-failure"))
	require.NoError(t, db.Callback().Update().Remove("canonical-source-failure"))
	after, e := q.WithContext(t.Context()).First()
	require.NoError(t, e)
	require.Equal(t, before, after)
	for table, n := range counts {
		var got int64
		require.NoError(t, db.Table(table).Count(&got).Error)
		require.Equal(t, n, got, table)
	}
}

func TestCanonicalIngestionSourceBindingsAndInvalidDocumentsFailWithoutWrites(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideEventIngestion(s))
	require.NoError(t, s.Seal())
	reader, e := module.Resolve[EventSourceReaderConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, reader.Bind(EventSourceReader{}), ErrUnavailable)
	accepted, e := module.Resolve[ClientAcceptanceReaderConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, accepted.Bind(ClientAcceptanceReader{}), ErrUnavailable)
	missing := ClientAcceptanceReader{ReadOn: func(context.Context, *gorm.DB, events.Envelope) ([]guid.ID, error) { return nil, ErrNotFound }}
	require.NoError(t, accepted.Bind(missing))
	require.ErrorIs(t, accepted.Bind(missing), ErrConflict)
	event := events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99}
	require.ErrorIs(t, m.ingestEventOn(t.Context(), db, event), ErrInvalid)
	require.ErrorIs(t, m.ingestEventOn(t.Context(), nil, event), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }), ErrUnavailable)
	failure := errors.New("canonical owner unavailable")
	require.NoError(t, reader.Bind(EventSourceReader{ReadOn: func(context.Context, *gorm.DB, events.Envelope) ([]EventSourceDocument, error) { return nil, failure }}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ingestEventOn(t.Context(), tx, event) }), failure)
	for _, doc := range []EventSourceDocument{{}, {Kind: "REQUIREMENT", Origin: "decision", OriginID: 4, Version: 1, Title: "Requirement", Content: "Canonical bytes"}, {Kind: "FEEDBACK", Origin: "requirement", OriginID: 4, Version: 1, Title: "Feedback", Content: "Canonical bytes"}, {Kind: "DELIVERY", Origin: "work_item_delivery", OriginID: 4, Version: 1, Title: "Delivery", Content: "Canonical bytes", WorkItemID: ptr(0)}, {Kind: "REQUIREMENT", Origin: "requirement", OriginID: 4, Version: 1, Title: "Empty canonical source", Content: "   "}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }), ErrInvalid)
	}
	var n int64
	require.NoError(t, query.BindSources(db, m.names).WithContext(t.Context()).UnderlyingDB().Count(&n).Error)
	require.Zero(t, n)
}

func TestRetirementSourceInvalidationRollsBackEveryStorageFailure(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := sourceDraft()
	input.Scope = Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, input), nil, false)
	event := events.Envelope{EventID: 301, EventType: "agent.retired", TenantID: 1, Subject: events.Subject{Kind: "agent", ID: 7}, Data: json.RawMessage(`{"principal_id":"7"}`)}
	failure := errors.New("retirement source storage unavailable")
	rollback := errors.New("rollback retirement diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("retirement-source-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("retirement-source-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retirement-source-failure", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := m.ingestEventOn(t.Context(), tx, event); err != nil {
				return err
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 4)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "retirement storage stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("retirement-source-failure"))
	require.NoError(t, db.Callback().Create().Remove("retirement-source-failure"))
	require.NoError(t, db.Callback().Update().Remove("retirement-source-failure"))
	q := query.BindSources(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "INDEXED", row.State)
	require.Equal(t, source.Version, row.Version)
	require.NotEmpty(t, readKnowledgeChunks(t, m, db, source).Items)
}
