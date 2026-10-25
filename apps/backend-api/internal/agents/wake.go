package agents

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type WakeConfigInput struct {
	WakeMode     string `json:"wake_mode" enum:"CHANNEL,POLL,WEBHOOK"`
	WebhookURL   string `json:"webhook_url,omitempty" maxLength:"512"`
	RotateSecret bool   `json:"rotate_secret,omitempty"`
}
type WakeConfigView struct {
	WakeMode            string `json:"wake_mode" enum:"CHANNEL,POLL,WEBHOOK"`
	WebhookURL          string `json:"webhook_url,omitempty"`
	Secret              string `json:"secret,omitempty" readOnly:"true" doc:"Returned only for a new or rotated webhook secret, never on replay. Save it before leaving this response."`
	Version             int    `json:"version"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	Next                []Next `json:"next"`
}

func (v WakeConfigView) NextSteps() []Next { return v.Next }
func wakeConfigView(r *model.LiveState) WakeConfigView {
	mode := r.WakeMode
	if mode == "" {
		mode = "CHANNEL"
	}
	return WakeConfigView{WakeMode: mode, WebhookURL: r.WakeURL, Version: r.WakeVersion, ConsecutiveFailures: r.WakeFailures, Next: []Next{{Operation: "inbox_events.list"}}}
}
func wakeSecretAAD(tenant, agent guid.ID) []byte {
	return []byte(tenant.String() + ":" + agent.String())
}
func validWakeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 512 || u.User != nil || u.Fragment != "" || u.RawQuery != "" || strings.EqualFold(u.Hostname(), "localhost") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") {
		return false
	}
	return (httpx.Policy{}).ValidateURL(raw) == nil
}
func (m *Module) wakeConfigOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (WakeConfigView, error) {
	if _, err := m.authorize(ctx, tx, tenant, agent, "agent_manage", true); err != nil {
		return WakeConfigView{}, err
	}
	r, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return WakeConfigView{}, err
	}
	return wakeConfigView(r), nil
}

// setWakeConfigOn returns a secret separately, so idempotency receipts never
// persist its plaintext and a replay cannot reveal a previously displayed key.
func (m *Module) setWakeConfigOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, version int, in WakeConfigInput) (WakeConfigView, string, error) {
	var empty WakeConfigView
	a, err := m.authorize(ctx, tx, tenant, agent, "agent_manage", true)
	if err != nil {
		return empty, "", err
	}
	if a.State == "RETIRED" || a.State == "RETIRING" {
		return empty, "", ErrConflict
	}
	if in.WakeMode != "CHANNEL" && in.WakeMode != "POLL" && in.WakeMode != "WEBHOOK" {
		return empty, "", ErrInvalid
	}
	if in.WakeMode == "WEBHOOK" && !validWakeURL(in.WebhookURL) || in.WakeMode != "WEBHOOK" && (in.WebhookURL != "" || in.RotateSecret) {
		return empty, "", ErrInvalid
	}
	r, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return empty, "", err
	}
	if version != r.WakeVersion {
		return empty, "", ErrConflict
	}
	secret := ""
	sealed := r.WakeSecretSealed
	if in.WakeMode == "WEBHOOK" && (sealed == "" || in.RotateSecret || in.WebhookURL != r.WakeURL) {
		ring, e := m.keys()
		if e != nil {
			return empty, "", e
		}
		material := make([]byte, 32)
		if _, e = rand.Read(material); e != nil {
			return empty, "", e
		}
		secret = base64.RawURLEncoding.EncodeToString(material)
		sealed, e = ring.Seal("wake-webhook", wakeSecretAAD(tenant, agent), []byte(secret))
		if e != nil {
			return empty, "", ErrUnavailable
		}
	}
	if in.WakeMode != "WEBHOOK" {
		sealed = ""
	}
	q := query.BindLiveState(tx, m.names)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).Updates(map[string]any{"wake_mode": in.WakeMode, "wake_url": in.WebhookURL, "wake_secret_sealed": sealed, "wake_version": version + 1, "wake_failures": 0, "last_wake_seq": r.Ack, "next_wake_at": nil, "updated_at": m.now()})
	if err != nil {
		return empty, "", err
	}
	if err = m.record(ctx, tx, tenant, agent, "WAKE_CONFIG_CHANGED", true); err != nil {
		return empty, "", err
	}
	r.WakeMode, r.WakeURL, r.WakeSecretSealed, r.WakeVersion, r.WakeFailures = in.WakeMode, in.WebhookURL, sealed, version+1, 0
	return wakeConfigView(r), secret, nil
}

// WakeFailureObserver receives only an Agent identity and a failure count. It
// participates in the POLL fallback transaction, including the owner's notice.
type WakeFailure struct {
	TenantID, AgentPrincipalID, OwnerPrincipalID guid.ID
	Failures                                     int
}
type WakeFailureObserver func(context.Context, *gorm.DB, WakeFailure) error
type WakeNotifications struct {
	Bind func(WakeFailureObserver) error
}

func (m *Module) provideWakeNotifications(s *module.Services) error {
	return module.Provide[WakeNotifications](s, WakeNotifications{Bind: func(observer WakeFailureObserver) error {
		if observer == nil || m.wakeFailureObserver != nil {
			return ErrConflict
		}
		m.wakeFailureObserver = observer
		return nil
	}})
}

// wakeRequest contains no inbox references, object types, titles or bodies.
func wakeRequest(ctx context.Context, target, secret string, agent guid.ID, head int64, now time.Time) (*http.Request, error) {
	body, err := json.Marshal(struct {
		AgentID  guid.ID   `json:"agent_id"`
		InboxSeq int64     `json:"inbox_seq"`
		SentAt   time.Time `json:"sent_at"`
	}{agent, head, now})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wake-Timestamp", timestamp)
	req.Header.Set("X-Wake-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req, nil
}
func wakeHTTPClient() *http.Client {
	client := httpx.NewClient(5*time.Second, httpx.Policy{})
	// Never forward a signature to a different endpoint through a redirect.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// deliverWake performs network I/O after the inbox commit, outside a database
// transaction. Its result is fenced by the configuration version and head.
func (m *Module) deliverWake(ctx context.Context, tenant, agent guid.ID, client *http.Client) error {
	db, err := m.database()
	if err != nil {
		return err
	}
	var snapshot *model.LiveState
	var owner guid.ID
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.agents.ByPrincipalOn == nil || m.runtimeFacts.ByPrincipalOn == nil {
			return ErrUnavailable
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
		if e != nil {
			return e
		}
		if !found || a.State != "ACTIVE" {
			return nil
		}
		f, found, e := m.runtimeFacts.ByPrincipalOn(ctx, tx, tenant, agent)
		if e != nil {
			return e
		}
		if !found || f.ProcessStatus != "RUNNING" {
			return nil
		}
		q := query.BindLiveState(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).First()
		if e != nil {
			return e
		}
		if r.WakeMode != "WEBHOOK" || r.Head <= r.Ack || r.Head <= r.LastWakeSeq || r.NextWakeAt != nil && r.NextWakeAt.After(m.now()) {
			return nil
		}
		snapshot, owner = r, a.OwnerPrincipalID
		return nil
	})
	if err != nil || snapshot == nil {
		return err
	}
	if !validWakeURL(snapshot.WakeURL) {
		return ErrInvalid
	}
	ring, err := m.keys()
	if err != nil {
		return err
	}
	secret, err := ring.Open("wake-webhook", wakeSecretAAD(tenant, agent), snapshot.WakeSecretSealed)
	if err != nil {
		return ErrUnavailable
	}
	req, err := wakeRequest(ctx, snapshot.WakeURL, string(secret), agent, snapshot.Head, m.now())
	if err != nil {
		return err
	}
	response, sendErr := client.Do(req)
	success := sendErr == nil && response.StatusCode >= 200 && response.StatusCode < 300
	if response != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.lockState(ctx, tx, tenant, agent)
		if e != nil {
			return e
		}
		if r.WakeMode != "WEBHOOK" || r.WakeVersion != snapshot.WakeVersion || r.LastWakeSeq >= snapshot.Head {
			return nil
		}
		q := query.BindLiveState(tx, m.names)
		updates := map[string]any{}
		if success {
			updates["last_wake_seq"], updates["wake_failures"], updates["next_wake_at"] = snapshot.Head, 0, nil
		} else {
			failures := r.WakeFailures + 1
			// Five exponential retries per wake cycle; failed cycles remain durable.
			delay := time.Second * time.Duration(1<<uint((failures-1)%6))
			next := m.now().Add(delay)
			updates["wake_failures"], updates["next_wake_at"] = failures, next
			if failures >= 20 {
				if m.wakeFailureObserver == nil {
					return ErrUnavailable
				}
				updates["wake_mode"], updates["wake_version"] = "POLL", r.WakeVersion+1
				notice := WakeFailure{tenant, agent, owner, failures}
				if e = m.wakeFailureObserver(ctx, tx, notice); e != nil {
					return e
				}
				event := audit.NewDomainEvent(ctx, tenant, "agents.changed", "agent", agent)
				event.Detail["action"], event.Detail["authority_source"], event.Detail["failures"] = "WAKE_FALLBACK", "SYSTEM:agents.wake", failures
				if e = m.audit(ctx, tx, event); e != nil {
					return e
				}
				if _, e = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agents.changed", Subject: events.Subject{Kind: "agent", ID: agent}, SubjectVersion: int64(r.WakeVersion + 1), Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:agents.wake"}, Data: map[string]any{"action": "WAKE_FALLBACK", "wake_mode": "POLL"}}); e != nil {
					return e
				}
			}
		}
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).Updates(updates)
		return e
	})
}
func (m *Module) wakeJob() jobs.Handler {
	return jobs.Handler{Kind: "agents.webhook_wake", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return err
		}
		client := wakeHTTPClient()
		defer client.CloseIdleConnections()
		q := query.BindLiveState(db, m.names)
		var after guid.ID
		var failures []error
		for {
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.AgentPrincipalID.Gt(after), q.Head.Gt(0)).Order(q.AgentPrincipalID).Limit(100).Find()
			if e != nil {
				return e
			}
			for _, r := range rows {
				after = r.AgentPrincipalID
				if r.WakeMode == "WEBHOOK" {
					if e = m.deliverWake(ctx, job.TenantID, r.AgentPrincipalID, client); e != nil {
						failures = append(failures, e)
					}
				}
			}
			if len(rows) < 100 {
				return errors.Join(failures...)
			}
		}
	}}
}
