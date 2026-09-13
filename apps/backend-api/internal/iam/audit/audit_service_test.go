package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var auditTestNow = time.Date(2026, 9, 3, 20, 0, 0, 123_000_000, time.UTC)

type auditTestIDs struct{ next atomic.Int64 }

func newAuditTestIDs(start int64) *auditTestIDs {
	ids := &auditTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *auditTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type auditFailingIDs struct{}

func (auditFailingIDs) Next() (int64, error) { return 0, errors.New("ID source down") }

type auditHarness struct {
	service *Service
	runtime *appenv.Runtime
	db      *gorm.DB
	prefix  string
}

func newAuditHarness(t *testing.T, prefix string) *auditHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on",
		filepath.ToSlash(filepath.Join(t.TempDir(), "audit.sqlite")),
	)
	initializeAuditDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(
		appenv.NewIdentity("audit-test", "1"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	service := NewService(runtime, newAuditTestIDs(1000))
	service.now = func() time.Time { return auditTestNow }
	return &auditHarness{service: service, runtime: runtime, db: router.Write(), prefix: prefix}
}

func initializeAuditDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, "sqlite", 2))
	require.NoError(t, sqlDB.Close())
}

func (h *auditHarness) table(name string) string { return h.prefix + name }

func (h *auditHarness) count(t *testing.T, table string, tenantID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).Where("tenant_id = ?", tenantID).Count(&count).Error)
	return count
}

func TestAuditAppendListVerifyTenantIsolationAndImmutability(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	tenantA, tenantB := guid.ID(11), guid.ID(12)
	first, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantA, PrincipalID: 21, EventType: "oauth_client_created",
		TargetType: "oauth_client", TargetID: 31, Outcome: "success",
		IPAddress: "127.0.0.1", UserAgent: "audit-test", Detail: map[string]any{"public": false},
	})
	require.NoError(t, err)
	second, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantA, EventType: "oauth_client_rotated", Outcome: "success",
	})
	require.NoError(t, err)
	foreign, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantB, EventType: "login", Outcome: "denied",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), first.Sequence)
	assert.Equal(t, int64(2), second.Sequence)
	assert.Equal(t, first.EventHash, second.PreviousHash)
	assert.Equal(t, int64(1), foreign.Sequence)
	assert.JSONEq(t, `{}`, string(second.Detail))

	events, total, err := h.service.List(t.Context(), Filter{
		TenantID: tenantA, TargetType: "oauth_client", Offset: 0, Limit: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, events, 1)
	assert.Equal(t, first.ID, events[0].ID)

	filtered, total, err := h.service.List(t.Context(), Filter{
		TenantID: tenantA, PrincipalID: 21, EventType: "oauth_client_created",
		Outcome: "success", TargetType: "oauth_client", TargetID: 31,
		From: auditTestNow.Add(-time.Second), To: auditTestNow.Add(time.Second), Offset: 0, Limit: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, filtered, 1)
	assert.Equal(t, first.ID, filtered[0].ID)

	got, err := h.service.Get(t.Context(), tenantA, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first.EventHash, got.EventHash)
	_, err = h.service.Get(t.Context(), tenantB, first.ID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	for _, tenantID := range []guid.ID{tenantA, tenantB} {
		integrity, verifyErr := h.service.Verify(t.Context(), tenantID)
		require.NoError(t, verifyErr)
		assert.True(t, integrity.Valid)
		assert.Equal(t, h.count(t, "iam_audit_events", tenantID), integrity.VerifiedEvents)
	}

	var stored model.Event
	require.NoError(t, h.db.Table(h.table("iam_audit_events")).Where("id = ?", second.ID).Take(&stored).Error)
	assert.Nil(t, stored.PrincipalID)
	assert.Nil(t, stored.TargetID)

	err = h.db.Exec("UPDATE "+h.table("iam_audit_events")+" SET outcome = 'failure' WHERE id = ?", first.ID).Error
	assert.ErrorContains(t, err, "append-only")
	err = h.db.Exec("DELETE FROM "+h.table("iam_audit_events")+" WHERE id = ?", first.ID).Error
	assert.ErrorContains(t, err, "append-only")
}

func TestAuditNormalizesFixedWidthEmptyHeadHash(t *testing.T) {
	h := newAuditHarness(t, "")
	tenantID := guid.ID(13)
	require.NoError(t, h.db.Table("iam_audit_heads").Create(&model.Head{
		TenantID: tenantID, EventHash: strings.Repeat(" ", 64), UpdatedAt: auditTestNow,
	}).Error)

	event, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantID, EventType: "created", Outcome: "success",
	})
	require.NoError(t, err)
	assert.Empty(t, event.PreviousHash)

	integrity, err := h.service.Verify(t.Context(), tenantID)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
	assert.Equal(t, int64(1), integrity.VerifiedEvents)

	legacy := model.Event{
		ID: 1, TenantID: tenantID, EventType: "legacy", Outcome: "success", Detail: "{}",
		CreatedAt: auditTestNow, Sequence: 1, PreviousHash: strings.Repeat(" ", 64),
	}
	legacy.EventHash = hashStored(legacy)
	assert.True(t, validEventHash(legacy))
}

