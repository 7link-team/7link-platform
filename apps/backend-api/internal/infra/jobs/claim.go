package jobs

import (
	"context"
	"database/sql/driver"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// claim owns a short transaction. The per-queue guard makes the tenant running
// count and admission atomic across replicas, including an empty tenant queue
// where there is no existing RUNNING row to lock. Execution occurs after commit.
func (m *Module) claim(ctx context.Context, queue, owner string, slots int) ([]model.Job, error) {
	if !namespacePattern.MatchString(queue) || owner == "" || !utf8.ValidString(owner) || utf8.RuneCountInString(owner) > 64 || slots < 0 || slots > math.MaxInt/2 {
		return nil, ErrInvalidRequest
	}
	if m == nil || m.config.MaxRunningPerTenant < 1 || m.config.Lease < time.Millisecond {
		return nil, ErrUnavailable
	}
	m.mu.RLock()
	kinds := make([]string, 0, len(m.handlers))
	for kind, h := range m.handlers {
		if h.Queue == queue {
			kinds = append(kinds, kind)
		}
	}
	sealed := m.sealed
	m.mu.RUnlock()
	if !sealed {
		return nil, ErrUnavailable
	}
	claimed := make([]model.Job, 0)
	if slots == 0 || len(kinds) == 0 {
		return claimed, nil
	}
	// A read-only hint avoids taking the queue guard and SQLite writer slot
	// when there is no due work. Admission is still rechecked under the guard.
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	db := m.runtime.Database().Write()
	nowMS, err := gormx.DatabaseClockMillis(ctx, db)
	if err != nil {
		return nil, err
	}
	table := query.BindJobs(db, m.names)
	pending, err := table.WithContext(ctx).Where(table.Queue.Eq(queue), table.State.Eq("PENDING"), table.RunAtMS.Lte(nowMS), table.Kind.In(kinds...)).Limit(1).Find()
	if err != nil || len(pending) == 0 {
		return claimed, err
	}
	slices.Sort(kinds)
	err = m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		if err := m.guard(ctx, tx, "jobs:claim:"+queue); err != nil {
			return err
		}
		table := query.BindJobs(tx, m.names)
		var counts []struct {
			TenantID guid.ID
			Running  int
		}
		if err := table.WithContext(ctx).Select(table.TenantID, table.ID.Count().As("running")).Where(table.Queue.Eq(queue), table.State.Eq("RUNNING")).Group(table.TenantID).Scan(&counts); err != nil {
			return err
		}
		running := make(map[guid.ID]int, len(counts))
		full := make([]driver.Valuer, 0)
		for _, count := range counts {
			running[count.TenantID] = count.Running
			if count.TenantID != 0 && count.Running >= m.config.MaxRunningPerTenant {
				full = append(full, count.TenantID)
			}
		}
		nowMS, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		read := table.WithContext(ctx)
		if tx.Dialector.Name() != "sqlite" {
			read = read.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		read = read.Where(table.Queue.Eq(queue), table.State.Eq("PENDING"), table.RunAtMS.Lte(nowMS), table.Kind.In(kinds...))
		if len(full) > 0 {
			read = read.Where(table.TenantID.NotIn(full...))
		}
		candidates, err := read.Order(table.Priority, table.RunAtMS, table.ID).Limit(2 * slots).Find()
		if err != nil {
			return err
		}
		now := time.UnixMilli(nowMS).UTC()
		expires := nowMS + m.config.Lease.Milliseconds()
		for _, row := range candidates {
			if len(claimed) == slots {
				break
			}
			if row.TenantID != 0 && running[row.TenantID] >= m.config.MaxRunningPerTenant {
				continue
			}
			result, err := table.WithContext(ctx).Where(table.ID.Eq(row.ID), table.State.Eq("PENDING")).UpdateSimple(table.State.Value("RUNNING"), table.Attempts.Add(1), table.LeaseToken.Add(1), table.LeaseOwner.Value(owner), table.LeaseExpiresAtMS.Value(expires), table.StartedAt.Value(now), table.UpdatedAt.Value(now), table.Version.Add(1))
			if err != nil {
				return err
			}
			if result.RowsAffected == 0 {
				continue
			}
			row.State = "RUNNING"
			row.Attempts++
			row.LeaseToken++
			row.LeaseOwner = &owner
			row.LeaseExpiresAtMS = &expires
			row.StartedAt = &now
			row.UpdatedAt = now
			row.Version++
			claimed = append(claimed, *row)
			running[row.TenantID]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}
