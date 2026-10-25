package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"bytes"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	htmltemplate "html/template"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
)

type ChannelName string

const (
	Email   ChannelName = "EMAIL"
	WebPush ChannelName = "WEB_PUSH"
)

type EmailMessage struct {
	severity                                                       Severity
	TenantID                                                       guid.ID
	To                                                             []mail.Address
	FromName                                                       string
	ReplyTo                                                        *mail.Address
	MessageID, InReplyTo                                           string
	References                                                     []string
	AutoSubmitted, ListUnsubscribe, TemplateKey, Locale, SourceRef string
	Params                                                         map[string]string
}
type emailPayload struct {
	To                            []string `json:"to"`
	FromName, Subject, Text, HTML string
	Headers                       map[string]string
}

var messageIDPattern = regexp.MustCompile(`^<[^<>@\s]+@([A-Za-z0-9.-]+)>$`)

func validMessageID(id string) bool { return len(id) <= 998 && messageIDPattern.MatchString(id) }
func (m *Module) emailPayload(ctx context.Context, msg EmailMessage) (emailPayload, error) {
	msg.Params = notificationTemplateParams(msg.TemplateKey, msg.Params)
	if len(msg.To) < 1 || len(msg.To) > 20 || len(msg.SourceRef) > 160 || len(msg.References) > 20 || msg.TemplateKey == "" || len(msg.TemplateKey) > 160 {
		return emailPayload{}, ErrInvalid
	}
	p := emailPayload{FromName: msg.FromName, Headers: map[string]string{}}
	if p.FromName == "" {
		p.FromName = m.config.Email.FromName
	}
	if strings.ContainsAny(p.FromName, "\r\n") {
		return p, ErrInvalid
	}
	for _, to := range msg.To {
		if strings.ContainsAny(to.Address+to.Name, "\r\n") {
			return p, ErrInvalid
		}
		a, err := mail.ParseAddress(to.Address)
		if err != nil || a.Address != to.Address {
			return p, ErrInvalid
		}
		p.To = append(p.To, a.Address)
	}
	if msg.ReplyTo != nil {
		a, err := mail.ParseAddress(msg.ReplyTo.Address)
		if err != nil || a.Address != msg.ReplyTo.Address || strings.ContainsAny(msg.ReplyTo.Address+msg.ReplyTo.Name, "\r\n") {
			return p, ErrInvalid
		}
		p.Headers["Reply-To"] = msg.ReplyTo.String()
	}
	if msg.InReplyTo != "" {
		if !validMessageID(msg.InReplyTo) {
			return p, ErrInvalid
		}
		p.Headers["In-Reply-To"] = msg.InReplyTo
	}
	for _, id := range msg.References {
		if !validMessageID(id) {
			return p, ErrInvalid
		}
	}
	if len(msg.References) > 0 {
		p.Headers["References"] = strings.Join(msg.References, " ")
	}
	if msg.AutoSubmitted != "" {
		if msg.AutoSubmitted != "auto-generated" && msg.AutoSubmitted != "auto-replied" {
			return p, ErrInvalid
		}
		p.Headers["Auto-Submitted"] = msg.AutoSubmitted
	}
	if msg.ListUnsubscribe != "" {
		if strings.ContainsAny(msg.ListUnsubscribe, "\r\n") || len(msg.ListUnsubscribe) > 998 {
			return p, ErrInvalid
		}
		p.Headers["List-Unsubscribe"] = msg.ListUnsubscribe
	}
	if msg.Locale == "" {
		msg.Locale = m.config.DefaultLocale
	}
	ctx = i18n.WithLocale(ctx, msg.Locale)
	var err error
	if p.Subject, err = render(ctx, msg.TemplateKey+"_email_subject", msg.Params); err != nil {
		return p, err
	}
	if strings.ContainsAny(p.Subject, "\r\n") {
		return p, ErrInvalid
	}
	if p.Text, err = render(ctx, msg.TemplateKey+"_email_text", msg.Params); err != nil {
		return p, err
	}
	text := i18n.TContext(ctx, msg.TemplateKey+"_email_html")
	if text == msg.TemplateKey+"_email_html" {
		return p, ErrInvalid
	}
	t, err := htmltemplate.New("email").Option("missingkey=error").Parse(text)
	if err != nil {
		return p, err
	}
	var html bytes.Buffer
	if err = t.Execute(&html, msg.Params); err != nil {
		return p, err
	}
	p.HTML = html.String()
	return p, nil
}
func (m *Module) publishDeliveryStateOn(ctx context.Context, tx *gorm.DB, r *model.Delivery) error {
	if m.publisher == nil {
		return ErrUnavailable
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: "notification.delivery_state_changed", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "notification_delivery", ID: r.ID}, Data: map[string]any{"delivery_id": r.ID, "channel": r.Channel, "state": r.State, "source_ref": r.SourceRef, "message_id": r.MessageID, "error_code": r.LastError}})
	return err
}
func (m *Module) createDeliveryOn(ctx context.Context, tx *gorm.DB, msg EmailMessage, notification, principal *guid.ID, reason string, runAt time.Time) (guid.ID, error) {
	if !validTransaction(tx) || msg.TenantID < 0 {
		return 0, ErrInvalid
	}
	if m.ids == nil {
		return 0, ErrUnavailable
	}
	payload, err := m.emailPayload(ctx, msg)
	if err != nil {
		return 0, err
	}
	if msg.MessageID != "" {
		parts := messageIDPattern.FindStringSubmatch(msg.MessageID)
		if len(parts) != 2 || !strings.EqualFold(parts[1], m.config.Email.MessageIDDomain) {
			return 0, ErrInvalid
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	now := m.now()
	r := model.Delivery{ID: guid.ID(id), TenantID: msg.TenantID, NotificationID: notification, Channel: string(Email), RecipientPrincipalID: principal, MessageID: msg.MessageID, SourceRef: msg.SourceRef, State: "PENDING", CreatedAt: now, UpdatedAt: now}
	if r.MessageID == "" && validDomain(m.config.Email.MessageIDDomain) {
		r.MessageID = "<" + r.ID.String() + "@" + m.config.Email.MessageIDDomain + ">"
	}
	if !m.config.Email.Enabled {
		reason = "CHANNEL_DISABLED"
	}
	if reason != "" {
		r.State = "SUPPRESSED"
		r.SuppressReason = reason
	} else {
		if m.keyring == nil || m.queue == nil {
			return 0, ErrUnavailable
		}
		associated := []byte(r.TenantID.String() + ":" + r.ID.String())
		addresses, err := json.Marshal(payload.To)
		if err != nil {
			return 0, err
		}
		bucket := "normal:"
		if msg.severity == Urgent {
			bucket = "urgent:"
		}
		r.RecipientAddressHash = m.keyring.MAC("address", []byte(bucket+strings.ToLower(strings.Join(payload.To, ","))))
		if r.RecipientAddressSealed, err = m.keyring.Seal("address", associated, addresses); err != nil {
			return 0, err
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		if len(encoded) > 128<<10 {
			return 0, ErrInvalid
		}
		if r.PayloadSealed, err = m.keyring.Seal("payload", associated, encoded); err != nil {
			return 0, err
		}
		q := query.BindDeliveries(tx, m.names)
		since := now.Add(-time.Hour)
		limit := m.config.Email.PerAddressHourly
		count, err := q.WithContext(ctx).Where(q.RecipientAddressHash.Eq(r.RecipientAddressHash), q.Channel.Eq(string(Email)), q.CreatedAt.Gte(since), q.State.Neq("SUPPRESSED")).Count()
		if err != nil {
			return 0, err
		}
		tenantCount, err := q.WithContext(ctx).Where(q.TenantID.Eq(msg.TenantID), q.NotificationID.IsNull(), q.CreatedAt.Gte(since), q.State.Neq("SUPPRESSED")).Count()
		if err != nil {
			return 0, err
		}
		atLimit := count >= int64(limit)
		if msg.severity == Urgent {
			atLimit = count/2 >= int64(limit)
		}
		if atLimit || notification == nil && tenantCount >= int64(m.config.Email.PerTenantHourly) {
			r.State = "SUPPRESSED"
			r.SuppressReason = "RATE_LIMIT"
		}
	}
	if err = query.BindDeliveries(tx, m.names).WithContext(ctx).Create(&r); err != nil {
		return 0, err
	}
	if r.State == "SUPPRESSED" {
		return r.ID, m.publishDeliveryStateOn(ctx, tx, &r)
	}
	result, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: r.TenantID, Kind: Name + ".deliver", IdempotencyKey: "delivery:" + r.ID.String(), ConcurrencyKey: "notification:address:" + r.RecipientAddressHash, Payload: map[string]any{"delivery_id": r.ID}, RunAt: runAt})
	if err != nil {
		return 0, err
	}
	r.JobID = &result.JobID
	_, err = query.BindDeliveries(tx, m.names).WithContext(ctx).Where(query.BindDeliveries(tx, m.names).ID.Eq(r.ID)).Update(query.BindDeliveries(tx, m.names).JobID, result.JobID)
	return r.ID, err
}
func (m *Module) SendEmail(ctx context.Context, tx *gorm.DB, msg EmailMessage) (guid.ID, error) {
	if !validTransaction(tx) || msg.TenantID <= 0 {
		return 0, ErrInvalid
	}
	if m.config.Email.Enabled {
		if m.admission == nil || m.guard == nil {
			return 0, ErrUnavailable
		}
		if err := m.admission(ctx, tx, msg.TenantID); err != nil {
			return 0, err
		}
		if err := m.guard(ctx, tx, "notification:rate"); err != nil {
			return 0, err
		}
	}
	// Validate the complete request before creating any recipient's delivery.
	// A separate row/job per address gives each recipient independent rate
	// accounting and retry state and avoids revealing other recipients to them.
	if _, err := m.emailPayload(ctx, msg); err != nil {
		return 0, err
	}
	var first guid.ID
	seen := map[string]bool{}
	for _, address := range msg.To {
		key := strings.ToLower(address.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		single := msg
		single.To = []mail.Address{address}
		id, err := m.createDeliveryOn(ctx, tx, single, nil, nil, "", time.Time{})
		if err != nil {
			return 0, err
		}
		if first == 0 {
			first = id
		}
	}
	return first, nil
}

// externalPreferenceOn applies per-type overrides then global preferences;
// urgent notifications bypass quiet hours and require immediate email.
func (m *Module) externalPreferenceOn(ctx context.Context, tx *gorm.DB, principal, tenant guid.ID, typ string, severity Severity) (bool, time.Time, error) {
	return m.externalPreferenceAtOn(ctx, tx, principal, tenant, typ, severity, m.now())
}

func (m *Module) externalPreferenceAtOn(ctx context.Context, tx *gorm.DB, principal, tenant guid.ID, typ string, severity Severity, created time.Time) (bool, time.Time, error) {
	global, err := m.preferenceOn(ctx, tx, principal, tenant, "*")
	if err != nil {
		return false, time.Time{}, err
	}
	specific, err := m.preferenceOn(ctx, tx, principal, tenant, typ)
	if err != nil {
		return false, time.Time{}, err
	}
	p := global
	if specific.Version > 0 {
		p = specific
	}
	if p.Version == 0 {
		p.Email = defaultEmail(typ, severity)
	}
	enabled := p.Email != "OFF"
	if severity == Urgent {
		return true, time.Time{}, nil
	}
	if !enabled {
		return false, time.Time{}, nil
	}
	now := m.now()
	runAt := time.Time{}
	zone, err := time.LoadLocation(global.Timezone)
	if err != nil {
		return false, runAt, ErrInvalid
	}
	local := now.In(zone)
	if p.Email == "DAILY_DIGEST" {
		digestLocal := created.In(zone)
		runAt = time.Date(digestLocal.Year(), digestLocal.Month(), digestLocal.Day(), global.DigestHour, 0, 0, 0, zone)
		if !runAt.After(created) {
			runAt = runAt.AddDate(0, 0, 1)
		}
	}
	if global.QuietEnabled {
		minute := local.Hour()*60 + local.Minute()
		start, end := global.QuietStartMin, global.QuietEndMin
		quiet := start < end && minute >= start && minute < end || start > end && (minute >= start || minute < end)
		if quiet {
			quietEnd := time.Date(local.Year(), local.Month(), local.Day(), end/60, end%60, 0, 0, zone)
			if !quietEnd.After(now) {
				quietEnd = quietEnd.AddDate(0, 0, 1)
			}
			if quietEnd.After(runAt) {
				runAt = quietEnd
			}
		}
	}
	return true, runAt, nil
}
func (m *Module) notificationEmailOn(ctx context.Context, tx *gorm.DB, r *model.Notification, requested []ChannelName) error {
	if requested != nil && !slices.Contains(requested, Email) {
		return nil
	}
	enabled, runAt, err := m.externalPreferenceOn(ctx, tx, r.RecipientPrincipalID, r.TenantID, r.Type, Severity(r.Severity))
	if err != nil {
		return err
	}
	if requested == nil && Severity(r.Severity) == Info && !enabled {
		return nil
	}
	reason := ""
	if !enabled {
		reason = "PREFERENCE"
	}
	human, active, err := m.principal.ActiveHumanByID(ctx, tx, r.RecipientPrincipalID)
	if err != nil {
		return err
	}
	if !active {
		reason = "RECIPIENT_INACTIVE"
	}
	address := human.Email
	if address == "" {
		reason = "NO_ADDRESS"
		address = "unavailable@invalid.local"
	}
	var params map[string]string
	if err = json.Unmarshal([]byte(r.Params), &params); err != nil {
		return err
	}
	msg := EmailMessage{severity: Severity(r.Severity), TenantID: r.TenantID, To: []mail.Address{{Address: address}}, TemplateKey: r.TemplateKey, Params: params, SourceRef: "notification:" + r.ID.String()}
	if (r.Type == "machine.offline" && r.Permission == "") || r.Type == "machine.recovered" {
		yes, err := m.machineEmailOn(ctx, tx, r, &msg, &runAt)
		if err != nil || !yes {
			return err
		}
	}
	_, err = m.createDeliveryOn(ctx, tx, msg, &r.ID, &r.RecipientPrincipalID, reason, runAt)
	return err
}
func terminal(state string) bool {
	return state == "SENT" || state == "FAILED" || state == "SUPPRESSED"
}
