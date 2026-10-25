package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

// notifySourceOn keeps once-only occurrence receipts separate from mutable
// unread grouping. Caller has checked current source audience in this transaction.
func (m *Module) notifySourceOn(ctx context.Context, tx *gorm.DB, n Notification, occurrence, source guid.ID) error {
	if len(n.Recipients) != 1 || occurrence <= 0 || source <= 0 || m.guard == nil {
		return ErrInvalid
	}
	recipient := n.Recipients[0]
	if err := m.principal.LockActive(ctx, tx, recipient); err != nil {
		return err
	}
	if err := m.guard(ctx, tx, "notification:source:"+n.TenantID.String()+":"+recipient.String()); err != nil {
		return err
	}
	q := query.BindSourceReceipts(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(n.TenantID), q.Type.Eq(n.Type), q.OccurrenceID.Eq(occurrence), q.RecipientPrincipalID.Eq(recipient)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if old != nil {
		return nil
	}
	ids, err := m.Notify(ctx, tx, n)
	if err != nil || len(ids) == 0 {
		return err
	}
	return q.WithContext(ctx).Create(&model.SourceReceipt{TenantID: n.TenantID, Type: n.Type, OccurrenceID: occurrence, RecipientPrincipalID: recipient, SourceID: source, NotificationID: ids[0], CreatedAt: m.now()})
}

// Each grouped source is rechecked independently: a completed first source
// cannot hide its pending siblings, and unavailable sources never count.
func (m *Module) sourceGroupAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	q := query.BindSourceReceipts(tx, m.names)
	receipts, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.Type.Eq(row.Type), q.NotificationID.Eq(row.ID), q.RecipientPrincipalID.Eq(principal)).Order(q.OccurrenceID).Find()
	if err != nil {
		return false, err
	}
	if len(receipts) == 0 {
		legacy := *row
		legacy.ID = 0
		return m.sourceAudienceOn(ctx, tx, &legacy, principal)
	}
	count := 0
	var machineOfflineAt time.Time
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	for _, receipt := range receipts {
		candidate := *row
		candidate.ID = 0
		p := map[string]string{}
		for k, v := range params {
			p[k] = v
		}
		p["subject_id"] = receipt.SourceID.String()
		data, err := json.Marshal(p)
		if err != nil {
			return false, err
		}
		candidate.Params = string(data)
		yes, err := m.sourceAudienceOn(ctx, tx, &candidate, principal)
		if err != nil {
			return false, err
		}
		if yes {
			count++
			params["subject_id"] = receipt.SourceID.String()
			if row.Type == "machine.offline" {
				f, found, err := m.machineNoticeFacts(ctx, tx, row.TenantID, 0, receipt.SourceID)
				if err != nil {
					return false, err
				}
				if !found {
					return false, ErrConflict
				}
				params["machine_id"] = f.MachineID.String()
				if machineOfflineAt.IsZero() || f.OfflineAt.Before(machineOfflineAt) {
					machineOfflineAt = f.OfflineAt
				}
				params["offline_at"] = machineOfflineAt.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if count == 0 {
		return false, nil
	}
	params["source_count"] = strconv.Itoa(count)
	data, err := json.Marshal(params)
	if err != nil {
		return false, err
	}
	row.Params, row.MergeCount = string(data), count
	return true, nil
}
