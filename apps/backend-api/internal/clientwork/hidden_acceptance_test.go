package clientwork

import (
	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type hiddenTestProjects struct {
	*testProjects
	revoked bool
}

func (p *hiddenTestProjects) LockProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if permission == "acceptance_suite_manage" && (p.revoked || authn.PrincipalIDFromContext(ctx) != 2) {
		return ErrNotFound
	}
	return p.testProjects.LockProjectOn(ctx, tx, tenant, project, permission)
}

func TestHiddenAcceptanceReadWriteAndSnapshotBoundary(t *testing.T) {
	m, db, projects, _ := harness(t)
	access := &hiddenTestProjects{testProjects: projects}
	m.projects = access
	owner, reader := testContext(t, 2), testContext(t, 6)
	in := TestCaseBody{Title: "SECRET-TITLE", Steps: "SECRET-STEPS", Expected: "SECRET-EXPECTED", WorkItemID: 11, IsHiddenAcceptance: true}
	var public Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		public, err = m.writeCaseOn(owner, tx, 3, 0, 0, TestCaseBody{Title: "Public", Steps: "Public steps", Expected: "Public result", WorkItemID: 11})
		return err
	}))
	var hidden Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		hidden, err = m.writeCaseOn(owner, tx, 3, 0, 0, in)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(reader, tx, 3, 0, 0, in); return err }), ErrNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(reader, tx, 3, hidden.ID, 1, in); return err }), ErrNotFound)
	publicBody := in
	publicBody.IsHiddenAcceptance = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(owner, tx, 3, hidden.ID, 1, publicBody); return err }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(owner, tx, 3, public.ID, 1, in); return err }), ErrConflict)
	_, err := m.get_test_caseHTTP(reader, &ObjectInput{EntityID: "3", ID: hidden.ID.String()})
	require.Error(t, err)
	for _, actor := range []guid.ID{6, 7, 4} {
		_, err := m.get_test_caseHTTP(testContext(t, actor), &ObjectInput{EntityID: "3", ID: hidden.ID.String()})
		require.Error(t, err)
	}
	visible, err := m.get_test_caseHTTP(owner, &ObjectInput{EntityID: "3", ID: hidden.ID.String()})
	require.NoError(t, err)
	require.Equal(t, in.Steps, visible.Body.Data.Steps)
	page, err := m.list_test_caseHTTP(reader, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, public.ID, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	require.Empty(t, page.Body.Data.NextCursor)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createPlanOn(owner, tx, 3, TestPlanBody{Title: "Shared", CaseIDs: []guid.ID{hidden.ID}})
		return err
	}), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.requirementCasesOn(reader, tx, 1, 3, []guid.ID{11})
		if err != nil {
			return err
		}
		require.Len(t, rows, 2)
		require.True(t, rows[1].IsHiddenAcceptance)
		require.Len(t, rows[1].ContentHash, 64)
		data, err := json.Marshal(rows)
		require.NoError(t, err)
		require.NotContains(t, string(data), "SECRET-")
		return nil
	}))
	create := &CreateInput[TestCaseBody]{EntityID: "3", Key: "hidden-case-replay", Body: in}
	_, err = m.create_test_caseHTTP(owner, create)
	require.NoError(t, err)
	access.revoked = true
	_, err = m.create_test_caseHTTP(owner, create)
	require.Error(t, err)
	_, err = m.get_test_caseHTTP(owner, &ObjectInput{EntityID: "3", ID: hidden.ID.String()})
	require.Error(t, err)
}

func TestHiddenSnapshotHashBindsContentAndVersion(t *testing.T) {
	row := &model.TestCase{ID: 10, Version: 1, WorkItemID: 11, Title: "Secret", Steps: "Private steps", Expected: "Private result", IsHiddenAcceptance: true}
	first := requirementCaseSnapshot(row)
	require.Equal(t, first, requirementCaseSnapshot(row))
	row.Steps += " changed"
	require.NotEqual(t, first.ContentHash, requirementCaseSnapshot(row).ContentHash)
	row.Version++
	require.NotEqual(t, first.ContentHash, requirementCaseSnapshot(row).ContentHash)
	data, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(data), "Private")
}

