package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/performance"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestTeamFactsPreserveCurrentMembershipAndStorageFailures(t *testing.T) {
	for _, fault := range []string{"ok", "human", "member", "policy", "scope", "hidden", "project", "work", "owner", "started", "no_work"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("team_mode", "HUMAN_ONLY").Error)
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			injected := errors.New("team source unavailable")
			want := error(nil)
			id := h.work
			at := time.Now().UTC().Truncate(time.Millisecond)
			switch fault {
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
				want = injected
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
				want = injected
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
				want = injected
			case "scope":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			case "hidden":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
				}
				want = ErrProjectNotFound
			case "owner":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = ErrWorkForbidden
			case "project", "work":
				table := h.m.service.names.WorkItems.String()
				if fault == "project" {
					table = h.m.service.names.Projects.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("team-read-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
				want = injected
			case "started":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("first_claimed_at", at).Error)
			case "no_work":
				id = 0
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				out, e := h.m.teamWorkOn(h.ctx, tx, 1, 99, id)
				if e == nil && id > 0 {
					require.Equal(t, h.work, out.ID)
					if fault == "started" {
						require.True(t, at.Equal(out.StartedAt))
					}
				}
				if id == 0 {
					require.Zero(t, out.ID)
				}
				return e
			})
			if fault == "scope" {
				require.Error(t, e)
			} else {
				require.ErrorIs(t, e, want)
			}
		})
	}
}

func TestArchiveCommandCurrentAuthorityFailuresRemainExplicit(t *testing.T) {
	for _, fault := range []string{"lock", "human", "member", "policy", "query", "missing", "history_scope", "history_query"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			injected := errors.New("archive authority unavailable")
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			want := injected
			switch fault {
			case "lock":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "query", "history_query":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("archive-authority-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
			case "missing":
				want = ErrProjectNotFound
			case "history_scope":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				want = nil
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(fault, "history_") {
					return h.m.projectHistoryAuthorityOn(h.ctx, tx, 1, 99, 11, "project_read")
				}
				project := guid.ID(99)
				if fault == "missing" {
					project = 999
				}
				_, e := h.m.archiveProjectOn(h.ctx, tx, 1, project, 11)
				return e
			})
			if want == nil {
				require.Error(t, e)
			} else {
				require.ErrorIs(t, e, want)
			}
		})
	}
}

func TestTeamBoardUsesCurrentScopedHumanWorkFacts(t *testing.T) {
	m, db, ctx, body := workHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { view, e := m.createWorkOn(ctx, tx, 1, 99, 11, body); id = view.ID; return e }))
	projects := query.BindProjects(db, m.service.names)
	_, err := projects.WithContext(ctx).Where(projects.ProjectID.Eq(guid.ID(99))).Update(projects.TeamMode, "HUMAN_ONLY")
	require.NoError(t, err)
	read := func(tenant, project, work guid.ID) (TeamWorkFact, error) {
		var out TeamWorkFact
		err := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.teamWorkOn(ctx, tx, tenant, project, work); return e })
		return out, err
	}
	fact, err := read(1, 99, id)
	require.NoError(t, err)
	require.Equal(t, id, fact.ID)
	require.Equal(t, "DRAFT", fact.State)
	require.Equal(t, 1, fact.Version)
	require.Equal(t, guid.ID(11), fact.OwnerID)
	require.NotZero(t, fact.StartedAt)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", id).Updates(map[string]any{"version": 2, "state": "IN_PROGRESS"}).Error)
	fact, err = read(1, 99, id)
	require.NoError(t, err)
	require.Equal(t, "IN_PROGRESS", fact.State)
	require.Equal(t, 2, fact.Version)
	_, err = read(2, 99, id)
	require.Error(t, err)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
	}
	_, err = read(1, 99, id)
	require.Error(t, err)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	_, err = read(1, 99, id)
	require.ErrorIs(t, err, ErrWorkForbidden)
	require.ErrorIs(t, m.teamHumanOn(ctx, db, 1, 12), ErrWorkForbidden)
	_, err = projects.WithContext(ctx).Where(projects.ProjectID.Eq(guid.ID(99))).Update(projects.TeamMode, "HYBRID")
	require.NoError(t, err)
	_, err = read(1, 99, id)
	require.ErrorIs(t, err, ErrWorkForbidden)
}

func TestArchiveConnectorsExposeExactOwnerFactsAndPreserveSourceErrors(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideArchiveReviews(s))
	require.NoError(t, consumerProviders(s))
	em := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")))
	require.NoError(t, em.ProvideServices(s))
	injected := errors.New("archive resource source unavailable")
	fail := false
	require.NoError(t, module.Provide(s, knowledge.ProjectArchiveFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]knowledge.ProjectArchiveResource, error) {
		if fail {
			return nil, injected
		}
		return []knowledge.ProjectArchiveResource{{ID: 801, State: "PENDING", Version: 2}}, nil
	})))
	require.NoError(t, module.Provide(s, fleet.ProjectArchiveFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]fleet.ProjectArchiveFact, error) {
		if fail {
			return nil, injected
		}
		return []fleet.ProjectArchiveFact{{ID: 802, Kind: "TERMINAL", State: "RELEASED", Version: 3, Ready: true}}, nil
	})))
	require.NoError(t, module.Provide(s, governance.ProjectArchiveFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]governance.ProjectArchiveFact, error) {
		if fail {
			return nil, injected
		}
		return []governance.ProjectArchiveFact{{ID: 803, Kind: "DELEGATION", State: "REVOKED", Version: 4, Ready: true}}, nil
	})))
	require.NoError(t, module.Provide(s, jobs.ProjectArchiveJobs(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]jobs.ProjectArchiveJob, error) {
		if fail {
			return nil, injected
		}
		return []jobs.ProjectArchiveJob{{ID: 804, State: "RUNNING", Version: 5}}, nil
	})))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectArchiveReviews(s))
	expected := map[string]ProjectArchiveResource{"knowledge": {ID: 801, Kind: "MEMORY_APPROVAL", State: "PENDING", Version: 2}, "fleet": {ID: 802, Kind: "TERMINAL", State: "RELEASED", Version: 3, Ready: true}, "governance": {ID: 803, Kind: "DELEGATION", State: "REVOKED", Version: 4, Ready: true}, "jobs": {ID: 804, Kind: "BACKGROUND_JOB", State: "RUNNING", Version: 5}}
	for owner, want := range expected {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			rows, e := m.archiveSources[owner](ctx, tx, 1, 99)
			require.Equal(t, []ProjectArchiveResource{want}, rows)
			return e
		}))
	}
	fail = true
	for owner := range expected {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.archiveSources[owner](ctx, tx, 1, 99); return e }), injected)
	}
	connector, e := module.Resolve[ProjectArchiveConnector](s)
	require.NoError(t, e)
	reader := ProjectArchiveReader(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return nil, nil })
	require.ErrorIs(t, connector.Bind("unknown", reader), ErrInvalidArgument)
	require.ErrorIs(t, connector.Bind("execution", nil), ErrInvalidArgument)
	require.NoError(t, connector.Bind("execution", reader))
	require.ErrorIs(t, connector.Bind("execution", reader), ErrInvalidArgument)
	m.initialized = true
	require.ErrorIs(t, connector.Bind("planning", reader), ErrInvalidArgument)
}

func TestPerformanceEvidenceReadFailuresNeverProduceCompleteReport(t *testing.T) {
	h := newResultFixture(t)
	_, e := h.submit()
	require.NoError(t, e)
	start := time.Now().UTC().Add(-time.Hour)
	injected := errors.New("performance evidence unavailable")
	request := performance.EvidenceRequest{TenantID: 1, SubjectKind: "TEAM", SubjectID: 1}
	request.Period.StartAt = start
	request.Period.CutoffAt = start.Add(2 * time.Hour)
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("performance-source-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("performance-source-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.performanceEvidenceOn(h.ctx, tx, request); return e })
	}
	require.NoError(t, read())
	total := reads
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.ErrorIs(t, read(), injected, "failed immutable source %d", i)
	}
	failAt = 0
	request.Period.CutoffAt = request.Period.StartAt
	require.ErrorIs(t, read(), ErrInvalidArgument)
	request.Period.CutoffAt = start.Add(time.Hour)
	request.SubjectKind = "HUMAN"
	request.SubjectID = 99
	require.Error(t, read())
}

