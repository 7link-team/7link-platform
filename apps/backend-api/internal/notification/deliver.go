package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/mail"
	"net/textproto"
	"time"
)

func (m *Module) deliveryTerminalOn(ctx context.Context, tx *gorm.DB, r *model.Delivery, state, reason, code string) error {
	if terminal(r.State) {
		return nil
	}
	r.State = state
	r.SuppressReason = reason
	r.LastError = code
	r.UpdatedAt = m.now()
	if state == "SENT" {
		r.SentAt = &r.UpdatedAt
	}
	q := query.BindDeliveries(tx, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).Updates(map[string]any{"state": r.State, "suppress_reason": r.SuppressReason, "last_error": r.LastError, "sent_at": r.SentAt, "updated_at": r.UpdatedAt})
	if err != nil {
		return err
	}
	return m.publishDeliveryStateOn(ctx, tx, r)
}
func (m *Module) deliverJob(ctx context.Context, j jobs.Job) error {
	var input struct {
		DeliveryID guid.ID `json:"delivery_id"`
	}
	if err := json.Unmarshal(j.Payload, &input); err != nil || input.DeliveryID <= 0 || j.TenantID < 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	var row *model.Delivery
	var batch []*model.Delivery
	var payload emailPayload
	var currentMachineNotice *model.Notification
	send := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.admission == nil {
			return ErrUnavailable
		}
		if err := m.admission(ctx, tx, j.TenantID); err != nil {
			return err
		}
		q := query.BindDeliveries(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.ID.Eq(input.DeliveryID), q.TenantID.Eq(j.TenantID)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return jobs.Permanent(ErrNotFound)
		}
		if err != nil {
			return err
		}
		if terminal(r.State) || r.JobID == nil || *r.JobID != j.ID {
			return nil
		}
		if r.RecipientPrincipalID != nil {
			if err = m.principal.LockActive(ctx, tx, *r.RecipientPrincipalID); err != nil {
				return err
			}
		}
		scope := q.WithContext(ctx).Where(q.ID.Eq(r.ID))
		if tx.Dialector.Name() != "sqlite" {
			scope = scope.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		r, err = scope.First()
		if err != nil {
			return err
		}
		if terminal(r.State) || r.JobID == nil || *r.JobID != j.ID {
			return nil
		}
		if !m.config.Email.Enabled {
			return m.deliveryTerminalOn(ctx, tx, r, "SUPPRESSED", "CHANNEL_DISABLED", "")
		}
		if r.RecipientPrincipalID != nil {
			_, active, err := m.principal.ActiveHumanByID(ctx, tx, *r.RecipientPrincipalID)
			if err != nil {
				return err
			}
			member, err := m.recipientScopeOn(ctx, tx, r.TenantID, *r.RecipientPrincipalID, "")
			if err != nil {
				return err
			}
			if !active || !member {
				return m.deliveryTerminalOn(ctx, tx, r, "SUPPRESSED", "RECIPIENT_INACTIVE", "")
			}
			if r.NotificationID != nil {
				n := query.BindNotifications(tx, m.names)
				notification, err := n.WithContext(ctx).Where(n.ID.Eq(*r.NotificationID)).First()
				if err != nil {
					return err
				}
				visible, err := m.visibleOn(ctx, tx, notification, *r.RecipientPrincipalID)
				if err != nil {
					return err
				}
				if !visible {
					return m.deliveryTerminalOn(ctx, tx, r, "SUPPRESSED", "RECIPIENT_INACTIVE", "")
				}
				if notification.Type == "machine.offline" && notification.Permission == "" {
					currentMachineNotice = notification
					var params map[string]string
					if json.Unmarshal([]byte(notification.Params), &params) != nil {
						return ErrInvalid
					}
					at, err := time.Parse(time.RFC3339Nano, params["offline_at"])
					if err != nil {
						return ErrInvalid
					}
					if !m.now().After(at.Add(15 * time.Minute)) {
						return jobs.RetryAfter(at.Add(15*time.Minute+time.Millisecond).Sub(m.now()), ErrConflict)
					}
					next, err := m.machineEmailNextOn(ctx, tx, r)
					if err != nil {
						return err
					}
					if next.After(m.now()) {
						return jobs.RetryAfter(next.Sub(m.now()), ErrConflict)
					}
				}
				enabled, runAt, err := m.externalPreferenceAtOn(ctx, tx, *r.RecipientPrincipalID, r.TenantID, notification.Type, Severity(notification.Severity), r.CreatedAt)
				if err != nil {
					return err
				}
				if !enabled {
					return m.deliveryTerminalOn(ctx, tx, r, "SUPPRESSED", "PREFERENCE", "")
				}
				if runAt.After(m.now()) {
					result, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: r.TenantID, Kind: Name + ".deliver", IdempotencyKey: "delivery:" + r.ID.String() + ":defer:" + fmt.Sprint(runAt.UnixMilli()), ConcurrencyKey: "notification:address:" + r.RecipientAddressHash, Payload: map[string]any{"delivery_id": r.ID}, RunAt: runAt})
					if err != nil {
						return err
					}
					_, err = q.WithContext(ctx).Where(q.ID.Eq(r.ID)).Updates(map[string]any{"job_id": result.JobID, "state": "PENDING", "updated_at": m.now()})
					return err
				}
			}
		}
		if m.keyring == nil {
			return ErrUnavailable
		}
		associated := []byte(r.TenantID.String() + ":" + r.ID.String())
		encoded, err := m.keyring.Open("payload", associated, r.PayloadSealed)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(encoded, &payload); err != nil {
			return err
		}
		if len(payload.To) == 0 {
			return ErrInvalid
		}
		if currentMachineNotice != nil {
			var params map[string]string
			if json.Unmarshal([]byte(currentMachineNotice.Params), &params) != nil {
				return ErrInvalid
			}
			addresses := make([]mail.Address, 0, len(payload.To))
			for _, address := range payload.To {
				addresses = append(addresses, mail.Address{Address: address})
			}
			fresh, err := m.emailPayload(ctx, EmailMessage{TenantID: r.TenantID, To: addresses, TemplateKey: currentMachineNotice.TemplateKey, Params: params})
			if err != nil {
				return err
			}
			payload.Subject, payload.Text, payload.HTML = fresh.Subject, fresh.Text, fresh.HTML
		}
		payload, batch, err = m.digestOn(ctx, tx, r, payload, j.ID)
		if err != nil {
			return err
		}
		r.Attempts++
		r.State = "SENDING"
		r.UpdatedAt = m.now()
		_, err = q.WithContext(ctx).Where(q.ID.Eq(r.ID)).Updates(map[string]any{"attempts": r.Attempts, "state": r.State, "updated_at": r.UpdatedAt})
		if err != nil {
			return err
		}
		row = r
		send = true
		return nil
	})
	if err != nil || !send {
		return err
	}
	transportErr := m.sendSMTP(ctx, payload, row.MessageID)
	permanent := false
	code := ""
	if transportErr != nil {
		code = "SMTP_TEMPORARY"
		var smtpErr *textproto.Error
		if errors.As(transportErr, &smtpErr) {
			code = fmt.Sprintf("SMTP_%d", smtpErr.Code)
			permanent = smtpErr.Code >= 500
		}
		maxAttempts := j.MaxAttempts
		if maxAttempts < 1 {
			maxAttempts = 8
		}
		if row.Attempts >= maxAttempts {
			permanent = true
		}
	}
	finalCtx := context.WithoutCancel(ctx)
	err = db.WithContext(finalCtx).Transaction(func(tx *gorm.DB) error {
		q := query.BindDeliveries(tx, m.names)
		for _, member := range batch {
			r, err := q.WithContext(finalCtx).Where(q.ID.Eq(member.ID), q.TenantID.Eq(j.TenantID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
			if err != nil {
				return err
			}
			if terminal(r.State) || r.JobID == nil || *r.JobID != j.ID || r.Attempts != row.Attempts {
				continue
			}
			if transportErr == nil {
				if err := m.deliveryTerminalOn(finalCtx, tx, r, "SENT", "", ""); err != nil {
					return err
				}
				continue
			}
			if permanent {
				if err := m.deliveryTerminalOn(finalCtx, tx, r, "FAILED", "", code); err != nil {
					return err
				}
				continue
			}
			_, err = q.WithContext(finalCtx).Where(q.ID.Eq(r.ID)).Updates(map[string]any{"state": "PENDING", "last_error": code, "updated_at": m.now()})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if transportErr != nil {
		if permanent {
			return jobs.Permanent(errors.New(code))
		}
		return errors.New(code)
	}
	return nil
}
