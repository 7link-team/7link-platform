package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	"gorm.io/gorm"
)

const fanoutKind = "jobs.fan_out"

// TenantFanoutConnector joins the organization-owned active tenant reader at
// the composition root without making infrastructure depend on IAM lifecycle.
type TenantFanoutConnector struct {
	Bind func(func(context.Context, guid.ID, int) ([]guid.ID, error)) error
}

func (m *Module) bindTenantFanout(enumerate func(context.Context, guid.ID, int) ([]guid.ID, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if enumerate == nil || m.activeTenants != nil {
		return ErrInvalidRequest
	}
	if _, exists := m.handlers[fanoutKind]; exists {
		return ErrInvalidDeclaration
	}
	handler, err := normalizeHandler(Name, Handler{Kind: fanoutKind, Run: m.fanOut})
	if err != nil {
		return err
	}
	if m.handlers == nil {
		m.handlers = map[string]Handler{}
	}
	m.handlers[fanoutKind] = handler
	m.activeTenants = enumerate
	return nil
}

func fanoutKey(name string, fireMS int64, tenant guid.ID) string {
	key := fmt.Sprintf("%s:%d:%s", name, fireMS, tenant.String())
	if len(key) <= 128 {
		return key
	}
	return fmt.Sprintf("fanout-sha256:%x", sha256.Sum256([]byte(key)))
}

// fanOut reads the immutable dispatch snapshot, never the mutable schedule.
// Pages commit separately. Retrying from the beginning is safe because each
// child has a deterministic key and Queue checks duplicates before its quota.
func (m *Module) fanOut(ctx context.Context, job Job) error {
	if m.activeTenants == nil {
		return ErrUnavailable
	}
	if job.ID <= 0 || job.TenantID != 0 {
		return Permanent(ErrInvalidRequest)
	}
	var source *model.Job
	err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		table := query.BindJobs(tx, m.names)
		var err error
		source, err = table.WithContext(ctx).Where(table.ID.Eq(job.ID), table.TenantID.Eq(guid.ID(0)), table.Kind.Eq(fanoutKind), table.State.Eq("RUNNING")).First()
		return err
	})
	if err != nil {
		return err
	}
	if source.FanoutKind == nil || source.FanoutFireMS == nil || source.ConcurrencyKey == nil || *source.ConcurrencyKey == "" || *source.FanoutKind == fanoutKind {
		return Permanent(ErrInvalidRequest)
	}
	var after guid.ID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tenants, err := m.activeTenants(ctx, after, 500)
		if err != nil {
			return err
		}
		if len(tenants) > 500 {
			return ErrInvalidRequest
		}
		if len(tenants) == 0 {
			return nil
		}
		requests := make([]EnqueueRequest, len(tenants))
		cursor := after
		for i, tenant := range tenants {
			if tenant <= cursor {
				return ErrInvalidRequest
			}
			cursor = tenant
			requests[i] = EnqueueRequest{TenantID: tenant, Kind: *source.FanoutKind, Payload: json.RawMessage(source.Payload), ConcurrencyKey: *source.ConcurrencyKey, IdempotencyKey: fanoutKey(*source.ConcurrencyKey, *source.FanoutFireMS, tenant)}
		}
		if _, err := m.EnqueueBatch(ctx, nil, requests); err != nil {
			return err
		}
		after = cursor
		if len(tenants) < 500 {
			return nil
		}
	}
}
