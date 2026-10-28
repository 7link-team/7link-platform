package collab

import (
	activityi18n "7link.com/platform/internal/collab/i18n"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"gorm.io/gorm"
)

type ConnectorInput struct {
	Version  int    `json:"version" minimum:"0"`
	Endpoint string `json:"endpoint" maxLength:"512"`
	Secret   string `json:"secret,omitempty" minLength:"32" maxLength:"512" writeOnly:"true"`
	Enabled  bool   `json:"enabled"`
}
type ConnectorView struct {
	ID       guid.ID `json:"id"`
	Kind     string  `json:"kind" enum:"HOOK,HTTP_BRIDGE,TRANSCRIPTION,SEARCH"`
	Endpoint string  `json:"endpoint"`
	Enabled  bool    `json:"enabled"`
	Version  int     `json:"version"`
}

func connectorView(r model.ChannelConnector) ConnectorView {
	return ConnectorView{r.ID, r.Kind, r.Endpoint, r.Enabled, r.Version}
}
func (m *Module) connectorKeys() (*keyringx.Keyring, error) {
	key, err := secretx.Resolve("collab.connector_encryption_key", m.config.ConnectorEncryptionKey, m.config.ConnectorEncryptionKeyFile, 4096)
	if err != nil || key == "" {
		return nil, ErrUnavailable
	}
	previous, err := secretx.ResolveList("collab.previous_connector_encryption_keys", m.config.PreviousConnectorEncryptionKeys, m.config.PreviousConnectorEncryptionKeyFiles, 4096)
	if err != nil {
		return nil, ErrUnavailable
	}
	ring, err := keyringx.New("collab:", key, previous...)
	if err != nil {
		return nil, ErrUnavailable
	}
	return ring, nil
}
func connectorAAD(tenant, channel, id guid.ID) []byte {
	return []byte(tenant.String() + ":" + channel.String() + ":" + id.String())
}
func validConnectorURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && len(raw) <= 512 && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.EqualFold(u.Hostname(), "localhost") && !strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") && (httpx.Policy{}).ValidateURL(raw) == nil
}

// Only internal Human channel owners can authorize external forwarding. Neither
// shared-channel clients nor Agent credentials can grant this capability.
func (m *Module) connectorOwnerOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID) error {
	_, member, e := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if e != nil {
		return e
	}
	if member.Role != "OWNER" || member.External || member.MemberKind != "HUMAN" {
		return ErrForbidden
	}
	_, e = m.humanOn(ctx, tx, tenant, member.PrincipalID)
	return e
}
func (m *Module) configureConnectorOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string, in ConnectorInput) (ConnectorView, error) {
	ctx = mutationContext(ctx)
	var empty ConnectorView
	if e := m.connectorOwnerOn(ctx, tx, tenant, project, channel); e != nil {
		return empty, e
	}
	if (kind != "HOOK" && kind != "HTTP_BRIDGE" && kind != "TRANSCRIPTION" && kind != "SEARCH") || !validConnectorURL(in.Endpoint) || in.Version < 0 || len(in.Secret) > 512 || (in.Secret != "" && len(in.Secret) < 32) {
		return empty, ErrInvalidArgument
	}
	var row model.ChannelConnector
	e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: tenant, ChannelID: channel, Kind: kind}).First(&row).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, e
	}
	if errors.Is(e, gorm.ErrRecordNotFound) {
		if in.Version != 0 || in.Secret == "" {
			return empty, ErrChannelVersion
		}
		id, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		row = model.ChannelConnector{ID: guid.ID(id), TenantID: tenant, ChannelID: channel, Kind: kind, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now()}
	} else if row.Version != in.Version {
		return empty, ErrChannelVersion
	}
	if row.Endpoint != in.Endpoint && in.Secret == "" {
		return empty, ErrInvalidArgument
	}
	if in.Secret != "" {
		ring, e := m.connectorKeys()
		if e != nil {
			return empty, e
		}
		row.SecretSealed, e = ring.Seal("channel-connector", connectorAAD(tenant, channel, row.ID), []byte(in.Secret))
		if e != nil {
			return empty, ErrUnavailable
		}
	}
	row.CreatedBy = authn.PrincipalIDFromContext(ctx)
	row.Endpoint, row.Enabled, row.Version, row.UpdatedAt = in.Endpoint, in.Enabled, row.Version+1, m.now()
	q := query.BindConnectors(tx, m.names)
	if in.Version == 0 {
		e = q.Create(&row).Error
	} else {
		result := q.Where(&model.ChannelConnector{ID: row.ID, TenantID: tenant, Version: in.Version}).Select("endpoint", "enabled", "version", "secret_sealed", "created_by", "updated_at").Updates(&row)
		e = result.Error
		if e == nil && result.RowsAffected != 1 {
			e = ErrChannelVersion
		}
	}
	if e != nil {
		return empty, e
	}
	if e = m.channelEvent(ctx, tx, tenant, project, channel, "collab.channel_updated", "CHANNEL_OWNER"); e != nil {
		return empty, e
	}
	return connectorView(row), nil
}

// Queue insertion shares the message transaction. Bridge ingress suppresses
// forwarding to its own connector, preventing an HTTP echo loop.
type bridgeOriginKey struct{}

