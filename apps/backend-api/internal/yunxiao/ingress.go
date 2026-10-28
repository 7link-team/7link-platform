package yunxiao

import (
	activityi18n "7link.com/platform/internal/yunxiao/i18n"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrIngressSignature = errors.New("yunxiao: invalid ingress signature")

type ingressAuthentication struct {
	tenant, connection, owner guid.ID
	version                   int
}
type ingressAuthenticationKey struct{}

type ingressPayloadStore interface {
	Stage(context.Context, guid.ID, guid.ID, guid.ID, []byte) (*files.PreparedIntegrationPayload, error)
	AttachOn(context.Context, *gorm.DB, *files.PreparedIntegrationPayload) (guid.ID, error)
	OpenOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (io.ReadCloser, error)
}

type IngressReceipt struct {
	ID       guid.ID `json:"id"`
	State    string  `json:"state" enum:"RECEIVED,PROCESSED,IGNORED,FAILED"`
	Replayed bool    `json:"replayed"`
}

func (m *Module) bindIngress(s *module.Services) error {
	var err error
	store, err := module.Resolve[*files.IntegrationPayloads](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if store != nil {
		if err = store.Bind(m.authorizeIngressPayloadOn); err != nil {
			return err
		}
		m.ingressFiles = store
	}
	m.ingressQueue, err = module.Resolve[jobs.Queue](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.ingressAdmission, err = module.Resolve[jobs.QueueAdmission](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	return err
}

// The unexported marker is created only after checking the source credential.
// Credential rotation/disable fences staging, attachment and worker reads.
func (m *Module) authorizeIngressPayloadOn(ctx context.Context, tx *gorm.DB, tenant, connection, owner guid.ID) error {
	a, ok := ctx.Value(ingressAuthenticationKey{}).(ingressAuthentication)
	if !ok || a.tenant != tenant || a.connection != connection || a.owner != owner {
		return ErrForbidden
	}
	r, err := m.connectionOn(ctx, tx, tenant, connection)
	if err != nil {
		return err
	}
	if r.CreatedBy != owner || r.Version != a.version || r.State == "DISABLED" {
		return ErrForbidden
	}
	return nil
}

func ingressDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (m *Module) authenticateIngressOn(ctx context.Context, tx *gorm.DB, connection guid.ID, source, signature string, raw []byte) (*model.Connection, context.Context, error) {
	q := query.BindConnections(tx, m.names)
	r, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(connection)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ctx, ErrIngressSignature
	}
	if err != nil {
		return nil, ctx, err
	}
	if r.State == "DISABLED" || r.WebhookSecretSealed == "" || r.CreatedBy <= 0 {
		return nil, ctx, ErrIngressSignature
	}
	ring, err := m.keys()
	if err != nil {
		return nil, ctx, err
	}
	secret, err := ring.Open("webhook", []byte(r.TenantID.String()+":"+r.ID.String()), r.WebhookSecretSealed)
	if err != nil {
		return nil, ctx, ErrUnavailable
	}
	defer clear(secret)
	valid := false
	switch source {
	case "CODEUP":
		// Hash both tokens to avoid token-length dependent comparison timing.
		expected, actual := sha256.Sum256(secret), sha256.Sum256([]byte(signature))
		valid = len(secret) > 0 && subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
	case "FLOW":
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(raw)
		actual, decodeErr := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
		valid = decodeErr == nil && len(secret) > 0 && hmac.Equal(mac.Sum(nil), actual)
	}
	if !valid {
		return nil, ctx, ErrIngressSignature
	}
	return r, context.WithValue(ctx, ingressAuthenticationKey{}, ingressAuthentication{r.TenantID, r.ID, r.CreatedBy, r.Version}), nil
}

func ingressKind(source string, raw []byte) (string, error) {
	var header struct {
		Kind     string `json:"object_kind"`
		Pipeline string `json:"pipeline_id"`
		Run      string `json:"run_id"`
		Commit   string `json:"commit"`
	}
	if !json.Valid(raw) || json.Unmarshal(raw, &header) != nil {
		return "", ErrInvalid
	}
	if source == "FLOW" {
		if strings.TrimSpace(header.Pipeline) == "" || !textValid(header.Pipeline, 200) || strings.TrimSpace(header.Run) == "" || !textValid(header.Run, 128) || !ingressCommit.MatchString(header.Commit) {
			return "", ErrInvalid
		}
		return "pipeline", nil
	}
	switch header.Kind {
	case "push", "merge_request", "note":
		return header.Kind, nil
	}
	return "", ErrInvalid
}

func (m *Module) receiveIngress(ctx context.Context, connection guid.ID, source, delivery, signature string, raw []byte) (IngressReceipt, error) {
	var out IngressReceipt
	if connection <= 0 || (source != "CODEUP" && source != "FLOW") || strings.TrimSpace(delivery) == "" || !textValid(delivery, 128) || strings.ContainsAny(delivery, "\r\n") || len(raw) == 0 || len(raw) > 1<<20 {
		return out, ErrInvalid
	}
	db := m.database()
	if db == nil {
		return out, ErrUnavailable
	}
	var conn *model.Connection
	var signed context.Context
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		conn, signed, err = m.authenticateIngressOn(ctx, tx, connection, source, signature, raw)
		return err
	})
	if err != nil {
		return out, err
	}
	kind, err := ingressKind(source, raw)
	if err != nil {
		return out, err
	}
	if m.ingressFiles == nil || m.ingressQueue == nil || m.ingressAdmission == nil || m.audit == nil || m.events == nil {
		return out, ErrUnavailable
	}
	// Check replay before staging. The final transaction repeats this check under
	// the connection fence; concurrent losers leave only expiry-tracked staging.
	lookup := func(tx *gorm.DB) (*model.InboundEvent, error) {
		q := query.BindInboundEvents(tx, m.names)
		r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ConnectionID.Eq(connection), q.Source.Eq(source), q.DeliveryKey.Eq(delivery)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return r, e
	}
	check := func(r *model.InboundEvent) error {
		if r.PayloadHash != ingressDigest(raw) || r.TenantID != conn.TenantID || !r.SignatureOK {
			return ErrConflict
		}
		out = IngressReceipt{r.ID, r.State, true}
		return nil
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeIngressPayloadOn(signed, tx, conn.TenantID, connection, conn.CreatedBy); e != nil {
			return e
		}
		r, e := lookup(tx)
		if e != nil {
			return e
		}
		if r != nil {
			return check(r)
		}
		return nil
	})
	if err != nil || out.Replayed {
		return out, err
	}
	prepared, err := m.ingressFiles.Stage(signed, conn.TenantID, connection, conn.CreatedBy, raw)
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// All producers acquire queue quota before aggregate locks.
		if e := m.ingressAdmission(ctx, tx, conn.TenantID); e != nil {
			return e
		}
		current, _, e := m.authenticateIngressOn(ctx, tx, connection, source, signature, raw)
		if e != nil {
			return e
		}
		if current.Version != conn.Version {
			return ErrConflict
		}
		r, e := lookup(tx)
		if e != nil {
			return e
		}
		if r != nil {
			return check(r)
		}
		id, e := m.nextID()
		if e != nil {
			return e
		}
		file, e := m.ingressFiles.AttachOn(signed, tx, prepared)
		if e != nil {
			return e
		}
		now := m.now()
		r = &model.InboundEvent{ID: id, TenantID: conn.TenantID, Version: 1, CreatedAt: now, UpdatedAt: now, ConnectionID: connection, Source: source, EventKind: kind, DeliveryKey: delivery, SignatureOK: true, PayloadHash: ingressDigest(raw), PayloadFileID: file, State: "RECEIVED"}
		if e = query.BindInboundEvents(tx, m.names).WithContext(ctx).Create(r); e != nil {
			return e
		}
		if _, e = m.ingressQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: conn.TenantID, Kind: "yunxiao.ingress", Payload: map[string]any{"event_id": id.String()}, IdempotencyKey: "ingress:" + id.String(), ConcurrencyKey: "ingress:" + connection.String()}); e != nil {
			return e
		}
		if e = m.ingressChangedOn(ctx, tx, r, "integration.ingress_received"); e != nil {
			return e
		}
		out = IngressReceipt{id, "RECEIVED", false}
		return nil
	})
	return out, err
}

func (m *Module) ingressChangedOn(ctx context.Context, tx *gorm.DB, row *model.InboundEvent, action string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	if err := m.audit(ctx, tx, audit.DomainEvent{TenantID: row.TenantID, EventType: action, TargetType: "integration_ingress", TargetID: row.ID, Detail: map[string]any{"connection_id": row.ConnectionID.String(), "source": row.Source, "payload_hash": row.PayloadHash, "system_write": true}}); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, Type: "integration.ingress_changed", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "integration_ingress", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"state": row.State, "source": row.Source}})
	return err
}

func ingressHandler(m *Module) jobs.Handler {
	return jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_yunxiao_ingress_name", DescriptionKey: activityi18n.Namespace + ".activity_job_yunxiao_ingress_description", Kind: "yunxiao.ingress", Queue: "default", Timeout: time.Minute, MaxAttempts: 5, BackoffBase: time.Second, BackoffMax: time.Minute, Run: m.processIngressJob}
}
