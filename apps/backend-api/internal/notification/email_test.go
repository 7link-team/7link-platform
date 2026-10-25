package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryCreationStorageFailureRollsBackJobsAndOutbox(t *testing.T) {
	for _, mode := range []string{"rate_read", "tenant_read", "create", "job_update"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			c, _ := smtpFixture(t)
			enableEmail(t, f, c)
			reads := 0
			if mode == "rate_read" || mode == "tenant_read" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("email_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Deliveries.String() {
						reads++
						if mode == "rate_read" || reads == 2 {
							tx.AddError(ErrUnavailable)
						}
					}
				}))
			} else if mode == "create" {
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("email_create", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Deliveries.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			} else {
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("email_update", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Deliveries.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.createDeliveryOn(t.Context(), tx, emailSample(), nil, nil, "", time.Time{})
				return err
			}))
			if mode == "rate_read" || mode == "tenant_read" {
				require.NoError(t, f.db.Callback().Query().Remove("email_read"))
			} else if mode == "create" {
				require.NoError(t, f.db.Callback().Create().Remove("email_create"))
			} else {
				require.NoError(t, f.db.Callback().Update().Remove("email_update"))
			}
			n, err := query.BindDeliveries(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
			var count int64
			require.NoError(t, f.db.Table("test_notification_jobs").Count(&count).Error)
			require.Zero(t, count)
		})
	}
	f := newFixture(t)
	msg := emailSample()
	msg.TenantID = -1
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.createDeliveryOn(t.Context(), tx, msg, nil, nil, "", time.Time{})
		return err
	}), ErrInvalid)
	msg = emailSample()
	msg.To[0].Name = "Reader\r\nBcc: other@example.test"
	_, err := f.m.emailPayload(t.Context(), msg)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestEmailTemplatesAndHeaders(t *testing.T) {
	f := newFixture(t)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		msg := emailSample()
		msg.Locale = locale
		msg.Params["type"] = "<script>alert(1)</script>"
		msg.ReplyTo = &mail.Address{Name: "Support", Address: "support@example.test"}
		msg.InReplyTo = "<prior@example.test>"
		msg.References = []string{"<prior@example.test>"}
		msg.AutoSubmitted = "auto-generated"
		msg.ListUnsubscribe = "<mailto:unsubscribe@example.test>"
		p, err := f.m.emailPayload(t.Context(), msg)
		require.NoError(t, err)
		require.NotEmpty(t, p.Subject)
		require.NotEmpty(t, p.Text)
		require.NotContains(t, p.HTML, "<script>")
		require.Contains(t, p.HTML, "&lt;script&gt;")
		require.Equal(t, msg.InReplyTo, p.Headers["In-Reply-To"])
	}
	for _, mutate := range []func(*EmailMessage){
		func(m *EmailMessage) { m.To = nil }, func(m *EmailMessage) { m.To[0].Address = "bad" }, func(m *EmailMessage) { m.FromName = "inject\r\nBcc: victim@example.test" }, func(m *EmailMessage) { m.ReplyTo = &mail.Address{Address: "bad"} }, func(m *EmailMessage) { m.InReplyTo = "invalid" }, func(m *EmailMessage) { m.References = []string{"invalid"} }, func(m *EmailMessage) { m.AutoSubmitted = "anything" }, func(m *EmailMessage) { m.ListUnsubscribe = "bad\nheader" }, func(m *EmailMessage) { m.TemplateKey = "missing" }, func(m *EmailMessage) { m.Params = nil },
	} {
		msg := emailSample()
		mutate(&msg)
		_, err := f.m.emailPayload(t.Context(), msg)
		require.Error(t, err)
	}
}

func TestEmailPreferenceDeferralAndCriticalBypass(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	p := PreferenceView{InApp: true, Email: "DAILY_DIGEST", Timezone: "UTC", DigestHour: 13, QuietEnabled: true, QuietStartMin: 11 * 60, QuietEndMin: 14 * 60}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", p, 0); return err }))
	enabled, at, err := f.m.externalPreferenceOn(t.Context(), f.db, 11, 7, "machine.offline", Action)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, now.Add(2*time.Hour), at)
	enabled, at, err = f.m.externalPreferenceOn(t.Context(), f.db, 11, 7, "machine.offline", Urgent)
	require.NoError(t, err)
	require.True(t, enabled)
	require.True(t, at.IsZero())
	f.m.now = func() time.Time { return now.Add(3 * time.Hour) }
	_, at, err = f.m.externalPreferenceAtOn(t.Context(), f.db, 11, 7, "machine.offline", Action, now)
	require.NoError(t, err)
	require.False(t, at.After(f.m.now()), "a due digest must not move to tomorrow on retry")
}