func TestAuditAppenderUsesCallerTransactionAndRollback(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	tenantID := guid.ID(41)
	tx := h.db.Begin()
	require.NoError(t, tx.Error)
	_, err := h.service.AppendTo(t.Context(), tx, EventInput{
		TenantID: tenantID, EventType: "direct", Outcome: "success",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Rollback().Error)
	assert.Zero(t, h.count(t, "iam_audit_events", tenantID))
	assert.Zero(t, h.count(t, "iam_audit_heads", tenantID))

	tx = h.db.Begin()
	require.NoError(t, tx.Error)
	err = h.service.DomainAppender()(t.Context(), tx, DomainEvent{
		TenantID: tenantID, PrincipalID: 51, EventType: "domain_changed",
		TargetType: "principal", TargetID: 51, Detail: map[string]any{"field": "name"},
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit().Error)
	assert.Equal(t, int64(1), h.count(t, "iam_audit_events", tenantID))

	events, _, err := h.service.List(t.Context(), Filter{TenantID: tenantID, Offset: 0, Limit: 50})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "success", events[0].Outcome)
	assert.Equal(t, guid.ID(51), events[0].PrincipalID)
}

func TestAuditRequestExportCreatesVerifiedFrozenSnapshot(t *testing.T) {
	h := newAuditHarness(t, "")
	tenantA, tenantB := guid.ID(61), guid.ID(62)
	first, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantA, PrincipalID: 71, EventType: "principal_created", Outcome: "success",
	})
	require.NoError(t, err)
	_, err = h.service.Append(t.Context(), EventInput{
		TenantID: tenantB, EventType: "foreign_event", Outcome: "success",
	})
	require.NoError(t, err)

	snapshot, err := h.service.RequestExport(t.Context(), Filter{TenantID: tenantA}, 71)
	require.NoError(t, err)
	assert.Equal(t, int64(2), snapshot.Integrity.HeadSequence)
	assert.Equal(t, int64(2), snapshot.Integrity.VerifiedEvents)
	_, err = h.service.Append(t.Context(), EventInput{
		TenantID: tenantA, EventType: "after_snapshot", Outcome: "success",
	})
	require.NoError(t, err)

	var output bytes.Buffer
	require.NoError(t, h.service.WriteExport(t.Context(), snapshot, &output))
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 4)
	var manifest exportManifest
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &manifest))
	assert.Equal(t, "audit-export.v1", manifest.Schema)
	assert.Equal(t, tenantA, manifest.TenantID)
	assert.Equal(t, snapshot.Integrity.HeadHash, manifest.HeadHash)
	assert.Equal(t, first.CreatedAt, manifest.GeneratedAt)
	assert.NotContains(t, output.String(), "foreign_event")
	assert.NotContains(t, output.String(), "after_snapshot")

	var complete exportComplete
	require.NoError(t, json.Unmarshal([]byte(lines[3]), &complete))
	assert.Equal(t, int64(2), complete.ExportedEvents)
	contentHash := sha256.Sum256([]byte(lines[1] + "\n" + lines[2] + "\n"))
	assert.Equal(t, hex.EncodeToString(contentHash[:]), complete.ContentSHA256)
}

func TestAuditExportValidationWriterFailureAndTampering(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	tenantID := guid.ID(81)
	_, err := h.service.PrepareExport(t.Context(), Filter{
		TenantID: tenantID, From: auditTestNow, To: auditTestNow.Add(-time.Second),
	})
	assert.ErrorIs(t, err, ErrInvalidFilter)
	assert.ErrorIs(t, h.service.WriteExport(t.Context(), ExportSnapshot{}, nil), ErrInvalidFilter)
	assert.Error(t, writeJSONLine(io.Discard, make(chan int)))
	assert.ErrorIs(t, writeLine(shortAuditWriter{}, []byte("line")), io.ErrShortWrite)

	event, err := h.service.Append(t.Context(), EventInput{
		TenantID: tenantID, EventType: "created", Outcome: "success",
	})
	require.NoError(t, err)
	snapshot, err := h.service.PrepareExport(t.Context(), Filter{TenantID: tenantID})
	require.NoError(t, err)
	assert.ErrorContains(t, h.service.WriteExport(t.Context(), snapshot, errorAuditWriter{}), "manifest")

	require.NoError(t, h.db.Exec("DROP TRIGGER "+h.prefix+"trg_iam_audit_events_no_update").Error)
	require.NoError(t, h.db.Exec(
		"UPDATE "+h.table("iam_audit_events")+" SET event_hash = 'tampered' WHERE id = ?", event.ID,
	).Error)
	integrity, err := h.service.Verify(t.Context(), tenantID)
	require.NoError(t, err)
	assert.False(t, integrity.Valid)
	_, err = h.service.PrepareExport(t.Context(), Filter{TenantID: tenantID})
	assert.ErrorIs(t, err, ErrIntegrity)
	var output bytes.Buffer
	assert.ErrorIs(t, h.service.WriteExport(t.Context(), snapshot, &output), ErrIntegrity)
	assert.NotContains(t, output.String(), `"type":"complete"`)
}

