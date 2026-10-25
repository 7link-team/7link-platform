package agents

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func wakeHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	m, db, _ := agentHarness(t)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.runtimeFacts.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
		return fleet.AgentRuntimeFact{ProcessStatus: "RUNNING"}, true, nil
	}
	return m, db, agentContext(t, 2)
}
func TestWakeConfigurationAuthoritySealingVersionsAndSecretRotation(t *testing.T) {
	m, db, ctx := wakeHarness(t)
	var value WakeConfigView
	var secret string
	configure := func(version int, in WakeConfigInput) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			value, secret, e = m.setWakeConfigOn(ctx, tx, 1, 7, version, in)
			return e
		})
	}
	in := WakeConfigInput{WakeMode: "WEBHOOK", WebhookURL: "https://receiver.example/wake"}
	require.NoError(t, configure(1, in))
	require.NotEmpty(t, secret)
	first := secret
	require.Equal(t, 2, value.Version)
	require.Empty(t, value.Secret)
	q := query.BindLiveState(db, m.names)
	row, e := q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
	require.NoError(t, e)
	require.NotContains(t, row.WakeSecretSealed, first)
	ring, e := m.keys()
	require.NoError(t, e)
	plain, e := ring.Open("wake-webhook", wakeSecretAAD(1, 7), row.WakeSecretSealed)
	require.NoError(t, e)
	require.Equal(t, first, string(plain))
	_, e = ring.Open("wake-webhook", wakeSecretAAD(2, 7), row.WakeSecretSealed)
	require.Error(t, e)
	require.ErrorIs(t, configure(1, in), ErrConflict)
	require.NoError(t, configure(2, in))
	require.Empty(t, secret)
	in.RotateSecret = true
	require.NoError(t, configure(3, in))
	require.NotEqual(t, first, secret)
	for _, actor := range []guid.ID{7, 8, 9} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, e := m.setWakeConfigOn(agentContext(t, actor), tx, 1, 7, 4, in)
			return e
		}))
	}
	require.NoError(t, configure(4, WakeConfigInput{WakeMode: "POLL"}))
	row, e = q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
	require.NoError(t, e)
	require.Empty(t, row.WakeSecretSealed)
	require.Empty(t, row.WakeURL)
}
func TestWakeRejectsUnsafeEndpoints(t *testing.T) {
	for _, raw := range []string{"http://public.example/wake", "https://127.0.0.1/wake", "https://10.0.0.1/", "https://169.254.169.254/", "https://[::1]/", "https://[::ffff:127.0.0.1]/", "https://localhost/", "https://a.localhost/", "https://user:password@public.example/", "https://public.example/#secret", "https://public.example/?token=secret"} {
		require.False(t, validWakeURL(raw), raw)
	}
	require.True(t, validWakeURL("https://receiver.example/wake"))
	require.Equal(t, 5*time.Second, wakeHTTPClient().Timeout)
	require.ErrorIs(t, wakeHTTPClient().CheckRedirect(nil, nil), http.ErrUseLastResponse)
}
func TestWakeSignatureAndContentFreePayload(t *testing.T) {
	now := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	req, e := wakeRequest(t.Context(), "https://receiver.example/wake", "receiver-secret", 7, 42, now)
	require.NoError(t, e)
	body, e := io.ReadAll(req.Body)
	require.NoError(t, e)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Len(t, payload, 3)
	require.Equal(t, "7", payload["agent_id"])
	require.InDelta(t, float64(42), payload["inbox_seq"], 0)
	require.Equal(t, now.Format(time.RFC3339), payload["sent_at"])
	timestamp := req.Header.Get("X-Wake-Timestamp")
	mac := hmac.New(sha256.New, []byte("receiver-secret"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	require.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), req.Header.Get("X-Wake-Signature"))
}
func TestWakeDeliveryRetriesFallbackNotificationAtomicityAndInboxPreservation(t *testing.T) {
	m, db, ctx := wakeHarness(t)
	now := m.now()
	m.now = func() time.Time { return now }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.setWakeConfigOn(ctx, tx, 1, 7, 1, WakeConfigInput{WakeMode: "WEBHOOK", WebhookURL: "https://receiver.example/wake"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "sensitive-event", Kind: "MESSAGE", SubjectID: 123, StreamKey: "thread:123"})
		return e
	}))
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		assert.NotContains(t, string(body), "sensitive")
		assert.NotContains(t, string(body), "private")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	// Test-only transport reaches a local TLS receiver while retaining the real
	// signed request URL. Production always uses wakeHTTPClient's guarded dialer.
	client := &http.Client{Transport: wakeTestTransport{server.Client().Transport, server.URL}, Timeout: 5 * time.Second}
	observed := 0
	observer := WakeFailureObserver(func(_ context.Context, tx *gorm.DB, f WakeFailure) error {
		observed++
		require.Equal(t, 20, f.Failures)
		require.Equal(t, guid.ID(2), f.OwnerPrincipalID)
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", "wake-notice").Error
	})
	services := module.NewServices()
	require.NoError(t, m.provideWakeNotifications(services))
	require.NoError(t, services.Seal())
	notifications, e := module.Resolve[WakeNotifications](services)
	require.NoError(t, e)
	require.ErrorIs(t, notifications.Bind(nil), ErrConflict)
	require.NoError(t, notifications.Bind(observer))
	require.ErrorIs(t, notifications.Bind(observer), ErrConflict)
	q := query.BindLiveState(db, m.names)
	for i := 1; i <= 19; i++ {
		require.NoError(t, m.deliverWake(ctx, 1, 7, client))
		row, e := q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
		require.NoError(t, e)
		require.Equal(t, i, row.WakeFailures)
		require.Equal(t, "WEBHOOK", row.WakeMode)
		require.Zero(t, row.LastWakeSeq)
		require.Equal(t, int64(1), row.Head)
		require.Zero(t, row.Ack)
		before := requests
		require.NoError(t, m.deliverWake(ctx, 1, 7, client))
		require.Equal(t, before, requests, "backoff blocks a premature retry")
		now = row.NextWakeAt.Add(time.Millisecond)
	}
	m.wakeFailureObserver = func(context.Context, *gorm.DB, WakeFailure) error { return errors.New("notice failed") }
	require.Error(t, m.deliverWake(ctx, 1, 7, client))
	row, e := q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
	require.NoError(t, e)
	require.Equal(t, 19, row.WakeFailures)
	require.Equal(t, "WEBHOOK", row.WakeMode)
	m.wakeFailureObserver = func(_ context.Context, tx *gorm.DB, f WakeFailure) error {
		observed++
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", "wake-notice").Error
	}
	require.NoError(t, m.deliverWake(ctx, 1, 7, client))
	require.Equal(t, 1, observed)
	row, e = q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
	require.NoError(t, e)
	require.Equal(t, "POLL", row.WakeMode)
	require.Equal(t, 20, row.WakeFailures)
	require.Equal(t, int64(1), row.Head)
	require.Zero(t, row.Ack)
	before := requests
	require.NoError(t, m.deliverWake(ctx, 1, 7, client))
	require.Equal(t, before, requests)
}

