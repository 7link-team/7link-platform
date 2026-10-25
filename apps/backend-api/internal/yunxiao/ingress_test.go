package yunxiao

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type ingressTestFiles struct {
	m      *Module
	db     *gorm.DB
	raw    []byte
	staged int
	err    error
}

func (f *ingressTestFiles) Stage(ctx context.Context, t, c, o guid.ID, raw []byte) (*files.PreparedIntegrationPayload, error) {
	err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.authorizeIngressPayloadOn(ctx, tx, t, c, o) })
	if err != nil {
		return nil, err
	}
	f.staged++
	f.raw = append([]byte(nil), raw...)
	return &files.PreparedIntegrationPayload{}, f.err
}
func (f *ingressTestFiles) AttachOn(_ context.Context, tx *gorm.DB, _ *files.PreparedIntegrationPayload) (guid.ID, error) {
	return 901, tx.Exec("INSERT INTO ingress_test_files(id,content) VALUES(?,?)", 901, string(f.raw)).Error
}
func (f *ingressTestFiles) OpenOn(ctx context.Context, tx *gorm.DB, t, c, o, id guid.ID) (io.ReadCloser, error) {
	if e := f.m.authorizeIngressPayloadOn(ctx, tx, t, c, o); e != nil {
		return nil, e
	}
	var raw string
	e := tx.Raw("SELECT content FROM ingress_test_files WHERE id=?", id).Scan(&raw).Error
	return io.NopCloser(strings.NewReader(raw)), e
}

type ingressTestQueue struct {
	err      error
	admitted *bool
}

func (q ingressTestQueue) Enqueue(_ context.Context, tx *gorm.DB, r jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	if q.err != nil {
		return jobs.EnqueueResult{}, q.err
	}
	if !*q.admitted {
		return jobs.EnqueueResult{}, ErrConflict
	}
	raw, e := json.Marshal(r.Payload)
	if e != nil {
		return jobs.EnqueueResult{}, e
	}
	return jobs.EnqueueResult{JobID: 1, Created: true}, tx.Exec("INSERT INTO ingress_test_jobs(kind,payload) VALUES(?,?)", r.Kind, string(raw)).Error
}
func (q ingressTestQueue) EnqueueBatch(context.Context, *gorm.DB, []jobs.EnqueueRequest) ([]jobs.EnqueueResult, error) {
	return nil, ErrUnavailable
}
func (q ingressTestQueue) Cancel(context.Context, *gorm.DB, guid.ID, string, string) (bool, error) {
	return false, ErrUnavailable
}

func ingressFixture(t *testing.T) (*Module, *gorm.DB, context.Context, ConnectionView, *ingressTestFiles) {
	m, db, ctx := configHarness(t)
	c := createConnection(t, m, db, ctx)
	require.NoError(t, db.Exec("CREATE TABLE ingress_test_files(id BIGINT PRIMARY KEY,content TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("CREATE TABLE ingress_test_jobs(kind TEXT NOT NULL,payload TEXT NOT NULL)").Error)
	f := &ingressTestFiles{m: m, db: db}
	m.ingressFiles = f
	admitted := false
	m.ingressAdmission = func(context.Context, *gorm.DB, guid.ID) error { admitted = true; return nil }
	m.ingressQueue = ingressTestQueue{admitted: &admitted}
	return m, db, ctx, c, f
}
func flowTestSignature(raw []byte) string {
	h := hmac.New(sha256.New, []byte(connectionBody().WebhookSecret))
	_, _ = h.Write(raw)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}
func codeupTestBody() []byte {
	return []byte(`{"object_kind":"merge_request","repository":{"full_name":"org/repo"},"object_attributes":{"title":"Fix #WI-142","last_commit":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`)
}
func flowTestBody() []byte {
	return []byte(`{"external_project_id":"external-1","pipeline_id":"P","run_id":"run-1","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage_results":[{"name":"integration","status":"PASSED","redlines":[{"metric":"escape_rate","op":"<=","threshold":0.01,"value":0.001,"passed":true}]}],"manual_gates":[{"name":"release","mode":"AND","approvers":["external-user"],"result":"PASSED"}]}`)
}

func TestIngressAuthenticationReplayCollisionAndDisable(t *testing.T) {
	m, db, ctx, c, f := ingressFixture(t)
	raw := codeupTestBody()
	for _, source := range []string{"CODEUP", "FLOW"} {
		_, e := m.receiveIngress(ctx, c.ID, source, "delivery", "wrong", raw)
		require.ErrorIs(t, e, ErrIngressSignature)
	}
	require.Zero(t, f.staged)
	first, e := m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, raw)
	require.NoError(t, e)
	require.Equal(t, "RECEIVED", first.State)
	require.False(t, first.Replayed)
	replayed, e := m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, raw)
	require.NoError(t, e)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, 1, f.staged)
	_, e = m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, append(raw, ' '))
	require.ErrorIs(t, e, ErrConflict)
	require.Equal(t, 1, f.staged)
	for _, table := range []string{m.names.InboundEvents.String(), "ingress_test_files", "ingress_test_jobs"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.EqualValues(t, 1, n)
	}
	row, e := query.BindInboundEvents(db, m.names).WithContext(ctx).First()
	require.NoError(t, e)
	require.Zero(t, row.CreatedBy)
	require.Zero(t, row.UpdatedBy)
	require.True(t, row.SignatureOK)
	require.Equal(t, ingressDigest(raw), row.PayloadHash)
	q := query.BindConnections(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(c.ID)).Update(q.State, "DISABLED")
	require.NoError(t, e)
	_, e = m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, raw)
	require.ErrorIs(t, e, ErrIngressSignature)
}

