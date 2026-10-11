package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"7link.com/platform/internal/iam/audit"
)

// Header names are deliberately generic. A receiver reads them from whichever
// deployment sends them, and a product's name in a protocol header would make
// one build serve one deployment.
const (
	HeaderSignature = "X-Webhook-Signature"
	HeaderTimestamp = "X-Webhook-Timestamp"
	HeaderEventID   = "X-Webhook-Event-Id"
	HeaderDelivery  = "Idempotency-Key"
)

// maxResponseBody bounds what a receiver can make this deployment read back. The
// body is only used to explain a failure, so there is no reason to accept more.
const maxResponseBody = 4 << 10

// payload is what a receiver is sent. It is audit's projection verbatim, which is
// how the decision about what may leave stays in the module that owns the data
// rather than being re-made here.
type payload struct {
	Delivery string            `json:"delivery_id"`
	Attempt  int               `json:"attempt"`
	Event    audit.StreamEvent `json:"event"`
}

// sign returns the hex HMAC-SHA256 over the timestamp and body. The timestamp is
// inside the signed content so a receiver can refuse a replay of a body it has
// seen before without the signature still matching.
func sign(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// post sends one delivery. A receiver that answers outside 2xx has not accepted
// it, and the status is reported back so an operator can see why.
func (s *Service) post(ctx context.Context, row deliveryRow, secret []byte, event audit.StreamEvent, url string) error {
	body, err := json.Marshal(payload{Delivery: row.ID.String(), Attempt: row.Attempts, Event: event})
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	timestamp := s.now().UTC().Unix()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderDelivery, row.ID.String())
	request.Header.Set(HeaderEventID, row.EventID.String())
	request.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	if len(secret) > 0 {
		request.Header.Set(HeaderSignature, sign(secret, timestamp, body))
	}
	if s.userAgent != "" {
		request.Header.Set("User-Agent", s.userAgent)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeliveryFailed, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 == 2 {
		return nil
	}
	reason, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	return fmt.Errorf("%w: status %d: %s", ErrDeliveryFailed, response.StatusCode, bytes.TrimSpace(reason))
}

// FanOut offers each active subscription the events it has not been offered yet.
// It is separate from delivery so a receiver that is down cannot stop the chain
// from being followed, and so catching up is a read plus one insert rather than
// a request per event.
func (s *Service) FanOut(ctx context.Context) error {
	subscriptions, err := s.repo.activeSubscriptions(ctx, s.cfg.FanOutSubscriptions)
	if err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		if err := s.fanOutOne(ctx, subscription); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) fanOutOne(ctx context.Context, subscription subscriptionRow) error {
	events, err := s.readStream(ctx, subscription.TenantID, subscription.AfterSequence, s.cfg.FanOutBatch)
	if err != nil {
		return fmt.Errorf("read event stream for subscription %s: %w", subscription.ID, err)
	}
	if len(events) == 0 {
		return nil
	}
	now := s.now().UTC()
	rows := make([]deliveryRow, 0, len(events))
	through := subscription.AfterSequence
	for _, event := range events {
		through = event.Sequence
		if !matchesEventType(subscription.EventTypes, event.EventType) {
			// Skipped types still move the cursor: the subscription has been
			// offered them, and it does not want them.
			continue
		}
		id, err := s.nextID()
		if err != nil {
			return err
		}
		rows = append(rows, deliveryRow{
			ID: id, SubscriptionID: subscription.ID, TenantID: subscription.TenantID,
			EventID: event.ID, EventSequence: event.Sequence, EventType: event.EventType,
			Status: DeliveryPending, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		})
	}
	return s.repo.enqueue(ctx, subscription, rows, through, now)
}

// DeliverDue sends what is owed and is due. One failure does not stop the pass:
// a receiver that is down must not delay every other tenant's deliveries.
func (s *Service) DeliverDue(ctx context.Context) error {
	now := s.now().UTC()
	rows, err := s.repo.dueDeliveries(ctx, now, s.cfg.DeliveryBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.deliverOne(ctx, row); err != nil {
			s.failures.Add(1)
		}
	}
	return nil
}

func (s *Service) deliverOne(ctx context.Context, row deliveryRow) error {
	claimed, err := s.repo.claimDelivery(ctx, row.ID, row.Attempts, s.now().UTC())
	if err != nil || !claimed {
		return err
	}
	row.Attempts++

	subscription, err := s.repo.getSubscription(ctx, row.TenantID, row.SubscriptionID)
	if err != nil {
		return s.recordFailure(ctx, row, err)
	}
	secret, err := s.openSecret(subscription)
	if err != nil {
		return s.recordFailure(ctx, row, err)
	}
	// The event is read back rather than stored on the delivery, so a payload
	// always reflects what audit is willing to hand out now.
	events, err := s.readStream(ctx, row.TenantID, row.EventSequence-1, 1)
	if err != nil {
		return s.recordFailure(ctx, row, err)
	}
	if len(events) == 0 || events[0].ID != row.EventID {
		// The event is gone, which happens once its retention window closes.
		// There is nothing left to deliver and no point retrying.
		return s.terminate(ctx, row, "the event is no longer retained")
	}
	if err := s.post(ctx, row, secret, events[0], subscription.URL); err != nil {
		return s.recordFailure(ctx, row, err)
	}
	if err := s.repo.markDelivered(ctx, row.ID, s.now().UTC()); err != nil {
		return err
	}
	s.delivered.Add(1)
	return nil
}

// recordFailure schedules a retry, or gives up once the attempts are spent.
func (s *Service) recordFailure(ctx context.Context, row deliveryRow, cause error) error {
	now := s.now().UTC()
	status := DeliveryPending
	if row.Attempts >= s.cfg.MaxAttempts {
		status = DeliveryFailed
	}
	backoff := time.Duration(1<<min(row.Attempts, 8)) * s.cfg.RetryBase
	if err := s.repo.markAttemptFailed(ctx, row.ID, status, cause.Error(), now.Add(backoff), now); err != nil {
		return err
	}
	return cause
}

// terminate stops retrying something that cannot succeed.
func (s *Service) terminate(ctx context.Context, row deliveryRow, reason string) error {
	now := s.now().UTC()
	return s.repo.markAttemptFailed(ctx, row.ID, DeliveryFailed, reason, now, now)
}
