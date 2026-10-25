package templates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func teamHarness(t *testing.T) (*Module, *gorm.DB, context.Context, StudioContent, *delivery.TeamWorkFact) {
	t.Helper()
	m, db, ctx := bindStudio(t, "TEAM_TASKS")
	m.teamHuman = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	started := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	author, owner := guid.ID(11), guid.ID(12)
	work := &delivery.TeamWorkFact{ID: 700, Title: "Canonical work", State: "IN_PROGRESS", Risk: "MEDIUM", OwnerID: owner, AuthorID: &author, Version: 3, StartedAt: started}
	m.teamWork = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) (delivery.TeamWorkFact, error) {
		if tenant != 1 || project != 99 || (id != 0 && id != 700) {
			return delivery.TeamWorkFact{}, ErrNotFound
		}
		return *work, nil
	}
	modified := started.Add(time.Hour)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !assert.Equal(t, http.MethodHead, r.Method) {
			return
		}
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	m.teamEvidenceClient = server.Client()
	due := time.Now().UTC().Add(time.Hour)
	c := StudioContent{Title: "Deliver report", OwnerID: &owner, DueAt: &due, Team: &TeamTaskData{WorkItemID: 700, SignerID: 13, Checklist: []TeamChecklistItem{{Text: "Report supplied", Done: true, EvidenceIndex: 0}}, Evidence: []TeamEvidence{{URL: server.URL, Version: "v2", ModifiedAt: modified}}, Risks: []TeamRisk{}}}
	// Creation syntax uses the public guarded URL; the fixture transport redirects
	// the actual HEAD to a real TLS server, not a fabricated successful receipt.
	c.Team.Evidence[0].URL = "https://evidence.example.invalid/report"
	fixtureClient := server.Client()
	originalTransport := fixtureClient.Transport
	fixtureClient.Transport = teamTransport{target: server.URL, transport: originalTransport}
	m.teamEvidenceClient = fixtureClient
	return m, db, ctx, c, work
}

type teamTransport struct {
	target    string
	transport http.RoundTripper
}