func TestIngressFlowHMACCoversExactBytesAndSource(t *testing.T) {
	m, db, ctx, c, f := ingressFixture(t)
	raw := flowTestBody()
	sig := flowTestSignature(raw)
	_, e := m.receiveIngress(ctx, c.ID, "FLOW", "flow", sig, append(raw, ' '))
	require.ErrorIs(t, e, ErrIngressSignature)
	_, e = m.receiveIngress(ctx, c.ID, "CODEUP", "flow", sig, raw)
	require.ErrorIs(t, e, ErrIngressSignature)
	require.Zero(t, f.staged)
	v, e := m.receiveIngress(ctx, c.ID, "FLOW", "flow", sig, raw)
	require.NoError(t, e)
	var jobRaw string
	require.NoError(t, db.Raw("SELECT payload FROM ingress_test_jobs").Scan(&jobRaw).Error)
	require.NotContains(t, jobRaw, "external-user")
	require.NotContains(t, jobRaw, connectionBody().WebhookSecret)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, mappingBody()); return e }))
	job := jobs.Job{TenantID: 1, Attempt: 1, MaxAttempts: 5, Payload: json.RawMessage(jobRaw)}
	require.NoError(t, m.processIngressJob(ctx, job))
	require.NoError(t, m.processIngressJob(ctx, job))
	logs, e := query.BindIngressObservations(db, m.names).WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, logs, 1)
	require.Equal(t, "FLOW", logs[0].Source)
	require.Equal(t, ingressDigest([]byte(logs[0].Detail)), logs[0].ContentHash)
	var observation IngressObservation
	require.NoError(t, json.Unmarshal([]byte(logs[0].Detail), &observation))
	require.Equal(t, v.ID, observation.EventID)
	require.False(t, observation.TrustedVerification)
	require.False(t, observation.LocalAcceptance)
	require.Equal(t, "EXTERNAL:YUNXIAO_FLOW:P", observation.AuthoritySource)
	require.Len(t, observation.ManualGates, 1)
	require.Equal(t, "AND", observation.ManualGates[0].Mode)
	require.Len(t, observation.StageResults, 1)
}

func TestIngressRollsBackLedgerFileQueueAndAuditTogether(t *testing.T) {
	for _, failure := range []string{"queue", "audit", "outbox"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, c, _ := ingressFixture(t)
			boom := errors.New("failed durable effect")
			var before int64
			require.NoError(t, db.Table("test_effects").Count(&before).Error)
			switch failure {
			case "queue":
				m.ingressQueue = ingressTestQueue{err: boom}
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return boom }
			case "outbox":
				m.events = testPublisher{err: boom}
			}
			_, e := m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, codeupTestBody())
			require.ErrorIs(t, e, boom)
			for _, table := range []string{m.names.InboundEvents.String(), "ingress_test_files", "ingress_test_jobs"} {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
			var after int64
			require.NoError(t, db.Table("test_effects").Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestIngressInvalidBodyAndMissingCapabilitiesNeverAcknowledge(t *testing.T) {
	m, _, ctx, c, f := ingressFixture(t)
	for _, raw := range [][]byte{[]byte(`null`), []byte(`{"object_kind":"unknown"}`), []byte(`{`)} {
		_, e := m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, raw)
		require.ErrorIs(t, e, ErrInvalid)
	}
	require.Zero(t, f.staged)
	m.ingressFiles = nil
	_, e := m.receiveIngress(ctx, c.ID, "CODEUP", "delivery", connectionBody().WebhookSecret, codeupTestBody())
	require.ErrorIs(t, e, ErrUnavailable)
}
