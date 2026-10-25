package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"context"
	"encoding/base64"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type connectorTransport func(*http.Request) (*http.Response, error)

func (f connectorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func configureTestConnector(t *testing.T, m *Module, db *gorm.DB, channel guid.ID, kind string) ConnectorView {
	t.Helper()
	m.config.ConnectorEncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	var out ConnectorView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.configureConnectorOn(conversationContext(t, 2), tx, 1, 3, channel, kind, ConnectorInput{Endpoint: "https://receiver.example/messages", Secret: strings.Repeat("s", 32), Enabled: true})
		return e
	}))
	return out
}
func TestChannelConnectorQueuesOnlyVisibleMessagesAndRollsBack(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	c := configureTestConnector(t, m, db, ch.ID, "HOOK")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "internal", Visibility: "INTERNAL"})
	visible := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "visible", Visibility: "CHANNEL"})
	var rows []model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, visible.ID, rows[0].MessageID)
	require.Equal(t, c.ID, rows[0].ConnectorID)
	abort := errors.New("abort")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "rollback", Visibility: "CHANNEL"})
		if e != nil {
			return e
		}
		return abort
	}), abort)
	var count int64
	require.NoError(t, query.BindDeliveries(db, m.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, "HOOK", c.Kind)
}
func TestChannelConnectorDeliverySignsRetriesAndCancelsRevokedConfig(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	configureTestConnector(t, m, db, ch.ID, "HOOK")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "actual body", Visibility: "CHANNEL"})
	var row model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).First(&row).Error)
	requests := 0
	client := &http.Client{Transport: connectorTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		require.Equal(t, row.ID.String(), req.Header.Get("Idempotency-Key"))
		payload, e := io.ReadAll(req.Body)
		require.NoError(t, e)
		require.Contains(t, string(payload), "actual body")
		expected, e := connectorRequest(t.Context(), req.URL.String(), strings.Repeat("s", 32), row.ID, payload, m.now())
		require.NoError(t, e)
		require.Equal(t, expected.Header.Get("X-Channel-Signature"), req.Header.Get("X-Channel-Signature"))
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	require.NoError(t, m.deliverConnector(t.Context(), row.ID, client))
	require.NoError(t, m.deliverConnector(t.Context(), row.ID, client))
	require.Equal(t, 1, requests)
	require.NoError(t, query.BindDeliveries(db, m.names).First(&row).Error)
	require.Equal(t, "DELIVERED", row.State)
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "retracted", Visibility: "CHANNEL"})
	var pending model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).Where(&model.ChannelDelivery{State: "PENDING"}).First(&pending).Error)
	mq := query.BindMessages(db, m.names)
	_, e := mq.WithContext(t.Context()).Where(mq.ID.Eq(pending.MessageID)).Updates(map[string]any{"withdrawn_at": m.now()})
	require.NoError(t, e)
	require.NoError(t, m.deliverConnector(t.Context(), pending.ID, client))
	require.Equal(t, 1, requests)
	require.NoError(t, query.BindDeliveries(db, m.names).Where(&model.ChannelDelivery{ID: pending.ID}).First(&pending).Error)
	require.Equal(t, "CANCELLED", pending.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
}
func TestChannelConnectorBridgeOriginAndOwnerRevocation(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "HTTP_BRIDGE")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(context.WithValue(conversationContext(t, 2), bridgeOriginKey{}, c.ID), tx, 1, 3, ch.ID, PostMessageInput{Body: "remote", Visibility: "CHANNEL"})
		return e
	}))
	var count int64
	require.NoError(t, query.BindDeliveries(db, m.names).Count(&count).Error)
	require.Zero(t, count)
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "do not leak", Visibility: "CHANNEL"})
	var pending model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).First(&pending).Error)
	cm := query.BindMembers(db, m.names)
	_, e := cm.WithContext(t.Context()).Where(cm.ChannelID.Eq(ch.ID), cm.PrincipalID.Eq(guid.ID(2))).Updates(map[string]any{"left_at": time.Now()})
	require.NoError(t, e)
	client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) { t.Fatal("revoked owner must not send"); return nil, nil })}
	require.NoError(t, m.deliverConnector(t.Context(), pending.ID, client))
	require.NoError(t, query.BindDeliveries(db, m.names).First(&pending).Error)
	require.Equal(t, "CANCELLED", pending.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
}
func TestChannelConnectorRejectsUnsafeDestinationsAndStaleWrites(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "HOOK")
	for _, target := range []string{"http://127.0.0.1/hook", "https://localhost/hook", "https://user:pass@example.com/hook", "https://receiver.example/hook?secret=x"} {
		require.False(t, validConnectorURL(target))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.configureConnectorOn(conversationContext(t, 2), tx, 1, 3, ch.ID, "HOOK", ConnectorInput{Version: c.Version + 1, Endpoint: c.Endpoint, Enabled: false})
		return e
	}), ErrChannelVersion)
}