func TestArchiveProjectStateAndPreflightFailClosedOnStorageAndInvalidOwners(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	p := query.BindProjects(db, m.service.names)
	row, e := p.WithContext(ctx).Take()
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.archiveProjectStateOn(ctx, tx, 1, 98); return e }), ErrProjectNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		archived, e := m.archiveProjectStateOn(ctx, tx, 1, 99)
		require.False(t, archived)
		return e
	}))
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("archived_at", at).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		archived, e := m.archiveProjectStateOn(ctx, tx, 1, 99)
		require.True(t, archived)
		return e
	}))
	_, e = m.archiveProjectStateOn(ctx, db, 1, 99)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = (&Module{}).archiveProjectStateOn(ctx, db, 1, 99)
	require.ErrorIs(t, e, ErrUnavailable)
	injected := errors.New("archive preflight storage unavailable")
	for _, table := range []string{m.service.names.Projects.String(), m.service.names.WorkItems.String(), m.service.names.Deliveries.String(), m.service.names.ResultHandoffs.String(), m.service.names.Constitutions.String(), m.service.names.ReviewPlans.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		err := db.Transaction(func(tx *gorm.DB) error {
			if table == m.service.names.Projects.String() {
				_, e := m.archiveProjectStateOn(ctx, tx, 1, 99)
				return e
			}
			_, e := m.archivePreflightOn(ctx, tx, row)
			return e
		})
		require.ErrorIs(t, err, injected)
		require.NoError(t, db.Callback().Query().Remove("archive-failure"))
	}
	for _, resources := range [][]ProjectArchiveResource{{{ID: 1, Kind: "EXECUTION", State: "RUNNING", Version: 0}}, {{ID: 1, Kind: "EXECUTION", State: "RUNNING", Version: 1}, {ID: 1, Kind: "EXECUTION", State: "DONE", Version: 2}}} {
		m.archiveSources = projectArchiveSources{"execution": func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
			return resources, nil
		}}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.archivePreflightOn(ctx, tx, row); return e }), ErrUnavailable)
	}
	m.archiveSources["execution"] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
		return nil, injected
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.archivePreflightOn(ctx, tx, row); return e }), injected)
}

func TestEffortScopeUsesTargetCreatorRatherThanProjectOwner(t *testing.T) {
	m, db, ctx, body := workHTTPHarness(t)
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	visible := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.workVisibilityOn(ctx, tx, 1, 99, created.Body.Data.ID) })
	}
	require.NoError(t, visible())
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", created.Body.Data.ID).Update("created_by", 12).Error)
	// Reproduce the prior reference-only acceptance of a now-hidden target.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.workItemReferenceOn(ctx, tx, 1, 99, created.Body.Data.ID) }))
	require.ErrorIs(t, visible(), ErrProjectNotFound)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", created.Body.Data.ID).Updates(map[string]any{"created_by": 11, "state": "CANCELLED", "archived_at": time.Now().UTC()}).Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.ErrorIs(t, visible(), ErrProjectNotFound)
}

func TestDefectReportRoundtripReplayAndCurrentAuthority(t *testing.T) {
	m, db, ctx, body := workHTTPHarness(t)
	body.Type = "BUG"
	severity, priority, repro := "CRITICAL", "HIGH", "ALWAYS"
	body.Severity, body.Priority, body.Reproducibility = &severity, &priority, &repro
	body.DefectDetails = &DefectDescription{Environment: "staging", Steps: "Open report", Expected: "Report loads", Actual: "Report fails"}
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	path := DefectPath{WorkItemID: created.Body.Data.ID.String()}
	read, err := m.getDefectHTTP(ctx, &path)
	require.NoError(t, err)
	require.Equal(t, body.DefectDetails, read.Body.Data.Details)
	original, err := m.readWork(ctx, db, 1, 99, created.Body.Data.ID, 11)
	require.NoError(t, err)
	input := &defectUpdateInput{DefectPath: path, Key: "defect-replace", IfMatch: fmtETag(created.Body.Data.Version), Body: defectUpdateBody{Severity: severity, Priority: priority, Reproducibility: repro, Details: *body.DefectDetails}}
	input.Body.Details.RootCause = "Missing query guard"
	first, err := m.replaceDefectHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, created.Body.Data.Version+1, first.Body.Data.Version)
	replay, err := m.replaceDefectHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	current, err := m.readWork(ctx, db, 1, 99, created.Body.Data.ID, 11)
	require.NoError(t, err)
	require.Equal(t, created.Body.Data.State, current.State)
	require.Equal(t, original.Contract, current.Contract)
	stale := *input
	stale.Key = "defect-stale"
	_, err = m.replaceDefectHTTP(ctx, &stale)
	assertWorkStatus(t, err, 409)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.replaceDefectHTTP(ctx, input)
	require.Error(t, err, "a previously successful receipt must not bypass current authority")
	_, err = m.getDefectHTTP(ctx, &path)
	require.Error(t, err)
}

func TestDefectReportRollbackAndFences(t *testing.T) {
	for _, scenario := range []string{"legacy", "audit", "event", "inactive", "archived", "terminal", "self_duplicate", "foreign_duplicate", "invalid", "wrong_type"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, body := workHTTPHarness(t)
			body.Type = "BUG"
			severity, priority, repro := "NORMAL", "MEDIUM", "ALWAYS"
			body.Severity, body.Priority, body.Reproducibility = &severity, &priority, &repro
			created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
			require.NoError(t, err)
			ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
			path := DefectPath{WorkItemID: created.Body.Data.ID.String()}
			read, err := m.getDefectHTTP(ctx, &path)
			require.NoError(t, err)
			require.Nil(t, read.Body.Data.Details, "legacy facts must remain unknown")
			input := &defectUpdateInput{DefectPath: path, Key: "defect-" + scenario, IfMatch: fmtETag(created.Body.Data.Version), Body: defectUpdateBody{Severity: severity, Priority: priority, Reproducibility: repro, Details: DefectDescription{Steps: "Open", Expected: "Loads", Actual: "Fails"}}}
			switch scenario {
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failure") }
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("outbox failure") })
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "archived":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = ?", 99).Update("archived_at", time.Now()).Error)
			case "terminal":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = ?", 99).Update("stage", "CLOSED").Error)
			case "self_duplicate", "foreign_duplicate":
				resolution := "DUPLICATE"
				id := created.Body.Data.ID
				if scenario == "foreign_duplicate" {
					id = 12345
				}
				input.Body.Details.Resolution, input.Body.Details.DuplicateOfWorkItemID = &resolution, &id
			case "invalid":
				input.Body.Details.Steps = " "
			case "wrong_type":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", created.Body.Data.ID).Update("type", "TASK").Error)
			}
			_, err = m.replaceDefectHTTP(ctx, input)
			if scenario == "legacy" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			w := query.BindWorkItems(db, m.service.names)
			row, err := w.WithContext(ctx).Where(w.ID.Eq(created.Body.Data.ID)).Take()
			require.NoError(t, err)
			require.Nil(t, row.DefectDetails)
			require.Equal(t, created.Body.Data.Version, row.Version)
		})
	}
}