func (m *Module) queueChannelDeliveriesOn(ctx context.Context, tx *gorm.DB, row *model.Message) error {
	if row.Visibility != "CHANNEL" {
		return nil
	}
	var connectors []model.ChannelConnector
	if e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: row.TenantID, ChannelID: row.ChannelID, Enabled: true}).Find(&connectors).Error; e != nil {
		return e
	}
	origin, _ := ctx.Value(bridgeOriginKey{}).(guid.ID)
	for _, c := range connectors {
		if c.ID == origin || (c.Kind != "HOOK" && c.Kind != "HTTP_BRIDGE") {
			continue
		}
		id, e := m.ids.Next()
		if e != nil {
			return e
		}
		r := model.ChannelDelivery{ID: guid.ID(id), TenantID: row.TenantID, ChannelID: row.ChannelID, ConnectorID: c.ID, MessageID: row.ID, ConfigVersion: c.Version, State: "PENDING", NextAt: m.now(), UpdatedAt: m.now()}
		if e = query.BindDeliveries(tx, m.names).Create(&r).Error; e != nil {
			return e
		}
	}
	return nil
}
func connectorRequest(ctx context.Context, endpoint, secret string, id guid.ID, body []byte, now time.Time) (*http.Request, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	stamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stamp + "."))
	_, _ = mac.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id.String())
	req.Header.Set("X-Channel-Timestamp", stamp)
	req.Header.Set("X-Channel-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req, nil
}
func connectorClient() *http.Client {
	client := httpx.NewClient(5*time.Second, httpx.Policy{})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// Delivery reads current content and authorizes the configuring owner again.
// Network I/O runs outside DB transactions; Idempotency-Key remains stable when
// the receiver accepted a request but the local acknowledgement was lost.
func (m *Module) deliverConnector(ctx context.Context, id guid.ID, client *http.Client) error {
	db, e := m.database()
	if e != nil {
		return e
	}
	var d model.ChannelDelivery
	var c model.ChannelConnector
	var body []byte
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := query.BindDeliveries(tx, m.names).Where(&model.ChannelDelivery{ID: id}).First(&d).Error; e != nil {
			return e
		}
		if d.State != "PENDING" || d.NextAt.After(m.now()) {
			return nil
		}
		if e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{ID: d.ConnectorID, TenantID: d.TenantID}).First(&c).Error; e != nil {
			return e
		}
		ownerCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: c.TenantID, PrincipalID: c.CreatedBy, SubjectType: authn.SubjectTypePrincipal})
		project, e := m.projectForChannel(ownerCtx, c.ChannelID)
		if e != nil {
			return e
		}
		if !c.Enabled || c.Version != d.ConfigVersion {
			return m.cancelDeliveryOn(tx, d.ID)
		}
		if e = m.connectorOwnerOn(ownerCtx, tx, c.TenantID, project, c.ChannelID); e != nil {
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
				return m.cancelDeliveryOn(tx, d.ID)
			}
			return e
		}
		var row model.Message
		mq := query.BindMessages(tx, m.names)
		message, e := mq.WithContext(ctx).Where(mq.ID.Eq(d.MessageID), mq.TenantID.Eq(d.TenantID), mq.ChannelID.Eq(d.ChannelID)).First()
		if e != nil {
			return e
		}
		row = *message
		if row.WithdrawnAt != nil || row.Visibility != "CHANNEL" {
			return m.cancelDeliveryOn(tx, d.ID)
		}
		body, e = json.Marshal(struct {
			DeliveryID guid.ID     `json:"delivery_id"`
			ChannelID  guid.ID     `json:"channel_id"`
			Message    MessageView `json:"message"`
		}{d.ID, c.ChannelID, messageView(&row)})
		return e
	})
	if e != nil || len(body) == 0 {
		return e
	}
	ring, e := m.connectorKeys()
	if e != nil {
		return e
	}
	secret, e := ring.Open("channel-connector", connectorAAD(c.TenantID, c.ChannelID, c.ID), c.SecretSealed)
	if e != nil {
		return ErrUnavailable
	}
	req, e := connectorRequest(ctx, c.Endpoint, string(secret), d.ID, body, m.now())
	if e != nil {
		return e
	}
	response, sendErr := client.Do(req)
	success := false
	if response != nil {
		success = sendErr == nil && response.StatusCode >= 200 && response.StatusCode < 300
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
	state := "PENDING"
	attempts := d.Attempts + 1
	if success {
		state = "DELIVERED"
	} else if attempts >= 5 {
		state = "FAILED"
	}
	// CAS prevents a stale worker from overwriting a newer retry result.
	return query.BindDeliveries(db.WithContext(ctx), m.names).Where(&model.ChannelDelivery{ID: id, State: "PENDING", ConfigVersion: d.ConfigVersion}).Where("attempts = ?", d.Attempts).Updates(map[string]any{"state": state, "attempts": attempts, "next_at": m.now().Add(time.Duration(1<<attempts) * time.Second), "updated_at": m.now()}).Error
}
func (m *Module) cancelDeliveryOn(tx *gorm.DB, id guid.ID) error {
	return query.BindDeliveries(tx, m.names).Where(&model.ChannelDelivery{ID: id}).Updates(map[string]any{"state": "CANCELLED", "updated_at": m.now()}).Error //nolint:misspell // Preserve the published domain or serialized contract spelling.
}
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{DisplayNameKey: activityi18n.Namespace + ".activity_job_collab_connector_delivery_name", DescriptionKey: activityi18n.Namespace + ".activity_job_collab_connector_delivery_description", Kind: "collab.connector_delivery", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		db, e := m.database()
		if e != nil {
			return e
		}
		var rows []model.ChannelDelivery
		e = query.BindDeliveries(db.WithContext(ctx), m.names).Where(&model.ChannelDelivery{TenantID: job.TenantID, State: "PENDING"}).Where("next_at <= ?", m.now()).Order("id").Limit(50).Find(&rows).Error
		if e != nil {
			return e
		}
		for _, r := range rows {
			if e = m.deliverConnector(ctx, r.ID, connectorClient()); e != nil {
				return e
			}
		}
		return nil
	}}}
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "collab.connector_delivery", Kind: "collab.connector_delivery", Interval: 5 * time.Second, TenantScope: jobs.ScopePerTenant}}
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