type wakeTestTransport struct {
	base   http.RoundTripper
	target string
}

func (t wakeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	u, e := http.NewRequest(http.MethodPost, t.target, nil)
	if e != nil {
		return nil, e
	}
	clone.URL = u.URL
	return t.base.RoundTrip(clone)
}
func TestWakeSuccessFencesConfigurationAndSkipsStoppedAgents(t *testing.T) {
	m, db, ctx := wakeHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.setWakeConfigOn(ctx, tx, 1, 7, 1, WakeConfigInput{WakeMode: "WEBHOOK", WebhookURL: "https://receiver.example/wake"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "event", Kind: "MESSAGE", SubjectID: 123})
		return e
	}))
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	client := &http.Client{Transport: wakeTestTransport{server.Client().Transport, server.URL}}
	m.runtimeFacts.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
		return fleet.AgentRuntimeFact{ProcessStatus: "NONE"}, true, nil
	}
	require.NoError(t, m.deliverWake(ctx, 1, 7, client))
	require.Zero(t, requests)
	m.runtimeFacts.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
		return fleet.AgentRuntimeFact{ProcessStatus: "RUNNING"}, true, nil
	}
	require.NoError(t, m.deliverWake(ctx, 1, 7, client))
	require.Equal(t, 1, requests)
	require.NoError(t, m.deliverWake(ctx, 1, 7, client))
	require.Equal(t, 1, requests, "a delivered head is coalesced")
	q := query.BindLiveState(db, m.names)
	row, e := q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(guid.ID(7))).First()
	require.NoError(t, e)
	require.Equal(t, int64(1), row.LastWakeSeq)
	require.Zero(t, row.Ack)
}