func TestDefectDescriptionValidation(t *testing.T) {
	base := DefectDescription{Steps: "Open", Expected: "Loads", Actual: "Fails"}
	for _, mutate := range []func(*DefectDescription){
		func(v *DefectDescription) { v.Steps = " " },
		func(v *DefectDescription) { v.Expected = strings.Repeat("界", 20001) },
		func(v *DefectDescription) { v.Actual = "" },
		func(v *DefectDescription) { v.Environment = strings.Repeat("a", 201) },
		func(v *DefectDescription) { v.RootCause = strings.Repeat("a", 20001) },
		func(v *DefectDescription) { s := "UNKNOWN"; v.Resolution = &s },
		func(v *DefectDescription) { s := "DUPLICATE"; v.Resolution = &s },
		func(v *DefectDescription) { id := guid.ID(1); v.DuplicateOfWorkItemID = &id },
	} {
		v := base
		mutate(&v)
		require.ErrorIs(t, v.validate(), ErrInvalidArgument)
	}
	require.NoError(t, base.validate())
}

func TestDefectPublicContract(t *testing.T) {
	m, _, ctx, body := workHTTPHarness(t)
	body.Type = "BUG"
	s, p, r := "NORMAL", "MEDIUM", "ALWAYS"
	body.Severity, body.Priority, body.Reproducibility = &s, &p, &r
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "defect-get-contract", Method: "GET", Path: "/work-items/{work_item_id}/defect"}, func(_ context.Context, in *DefectPath) (*respx.Body[DefectDetailView], error) {
		return m.getDefectHTTP(ctx, in)
	})
	huma.Register(api, huma.Operation{OperationID: "defect-put-contract", Method: "PUT", Path: "/work-items/{work_item_id}/defect"}, func(_ context.Context, in *defectUpdateInput) (*deliveryCommandOutput[DefectDetailReceipt], error) {
		return m.replaceDefectHTTP(ctx, in)
	})
	path := "/work-items/" + created.Body.Data.ID.String() + "/defect"
	response := api.Get(path)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"details":null`)
	generated, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	var specification map[string]any
	require.NoError(t, json.Unmarshal(generated, &specification))
	view := specification["components"].(map[string]any)["schemas"].(map[string]any)["DefectDetailView"].(map[string]any)
	detailsSchema := view["properties"].(map[string]any)["details"].(map[string]any)
	require.Contains(t, detailsSchema, "anyOf")
	require.Contains(t, detailsSchema["anyOf"], map[string]any{"type": "null"})
	description := specification["components"].(map[string]any)["schemas"].(map[string]any)["DefectDescription"].(map[string]any)
	resolution := description["properties"].(map[string]any)["resolution"].(map[string]any)
	require.Contains(t, resolution["enum"], nil)
	require.Equal(t, 422, api.Get("/work-items/nope/defect").Code)
	input := defectUpdateBody{Severity: s, Priority: p, Reproducibility: r, Details: DefectDescription{Steps: "Open", Expected: "Loads", Actual: "Fails"}}
	encoded, err := json.Marshal(input)
	require.NoError(t, err)
	require.Equal(t, 422, api.Put(path, strings.NewReader(string(encoded))).Code)
	response = api.Put(path, "Idempotency-Key: public-defect", "If-Match: "+fmtETag(created.Body.Data.Version), strings.NewReader(string(encoded)))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, api.Get(path).Body.String(), `"steps":"Open"`)
}

func TestDefectDuplicateScopeAndAtomicProvenance(t *testing.T) {
	m, db, ctx, body := workHTTPHarness(t)
	body.Type = "BUG"
	s, p, r := "NORMAL", "MEDIUM", "ALWAYS"
	body.Severity, body.Priority, body.Reproducibility = &s, &p, &r
	first, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	second, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	resolution, duplicate := "DUPLICATE", second.Body.Data.ID
	input := &defectUpdateInput{DefectPath: DefectPath{WorkItemID: first.Body.Data.ID.String()}, Key: "duplicate-current", IfMatch: fmtETag(first.Body.Data.Version), Body: defectUpdateBody{Severity: s, Priority: p, Reproducibility: r, Details: DefectDescription{Steps: "Open", Expected: "Loads", Actual: "Fails", Resolution: &resolution, DuplicateOfWorkItemID: &duplicate}}}
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", duplicate).Update("created_by", 12).Error)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	_, err = m.replaceDefectHTTP(ctx, input)
	assertWorkStatus(t, err, 404)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var audited audit.DomainEvent
	var published events.Event
	m.service.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		require.NotNil(t, tx)
		audited = e
		return nil
	}
	m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		require.NotNil(t, tx)
		published = e
		return 1, nil
	})
	// A denied command has a frozen failure receipt; changed authority is a new intent.
	input.Key = "duplicate-now-visible"
	_, err = m.replaceDefectHTTP(ctx, input)
	require.NoError(t, err)
	require.NotEmpty(t, audited)
	require.Equal(t, "work_item.defect_details_updated", published.Type)
	require.Equal(t, guid.ID(99), published.ProjectID)
	require.Equal(t, first.Body.Data.ID, published.Subject.ID)
	require.Equal(t, int64(first.Body.Data.Version+1), published.SubjectVersion)
	require.Equal(t, "HUMAN", published.Actor.Kind)
	require.Equal(t, guid.ID(11), published.Actor.ID)
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.getDefectHTTP(foreign, &input.DefectPath)
	assertWorkStatus(t, err, 404)
}

// Retain the real initialization-only connector contracts in module wiring
// fixtures. No source facts or successful archive receipts are fabricated.
func consumerProviders(s *module.Services) error {
	if err := performance.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), performance.Config{}).ProvideServices(s); err != nil {
		return err
	}
	if err := module.Provide[knowledge.ProjectArchiveStateConnector](s, knowledge.ProjectArchiveStateConnector{Bind: func(reader knowledge.ProjectArchiveStateReader) error {
		if reader == nil {
			return ErrInvalidArgument
		}
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[core.EntityStateFenceConnector](s, core.EntityStateFenceConnector{Bind: func(fence core.EntityStateFence) error {
		if fence == nil {
			return ErrInvalidArgument
		}
		return nil
	}})
}

func TestReviewDetailUsesExactCurrentTargetScope(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return err
	}))
	input := &RequirementReviewSheetPath{EntityID: "99", SheetID: sheet.ID.String()}
	out, err := m.getReviewHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, sheet, out.Body.Data)
	for _, path := range []RequirementReviewSheetPath{
		{EntityID: "98", SheetID: sheet.ID.String()},
		{EntityID: "99", SheetID: "9999999"},
		{EntityID: "invalid", SheetID: sheet.ID.String()},
		{EntityID: "99", SheetID: "0"},
	} {
		result, err := m.getReviewHTTP(ctx, &path)
		require.Error(t, err)
		require.Nil(t, result)
	}
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	result, err := m.getReviewHTTP(foreign, input)
	require.Error(t, err)
	require.Nil(t, result)
	policy := m.workPolicy
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	result, err = m.getReviewHTTP(ctx, input)
	require.Error(t, err)
	require.Nil(t, result, "an exact ID cannot bypass revoked target visibility")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("current target policy unavailable")
	}
	result, err = m.getReviewHTTP(ctx, input)
	require.Error(t, err)
	require.Nil(t, result)
	m.workPolicy = policy
	q := query.BindRequirements(db, m.service.names)
	require.NoError(t, q.WithContext(ctx).Where(q.ID.Eq(req.ID)).UnderlyingDB().Update("archived_at", time.Now().UTC()).Error)
	result, err = m.getReviewHTTP(ctx, input)
	require.Error(t, err, "a frozen sheet must not bypass current target visibility")
	require.Nil(t, result)
	result, err = (&Module{}).getReviewHTTP(ctx, input)
	require.Error(t, err)
	require.Nil(t, result)
}

func TestPerformanceEvidenceUsesImmutableAttributionAndPeriodBoundaries(t *testing.T) {
	h := newResultFixture(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := start.AddDate(0, 1, 0)
	at := start.Add(time.Hour)
	executor, reviewer := guid.ID(11), guid.ID(12)
	names := h.m.service.names
	result := model.ResultVersion{TenantID: 1, ID: 8101, ProjectID: 99, WorkItemID: h.work, Sequence: 1, Summary: "Frozen execution", ActorID: executor, CreatedAt: start}
	require.NoError(t, query.BindResultVersions(h.db, names).WithContext(h.ctx).Create(&result))
	boundary := result
	boundary.ID = 8102
	boundary.Sequence = 2
	boundary.CreatedAt = cutoff
	require.NoError(t, query.BindResultVersions(h.db, names).WithContext(h.ctx).Create(&boundary))
	action := model.ResultAction{TenantID: 1, ID: 8201, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, Kind: "CHECK", Verdict: "PASSED", ActorID: reviewer, AuthoritySource: "ROLE:reviewer", Reason: "Independent decision", CreatedAt: at}
	require.NoError(t, query.BindResultActions(h.db, names).WithContext(h.ctx).Create(&action))
	birth := model.WorkItemTransition{TenantID: 1, ID: 8301, ProjectID: 99, WorkItemID: h.work, ToState: "NEW", Trigger: "COMMAND", ActorKind: "HUMAN", ActorID: &reviewer, VersionAfter: 1, CreatedAt: start}
	require.NoError(t, query.BindWorkTransitions(h.db, names).WithContext(h.ctx).Create(&birth))
	receipt := model.ReportQualityReceipt{TenantID: 1, ID: 8401, ProjectID: 99, WorkItemID: h.work, Kind: "INTERNAL_QA_PASSED", SourceID: 8501, Rounds: 3, ExecutorPrincipalID: &executor, OccurredAt: at}
	require.NoError(t, query.BindReportQualityReceipts(h.db, names).WithContext(h.ctx).Create(&receipt))
	require.NoError(t, h.db.Table(names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", start.Add(-time.Hour)).Error)
	read := func(kind string, id guid.ID) performance.EvidenceSet {
		t.Helper()
		var out performance.EvidenceSet
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.performanceEvidenceOn(h.ctx, tx, performance.EvidenceRequest{TenantID: 1, SubjectKind: kind, SubjectID: id, Period: performance.PeriodWindow{StartAt: start, CutoffAt: cutoff}})
			return err
		}))
		return out
	}
	executed := read("HUMAN", executor)
	require.Len(t, executed.Facts, 2)
	require.Len(t, executed.Work, 1, "fixed results and quality receipts link one actual work item")
	require.Equal(t, h.work, executed.Work[0].WorkID)
	require.Equal(t, guid.ID(99), executed.Work[0].ProjectID)
	require.Len(t, executed.Work[0].EvidenceRefs, 2)
	require.Nil(t, executed.Work[0].Completed, "a completion claim is not accepted period-end completion")
	require.Nil(t, executed.Work[0].InProgress)
	require.Nil(t, executed.Work[0].Delayed, "current deadline cannot supply a historical baseline")
	require.Nil(t, executed.Work[0].Changed)
	require.NotEmpty(t, executed.Work[0].Gaps)
	canonical, err := json.Marshal(result)
	require.NoError(t, err)
	digest := sha256.Sum256(canonical)
	require.Equal(t, "delivery:result:8101", executed.Facts[0].ID)
	require.Equal(t, hex.EncodeToString(digest[:]), executed.Facts[0].SHA256)
	for _, fact := range executed.Facts {
		require.Equal(t, "EXECUTOR", fact.Role)
		require.Len(t, fact.SHA256, 64)
		require.True(t, fact.OccurredAt.Before(cutoff))
	}
	require.Equal(t, []performance.MetricValue{{Metric: "REWORK_COUNT", Value: 2, EvidenceRefs: []string{"delivery:quality:8401"}}}, executed.Metrics)
	decided := read("HUMAN", reviewer)
	require.Len(t, decided.Facts, 2)
	for _, fact := range decided.Facts {
		require.Equal(t, "DECISION_ACTOR", fact.Role)
	}
	require.Empty(t, decided.Metrics, "no receipts never means measured zero")
	before := read("PROJECT", 99)
	require.Equal(t, before.Facts[0].SHA256, read("TEAM", 1).Facts[0].SHA256)
	archived := cutoff.Add(time.Hour)
	require.NoError(t, h.db.Table(names.Projects.String()).Where("tenant_id=1 AND project_id=99").Update("archived_at", archived).Error)
	require.NoError(t, h.db.Table(names.WorkItems.String()).Where("tenant_id=1 AND id=?", h.work).Updates(map[string]any{"title": "Changed today", "accountable_principal_id": reviewer}).Error)
	require.Equal(t, before, read("PROJECT", 99), "current ownership, title and archive state cannot rewrite evidence")
	require.NoError(t, h.db.Table(names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", start.Add(time.Minute)).Error)
	require.Empty(t, read("HUMAN", executor).Metrics, "incomplete observation is unknown")
	require.NoError(t, h.db.Table(names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", start.Add(-time.Hour)).Error)
	require.NoError(t, h.db.Table(names.WorkTransitions.String()).Where("id=?", birth.ID).Update("created_at", start.Add(-2*time.Hour)).Error)
	require.Empty(t, read("HUMAN", executor).Metrics, "legacy work does not establish complete round observation")
	require.NoError(t, h.db.Table(names.ReportQualityReceipts.String()).Where("id=?", receipt.ID).Update("executor_principal_id", nil).Error)
	require.Len(t, read("HUMAN", executor).Facts, 1, "unknown historical executor is never inferred from current owner")
}

func TestPerformanceSubjectRequiresActualTenantTypeAndActiveIdentity(t *testing.T) {
	h := newResultFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, h.m.performanceSubjectOn(h.ctx, tx, 1, "TEAM", 1))
		require.Error(t, h.m.performanceSubjectOn(h.ctx, tx, 1, "TEAM", 99))
		require.Error(t, h.m.performanceSubjectOn(h.ctx, tx, 2, "PROJECT", 99))
		require.Error(t, h.m.performanceSubjectOn(h.ctx, tx, 1, "HUMAN", 14))
		h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{TenantID: 1, PrincipalID: 12, State: "PAUSED"}, true, nil
		}
		require.Error(t, h.m.performanceSubjectOn(h.ctx, tx, 1, "AGENT", 12))
		h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{TenantID: 1, PrincipalID: 12, State: "ACTIVE"}, true, nil
		}
		require.NoError(t, h.m.performanceSubjectOn(h.ctx, tx, 1, "AGENT", 12))
		return nil
	}))
	require.ErrorIs(t, h.m.performanceSubjectOn(h.ctx, h.db, 1, "TEAM", 1), ErrInvalidArgument)
}

func TestArchiveRequiresEveryOwnerReceiptAndDoesNotCompleteWork(t *testing.T) {
	m, db, ctx, in := workHarness(t)
	var work workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; work, e = m.createWorkOn(ctx, tx, 1, 99, 11, in); return e }))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var initial projectArchivePreflight
	read := func() (out projectArchivePreflight, e error) {
		e = db.Transaction(func(tx *gorm.DB) error {
			p, err := m.archiveProjectOn(ctx, tx, 1, 99, 11)
			if err != nil {
				return err
			}
			out, err = m.archivePreflightOn(ctx, tx, p)
			return err
		})
		return
	}
	initial, err := read()
	require.NoError(t, err)
	require.False(t, initial.Ready)
	require.Len(t, initial.UnavailableOwners, 8)
	require.Len(t, initial.Resources, 1)
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
			return []ProjectArchiveResource{}, nil
		}
	}
	checked, err := read()
	require.NoError(t, err)
	require.False(t, checked.Ready)
	w := query.BindWorkItems(db, m.service.names)
	now := time.Now().UTC()
	_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).UpdateSimple(w.PausedAt.Value(now), w.Version.Add(1))
	require.NoError(t, err)
	checked, err = read()
	require.NoError(t, err)
	require.True(t, checked.Ready)
	require.NotEqual(t, initial.Hash, checked.Hash)
	// A live owner's receipt overrides a superficially paused work item.
	m.archiveSources["execution"] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
		return []ProjectArchiveResource{{Kind: "EXECUTION", ID: 900, State: "RUNNING", Version: 4, Ready: false}}, nil
	}
	denied, err := read()
	require.NoError(t, err)
	require.False(t, denied.Ready)
	err = db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeProjectArchiveOn(ctx, tx, 1, 99, 11, checked.Version, false, projectArchiveBody{Reason: "Preserve project history", PreflightHash: checked.Hash})
		return e
	})
	require.ErrorIs(t, err, ErrArchivePreflight)
	current, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, e)
	require.Equal(t, work.State, current.State)
	require.Nil(t, current.AcceptedAt)
}

func TestArchiveRestoreCASAuthorityAndAtomicAudit(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
			return []ProjectArchiveResource{}, nil
		}
	}
	var checked projectArchivePreflight
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p, e := m.archiveProjectOn(ctx, tx, 1, 99, 11)
		if e != nil {
			return e
		}
		checked, e = m.archivePreflightOn(ctx, tx, p)
		return e
	}))
	require.True(t, checked.Ready)
	call := func(version int, restore bool) (ProjectView, error) {
		var out ProjectView
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.changeProjectArchiveOn(ctx, tx, 1, 99, 11, version, restore, projectArchiveBody{Reason: "Preserve project history", PreflightHash: checked.Hash})
			return e
		})
		return out, e
	}
	original := m.service.audit
	failure := errors.New("audit unavailable")
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	_, err := call(checked.Version, false)
	require.ErrorIs(t, err, failure)
	p := query.BindProjects(db, m.service.names)
	row, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Nil(t, row.ArchivedAt)
	require.Equal(t, checked.Version, row.Version)
	m.service.audit = original
	archived, err := call(checked.Version, false)
	require.NoError(t, err)
	require.NotNil(t, archived.ArchivedAt)
	require.Equal(t, row.Stage, archived.Stage)
	_, err = call(checked.Version, true)
	require.ErrorIs(t, err, ErrWorkVersion)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = call(archived.Version, true)
	require.ErrorIs(t, err, ErrWorkForbidden)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	restored, err := call(archived.Version, true)
	require.NoError(t, err)
	require.Nil(t, restored.ArchivedAt)
	require.Equal(t, archived.ProjectID, restored.ProjectID)
	require.Equal(t, archived.Stage, restored.Stage)
}

func TestArchivePreflightIncludesAcceptedWorkPendingHandoff(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	packaged, err := h.handoff(result.ID)
	require.NoError(t, err)
	h.m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		h.m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return nil, nil }
	}
	read := func() projectArchivePreflight {
		var out projectArchivePreflight
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			p := query.BindProjects(tx, h.m.service.names)
			row, e := p.WithContext(h.ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
			if e != nil {
				return e
			}
			out, e = h.m.archivePreflightOn(h.ctx, tx, row)
			return e
		}))
		return out
	}
	pending := read()
	require.False(t, pending.Ready)
	require.Len(t, pending.Resources, 2)
	for _, r := range pending.Resources {
		require.False(t, r.Ready)
	}
	_, err = h.receive(result.ID, packaged.Handoffs[0].ID, 13, true)
	require.NoError(t, err)
	settled := read()
	require.True(t, settled.Ready)
	require.NotEqual(t, pending.Hash, settled.Hash)
	d := query.BindDeliveries(h.db, h.m.service.names)
	_, err = d.WithContext(h.ctx).Where(d.ID.Eq(packaged.Handoffs[0].DeliveryID)).UpdateSimple(d.AcceptanceID.Null())
	require.NoError(t, err)
	require.False(t, read().Ready, "a receipt must be bound to its delivery acceptance")
}

func TestArchiveMigrationPreservesPopulatedParentAndRejectsLossyDown(t *testing.T) {
	m, db, ctx, in := workHarness(t)
	var work workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; work, err = m.createWorkOn(ctx, tx, 1, 99, 11, in); return err }))
	raw, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "d_"
	p := query.BindProjects(db, m.service.names)
	_, err = p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.ConstitutionVersionNo.Value(3), p.AutonomyVersion.Value(4))
	require.NoError(t, err)
	before, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.NoError(t, goosex.MigrateDownTo(ctx, raw, source, "sqlite", 31))
	require.NoError(t, goosex.MigrateUpTo(ctx, raw, source, "sqlite", 32))
	after, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Equal(t, before, after)
	w := query.BindWorkItems(db, m.service.names)
	child, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, guid.ID(99), child.ProjectID)
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.ArchivedAt.Value(now))
	require.NoError(t, err, "archive is independent of INTAKE business stage")
	require.Error(t, goosex.MigrateDownTo(ctx, raw, source, "sqlite", 31), "rollback must not discard independent archival")
	after, err = p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.NotNil(t, after.ArchivedAt)
	require.Error(t, db.Exec("DELETE FROM d_delivery_projects WHERE tenant_id=1 AND project_id=99").Error, "foreign-key enforcement survives parent rebuild and failed rollback")
	_, err = p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.ArchivedAt.Null())
	require.NoError(t, err)
	require.NoError(t, goosex.MigrateDownTo(ctx, raw, source, "sqlite", 31))
	require.NoError(t, goosex.MigrateUpTo(ctx, raw, source, "sqlite", 32))
	var violations []map[string]any
	require.NoError(t, db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error)
	require.Empty(t, violations)
}

func TestArchiveOutboxFailureRollsBackAuditAndProject(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return nil, nil }
	}
	m.service.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", e.EventType).Error
	}
	fail := errors.New("outbox insert then failure")
	m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		if err := tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", e.Type).Error; err != nil {
			return 0, err
		}
		return 0, fail
	})
	var version int
	err := db.Transaction(func(tx *gorm.DB) error {
		p, e := m.archiveProjectOn(ctx, tx, 1, 99, 11)
		if e != nil {
			return e
		}
		version = p.Version
		checked, e := m.archivePreflightOn(ctx, tx, p)
		if e != nil {
			return e
		}
		require.True(t, checked.Ready)
		_, e = m.changeProjectArchiveOn(ctx, tx, 1, 99, 11, version, false, projectArchiveBody{Reason: "Preserve full project history", PreflightHash: checked.Hash})
		return e
	})
	require.ErrorIs(t, err, fail)
	p := query.BindProjects(db, m.service.names)
	row, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Nil(t, row.ArchivedAt)
	require.Equal(t, version, row.Version)
	var count int64
	require.NoError(t, db.Table("provision_probe").Count(&count).Error)
	require.Zero(t, count, "both post-insert audit and outbox rows roll back")
}
func TestArchiveAuthorityChecksCurrentHumanMembershipAndOwnerScope(t *testing.T) {
	for _, fault := range []string{"non_human", "inactive_member", "owner_scope", "revoked", "tenant"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := workHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			want := ErrWorkForbidden
			switch fault {
			case "non_human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = ErrConstitutionHumanOnly
			case "inactive_member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
				want = ErrInvalidMember
			case "owner_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
				}
				want = ErrProjectNotFound
			case "revoked":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "tenant":
				want = ErrInvalidArgument
			}
			tenant := guid.ID(1)
			if fault == "tenant" {
				tenant = 2
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.archiveProjectOn(ctx, tx, tenant, 99, 11); return e })
			require.ErrorIs(t, err, want)
		})
	}
}

func TestArchivePreflightSnapshotMustStillMatchAtCommit(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return nil, nil }
	}
	facts := []ProjectArchiveResource{{Kind: "EXECUTION", ID: 21, State: "SUCCEEDED", Version: 2, Ready: true}, {Kind: "EXECUTION", ID: 20, State: "FAILED", Version: 3, Ready: true}}
	m.archiveSources["execution"] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return facts, nil }
	read := func() projectArchivePreflight {
		var out projectArchivePreflight
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			p, e := m.archiveProjectOn(ctx, tx, 1, 99, 11)
			if e != nil {
				return e
			}
			out, e = m.archivePreflightOn(ctx, tx, p)
			return e
		}))
		return out
	}
	first := read()
	require.True(t, first.Ready)
	facts[0], facts[1] = facts[1], facts[0]
	require.Equal(t, first.Hash, read().Hash, "owner iteration order must not cause spurious confirmation conflicts")
	facts[0].Version++
	changed := read()
	require.True(t, changed.Ready)
	require.NotEqual(t, first.Hash, changed.Hash)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeProjectArchiveOn(ctx, tx, 1, 99, 11, first.Version, false, projectArchiveBody{Reason: "Preserve full project history", PreflightHash: first.Hash})
		return e
	})
	require.ErrorIs(t, err, ErrArchivePreflight, "even ready resources require review of their changed snapshot")
	facts = append(facts, facts[0])
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		p, e := m.archiveProjectOn(ctx, tx, 1, 99, 11)
		if e != nil {
			return e
		}
		_, e = m.archivePreflightOn(ctx, tx, p)
		return e
	}), ErrUnavailable, "duplicate owner identities cannot yield a valid receipt")
}

func archiveReviewResources(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, kind string) []ProjectArchiveResource {
	t.Helper()
	var resources []ProjectArchiveResource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p, err := m.archiveProjectOn(ctx, tx, 1, 99, 11)
		if err != nil {
			return err
		}
		preflight, err := m.archivePreflightOn(ctx, tx, p)
		if err != nil {
			return err
		}
		for _, resource := range preflight.Resources {
			if resource.Kind == kind {
				resources = append(resources, resource)
			}
		}
		return nil
	}))
	return resources
}
func TestArchivePendingConstitutionReviewRequiresActualDecision(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	proposal := constitutionProposal(t, m, db, ctx)
	require.Empty(t, archiveReviewResources(t, m, db, ctx, "CONSTITUTION_REVIEW"))
	proposal = constitutionChange(t, m, db, ctx, proposal, nil)
	rows := archiveReviewResources(t, m, db, ctx, "CONSTITUTION_REVIEW")
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	require.Equal(t, proposal.ID, rows[0].ID)
	constitutionChange(t, m, db, ctx, proposal, &constitutionDecisionBody{Result: "REJECT", Comment: "Explicit archive disposition"})
	require.Empty(t, archiveReviewResources(t, m, db, ctx, "CONSTITUTION_REVIEW"))
}
func TestArchivePendingWorkPlanRequiresActualReview(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	content := ReviewPlanContent{Files: []string{"src/feature.go"}, CriteriaCoverage: []PlanCoverage{}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement exact criterion", Verification: "Run independent regression"})
	}
	var plan ReviewPlanView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
		return err
	}))
	rows := archiveReviewResources(t, m, db, ctx, "WORK_PLAN_REVIEW")
	require.Len(t, rows, 1)
	require.False(t, rows[0].Ready)
	require.Equal(t, plan.ID, rows[0].ID)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	reviewer := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decideReviewPlanOn(reviewer, tx, 1, 99, work.ID, plan.ID, 12, work.AccountablePrincipalID, plan.Version, plan.ContentHash, "REJECT", "Explicit archive disposition")
		return err
	}))
	require.Empty(t, archiveReviewResources(t, m, db, ctx, "WORK_PLAN_REVIEW"))
}

func TestArchiveEntityFenceSerializesWritesAndPreservesAuthorizedHistory(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	p := query.BindProjects(db, m.service.names)
	now := time.Now().UTC()
	_, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.ArchivedAt.Value(now))
	require.NoError(t, err)
	for _, permission := range []string{"knowledge_write", "plan_write", "report_confirm", "work_item_assign", "project_manage", "memory_approve"} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveEntityFenceOn(ctx, tx, 1, 99, permission) }), ErrWorkProjectReadOnly, permission)
	}
	for _, permission := range []string{"knowledge_read", "project_read", "work_item_read"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveEntityFenceOn(ctx, tx, 1, 99, permission) }), permission)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			return m.archiveEntityFenceOn(core.WithEntityMutation(ctx), tx, 1, 99, permission)
		}), ErrWorkProjectReadOnly, permission)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveEntityFenceOn(ctx, tx, 1, 12345, "knowledge_write") }))
	require.ErrorIs(t, m.archiveEntityFenceOn(ctx, db, 1, 99, "project_read"), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.archiveEntityFenceOn(context.WithValue(ctx, archiveCommandAuthority{}, true), tx, 1, 99, "project_manage")
	}))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.projectHistoryAuthorityOn(ctx, tx, 1, 99, 11, "project_read") }), ErrProjectNotFound)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeOn(ctx, tx, 1, 99, "project_read") }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.constitutionProjectOn(ctx, tx, 1, 99, 11, "project_read")
		return err
	}), ErrProjectNotFound)
}

func TestReviewCursorCollections(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	create := func() guid.ID {
		t.Helper()
		req, e := m.createRequirementOnInTest(ctx, db, 99)
		require.NoError(t, e)
		v, e := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: guid.ID(time.Now().UnixNano()).String(), Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}})
		require.NoError(t, e)
		return v.Body.Data.ID
	}
	first := create()
	second := create()
	in := &sheetListInput{EntityID: "99", ReviewCursorInput: ReviewCursorInput{Limit: 1}}
	page, e := m.archiveReviewsListSheetsHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, []guid.ID{second}, []guid.ID{page.Body.Data.Items[0].ID})
	require.True(t, page.Body.Data.HasMore)
	create() // A newer row must not shift the next page or duplicate second.
	in.Cursor = page.Body.Data.NextCursor
	page, e = m.archiveReviewsListSheetsHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, first, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	require.Empty(t, page.Body.Data.NextCursor)
	in.EntityID = "98"
	_, e = m.archiveReviewsListSheetsHTTP(ctx, in)
	require.Error(t, e, "cursor cannot cross projects")
	in.EntityID = "99"
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	_, e = m.archiveReviewsListSheetsHTTP(foreign, in)
	require.Error(t, e, "cursor cannot cross tenants")
	in.Cursor = ""
	policy := m.workPolicy
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	page, e = m.archiveReviewsListSheetsHTTP(ctx, in)
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	require.False(t, page.Body.Data.HasMore)
	require.Empty(t, page.Body.Data.NextCursor)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("policy unavailable")
	}
	_, e = m.archiveReviewsListSheetsHTTP(ctx, in)
	require.Error(t, e, "outages never become empty successes")
	m.workPolicy = policy
	for _, limit := range []int{0, -1, 101} {
		in.Limit = limit
		_, e = m.archiveReviewsListSheetsHTTP(ctx, in)
		require.Error(t, e)
	}
	in.Limit = 100
	_, e = m.archiveReviewsListSheetsHTTP(ctx, in)
	require.NoError(t, e)
	_, e = (&Module{}).archiveReviewsListSheetsHTTP(ctx, in)
	require.Error(t, e)
}
func TestOpinionCursorPreservesImmutableHistory(t *testing.T) {
	m, _, ctx, req := sheetHarness(t)
	created, e := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "cursor-sheet", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}})
	require.NoError(t, e)
	action := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: created.Body.Data.ID.String()}}
	action.Body.Action = "OPINION"
	action.Body.Opinion = RequirementReviewOpinion{Decision: "PASS", Comment: "Original", Criteria: map[string]string{}}
	for i := 1; i <= 2; i++ {
		action.Key = "cursor-opinion-" + guid.ID(i).String()
		action.Body.Version = i
		_, e = m.actionSheetHTTP(ctx, action)
		require.NoError(t, e)
	}
	input := &opinionListInput{RequirementReviewSheetPath: action.RequirementReviewSheetPath, ReviewCursorInput: ReviewCursorInput{Limit: 1}}
	page, e := m.listOpinionsHTTP(ctx, input)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	newest := page.Body.Data.Items[0]
	action.Key = "cursor-opinion-3"
	action.Body.Version = 3
	_, e = m.actionSheetHTTP(ctx, action)
	require.NoError(t, e)
	input.Cursor = page.Body.Data.NextCursor
	page, e = m.listOpinionsHTTP(ctx, input)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, *newest.SupersedesID, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	input.SheetID = "123"
	_, e = m.listOpinionsHTTP(ctx, input)
	require.Error(t, e, "opinion cursor must belong to its sheet")
	input.SheetID = action.SheetID
	input.Limit = 101
	_, e = m.listOpinionsHTTP(ctx, input)
	require.Error(t, e)
}
func TestReviewTypeCursorAndCurrentAuthority(t *testing.T) {
	m, _, ctx, _ := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var ids []guid.ID
	for i := 1; i <= 3; i++ {
		settings := reviewTypeFixture()
		settings.Name = "Cursor " + guid.ID(i).String()
		v, e := m.createReviewTypeHTTP(ctx, &reviewTypeCreateInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, Key: "cursor-type-" + guid.ID(i).String(), Body: settings})
		require.NoError(t, e)
		ids = append(ids, v.Body.Data.ID)
	}
	input := &archiveReviewsReviewTypeListInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, ReviewCursorInput: ReviewCursorInput{Limit: 2}}
	page, e := m.archiveReviewsListReviewTypesHTTP(ctx, input)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2)
	require.Equal(t, ids[0], page.Body.Data.Items[0].ID)
	require.True(t, page.Body.Data.HasMore)
	input.Cursor = page.Body.Data.NextCursor
	page, e = m.archiveReviewsListReviewTypesHTTP(ctx, input)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, ids[2], page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	input.ProjectID = "99"
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, input)
	require.Error(t, e, "team cursor cannot be reused for project copies")
	input.ProjectID = ""
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, input)
	require.Error(t, e, "cursor does not preserve revoked permission")
	input.Limit = 101
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, input)
	require.Error(t, e)
}

func TestReviewPublicCursorContract(t *testing.T) {
	_, api := humatest.New(t)
	m := &Module{}
	huma.Register(api, huma.Operation{OperationID: "cursor-sheets", Method: "GET", Path: "/entities/{entity_id}/review-sheets"}, m.archiveReviewsListSheetsHTTP)
	huma.Register(api, huma.Operation{OperationID: "cursor-opinions", Method: "GET", Path: "/entities/{entity_id}/review-sheets/{sheet_id}/opinions"}, m.listOpinionsHTTP)
	huma.Register(api, huma.Operation{OperationID: "cursor-types", Method: "GET", Path: "/tenants/{tenant_id}/review-types"}, m.archiveReviewsListReviewTypesHTTP)
	for template, path := range map[string]string{"/entities/{entity_id}/review-sheets": "/entities/99/review-sheets", "/entities/{entity_id}/review-sheets/{sheet_id}/opinions": "/entities/99/review-sheets/7/opinions", "/tenants/{tenant_id}/review-types": "/tenants/1/review-types"} {
		op := api.OpenAPI().Paths[template].Get
		params := map[string]*huma.Param{}
		for _, p := range op.Parameters {
			params[p.Name] = p
		}
		require.Contains(t, params, "cursor")
		require.NotContains(t, params, "offset")
		require.Equal(t, float64(100), *params["limit"].Schema.Maximum) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
		require.Equal(t, 422, api.Get(path+"?limit=101").Code)
		require.Equal(t, 422, api.Get(path+"?limit=0").Code)
	}
}

func TestConsumerProviderGroupIsRequired(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	for _, provide := range moduleProviders(t)[1:] {
		require.NoError(t, provide(services))
	}
	require.NoError(t, services.Seal())
	err := m.InjectServices(services)
	require.ErrorIs(t, err, module.ErrServiceMissing)
	require.ErrorContains(t, err, "EvidenceReaderConnector", "the capability missing-provider inventory must include the leading the capability consumer group")
}

func TestRubricWorkRequiresCurrentResultStageIndependentAuthority(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	read := func(actor guid.ID, version int, stage string) (out RubricSubject, e error) {
		ctx := h.actor(actor)
		e = h.db.Transaction(func(tx *gorm.DB) error {
			out, e = h.m.rubricWorkOn(ctx, tx, 99, h.work, result.ID, version, stage)
			return e
		})
		return
	}
	current := h.stored()
	before := h.snapshot()
	out, e := read(12, current.Version, "")
	require.NoError(t, e)
	require.Equal(t, result.ID, out.ResultID)
	require.Equal(t, result.Sequence, out.ResultVersion)
	require.NotEmpty(t, out.EvidenceIDs)
	_, e = read(11, current.Version, "HUMAN_REVIEW")
	require.ErrorIs(t, e, ErrResultIndependent)
	_, e = read(12, current.Version-1, "HUMAN_REVIEW")
	require.ErrorIs(t, e, ErrWorkVersion)
	require.Equal(t, before, h.snapshot(), "reads and scores must not accept work")
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	current = h.stored()
	out, e = read(13, current.Version, "HUMAN_REVIEW")
	require.NoError(t, e)
	require.Equal(t, "ROLE:reviewer", out.Authority)
	require.Equal(t, "HUMAN", out.ActorKind)
	h.deniedAuthority = true
	_, e = read(13, current.Version, "HUMAN_REVIEW")
	require.ErrorIs(t, e, ErrWorkForbidden)
	h.deniedAuthority = false
	q := query.BindWorkItems(h.db, h.m.service.names)
	other := guid.ID(999)
	_, e = q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).Update(q.CurrentClaimID, &other)
	require.NoError(t, e)
	_, e = read(13, current.Version, "HUMAN_REVIEW")
	require.ErrorIs(t, e, ErrResultState)
}

func TestRubricAgentRequiresActualAssignedReviewerAndUnchangedReceipt(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	active := true
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, active, nil
	}
	results := query.BindResultVersions(h.db, h.m.service.names)
	executionID := guid.ID(600)
	_, e = results.WithContext(h.ctx).Where(results.ID.Eq(result.ID)).Update(results.SourceExecutionID, &executionID)
	require.NoError(t, e)
	reviewer, task := guid.ID(14), guid.ID(800)
	digest := strings.Repeat("a", 64)
	now := time.Now().UTC()
	run := &model.VerificationRun{ID: 801, TenantID: 1, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, CriterionID: 1, Method: "AGENT_JUDGE", Stage: "AGENT_ACCEPTANCE", Round: 1, State: "DONE", Verdict: "FAILED", Reason: "Review found low quality", ActorID: 12, ReviewerID: &reviewer, TaskID: &task, ReceiptHash: digest, CreatedAt: now, CompletedAt: &now}
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(run))
	h.m.verificationExecutor.ReceiptOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error) {
		return VerificationReceipt{TaskID: task, ResultID: result.ID, ExecutionID: executionID, ResultVersion: result.Sequence, ReviewerID: 14, Stage: "AGENT_ACCEPTANCE", ReceiptHash: digest}, nil
	}
	read := func() (out RubricSubject, e error) {
		version := h.stored().Version
		e = h.db.Transaction(func(tx *gorm.DB) error {
			out, e = h.m.rubricWorkOn(h.actor(14), tx, 99, h.work, result.ID, version, "AGENT_ACCEPTANCE")
			return e
		})
		return
	}
	out, e := read()
	require.NoError(t, e)
	require.Equal(t, "AGENT", out.ActorKind)
	require.Equal(t, "verification-task:800", out.Authority)
	digest = strings.Repeat("b", 64)
	_, e = read()
	require.ErrorIs(t, e, ErrResultCoverage)
	digest = strings.Repeat("a", 64)
	active = false
	_, e = read()
	require.Error(t, e)
}

func TestRubricPublishedInternalScoresRequireIndependentDecisionAuthority(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	q := query.BindWorkItems(h.db, h.m.service.names)
	now := time.Now().UTC()
	_, err = q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).UpdateSimple(q.State.Value("ACCEPTED"), q.AcceptedAt.Value(now))
	require.NoError(t, err)
	services := module.NewServices()
	require.NoError(t, h.m.provideRubric(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[RubricClientConnector](services)
	require.NoError(t, err)
	require.NoError(t, connector.Bind(func(context.Context, *gorm.DB, RubricClientRequest) (RubricClientTarget, error) {
		return RubricClientTarget{Version: 4, WorkItemID: h.work, ResultID: result.ID, Hash: strings.Repeat("a", 64), ActorKind: "HUMAN", Authority: "permission:delivery_compose"}, nil
	}))
	source, err := module.Resolve[RubricSubjects](services)
	require.NoError(t, err)
	read := func(actor guid.ID) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := source.PackageOn(h.actor(actor), tx, RubricClientRequest{ProjectID: 99, PackageID: 900, Version: 4, Stage: "HUMAN_REVIEW", Write: true})
			return e
		})
	}
	require.ErrorIs(t, read(11), ErrResultIndependent)
	require.NoError(t, read(13))
	h.deniedAuthority = true
	require.ErrorIs(t, read(13), ErrWorkForbidden)
}

func TestReviewTypeCursorReturnsFrozenProjectCopiesAndFailsClosedOnCurrentSources(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	settings := reviewTypeFixture()
	settings.Propagate = false
	var typ RequirementReviewTypeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		typ, e = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, settings)
		return e
	}))
	settings.Name = "New team configuration"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, typ.Version, settings)
		return e
	}))
	in := &archiveReviewsReviewTypeListInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, ProjectID: "99", ReviewCursorInput: ReviewCursorInput{Limit: 1}}
	page, e := m.archiveReviewsListReviewTypesHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, typ.Settings.Name, page.Body.Data.Items[0].Settings.Name)
	require.Equal(t, typ.ID, page.Body.Data.Items[0].ID)
	for _, bad := range []archiveReviewsReviewTypeListInput{{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "2"}, ReviewCursorInput: ReviewCursorInput{Limit: 1}}, {RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, ProjectID: "9999999999999999999", ReviewCursorInput: ReviewCursorInput{Limit: 1}}} {
		out, e := m.archiveReviewsListReviewTypesHTTP(ctx, &bad)
		require.Nil(t, out)
		assertWorkStatus(t, e, 422)
	}
	failure := errors.New("current review cursor source unavailable")
	writer := m.runtime.Database().Write()
	target := m.service.names.ReviewTypes.String()
	require.NoError(t, writer.Callback().Query().Before("gorm:query").Register("type-cursor-source", func(tx *gorm.DB) {
		if tx.Statement.Table == target {
			tx.AddError(failure)
		}
	}))
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, in)
	assertWorkStatus(t, e, 500)
	target = m.service.names.Projects.String()
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, in)
	assertWorkStatus(t, e, 500)
	require.NoError(t, writer.Callback().Query().Remove("type-cursor-source"))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, in)
	assertWorkStatus(t, e, 403)
	m.runtime = nil
	_, e = m.archiveReviewsListReviewTypesHTTP(ctx, in)
	assertWorkStatus(t, e, 503)
}

func TestProjectArchiveStorageFailuresPreserveDispositionAndVersion(t *testing.T) {
	m, db, ctx, _ := workHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) { return nil, nil }
	}
	var checked projectArchivePreflight
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		project, e := m.archiveProjectOn(ctx, tx, 1, 99, 11)
		if e != nil {
			return e
		}
		checked, e = m.archivePreflightOn(ctx, tx, project)
		return e
	}))
	require.True(t, checked.Ready)
	q := query.BindProjects(db, m.service.names)
	before, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	failure := errors.New("archive storage unavailable")
	rollback := errors.New("rollback successful project archive")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive-command-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("archive-command-storage", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("archive-command-storage", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.changeProjectArchiveOn(ctx, tx, 1, 99, 11, checked.Version, false, projectArchiveBody{Reason: "Preserve project history", PreflightHash: checked.Hash})
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		e := run()
		require.Error(t, e)
		require.NotErrorIs(t, e, rollback, "archive stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("archive-command-storage"))
	require.NoError(t, db.Callback().Create().Remove("archive-command-storage"))
	require.NoError(t, db.Callback().Update().Remove("archive-command-storage"))
	after, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, before, after)
}

func TestPerformanceSubjectLookupFailuresDoNotCreateReportSubjects(t *testing.T) {
	for _, fault := range []string{"lock", "member", "human", "agent", "missing_human", "missing_agent", "missing_members"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			failure := errors.New("performance subject source unavailable")
			kind := "HUMAN"
			want := failure
			switch fault {
			case "lock":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "agent":
				kind = "AGENT"
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, failure
				}
			case "missing_human":
				h.m.principals.ActiveHumanByID = nil
				want = ErrUnavailable
			case "missing_agent":
				kind = "AGENT"
				h.m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			case "missing_members":
				h.m.members = nil
				want = ErrUnavailable
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.performanceSubjectOn(h.ctx, tx, 1, kind, 12) }), want)
		})
	}
}

func TestRubricSourcesPreserveEachStorageFailureAndBindingOwner(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	version := h.stored().Version
	before := h.snapshot()
	failure := errors.New("rubric source unavailable")
	calls, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("rubric-source-storage", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			subject, e := h.m.rubricWorkOn(h.actor(13), tx, 99, h.work, 0, version, "HUMAN_REVIEW")
			if e == nil {
				require.Equal(t, result.ID, subject.ResultID)
			}
			return e
		})
	}
	// A stage write must cite its exact result, so exercise that API separately.
	require.ErrorIs(t, read(), ErrInvalidArgument)
	read = func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			subject, e := h.m.rubricWorkOn(h.actor(13), tx, 99, h.work, result.ID, version, "HUMAN_REVIEW")
			if e == nil {
				require.Equal(t, result.ID, subject.ResultID)
			}
			return e
		})
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.Error(t, read(), "rubric current storage %d", i)
	}
	require.NoError(t, h.db.Callback().Query().Remove("rubric-source-storage"))
	require.Equal(t, before, h.snapshot())
	services := module.NewServices()
	require.NoError(t, h.m.provideRubric(services))
	require.NoError(t, services.Seal())
	source, e := module.Resolve[RubricSubjects](services)
	require.NoError(t, e)
	connector, e := module.Resolve[RubricClientConnector](services)
	require.NoError(t, e)
	request := RubricClientRequest{ProjectID: 99, PackageID: 900, Version: 4, Stage: "HUMAN_REVIEW", Write: true}
	packageRead := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := source.PackageOn(h.actor(13), tx, request); return e })
	}
	require.ErrorIs(t, packageRead(), ErrUnavailable)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
	providerFailure := false
	target := RubricClientTarget{Version: 4, WorkItemID: h.work, ResultID: result.ID, Hash: strings.Repeat("a", 64), ActorKind: "HUMAN", Authority: "permission:delivery_compose"}
	provider := RubricClientReader(func(context.Context, *gorm.DB, RubricClientRequest) (RubricClientTarget, error) {
		if providerFailure {
			return RubricClientTarget{}, failure
		}
		return target, nil
	})
	require.NoError(t, connector.Bind(provider))
	require.ErrorIs(t, connector.Bind(provider), ErrInvalidArgument)
	require.ErrorIs(t, packageRead(), ErrResultState)
	providerFailure = true
	require.ErrorIs(t, packageRead(), failure)
	providerFailure = false
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"state": "ACCEPTED", "accepted_at": time.Now().UTC()}).Error)
	require.NoError(t, packageRead())
	target.ResultID = 999
	require.Error(t, packageRead())
	target.ResultID = result.ID
	calls, failAt = 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("rubric-package-storage", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	require.NoError(t, packageRead())
	total = calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.Error(t, packageRead())
	}
	require.NoError(t, h.db.Callback().Query().Remove("rubric-package-storage"))
}
