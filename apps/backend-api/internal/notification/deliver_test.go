package notification

import (
	"7link.com/platform/internal/iam/identity"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func enableEmail(t *testing.T, f *fixture, c EmailConfig) {
	t.Helper()
	f.m.config.Email = c
	f.m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	require.NoError(t, f.m.configureEmail())
}
func emailSample() EmailMessage {
	return EmailMessage{TenantID: 7, To: []mail.Address{{Address: "reader@example.test"}}, TemplateKey: "notification.event", Params: map[string]string{"subject_id": "91", "type": "machine.offline"}, SourceRef: "test:91"}
}
func createTestDelivery(t *testing.T, f *fixture) *model.Delivery {
	t.Helper()
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = f.m.SendEmail(t.Context(), tx, emailSample())
		return err
	}))
	q := query.BindDeliveries(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	return row
}
func deliveryJob(t *testing.T, r *model.Delivery) jobs.Job {
	t.Helper()
	p, err := json.Marshal(map[string]any{"delivery_id": r.ID})
	require.NoError(t, err)
	return jobs.Job{ID: *r.JobID, TenantID: r.TenantID, Payload: p, MaxAttempts: 3}
}
func TestDeliveryTemporaryRetryAndIdempotence(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 451, 250)
	enableEmail(t, f, c)
	r := createTestDelivery(t, f)
	require.NotContains(t, r.PayloadSealed, "reader@")
	require.NotContains(t, r.RecipientAddressSealed, "reader@")
	j := deliveryJob(t, r)
	require.Error(t, f.m.deliverJob(t.Context(), j))
	q := query.BindDeliveries(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", r.State)
	require.Equal(t, 1, r.Attempts)
	require.NoError(t, f.m.deliverJob(t.Context(), j))
	require.NoError(t, f.m.deliverJob(t.Context(), j))
	require.Len(t, messages(), 2)
	r, err = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "SENT", r.State)
	require.Equal(t, 2, r.Attempts)
	require.NotNil(t, r.SentAt)
	for _, message := range messages() {
		require.Contains(t, message, "Message-ID: "+r.MessageID)
	}
	var count int64
	require.NoError(t, f.db.Table("test_notification_events").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestDeliveryPermanentFailure(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 550)
	enableEmail(t, f, c)
	r := createTestDelivery(t, f)
	j := deliveryJob(t, r)
	require.Error(t, f.m.deliverJob(t.Context(), j))
	require.NoError(t, f.m.deliverJob(t.Context(), j))
	require.Len(t, messages(), 1)
	q := query.BindDeliveries(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "FAILED", r.State)
	require.Equal(t, "SMTP_550", r.LastError)
}
func TestDeliveryEnqueueRollback(t *testing.T) {
	f := newFixture(t)
	c, _ := smtpFixture(t)
	enableEmail(t, f, c)
	abort := errors.New("abort")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.SendEmail(t.Context(), tx, emailSample())
		if err != nil {
			return err
		}
		return abort
	}), abort)
	for _, table := range []string{f.m.names.Deliveries.String(), "test_notification_jobs", "test_notification_events"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestDeliveryAdmissionAndRevocation(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*fixture, *model.Delivery)
		state     string
		wantError bool
	}{
		{"disabled", func(f *fixture, _ *model.Delivery) { f.m.config.Email.Enabled = false }, "SUPPRESSED", false},
		{"inactive", func(f *fixture, _ *model.Delivery) { f.human = false }, "SUPPRESSED", false},
		{"membership revoked", func(f *fixture, _ *model.Delivery) { f.members.active = false }, "SUPPRESSED", false},
		{"permission revoked", func(f *fixture, _ *model.Delivery) { f.allow = false }, "SUPPRESSED", false},
		{"missing key", func(f *fixture, _ *model.Delivery) { f.m.keyring = nil }, "PENDING", true},
		{"admission unavailable", func(f *fixture, _ *model.Delivery) { f.m.admission = nil }, "PENDING", true},
		{"admission denied", func(f *fixture, _ *model.Delivery) {
			f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("queue full") }
		}, "PENDING", true},
		{"ciphertext modified", func(f *fixture, r *model.Delivery) {
			require.NoError(t, f.db.Table(f.m.names.Deliveries.String()).Where("id = ?", r.ID).Update("payload_sealed", "tampered").Error)
		}, "PENDING", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			c, messages := smtpFixture(t)
			enableEmail(t, f, c)
			f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{Email: "reader@example.test"}, f.human, f.err
			}
			notice := sample()
			notice.Type = "test.delivery" // Exercise transport admission immediately, independently of catalog digest defaults.
			id := f.notify(t, notice)[0]
			q := query.BindDeliveries(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.NotificationID.Eq(id)).First()
			require.NoError(t, err)
			test.change(f, r)
			err = f.m.deliverJob(t.Context(), deliveryJob(t, r))
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, messages())
			r, err = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, err)
			require.Equal(t, test.state, r.State)
		})
	}
}