func TestConnectorJobScopesDueWorkAndPropagatesStorageFailures(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "HOOK")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "cancel after config rotation", Visibility: "CHANNEL"})
	require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).Update("enabled", false).Error)
	handlers := m.JobHandlers()
	require.Len(t, handlers, 1)
	require.Equal(t, "collab.connector_delivery", handlers[0].Kind)
	require.Equal(t, time.Minute, handlers[0].Timeout)
	require.NoError(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 9}))
	var row model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).First(&row).Error)
	require.Equal(t, "PENDING", row.State)
	require.NoError(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}))
	require.NoError(t, query.BindDeliveries(db, m.names).First(&row).Error)
	require.Equal(t, "CANCELLED", row.State) //nolint:misspell // Published state spelling.
	schedules := m.JobSchedules()
	require.Len(t, schedules, 1)
	require.Equal(t, handlers[0].Kind, schedules[0].Kind)
	require.Equal(t, jobs.ScopePerTenant, schedules[0].TenantScope)
	require.Equal(t, 5*time.Second, schedules[0].Interval)
	fault := errors.New("delivery storage unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:delivery-read", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Deliveries.String() {
			tx.AddError(fault)
		}
	}))
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}), fault)
	require.NoError(t, db.Callback().Query().Remove("test:delivery-read"))
	m.runtime = nil
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
	client := connectorClient()
	require.Equal(t, 5*time.Second, client.Timeout)
	require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestConnectorConfigurationFailurePreservesPreviousGrant(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "HOOK")
	ctx := conversationContext(t, 2)
	for _, in := range []ConnectorInput{
		{Version: c.Version, Endpoint: "https://changed.example/hook", Enabled: true},
		{Version: 0, Endpoint: c.Endpoint, Secret: strings.Repeat("s", 32)},
		{Version: c.Version, Endpoint: c.Endpoint, Secret: "short"},
	} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.configureConnectorOn(ctx, tx, 1, 3, ch.ID, "HOOK", in)
			return err
		}))
	}
	for _, kind := range []string{"SEARCH", "TRANSCRIPTION"} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.configureConnectorOn(ctx, tx, 1, 3, ch.ID, kind, ConnectorInput{Endpoint: c.Endpoint})
			return err
		}), ErrChannelVersion)
	}
	var row model.ChannelConnector
	require.NoError(t, query.BindConnectors(db, m.names).First(&row).Error)
	require.Equal(t, c.Version, row.Version)
	require.Equal(t, c.Endpoint, row.Endpoint)
	for _, scenario := range []string{"missing", "malformed", "previous_file"} {
		t.Run(scenario, func(t *testing.T) {
			config := m.config
			defer func() { m.config = config }()
			switch scenario {
			case "missing":
				m.config.ConnectorEncryptionKey = ""
			case "malformed":
				m.config.ConnectorEncryptionKey = "invalid"
			case "previous_file":
				m.config.PreviousConnectorEncryptionKeyFiles = []string{t.TempDir() + "/missing-key"}
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.configureConnectorOn(ctx, tx, 1, 3, ch.ID, "HOOK", ConnectorInput{Version: c.Version, Endpoint: c.Endpoint, Secret: strings.Repeat("x", 32), Enabled: true})
				return err
			}), ErrUnavailable)
		})
	}
}

func TestConnectorRetryExhaustionAndSecretFailureRetainDeliveryFacts(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "HOOK")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "pending", Visibility: "CHANNEL"})
	var row model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).First(&row).Error)
	calls := 0
	client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("receiver offline") })}
	original := m.config
	m.config.ConnectorEncryptionKey = ""
	require.ErrorIs(t, m.deliverConnector(t.Context(), row.ID, client), ErrUnavailable)
	m.config = original
	var config model.ChannelConnector
	require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).First(&config).Error)
	require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).Update("secret_sealed", "corrupt").Error)
	require.ErrorIs(t, m.deliverConnector(t.Context(), row.ID, client), ErrUnavailable)
	require.Zero(t, calls)
	require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).Update("secret_sealed", config.SecretSealed).Error)
	for attempt := 1; attempt <= 5; attempt++ {
		require.NoError(t, query.BindDeliveries(db, m.names).Where(&model.ChannelDelivery{ID: row.ID}).Update("next_at", m.now()).Error)
		require.NoError(t, m.deliverConnector(t.Context(), row.ID, client))
		require.NoError(t, query.BindDeliveries(db, m.names).Where(&model.ChannelDelivery{ID: row.ID}).First(&row).Error)
		require.Equal(t, attempt, row.Attempts)
		if attempt < 5 {
			require.Equal(t, "PENDING", row.State)
		} else {
			require.Equal(t, "FAILED", row.State)
		}
	}
	require.NoError(t, m.deliverConnector(t.Context(), row.ID, client))
	require.Equal(t, 5, calls)
	require.ErrorIs(t, m.deliverConnector(t.Context(), 999, client), gorm.ErrRecordNotFound)
}

func TestConnectorConfigurationStorageFailureRetainsOriginalVersion(t *testing.T) {
	for _, scenario := range []string{"read", "write", "audit", "success"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			c := configureTestConnector(t, m, db, ch.ID, "HOOK")
			fault := errors.New("configuration unavailable")
			remove := func() {}
			switch scenario {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:config", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Connectors.String() {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:config")) }
			case "write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:config", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Connectors.String() {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Update().Remove("test:config")) }
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			}
			var out ConnectorView
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				out, err = m.configureConnectorOn(conversationContext(t, 2), tx, 1, 3, ch.ID, "HOOK", ConnectorInput{Version: c.Version, Endpoint: c.Endpoint, Enabled: false})
				return err
			})
			remove()
			var stored model.ChannelConnector
			require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).First(&stored).Error)
			if scenario == "success" {
				require.NoError(t, err)
				require.Equal(t, c.Version+1, out.Version)
				require.False(t, stored.Enabled)
			} else {
				require.ErrorIs(t, err, fault)
				require.Equal(t, c.Version, stored.Version)
				require.True(t, stored.Enabled)
			}
		})
	}
}
