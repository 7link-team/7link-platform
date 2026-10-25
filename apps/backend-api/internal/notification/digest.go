package notification

import (
	"context"
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

// digestOn folds due daily-digest deliveries into the current address job.
// The caller holds the principal lock. Existing address concurrency keys keep
// the constituent jobs from sending while the combined SMTP request runs.
func (m *Module) digestOn(ctx context.Context, tx *gorm.DB, r *model.Delivery, payload emailPayload, job guid.ID) (emailPayload, []*model.Delivery, error) {
	batch := []*model.Delivery{r}
	if r.NotificationID == nil || r.RecipientPrincipalID == nil {
		return payload, batch, nil
	}
	n := query.BindNotifications(tx, m.names)
	current, err := n.WithContext(ctx).Where(n.ID.Eq(*r.NotificationID)).First()
	if err != nil {
		return payload, nil, err
	}
	global, err := m.preferenceOn(ctx, tx, *r.RecipientPrincipalID, r.TenantID, "*")
	if err != nil {
		return payload, nil, err
	}
	p, err := m.preferenceOn(ctx, tx, *r.RecipientPrincipalID, r.TenantID, current.Type)
	if err != nil {
		return payload, nil, err
	}
	if p.Version == 0 {
		p = global
	}
	if p.Version == 0 {
		p.Email = defaultEmail(current.Type, Severity(current.Severity))
	}
	machineDigest := current.Type == "machine.offline" && current.Permission == ""
	if (p.Email != "DAILY_DIGEST" && !machineDigest) || Severity(current.Severity) == Urgent {
		return payload, batch, nil
	}
	q := query.BindDeliveries(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.RecipientPrincipalID.Eq(*r.RecipientPrincipalID), q.RecipientAddressHash.Eq(r.RecipientAddressHash), q.State.Eq("PENDING"), q.ID.Neq(r.ID)).Order(q.ID).Limit(99).Find()
	if err != nil {
		return payload, nil, err
	}
	for _, candidate := range rows {
		if candidate.NotificationID == nil {
			continue
		}
		notice, err := n.WithContext(ctx).Where(n.ID.Eq(*candidate.NotificationID)).First()
		if err != nil {
			return payload, nil, err
		}
		if Severity(notice.Severity) == Urgent {
			continue
		}
		if machineDigest && (notice.Type != "machine.offline" || notice.Permission != "") {
			continue
		}
		visible, err := m.visibleOn(ctx, tx, notice, *r.RecipientPrincipalID)
		if err != nil {
			return payload, nil, err
		}
		if !visible {
			continue
		}
		specific, err := m.preferenceOn(ctx, tx, *r.RecipientPrincipalID, r.TenantID, notice.Type)
		if err != nil {
			return payload, nil, err
		}
		if specific.Version == 0 {
			specific = global
		}
		if specific.Version == 0 {
			specific.Email = defaultEmail(notice.Type, Severity(notice.Severity))
		}
		if specific.Email != "DAILY_DIGEST" && !machineDigest {
			continue
		}
		enabled, due, err := m.externalPreferenceAtOn(ctx, tx, *r.RecipientPrincipalID, r.TenantID, notice.Type, Severity(notice.Severity), candidate.CreatedAt)
		if err != nil {
			return payload, nil, err
		}
		if !enabled || due.After(m.now()) {
			continue
		}
		var machineParams map[string]string
		if machineDigest {
			if json.Unmarshal([]byte(notice.Params), &machineParams) != nil {
				return payload, nil, ErrInvalid
			}
			at, err := time.Parse(time.RFC3339Nano, machineParams["offline_at"])
			if err != nil {
				return payload, nil, ErrInvalid
			}
			if !m.now().After(at.Add(15 * time.Minute)) {
				continue
			}
		}
		encoded, err := m.keyring.Open("payload", []byte(candidate.TenantID.String()+":"+candidate.ID.String()), candidate.PayloadSealed)
		if err != nil {
			return payload, nil, err
		}
		var part emailPayload
		if err = json.Unmarshal(encoded, &part); err != nil {
			return payload, nil, err
		}
		// Do not merge a reply or subscription message into a different thread.
		if len(part.Headers) > 0 || len(payload.Headers) > 0 || strings.Join(part.To, ",") != strings.Join(payload.To, ",") {
			continue
		}
		if machineDigest {
			addresses := make([]mail.Address, 0, len(part.To))
			for _, address := range part.To {
				addresses = append(addresses, mail.Address{Address: address})
			}
			fresh, err := m.emailPayload(ctx, EmailMessage{TenantID: r.TenantID, To: addresses, TemplateKey: notice.TemplateKey, Params: machineParams})
			if err != nil {
				return payload, nil, err
			}
			part.Text, part.HTML = fresh.Text, fresh.HTML
			candidate.MessageID = r.MessageID
		}
		payload.Text += "\n\n" + part.Text
		payload.HTML += "<hr>" + part.HTML
		candidate.JobID = &job
		candidate.Attempts = r.Attempts + 1
		candidate.State = "SENDING"
		if _, err = q.WithContext(ctx).Where(q.ID.Eq(candidate.ID)).Updates(map[string]any{"job_id": job, "attempts": candidate.Attempts, "state": "SENDING", "message_id": candidate.MessageID, "updated_at": m.now()}); err != nil {
			return payload, nil, err
		}
		batch = append(batch, candidate)
	}
	return payload, batch, nil
}