func TestHiddenAcceptanceDoesNotLogContentOnSuccessOrFailure(t *testing.T) {
	m, db, _, _ := harness(t)
	var output bytes.Buffer
	logged := db.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
	in := TestCaseBody{Title: "SECRET-TITLE", Steps: "SECRET-STEPS", Expected: "SECRET-EXPECTED", WorkItemID: 11, IsHiddenAcceptance: true}
	require.NoError(t, logged.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(testContext(t, 2), tx, 3, 0, 0, in); return err }))
	require.NoError(t, db.Exec("CREATE TRIGGER deny_case BEFORE INSERT ON g_clientwork_test_cases BEGIN SELECT RAISE(ABORT, 'write denied'); END").Error)
	require.Error(t, logged.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(testContext(t, 2), tx, 3, 0, 0, in); return err }))
	require.NotContains(t, output.String(), "SECRET-")
}

func TestHiddenCaseDomainAuditAndRealtimeEnvelopeContainOnlyMetadata(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	audits := []audit.DomainEvent{}
	published := []events.Event{}
	originalAudit := m.audit
	originalPublisher := m.events
	m.audit = func(ctx context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		audits = append(audits, e)
		return originalAudit(ctx, tx, e)
	}
	m.events = expiryPublisher{Publisher: originalPublisher, publish: func(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		published = append(published, e)
		return originalPublisher.Publish(ctx, tx, e)
	}}
	in := TestCaseBody{Title: "PRIVATE-TITLE", Steps: "PRIVATE-STEPS", Expected: "PRIVATE-EXPECTED", WorkItemID: 11, IsHiddenAcceptance: true}
	var result Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; result, e = m.writeCaseOn(ctx, tx, 3, 0, 0, in); return e }))
	in.Steps = "PRIVATE-UPDATED"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.writeCaseOn(ctx, tx, 3, result.ID, 1, in); return e }))
	require.Len(t, audits, 2)
	require.Len(t, published, 2)
	raw, e := json.Marshal(struct {
		Audit  []audit.DomainEvent
		Events []events.Event
	}{audits, published})
	require.NoError(t, e)
	require.NotContains(t, string(raw), "PRIVATE-")
	for _, event := range published {
		require.Equal(t, "clientwork.test_case_written", event.Type)
		require.Len(t, event.Data.(map[string]any), 1)
	}
}

type hiddenTransportSink struct{ events []transportaudit.Event }

func (s *hiddenTransportSink) Write(_ context.Context, events []transportaudit.Event) error {
	s.events = append(s.events, events...)
	return nil
}
func TestHiddenTransportMiddlewareOmitsDeniedAndMalformedBodies(t *testing.T) {
	for _, actor := range []guid.ID{4, 2} {
		t.Run(actor.String(), func(t *testing.T) {
			m, _, _, _ := harness(t)
			m.verifier = testVerifier{actor}
			m.owner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{TenantID: 1, EntityID: 3}, true, nil
			}
			sink := &hiddenTransportSink{}
			capture := transportaudit.NewModule(appenv.NewRuntime(appenv.NewIdentity("api", "")), transportaudit.Config{Enabled: true, CaptureBodies: true, MaxBodyBytes: 2048, QueueSize: 10, BatchSize: 1, FlushInterval: time.Millisecond, FlushTimeout: time.Second}, transportaudit.WithSinks(sink))
			require.NoError(t, capture.Start(t.Context()))
			called := false
			var decodeErr error
			endpoint := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				var body TestCaseBody
				decodeErr = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(http.StatusUnprocessableEntity)
			})
			handler := capture.HTTPMiddleware(m.HTTPMiddleware(endpoint))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/entities/3/test-cases", strings.NewReader(`{"title":"PRIVATE-TITLE","steps":"PRIVATE-STEPS","expected":"PRIVATE-EXPECTED",`)))
			if actor == 4 {
				require.False(t, called)
				require.Equal(t, 404, response.Code)
			} else {
				require.True(t, called)
				require.Error(t, decodeErr)
				require.Equal(t, 422, response.Code)
			}
			require.NoError(t, capture.Stop(context.Background()))
			require.Len(t, sink.events, 1)
			raw, err := json.Marshal(sink.events)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "PRIVATE-")
			require.Contains(t, string(raw), "confidential_resource")
		})
	}
}
