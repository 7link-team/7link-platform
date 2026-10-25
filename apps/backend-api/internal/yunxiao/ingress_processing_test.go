package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIngressWorkerCurrentAuthorityRetryTamperAndMappingScope(t *testing.T) {
	for _, failure := range []string{"revoked", "paused", "tampered", "unknown_repository"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, c, _ := ingressFixture(t)
			input := mappingBody()
			input.CodeupRepositories = []string{"org/repo"}
			var mapping MappingView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				mapping, e = m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, input)
				return e
			}))
			r, e := m.receiveIngress(ctx, c.ID, "CODEUP", "mr-1", connectionBody().WebhookSecret, codeupTestBody())
			require.NoError(t, e)
			jobRaw, _ := json.Marshal(map[string]string{"event_id": r.ID.String()})
			job := jobs.Job{TenantID: 1, Attempt: 1, MaxAttempts: 5, Payload: jobRaw}
			switch failure {
			case "revoked":
				m.members = testMembers(false)
			case "paused":
				q := query.BindProjectMappings(db, m.names)
				_, e = q.WithContext(ctx).Where(q.ID.Eq(mapping.ID)).Update(q.Paused, true)
				require.NoError(t, e)
			case "tampered":
				require.NoError(t, db.Exec("UPDATE ingress_test_files SET content=?", string(codeupTestBody())+" ").Error)
			case "unknown_repository":
				q := query.BindProjectMappings(db, m.names)
				_, e = q.WithContext(ctx).Where(q.ID.Eq(mapping.ID)).Update(q.CodeupRepositories, `["foreign/repo"]`)
				require.NoError(t, e)
			}
			e = m.processIngressJob(ctx, job)
			q := query.BindInboundEvents(db, m.names)
			row, getErr := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, getErr)
			if failure == "unknown_repository" {
				require.NoError(t, e)
				require.Equal(t, "IGNORED", row.State)
				return
			}
			require.Error(t, e)
			require.Equal(t, "FAILED", row.State, "BR-12-30 persists each failure; the job queue owns retry exhaustion")
			job.Attempt = 5
			require.Error(t, m.processIngressJob(ctx, job))
			row, e = q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "FAILED", row.State)
			require.Equal(t, "RETRIES_EXHAUSTED", *row.Error)
			require.Nil(t, row.ProcessedAt)
			count, e := query.BindIngressObservations(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestIngressWorkerReferenceAssociationAndObservationReadReauthorization(t *testing.T) {
	m, db, ctx, c, _ := ingressFixture(t)
	input := mappingBody()
	input.CodeupRepositories = []string{"org/repo"}
	var mapping MappingView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		mapping, e = m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, input)
		return e
	}))
	r, e := m.receiveIngress(ctx, c.ID, "CODEUP", "mr-1", connectionBody().WebhookSecret, codeupTestBody())
	require.NoError(t, e)
	raw, _ := json.Marshal(map[string]string{"event_id": r.ID.String()})
	job := jobs.Job{TenantID: 1, Attempt: 1, MaxAttempts: 5, Payload: raw}
	require.NoError(t, m.processIngressJob(ctx, job))
	in := &ingressObservationListInput{MappingPath: MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, MappingID: mapping.ID.String()}, Limit: 1}
	page, e := m.ingressObservationsHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	observation := page.Body.Data.Items[0].Observation
	require.Equal(t, []string{"#WI-142"}, observation.References)
	require.False(t, observation.TrustedVerification)
	require.False(t, observation.LocalAcceptance)
	m.members = testMembers(false)
	_, e = m.ingressObservationsHTTP(ctx, in)
	requireStatus(t, e, 403)
	m.members = testMembers(true)
	in.Cursor = pageCursor(1, mapping.ID+1, 1)
	_, e = m.ingressObservationsHTTP(ctx, in)
	requireStatus(t, e, 422)
	in.Cursor = ""
	q := query.BindIngressObservations(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(page.Body.Data.Items[0].ID)).Update(q.Detail, `{"trusted_verification":true}`)
	require.NoError(t, e)
	_, e = m.ingressObservationsHTTP(ctx, in)
	requireStatus(t, e, 503)
}