func TestDeliveryAttemptExhaustionAndStaleJob(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 451)
	enableEmail(t, f, c)
	r := createTestDelivery(t, f)
	j := deliveryJob(t, r)
	stale := j
	stale.ID++
	require.NoError(t, f.m.deliverJob(t.Context(), stale))
	require.Empty(t, messages())
	j.MaxAttempts = 1
	require.Error(t, f.m.deliverJob(t.Context(), j))
	q := query.BindDeliveries(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "FAILED", r.State)
	require.Error(t, f.m.deliverJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`invalid`)}))
	j.Payload = []byte(`{"delivery_id":"999"}`)
	require.Error(t, f.m.deliverJob(t.Context(), j))
}

func TestQueuedEmailRechecksChangedPreferencesAndQuietHours(t *testing.T) {
	for _, mode := range []string{"OFF", "QUIET", "QUIET_QUEUE_FAILURE"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			c, messages := smtpFixture(t, 250)
			enableEmail(t, f, c)
			f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{Email: "reader@example.test"}, true, nil
			}
			input := sample()
			input.Type = "test.delivery" // Reach the payload boundary without a catalog digest deferral.
			notice := f.notify(t, input)[0]
			q := query.BindDeliveries(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.NotificationID.Eq(notice)).First()
			require.NoError(t, err)
			p := PreferenceView{InApp: true, Email: "IMMEDIATE", Timezone: "UTC", QuietEnabled: mode != "OFF", QuietStartMin: 11 * 60, QuietEndMin: 13 * 60}
			if mode == "OFF" {
				p.Email = "OFF"
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", p, 0); return err }))
			if mode == "QUIET_QUEUE_FAILURE" {
				f.m.queue.(*fixtureQueue).err = errors.New("queue unavailable")
			}
			err = f.m.deliverJob(t.Context(), deliveryJob(t, r))
			if mode == "QUIET_QUEUE_FAILURE" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, messages())
			updated, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, err)
			if mode == "OFF" {
				require.Equal(t, "SUPPRESSED", updated.State)
				require.Equal(t, "PREFERENCE", updated.SuppressReason)
				return
			}
			require.Equal(t, "PENDING", updated.State)
			require.Zero(t, updated.Attempts)
			if mode == "QUIET_QUEUE_FAILURE" {
				require.Equal(t, r.JobID, updated.JobID)
				return
			}
			require.NotEqual(t, r.JobID, updated.JobID)
			start := f.m.now()
			f.m.now = func() time.Time { return start.Add(time.Hour) }
			require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, updated)))
			require.Len(t, messages(), 1)
		})
	}
}
func TestDeliveryOwnerFailuresAndCorruptPayloadNeverReachSMTP(t *testing.T) {
	for _, mode := range []string{"lock", "principal", "membership", "missing_notice", "invalid_json", "empty_address"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			c, messages := smtpFixture(t)
			enableEmail(t, f, c)
			f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{Email: "reader@example.test"}, true, nil
			}
			input := sample()
			input.Type = "test.delivery" // Reach the payload boundary without a catalog digest deferral.
			notice := f.notify(t, input)[0]
			q := query.BindDeliveries(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.NotificationID.Eq(notice)).First()
			require.NoError(t, err)
			sentinel := errors.New("owner storage unavailable")
			switch mode {
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return sentinel }
			case "principal":
				f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, sentinel
				}
			case "membership":
				f.members.err = sentinel
			case "missing_notice":
				n := query.BindNotifications(f.db, f.m.names)
				_, err = n.WithContext(t.Context()).Where(n.ID.Eq(notice)).Delete()
				require.NoError(t, err)
			case "invalid_json", "empty_address":
				data := []byte("invalid JSON")
				if mode == "empty_address" {
					data = []byte(`{"To":[]}`)
				}
				sealed, err := f.m.keyring.Seal("payload", []byte(r.TenantID.String()+":"+r.ID.String()), data)
				require.NoError(t, err)
				_, err = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).Update(q.PayloadSealed, sealed)
				require.NoError(t, err)
			}
			require.Error(t, f.m.deliverJob(t.Context(), deliveryJob(t, r)))
			require.Empty(t, messages())
			updated, err := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", updated.State)
			require.Zero(t, updated.Attempts)
		})
	}
}