func (r teamTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	target, _ := http.NewRequest(http.MethodHead, r.target, nil)
	clone.URL = target.URL
	return r.transport.RoundTrip(clone)
}
func editTeam(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID, in TeamEdit) TeamTaskView {
	t.Helper()
	var out TeamTaskView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.teamEditOn(ctx, tx, 99, id, in); return e }))
	return out
}
func TestTeamBoardIndependentSignoffPinsActualWorkAndPreservesHistory(t *testing.T) {
	m, db, ctx, c, work := teamHarness(t)
	created := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Link canonical task", Content: c})
	require.Equal(t, "IN_PROGRESS", created.Work.State)
	require.False(t, created.Signed)
	signer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 13, SubjectType: authn.SubjectTypePrincipal})
	signed := editTeam(t, m, db, signer, created.Record.ID, TeamEdit{Version: 1, Action: "SIGN_OFF", Reason: "Report content and scope reviewed"})
	require.True(t, signed.Signed)
	require.Len(t, signed.Record.Content.Team.SignOffs, 1)
	require.Equal(t, guid.ID(13), signed.Record.Content.Team.SignOffs[0].ActorID)
	require.NotEmpty(t, signed.Record.Content.Team.SignOffs[0].ContentHash)
	require.NotNil(t, signed.Record.Content.Team.Validation)
	work.Version++
	page, e := m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 50, Sort: "id"})
	require.NoError(t, e)
	require.False(t, page.Body.Data.Items[0].Signed)
	require.Equal(t, work.Version, page.Body.Data.Items[0].Work.Version)
	revised := signed.Record.Content
	revised.Team.Checklist[0].Text = "Revised report"
	revised.Team.SignOffs = []TeamSignOff{{ActorID: 999, Scope: "forged"}}
	saved := editTeam(t, m, db, ctx, created.Record.ID, TeamEdit{Version: 2, Action: "SAVE", Reason: "Scope changed", Content: revised})
	require.False(t, saved.Signed)
	require.Nil(t, saved.Record.Content.Team.Validation)
	require.Equal(t, guid.ID(13), saved.Record.Content.Team.SignOffs[0].ActorID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, created.Record.ID, TeamEdit{Version: 1, Action: "SAVE", Reason: "Stale", Content: c})
		return e
	}), ErrConflict)
}
func TestTeamBoardSignoffRejectsIncompleteEvidenceRiskAndSelf(t *testing.T) {
	cases := []string{"author", "owner", "wrong_signer", "unchecked", "open_risk", "old_version", "old_time", "missing_evidence", "cancelled", "changed_owner", "nonhuman"} //nolint:misspell // Preserve the published domain or serialized contract spelling.
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			m, db, ctx, c, work := teamHarness(t)
			actor := guid.ID(13)
			switch name {
			case "author":
				c.Team.SignerID = 11
				actor = 11
			case "owner":
				c.Team.SignerID = 12
				actor = 12
			case "wrong_signer":
				actor = 14
			case "unchecked":
				c.Team.Checklist[0].Done = false
			case "open_risk":
				c.Team.Risks = []TeamRisk{{Title: "Unreviewed scope", OwnerID: 12, DueAt: time.Now().UTC().Add(time.Hour)}}
			case "old_version":
				c.Team.Evidence[0].Version = "v1"
			case "old_time":
				c.Team.Evidence[0].ModifiedAt = work.StartedAt.Add(-time.Second)
			case "missing_evidence":
				c.Team.Evidence = nil
				c.Team.Checklist[0].Done = false
			case "cancelled": //nolint:misspell // Preserve the published domain or serialized contract spelling.
				work.State = "CANCELLED" //nolint:misspell // Preserve the published domain or serialized contract spelling.
			}
			created := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Record", Content: c})
			if name == "changed_owner" {
				work.OwnerID = 15
			}
			claims := &authn.Claims{TenantID: 1, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal}
			if name == "nonhuman" {
				claims.SubjectType = authn.SubjectTypeServiceAccount
			}
			actorCtx := authn.WithClaims(ctx, claims)
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.teamEditOn(actorCtx, tx, 99, created.Record.ID, TeamEdit{Version: 1, Action: "SIGN_OFF", Reason: "Review"})
				return e
			}))
			row, e := m.studioRecordOn(ctx, db, created.Record.BindingID, created.Record.ID)
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			require.Equal(t, "CONFIRMED", row.State)
		})
	}
}
func TestTeamBoardURLChecksObserveActualHeadersAndGuardPrivateTargets(t *testing.T) {
	modified := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for _, name := range []string{"valid", "missing_etag", "wrong_time", "not_found"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name != "missing_etag" {
					w.Header().Set("ETag", `"v2"`)
				}
				observed := modified
				if name == "wrong_time" {
					observed = modified.Add(-time.Hour)
				}
				w.Header().Set("Last-Modified", observed.Format(http.TimeFormat))
				status := 200
				if name == "not_found" {
					status = 404
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			e := checkTeamEvidence(t.Context(), server.Client(), TeamEvidence{URL: server.URL, Version: "v2", ModifiedAt: modified}, modified.Add(-time.Hour))
			if name == "valid" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, ErrTeamEvidence)
			}
		})
	}
	client := httpx.NewClient(time.Second, httpx.Policy{})
	defer client.CloseIdleConnections()
	require.Error(t, checkTeamEvidence(t.Context(), client, TeamEvidence{URL: "https://127.0.0.1/secret", Version: "v1", ModifiedAt: modified}, modified.Add(-time.Hour)))
}
func TestTeamBoardAuditAndOutboxFailuresRollback(t *testing.T) {
	for _, kind := range []string{"audit", "outbox"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx, c, _ := teamHarness(t)
			created := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Record", Content: c})
			failure := errors.New("owner storage failed")
			if kind == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			} else {
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			}
			signer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 13, SubjectType: authn.SubjectTypePrincipal})
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.teamEditOn(signer, tx, 99, created.Record.ID, TeamEdit{Version: 1, Action: "SIGN_OFF", Reason: "Review"})
				return e
			}), failure)
			row, e := m.studioRecordOn(ctx, db, created.Record.BindingID, created.Record.ID)
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			view, e := studioView(row)
			require.NoError(t, e)
			require.Empty(t, view.Content.Team.SignOffs)
		})
	}
}

