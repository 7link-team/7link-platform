package fleet

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Prices are exact decimal strings in JSON, in USD micros per million tokens.
type ModelPriceWrite struct {
	Input      int64 `json:"input_micros_per_mtok,string" minimum:"0"`
	Output     int64 `json:"output_micros_per_mtok,string" minimum:"0"`
	CacheRead  int64 `json:"cache_read_micros_per_mtok,string" minimum:"0"`
	CacheWrite int64 `json:"cache_write_micros_per_mtok,string" minimum:"0"`
}
type ModelPriceView struct {
	ModelPriceAmounts
	ID        guid.ID   `json:"id"`
	Runtime   string    `json:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
	Model     string    `json:"model_id"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
type ModelPricePage struct {
	Items      []ModelPriceView `json:"items"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func priceView(row *model.ModelPrice) ModelPriceView {
	return ModelPriceView{ModelPriceAmounts: ModelPriceAmounts{strconv.FormatInt(row.InputMicros, 10), strconv.FormatInt(row.OutputMicros, 10), strconv.FormatInt(row.CacheReadMicros, 10), strconv.FormatInt(row.CacheWriteMicros, 10)}, ID: row.ID, Runtime: row.Runtime, Model: row.Model, Version: row.Version, UpdatedAt: row.UpdatedAt}
}
func (m *Module) ModelPricesOn(ctx context.Context, tx *gorm.DB, tenant, after guid.ID, limit int) (ModelPricePage, error) {
	out := ModelPricePage{Items: []ModelPriceView{}}
	if fleetTransaction(tx, tenant, tenant) != nil || after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
		return out, err
	}
	q := query.BindPrices(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.DeletedAt.IsNull(), q.ID.Gt(after)).Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
		out.NextCursor = rows[len(rows)-1].ID.String()
	}
	for _, row := range rows {
		out.Items = append(out.Items, priceView(row))
	}
	return out, nil
}

// WriteModelPriceOn keeps tombstones so an old version cannot delete a recreated
// price. Replays recheck current Human authority and never append effects twice.
func (m *Module) WriteModelPriceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, runtime, id string, version int, in *ModelPriceWrite) (ModelPriceView, error) {
	var out ModelPriceView
	if fleetTransaction(tx, tenant, tenant) != nil || version < 0 || !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, runtime) || !discoveryID(id, 160) {
		return out, ErrInvalidArgument
	}
	if in != nil && (in.Input < 0 || in.Output < 0 || in.CacheRead < 0 || in.CacheWrite < 0) {
		return out, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); err != nil {
		return out, err
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return out, ErrUnavailable
	}
	q := query.BindPrices(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Runtime.Eq(runtime), q.Model.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if row == nil {
		if in == nil {
			return out, nil
		}
		if version != 0 {
			return out, ErrVersionConflict
		}
		value, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		if value <= 0 {
			return out, ErrUnavailable
		}
		row = &model.ModelPrice{ID: guid.ID(value), TenantID: tenant, Runtime: runtime, Model: id}
	} else {
		out = priceView(row)
		if in == nil && row.DeletedAt != nil {
			return out, nil
		}
		same := in != nil && row.DeletedAt == nil && (ModelPriceWrite{row.InputMicros, row.OutputMicros, row.CacheReadMicros, row.CacheWriteMicros}) == *in
		if row.Version != version && !(row.DeletedAt != nil && version == 0 && in != nil) {
			if row.Version == version+1 && same {
				return out, nil
			}
			return out, ErrVersionConflict
		}
		if same {
			return out, nil
		}
	}
	oldVersion := row.Version
	row.Version++
	row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if in == nil {
		row.DeletedAt = &row.UpdatedAt
	} else {
		row.InputMicros = in.Input
		row.OutputMicros = in.Output
		row.CacheReadMicros = in.CacheRead
		row.CacheWriteMicros = in.CacheWrite
		row.DeletedAt = nil
	}
	if oldVersion == 0 {
		err = q.WithContext(ctx).Create(row)
	} else {
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(oldVersion)).Select(q.InputMicros, q.OutputMicros, q.CacheReadMicros, q.CacheWriteMicros, q.Version, q.UpdatedAt, q.DeletedAt).Updates(row)
		err = e
		if err == nil && result.RowsAffected != 1 {
			err = ErrVersionConflict
		}
	}
	if err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.model_price_changed", "model_price", row.ID)
	event.Detail["runtime_kind"] = runtime
	event.Detail["model_id"] = id
	event.Detail["version"] = row.Version
	event.Detail["deleted"] = in == nil
	event.Detail["reason"] = ""
	event.Detail["authority_source"] = "IAM"
	event.Detail["evidence_refs"] = []string{}
	if err = m.audit(ctx, tx, event); err != nil {
		return out, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "fleet.model_price_changed", Subject: events.Subject{Kind: "model_price", ID: row.ID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: event.Detail})
	return priceView(row), err
}