func TestAuditValidationAndStorageFailures(t *testing.T) {
	h := newAuditHarness(t, "")
	for _, input := range []EventInput{
		{TenantID: 1, EventType: "", Outcome: "success"},
		{TenantID: 1, EventType: strings.Repeat("e", 65), Outcome: "success"},
		{TenantID: 1, EventType: "event", Outcome: "unknown"},
		{TenantID: 1, EventType: "event", TargetType: strings.Repeat("x", 65), Outcome: "success"},
		{TenantID: 1, EventType: "event", IPAddress: strings.Repeat("i", 65), Outcome: "success"},
		{TenantID: 1, EventType: "event", UserAgent: strings.Repeat("u", 513), Outcome: "success"},
	} {
		_, err := h.service.Append(t.Context(), input)
		assert.ErrorIs(t, err, ErrInvalidEvent)
	}
	_, err := h.service.Append(t.Context(), EventInput{
		TenantID: 1, EventType: "event", Outcome: "success", Detail: map[string]any{"bad": make(chan int)},
	})
	assert.ErrorContains(t, err, "encode audit detail")
	_, err = h.service.AppendTo(t.Context(), nil, EventInput{TenantID: 1, EventType: "event", Outcome: "success"})
	assert.ErrorIs(t, err, ErrInvalidEvent)

	for _, filter := range []Filter{
		{TenantID: 1, Limit: 0},
		{TenantID: 1, Offset: -1, Limit: 50},
		{TenantID: 1, Limit: 201},
		{TenantID: 1, Outcome: "unknown", Limit: 50},
		{TenantID: 1, EventType: strings.Repeat("e", 65), Limit: 50},
		{TenantID: 1, TargetType: strings.Repeat("t", 65), Limit: 50},
	} {
		_, _, listErr := h.service.List(t.Context(), filter)
		assert.ErrorIs(t, listErr, ErrInvalidFilter)
	}
	empty, err := h.service.Verify(t.Context(), 99)
	require.NoError(t, err)
	assert.True(t, empty.Valid)
	assert.Zero(t, empty.VerifiedEvents)

	failing := NewService(h.runtime, auditFailingIDs{})
	_, err = failing.Append(t.Context(), EventInput{TenantID: 1, EventType: "event", Outcome: "success"})
	assert.ErrorContains(t, err, "ID source down")
	assert.Panics(t, func() { NewService(nil, nil) })

	require.NoError(t, h.db.Exec("DROP TABLE iam_audit_events").Error)
	_, _, err = h.service.List(t.Context(), Filter{TenantID: 1, Limit: 50})
	assert.ErrorContains(t, err, "count audit events")
	_, err = h.service.Get(t.Context(), 1, 1)
	assert.Error(t, err)
}

func TestAuditConcurrentAppendNeverForks(t *testing.T) {
	h := newAuditHarness(t, "")
	tenantID := guid.ID(101)
	const workers = 16
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = func() error {
				_, err := h.service.Append(context.Background(), EventInput{
					TenantID: tenantID, EventType: fmt.Sprintf("event_%d", index), Outcome: "success",
				})
				return err
			}()
		}(i)
	}
	wg.Wait()

	var successes int64
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	require.Positive(t, successes)
	integrity, err := h.service.Verify(t.Context(), tenantID)
	require.NoError(t, err)
	assert.True(t, integrity.Valid)
	assert.Equal(t, successes, integrity.VerifiedEvents)
	assert.Equal(t, successes, integrity.HeadSequence)
	assert.Equal(t, successes, h.count(t, "iam_audit_events", tenantID))

	var distinctSequences int64
	require.NoError(t, h.db.Table(h.table("iam_audit_events")).
		Where("tenant_id = ?", tenantID).Distinct("sequence").Count(&distinctSequences).Error)
	assert.Equal(t, successes, distinctSequences)
}

