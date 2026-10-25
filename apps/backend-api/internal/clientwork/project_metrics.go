package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"time"
)

func (m *Module) bindMetrics(s *module.Services) error {
	c, err := module.Resolve[delivery.DeliveryMetricsSourcesConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.BindClient == nil {
		return ErrUnavailable
	}
	return c.BindClient(m.metricClientOn)
}
func (m *Module) metricClientOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID, end time.Time) (delivery.MetricClientHistory, error) {
	out := delivery.MetricClientHistory{Receipts: []delivery.MetricClientReceipt{}, Complete: true}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID != tenant || tx == nil || end.IsZero() || len(work) > 10000 {
		return out, ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalid
	}
	scoped := *claims
	scoped.EntityID = project
	ctx = authn.WithClaims(ctx, &scoped)
	if _, err := m.authorize(ctx, tx, project, "delivery_read", false); err != nil {
		return out, err
	}
	visible := map[guid.ID]bool{}
	for _, id := range work {
		if id <= 0 {
			return out, ErrInvalid
		}
		visible[id] = true
	}
	if len(work) == 0 {
		return out, nil
	}
	q := query.BindPackageReceipt(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CreatedAt.Lt(end.UTC())).Order(q.CreatedAt, q.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > 10000 {
		return out, ErrUnavailable
	}
	p := query.BindDeliveryPackage(tx, m.names)
	for _, receipt := range rows {
		pack, er := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ID.Eq(receipt.PackageID), p.ContentHash.Eq(receipt.ContentHash)).First()
		if er != nil {
			return out, er
		}
		var manifest packageManifest
		if er = json.Unmarshal([]byte(pack.Manifest), &manifest); er != nil {
			return out, er
		}
		relevant := false
		for _, ref := range manifest.Refs {
			relevant = relevant || visible[ref.WorkItemID]
		}
		if !relevant {
			continue
		}
		items := packageItems(pack, manifest.Content)
		if len(items) != len(manifest.Refs) || receipt.ItemOutcomes == nil {
			out.Complete = false
			continue
		}
		byItem := map[string]guid.ID{}
		allVisible := true
		for i, item := range items {
			byItem[item.ItemID] = manifest.Refs[i].WorkItemID
			allVisible = allVisible && visible[manifest.Refs[i].WorkItemID]
		}
		var outcomes []PackageItemDecision
		if er = json.Unmarshal([]byte(*receipt.ItemOutcomes), &outcomes); er != nil {
			return out, er
		}
		if len(outcomes) != len(items) {
			out.Complete = false
			continue
		}
		if allVisible {
			out.Receipts = append(out.Receipts, delivery.MetricClientReceipt{PackageID: pack.ID, At: receipt.CreatedAt, Package: true, Accepted: receipt.Decision == "ACCEPT"})
		}
		seenItems := map[string]bool{}
		for _, item := range outcomes {
			if seenItems[item.ItemID] {
				return out, ErrUnavailable
			}
			seenItems[item.ItemID] = true
			id := byItem[item.ItemID]
			if id == 0 {
				out.Complete = false
				continue
			}
			if visible[id] {
				out.Receipts = append(out.Receipts, delivery.MetricClientReceipt{PackageID: pack.ID, WorkItemID: id, ItemKey: pack.ID.String() + ":" + item.ItemID, At: receipt.CreatedAt, Accepted: item.Result == "ACCEPTED"})
			}
		}
	}
	return out, nil
}