func TestWakeJobSkipsPollingAndPropagatesUnavailableCurrentRuntimeFacts(t *testing.T) {
	m, db, ctx := wakeHarness(t)
	require.Error(t, m.wakeJob().Run(ctx, jobs.Job{}))
	require.NoError(t, m.wakeJob().Run(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "wake-job", Kind: "STATE", SubjectID: 55})
		return e
	}))
	require.NoError(t, m.wakeJob().Run(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, db.Exec("UPDATE k_agents_live_states SET wake_mode='WEBHOOK',wake_url='https://receiver.example.test',wake_secret_sealed='corrupt' WHERE agent_principal_id=7").Error)
	fault := errors.New("runtime source unavailable")
	original := m.agents.ByPrincipalOn
	for _, mode := range []string{"directory", "missing-runtime", "runtime", "absent", "corrupt-secret", "bad-url", "missing-key"} {
		t.Run(mode, func(t *testing.T) {
			m.agents.ByPrincipalOn = original
			m.runtimeFacts.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
				return fleet.AgentRuntimeFact{ProcessStatus: "RUNNING"}, true, nil
			}
			switch mode {
			case "directory":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "missing-runtime":
				m.runtimeFacts.ByPrincipalOn = nil
			case "runtime":
				m.runtimeFacts.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					return fleet.AgentRuntimeFact{}, false, fault
				}
			case "absent":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
			case "bad-url":
				require.NoError(t, db.Exec("UPDATE k_agents_live_states SET wake_url='http://localhost' WHERE agent_principal_id=7").Error)
				defer db.Exec("UPDATE k_agents_live_states SET wake_url='https://receiver.example.test' WHERE agent_principal_id=7")
			case "missing-key":
				old := m.config.EncryptionKey
				m.config.EncryptionKey = ""
				defer func() { m.config.EncryptionKey = old }()
			}
			err := m.wakeJob().Run(ctx, jobs.Job{TenantID: 1})
			if mode == "absent" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestWakeConfigurationFailureRollsBackStateAndNeverReturnsSecret(t *testing.T) {
	for _, mode := range []string{"invalid-mode", "poll-url", "bad-webhook", "missing-key", "state-read", "state-write", "audit"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := wakeHarness(t)
			fault := errors.New("wake storage failed")
			input := WakeConfigInput{WakeMode: "WEBHOOK", WebhookURL: "https://receiver.example.test"}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.lockState(ctx, tx, 1, 7); return e }))
			switch mode {
			case "invalid-mode":
				input.WakeMode = "UNKNOWN"
			case "poll-url":
				input.WakeMode = "POLL"
			case "bad-webhook":
				input.WebhookURL = "http://localhost"
			case "missing-key":
				m.config.EncryptionKey = ""
			case "state-read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("wake-read", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.LiveState.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("wake-read")
			case "state-write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("wake-write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.LiveState.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Update().Remove("wake-write")
			case "audit":
				m.audit = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, secret, e := m.setWakeConfigOn(ctx, tx, 1, 7, 1, input)
				require.Empty(t, secret)
				return e
			})
			require.Error(t, err)
			if mode == "state-read" {
				require.NoError(t, db.Callback().Query().Remove("wake-read"))
			}
			q := query.BindLiveState(db, m.names)
			row, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, 1, row.WakeVersion)
			require.Empty(t, row.WakeSecretSealed)
		})
	}
}
