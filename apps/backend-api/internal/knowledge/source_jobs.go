package knowledge

import (
	activityi18n "7link.com/platform/internal/knowledge/i18n"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type sourceIndexJob struct {
	SourceID guid.ID `json:"source_id"`
	Version  int     `json:"version"`
	ActorID  guid.ID `json:"actor_id"`
}

// The platform job owns the renewable lease. Indexing and source CAS commit in
// one transaction, so a killed worker leaves PENDING, never a stranded INDEXING.
func (m *Module) JobHandlers() []jobs.Handler {
	handlers := []jobs.Handler{
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_knowledge_memory_forgetting_name", DescriptionKey: activityi18n.Namespace + ".activity_job_knowledge_memory_forgetting_description", Kind: "knowledge.memory_forgetting", Timeout: 5 * time.Minute, Run: m.memoryForgetJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_knowledge_index_source_name", DescriptionKey: activityi18n.Namespace + ".activity_job_knowledge_index_source_description", Kind: "knowledge.index_source", MaxAttempts: 5, Timeout: 5 * time.Minute, BackoffBase: 30 * time.Second, BackoffMax: 30 * time.Minute, Run: m.indexSourceJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_knowledge_index_reconcile_name", DescriptionKey: activityi18n.Namespace + ".activity_job_knowledge_index_reconcile_description", Kind: "knowledge.index_reconcile", Timeout: 5 * time.Minute, Run: m.reconcileSourcesJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_knowledge_chunk_purge_name", DescriptionKey: activityi18n.Namespace + ".activity_job_knowledge_chunk_purge_description", Kind: "knowledge.chunk_purge", Timeout: 5 * time.Minute, Run: m.purgeChunksJob},
	}
	if m.config.Embedding.Enabled {
		handlers = append(handlers, jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_knowledge_embedding_backfill_name", DescriptionKey: activityi18n.Namespace + ".activity_job_knowledge_embedding_backfill_description", Kind: "knowledge.embedding_backfill", Timeout: 3 * time.Minute, MaxAttempts: 5, BackoffBase: time.Minute, BackoffMax: 8 * time.Minute, Run: m.embeddingBackfillJob})
	}
	return handlers
}

func (m *Module) JobSchedules() []jobs.Schedule {
	schedules := []jobs.Schedule{
		{Name: "knowledge.memory_forgetting", Kind: "knowledge.memory_forgetting", Cron: "7 * * * *", Timezone: "UTC", TenantScope: jobs.ScopePerTenant},
		{Name: "knowledge.index_reconcile", Kind: "knowledge.index_reconcile", Interval: 15 * time.Minute, TenantScope: jobs.ScopePerTenant},
		{Name: "knowledge.chunk_purge", Kind: "knowledge.chunk_purge", Cron: "20 3 * * *", Timezone: "UTC", TenantScope: jobs.ScopePerTenant},
	}
	if m.config.Embedding.Enabled {
		schedules = append(schedules, jobs.Schedule{Name: "knowledge.embedding_backfill", Kind: "knowledge.embedding_backfill", Interval: time.Minute, TenantScope: jobs.ScopePerTenant})
	}
	return schedules
}

func (m *Module) enqueueSourceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, payload sourceIndexJob, recovery string) error {
	if m.queue == nil {
		return ErrUnavailable
	}
	_, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: tenant, Kind: "knowledge.index_source", Payload: payload,
		IdempotencyKey: fmt.Sprintf("source:%s:%d%s", payload.SourceID, payload.Version, recovery), ConcurrencyKey: "source:" + payload.SourceID.String(), CreatedBy: payload.ActorID})
	return err
}
func (m *Module) queueSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SourceSyncInput) (SourceView, error) {
	if in.Version < 1 || (in.Content != nil && (!utf8.ValidString(*in.Content) || utf8.RuneCountInString(*in.Content) > 2000000)) {
		return SourceView{}, ErrInvalid
	}
	// Queue guard precedes IAM and source locks; failure rolls the enqueue back.
	if err := m.enqueueSourceOn(ctx, tx, tenant, sourceIndexJob{SourceID: id, Version: in.Version + 1, ActorID: authn.PrincipalIDFromContext(ctx)}, ""); err != nil {
		return SourceView{}, err
	}
	row, kind, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if row.Version != in.Version || row.State == "INVALIDATED" || row.State == "INDEXING" {
		return SourceView{}, ErrConflict
	}
	if (row.SourceKind != "UPLOAD" && !m.remote.supports(row.SourceKind)) || row.Format == "PDF" {
		return SourceView{}, ErrUnavailable
	}
	if in.Content != nil && row.SourceKind != "UPLOAD" {
		return SourceView{}, ErrInvalid
	}
	if in.Content != nil {
		row.Content = *in.Content
	}
	if err = m.invalidateSourceChunksOn(ctx, tx, tenant, row, nil); err != nil {
		return SourceView{}, err
	}
	row.State, row.StateReason, row.ChunkCount = "PENDING", "", 0
	row.ContentHash, row.TextChars = "", 0
	row.IndexedAt = nil
	row.Version++
	row.UpdatedAt, row.UpdatedBy = m.now(), authn.PrincipalIDFromContext(ctx)
	if err = m.updateSourceOn(ctx, tx, row, in.Version); err != nil {
		return SourceView{}, err
	}
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(sourceScope(row)), id, "knowledge_source", row.Version, kind, "knowledge_write", "knowledge.source_queued", map[string]any{"state": row.State}); err != nil {
		return SourceView{}, err
	}
	return sourceView(row), nil
}
func (m *Module) indexSourceJob(ctx context.Context, job jobs.Job) error {
	var payload sourceIndexJob
	if json.Unmarshal(job.Payload, &payload) != nil || job.TenantID <= 0 || payload.SourceID <= 0 || payload.ActorID <= 0 || payload.Version < 1 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{PrincipalID: payload.ActorID, TenantID: job.TenantID, SubjectType: authn.SubjectTypePrincipal})
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, _, e := m.sourceOn(ctx, tx, job.TenantID, payload.SourceID, "knowledge_write", true)
		if e != nil {
			return e
		}
		if row.Version != payload.Version || row.State != "PENDING" {
			return nil
		}
		_, e = m.syncSourceOn(ctx, tx, job.TenantID, row.ID, SourceSyncInput{Version: row.Version}, false)
		return e
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalid) {
		if failure := m.failSourceJob(ctx, job, payload, "INDEX_AUTHORITY_REVOKED"); failure != nil {
			return failure
		}
		return jobs.Permanent(err)
	}
	if err != nil && job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
		if failure := m.failSourceJob(ctx, job, payload, "INGESTION_RETRIES_EXHAUSTED"); failure != nil {
			return failure
		}
	}
	return err
}
func (m *Module) failSourceJob(ctx context.Context, job jobs.Job, payload sourceIndexJob, reason string) error {
	// Settlement must survive the handler deadline, just like platform job
	// finalization. Preserve tracing/claims but bound the independent write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	db, err := m.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		frozen, e := m.sourceProjectFrozenOn(ctx, tx, job.TenantID, payload.SourceID)
		if e != nil || frozen {
			return e
		}
		q := query.BindSources(tx, m.names)
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(payload.SourceID), q.Version.Eq(payload.Version), q.State.Eq("PENDING")).Updates(map[string]any{"state": "FAILED", "state_reason": reason, "version": payload.Version + 1, "updated_at": m.now()})
		if e != nil {
			return e
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if m.audit == nil || m.events == nil {
			return ErrUnavailable
		}
		event := audit.NewDomainEvent(ctx, job.TenantID, "knowledge.source_failed", "knowledge_source", payload.SourceID)
		event.Detail["authority_source"], event.Detail["reason"], event.Detail["evidence_refs"] = "SYSTEM:knowledge.index_source", reason, []string{}
		if e = m.audit(ctx, tx, event); e != nil {
			return e
		}
		_, e = m.events.Publish(ctx, tx, events.Event{TenantID: job.TenantID, Type: "knowledge.source_failed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:knowledge.index_source"}, Subject: events.Subject{Kind: "knowledge_source", ID: payload.SourceID}, SubjectVersion: int64(payload.Version + 1), Data: map[string]any{"state": "FAILED", "state_reason": reason}})
		return e
	})
}
func (m *Module) reconcileSourcesJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	q := query.BindSources(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.State.Eq("PENDING"), q.UpdatedAt.Lt(m.now().Add(-10*time.Minute))).Order(q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		// A sweep-specific key can recover a dead-lettered original job; the
		// persisted source version makes every duplicate harmless.
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			frozen, e := m.sourceProjectFrozenOn(ctx, tx, job.TenantID, row.ID)
			if e != nil || frozen {
				return e
			}
			return m.enqueueSourceOn(ctx, tx, job.TenantID, sourceIndexJob{SourceID: row.ID, Version: row.Version, ActorID: row.UpdatedBy}, ":reconcile:"+job.ID.String())
		})
		if err != nil {
			return err
		}
	}
	// Upgrade indexed sources through the same authorized queue command. Existing
	// chunks are invalidated atomically with the new version, never silently
	// served under a newer chunker version.
	upgrades, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.State.Eq("INDEXED"), q.ChunkerVersion.Lt(sourceChunkerVersion)).Order(q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	for _, row := range upgrades {
		actorCtx := authn.WithClaims(ctx, &authn.Claims{PrincipalID: row.UpdatedBy, TenantID: job.TenantID, SubjectType: authn.SubjectTypePrincipal})
		err = db.WithContext(actorCtx).Transaction(func(tx *gorm.DB) error {
			_, e := m.queueSourceOn(actorCtx, tx, job.TenantID, row.ID, SourceSyncInput{Version: row.Version})
			return e
		})
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) purgeChunksJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var removed int64
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			q := query.BindChunks(tx, m.names)
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.InvalidatedAt.Lt(m.now().Add(-7*24*time.Hour))).Order(q.ID).Limit(1000).Find()
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				return nil
			}
			ids := make([]driver.Valuer, 0, len(rows))
			for _, row := range rows {
				ids = append(ids, row.ID)
			}
			result, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.In(ids...), q.InvalidatedAt.Lt(m.now().Add(-7*24*time.Hour))).Delete()
			if e != nil {
				return e
			}
			removed = result.RowsAffected
			if removed == 0 {
				return nil
			}
			if m.audit == nil {
				return ErrUnavailable
			}
			event := audit.NewDomainEvent(ctx, job.TenantID, "knowledge.chunks_purged", "knowledge_source", 0)
			event.Detail["authority_source"], event.Detail["count"], event.Detail["evidence_refs"] = "SYSTEM:knowledge.chunk_purge", result.RowsAffected, []string{}
			if e = m.audit(ctx, tx, event); e != nil {
				return e
			}
			if m.events == nil {
				return ErrUnavailable
			}
			_, e = m.events.Publish(ctx, tx, events.Event{TenantID: job.TenantID, Type: "knowledge.chunks_purged", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:knowledge.chunk_purge"}, Subject: events.Subject{Kind: "knowledge_source", ID: rows[0].SourceID}, SubjectVersion: 1, Data: map[string]any{"count": result.RowsAffected}})
			return e
		})
		if err != nil {
			return err
		}
		if removed < 1000 {
			return nil
		}
	}
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