func TestIngressObservationParserPreservesDecimalsAndRejectsUnboundedSourceData(t *testing.T) {
	event := &model.InboundEvent{ID: 1, Source: "FLOW", EventKind: "pipeline"}
	raw := strings.Replace(string(flowTestBody()), `0.001`, `0.000000000000000001`, 1)
	v, e := parseIngressObservation(event, []byte(raw))
	require.NoError(t, e)
	require.Equal(t, "0.000000000000000001", string(v.StageResults[0].Redlines[0].Value))
	require.False(t, v.TrustedVerification)
	for _, bad := range []string{strings.Replace(raw, `"AND"`, `"NONE"`, 1), strings.Replace(raw, `"passed":true`, `"passed":null`, 1), strings.Replace(raw, `"external-1"`, `""`, 1), strings.Replace(raw, `"external-user"`, `""`, 1), strings.Replace(raw, `0.000000000000000001`, `1e10000000`, 1)} {
		_, e := parseIngressObservation(event, []byte(bad))
		require.Error(t, e)
	}
}

func TestIngressObservationParserEmitsEmptyNestedArrays(t *testing.T) {
	for _, field := range []string{"", `,"redlines":null`} {
		for _, approvers := range []string{"", `,"approvers":null`} {
			raw := `{"external_project_id":"external-1","stage_results":[{"name":"build","status":"PASSED"` + field + `}],"manual_gates":[{"name":"review","mode":"AND","result":"PENDING"` + approvers + `}]}`
			v, err := parseIngressObservation(&model.InboundEvent{ID: 1, Source: "FLOW"}, []byte(raw))
			require.NoError(t, err)
			require.NotNil(t, v.StageResults[0].Redlines)
			require.Empty(t, v.StageResults[0].Redlines)
			require.NotNil(t, v.ManualGates[0].Approvers)
			require.Empty(t, v.ManualGates[0].Approvers)
			encoded, err := json.Marshal(v)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"redlines":[]`)
			require.Contains(t, string(encoded), `"approvers":[]`)
		}
	}
}

func TestIngressWorkerStorageFailureNeverCommitsPartialObservation(t *testing.T) {
	for _, mode := range []string{"mapping_read", "project_missing", "project_denied", "id", "observation_insert", "event_update", "outbox", "missing_event"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, c, _ := ingressFixture(t)
			input := mappingBody()
			input.CodeupRepositories = []string{"org/repo"}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, input); return e }))
			receipt, e := m.receiveIngress(ctx, c.ID, "CODEUP", "worker-storage", connectionBody().WebhookSecret, codeupTestBody())
			require.NoError(t, e)
			payload, e := json.Marshal(map[string]string{"event_id": receipt.ID.String()})
			require.NoError(t, e)
			job := jobs.Job{TenantID: 1, Attempt: 1, MaxAttempts: 5, Payload: payload}
			switch mode {
			case "mapping_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("worker_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ProjectMappings.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("worker_storage")
			case "project_missing":
				m.projects.AuthorizeOn = nil
			case "project_denied":
				m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
			case "id":
				m.ids = &testIDs{err: ErrUnavailable}
			case "observation_insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("worker_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IngressObservations.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("worker_storage")
			case "event_update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("worker_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.InboundEvents.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("worker_storage")
			case "outbox":
				m.events = testPublisher{err: ErrUnavailable}
			case "missing_event":
				require.NoError(t, db.Table(m.names.InboundEvents.String()).Where("id=?", receipt.ID).Delete(map[string]any{}).Error)
			}
			require.Error(t, m.processIngressJob(ctx, job))
			var count int64
			require.NoError(t, db.Table(m.names.IngressObservations.String()).Count(&count).Error)
			require.Zero(t, count)
			if mode != "missing_event" {
				row, e := query.BindInboundEvents(db, m.names).WithContext(ctx).First()
				require.NoError(t, e)
				require.NotEqual(t, "PROCESSED", row.State)
				require.Nil(t, row.ProcessedAt)
			}
		})
	}
}