func TestTeamBoardCreatesCanonicalWorkAndRejectsSourceFailures(t *testing.T) {
	m, db, ctx, c, _ := teamHarness(t)
	c.Team.WorkItemID = 0
	c.Team.Risks = nil
	calls := 0
	m.delivery.CreateWorkOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, in delivery.TemplateWorkInput) (guid.ID, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, c.Title, in.Title)
		require.Len(t, in.Criteria, 1)
		calls++
		return 700, nil
	}
	created := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Create canonical work", Content: c})
	require.Equal(t, 1, calls)
	require.Equal(t, guid.ID(700), created.Work.ID)
	require.Equal(t, guid.ID(700), created.Record.Content.Team.WorkItemID)
	denied := errors.New("work source unavailable")
	m.teamWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.TeamWorkFact, error) {
		return delivery.TeamWorkFact{}, denied
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, 0, TeamEdit{Action: "SAVE", Reason: "Source required", Content: c})
		return e
	}), denied)
	m.teamWork = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.teamWorkFactOn(ctx, tx, 99, 700); return e }), ErrUnavailable)
	m.teamWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.TeamWorkFact, error) {
		return delivery.TeamWorkFact{}, gorm.ErrRecordNotFound
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.teamWorkFactOn(ctx, tx, 99, 700); return e }), ErrNotFound)
}
func TestTeamBoardValidationStorageAndParticipantLockFailures(t *testing.T) {
	for _, failure := range []string{"invalid-title", "risk", "checklist", "url", "human-missing", "human-denied", "lock", "create", "update"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, c, _ := teamHarness(t)
			created := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Initial", Content: c})
			expected := ErrInvalid
			injected := errors.New("team storage unavailable")
			switch failure {
			case "invalid-title":
				c.Title = ""
			case "risk":
				c.Team.Risks = []TeamRisk{{Title: "Risk"}}
			case "checklist":
				c.Team.Checklist[0].Text = ""
			case "url":
				c.Team.Evidence[0].URL = "https://user:password@example.invalid/path"
			case "human-missing":
				m.teamHuman = nil
				expected = ErrUnavailable
			case "human-denied":
				m.teamHuman = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return ErrForbidden }
				expected = ErrForbidden
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
				expected = injected
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("team-create-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.StudioRecords.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Create().Remove("team-create-failure")
				expected = injected
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("team-update-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.StudioRecords.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Update().Remove("team-update-failure")
				expected = injected
			}
			id := guid.ID(0)
			version := 0
			if failure == "update" {
				id = created.Record.ID
				version = 1
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.teamEditOn(ctx, tx, 99, id, TeamEdit{Action: "SAVE", Version: version, Reason: "Must rollback", Content: c})
				return e
			}), expected)
			var count int64
			require.NoError(t, db.Table(m.names.StudioRecords.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestTeamBoardDraftAndTaskActionBoundaries(t *testing.T) {
	m, db, ctx, c, _ := teamHarness(t)
	task := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Initial", Content: c})
	for _, in := range []TeamEdit{{Action: "SAVE"}, {Action: "SIGN_OFF", Reason: "No task"}, {Action: "SAVE", Version: 1, Reason: "New cannot have revision"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.teamEditOn(ctx, tx, 99, 0, in); return e }), ErrInvalid)
	}
	for _, action := range []string{"EDIT_DRAFT", "ADOPT_DRAFT"} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.teamEditOn(ctx, tx, 99, task.Record.ID, TeamEdit{Version: 1, Action: action, Reason: "Task is not draft", Content: c})
			return e
		}), ErrConflict)
	}
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Suggestion", Text: "Proposed", Source: "source:1"}})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, draft.ID, TeamEdit{Version: 1, Action: "SAVE", Reason: "Requires adopt", Content: c})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, draft.ID, TeamEdit{Version: 1, Action: "EDIT_DRAFT", Reason: "Missing title"})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, draft.ID, TeamEdit{Version: 1, Action: "CHECK_EVIDENCE", Reason: "Not a task"})
		return e
	}), ErrInvalid)
	c.Team.WorkItemID = 0
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, task.Record.ID, TeamEdit{Version: 1, Action: "SAVE", Reason: "Cannot detach work", Content: c})
		return e
	}), ErrInvalid)
	c.Team.WorkItemID = 700
	c.Team.Risks = []TeamRisk{{Title: "Invalid risk"}}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.validateTeamContentOn(ctx, tx, 99, c); return e }), ErrInvalid)
	require.NoError(t, db.Table(m.names.StudioRecords.String()).Where("id=?", task.Record.ID).Update("payload", "invalid").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, task.Record.ID, TeamEdit{Version: 1, Action: "SIGN_OFF", Reason: "Corrupt source"})
		return e
	}))
}