func TestAuditEmptyFilteredAndInterruptedExports(t *testing.T) {
	h := newAuditHarness(t, "")
	empty, err := h.service.PrepareExport(t.Context(), Filter{TenantID: 111})
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, h.service.WriteExport(t.Context(), empty, &output))
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 2)
	var complete exportComplete
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &complete))
	assert.Zero(t, complete.ExportedEvents)
	assert.Equal(t, hex.EncodeToString(sha256.New().Sum(nil)), complete.ContentSHA256)

	_, err = h.service.Append(t.Context(), EventInput{TenantID: 111, EventType: "present", Outcome: "success"})
	require.NoError(t, err)
	filtered, err := h.service.PrepareExport(t.Context(), Filter{
		TenantID: 111, EventType: "missing", From: auditTestNow.Add(-time.Hour), To: auditTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	output.Reset()
	require.NoError(t, h.service.WriteExport(t.Context(), filtered, &output))
	assert.Contains(t, output.String(), `"from":"2026-09-03T19:00:00.123Z"`)
	assert.Contains(t, output.String(), `"to":"2026-09-03T21:00:00.123Z"`)

	full, err := h.service.PrepareExport(t.Context(), Filter{TenantID: 111})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("DROP TABLE iam_audit_events").Error)
	output.Reset()
	assert.ErrorContains(t, h.service.WriteExport(t.Context(), full, &output), "read audit export")
	assert.NotContains(t, output.String(), `"type":"complete"`)
}

func TestAuditExportPropagatesEventAndCompletionWriterFailures(t *testing.T) {
	h := newAuditHarness(t, "")
	_, err := h.service.Append(t.Context(), EventInput{TenantID: 121, EventType: "created", Outcome: "success"})
	require.NoError(t, err)
	snapshot, err := h.service.PrepareExport(t.Context(), Filter{TenantID: 121})
	require.NoError(t, err)
	assert.ErrorContains(t, h.service.WriteExport(t.Context(), snapshot, &failOnAuditWrite{failAt: 2}), "write audit export event")
	assert.ErrorContains(t, h.service.WriteExport(t.Context(), snapshot, &failOnAuditWrite{failAt: 3}), "write audit export completion")

	failing := NewService(h.runtime, auditFailingIDs{})
	failing.now = h.service.now
	_, err = failing.RequestExport(t.Context(), Filter{TenantID: 122}, 1)
	assert.ErrorContains(t, err, "ID source down")
	_, err = h.service.RequestExport(t.Context(), Filter{TenantID: 1, From: auditTestNow, To: auditTestNow}, 1)
	assert.ErrorIs(t, err, ErrInvalidFilter)
}

func TestAuditNilRepositoriesAndNestedTransactionsFailClosed(t *testing.T) {
	var repo *Repository
	assert.Nil(t, repo.writer())
	_, err := repo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, repo.runInTx(t.Context(), func(*Repository) error { return nil }))
	assert.Error(t, (&Repository{}).runInTx(t.Context(), nil))
	_, err = (&Repository{}).nextID()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{}).appendTo(t.Context(), nil, EventInput{}, "{}", auditTestNow)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	repo = &Repository{}
	_, err = repo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repo.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)
	_, _, err = repo.list(t.Context(), Filter{})
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.get(t.Context(), 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = repo.readHead(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.readPrefix(t.Context(), 1, 1, 0, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.readExport(t.Context(), Filter{TenantID: 1}, 0, 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.getRetentionPolicy(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repo.setRetentionPolicy(t.Context(), model.RetentionPolicy{}), errRepositoryUnavailable)
	_, _, err = repo.governanceCounts(t.Context(), 1, auditTestNow)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{ids: newAuditTestIDs(-1)}).nextID()
	assert.ErrorContains(t, err, "must be positive")

	var service *Service
	assert.ErrorIs(t, service.DomainAppender()(t.Context(), nil, DomainEvent{}), errRepositoryUnavailable)
	_, _, err = service.List(t.Context(), Filter{TenantID: 1, Limit: 50})
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = service.Get(t.Context(), 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = service.Verify(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = service.PrepareExport(t.Context(), Filter{TenantID: 1})
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	h := newAuditHarness(t, "")
	require.NoError(t, h.service.repo.runInTx(t.Context(), func(outer *Repository) error {
		return outer.runInTx(t.Context(), func(inner *Repository) error {
			assert.Same(t, outer, inner)
			return nil
		})
	}))
}

type shortAuditWriter struct{}

func (shortAuditWriter) Write(payload []byte) (int, error) { return len(payload) - 1, nil }

type errorAuditWriter struct{}

func (errorAuditWriter) Write([]byte) (int, error) { return 0, errors.New("writer failed") }

type failOnAuditWrite struct {
	writes int
	failAt int
}

func (writer *failOnAuditWrite) Write(payload []byte) (int, error) {
	writer.writes++
	if writer.writes == writer.failAt {
		return 0, errors.New("writer failed")
	}
	return len(payload), nil
}