func TestNotificationEmailAndSuppression(t *testing.T) {
	f := newFixture(t)
	c, _ := smtpFixture(t)
	enableEmail(t, f, c)
	f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{Email: "reader@example.test"}, true, nil
	}
	id := f.notify(t, sample())[0]
	q := query.BindDeliveries(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.NotificationID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", r.State)
	f.m.config.Email.PerAddressHourly = 1
	r = createTestDelivery(t, f)
	require.Equal(t, "SUPPRESSED", r.State)
	require.Equal(t, "RATE_LIMIT", r.SuppressReason)
	msg := emailSample()
	msg.MessageID = "<external@outside.test>"
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.SendEmail(t.Context(), tx, msg); return err }))
	f.m.config.Email.Enabled = false
	r = createTestDelivery(t, f)
	require.Equal(t, "CHANNEL_DISABLED", r.SuppressReason)
}

func TestNotificationChannelIntersectionAndUrgentQuota(t *testing.T) {
	f := newFixture(t)
	c, _ := smtpFixture(t)
	enableEmail(t, f, c)
	f.m.config.Email.PerAddressHourly = 1
	f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{Email: "reader@example.test"}, true, nil
	}
	// Normal mail exhausts its own bucket without consuming urgent capacity.
	createTestDelivery(t, f)
	for i := 0; i < 3; i++ {
		n := sample()
		n.DedupeKey = fmt.Sprintf("urgent:%d", i)
		n.Severity = Urgent
		f.notify(t, n)
	}
	q := query.BindDeliveries(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	require.Equal(t, "PENDING", rows[1].State)
	require.Equal(t, "PENDING", rows[2].State)
	require.Equal(t, "RATE_LIMIT", rows[3].SuppressReason)
	for _, channels := range [][]ChannelName{nil, {}, {WebPush}} {
		n := sample()
		n.DedupeKey = fmt.Sprintf("info:%v", channels)
		n.Type = "work_item.accepted" // This catalog type defaults to in-app only.
		n.Severity = Info
		n.Channels = channels
		f.notify(t, n)
	}
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, count, "preferences cannot enable a channel the caller did not request")
}

func TestDirectEmailRecipientIsolation(t *testing.T) {
	f := newFixture(t)
	c, _ := smtpFixture(t)
	enableEmail(t, f, c)
	msg := emailSample()
	msg.To = append(msg.To, mail.Address{Address: "second@example.test"}, msg.To[0])
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.SendEmail(t.Context(), tx, msg); return err }))
	q := query.BindDeliveries(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotEqual(t, rows[0].RecipientAddressHash, rows[1].RecipientAddressHash)
	for _, row := range rows {
		encoded, err := f.m.keyring.Open("payload", []byte(row.TenantID.String()+":"+row.ID.String()), row.PayloadSealed)
		require.NoError(t, err)
		var p emailPayload
		require.NoError(t, json.Unmarshal(encoded, &p))
		require.Len(t, p.To, 1)
	}
}

func TestEmailCreationFailuresRollBackAllEffects(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*fixture)
	}{
		{"ids unavailable", func(f *fixture) { f.m.ids = nil }},
		{"ids error", func(f *fixture) { f.ids.err = errors.New("ids unavailable") }},
		{"ids invalid", func(f *fixture) { f.ids.zero = true }},
		{"encryption unavailable", func(f *fixture) { f.m.keyring = nil }},
		{"queue unavailable", func(f *fixture) { f.m.queue = nil }},
		{"queue rejected", func(f *fixture) { f.m.queue.(*fixtureQueue).err = errors.New("queue full") }},
		{"admission missing", func(f *fixture) { f.m.admission = nil }},
		{"rate guard missing", func(f *fixture) { f.m.guard = nil }},
		{"admission rejected", func(f *fixture) {
			f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("admission failed") }
		}},
		{"rate guard rejected", func(f *fixture) {
			f.m.guard = func(context.Context, *gorm.DB, string) error { return errors.New("guard failed") }
		}},
		{"terminal event unavailable", func(f *fixture) { f.m.config.Email.Enabled = false; f.m.publisher = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			c, _ := smtpFixture(t)
			enableEmail(t, f, c)
			test.change(f)
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.SendEmail(t.Context(), tx, emailSample()); return err }))
			for _, table := range []string{f.m.names.Deliveries.String(), "test_notification_jobs", "test_notification_events"} {
				var count int64
				require.NoError(t, f.db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
	f := newFixture(t)
	c, _ := smtpFixture(t)
	enableEmail(t, f, c)
	_, err := f.m.SendEmail(t.Context(), f.db, emailSample())
	require.ErrorIs(t, err, ErrInvalid)
	msg := emailSample()
	msg.Params["type"] = strings.Repeat("x", 128<<10)
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.SendEmail(t.Context(), tx, msg); return err }))
}
