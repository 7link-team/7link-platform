package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func constitutionHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	m, db, ctx, _ := workHTTPHarness(t)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, entity guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Contains(t, []string{"project_read", "project_manage"}, permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	return m, db, authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
}

func TestConstitutionHTTPReplayETagPaginationAndIsolation(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	in := &constitutionCreateInput{EntityID: "99", IdempotencyKey: "constitution-once", Body: constitutionProposalBody{Body: "# Rules\nRun tests", ChangeSummary: "First"}}
	first, err := m.proposeConstitutionHTTP(ctx, in)
	require.NoError(t, err)
	replay, err := m.proposeConstitutionHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	changed := *in
	changed.Body.Body = "Different rules"
	_, err = m.proposeConstitutionHTTP(ctx, &changed)
	assertWorkStatus(t, err, 422)
	id := first.Body.Data.ID.String()
	owner, found, err := m.constitutionOwner(ctx, first.Body.Data.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, owner)
	get, err := m.getConstitutionHTTP(ctx, &constitutionGetInput{VersionID: id})
	require.NoError(t, err)
	require.Equal(t, `"1"`, get.ETag)
	require.Equal(t, in.Body.Body, get.Body.Data.Body)
	submit := &constitutionSubmitInput{VersionID: id, IfMatch: get.ETag, IdempotencyKey: "constitution-submit-once"}
	submitted, err := m.submitConstitutionHTTP(ctx, submit)
	require.NoError(t, err)
	require.Equal(t, "IN_REVIEW", submitted.Body.Data.State)
	repeated, err := m.submitConstitutionHTTP(ctx, submit)
	require.NoError(t, err)
	require.Equal(t, "true", repeated.Replayed)
	require.Equal(t, submitted.Body.Data, repeated.Body.Data)
	submit.IdempotencyKey = "stale-submit"
	_, err = m.submitConstitutionHTTP(ctx, submit)
	assertWorkStatus(t, err, 409)
	decision := &constitutionDecisionInput{VersionID: id, IfMatch: `"2"`, IdempotencyKey: "constitution-review-once", Body: constitutionDecisionBody{Result: "APPROVE"}}
	active, err := m.decideConstitutionHTTP(ctx, decision)
	require.NoError(t, err)
	activeReplay, err := m.decideConstitutionHTTP(ctx, decision)
	require.NoError(t, err)
	require.Equal(t, "true", activeReplay.Replayed)
	require.Equal(t, active.Body.Data, activeReplay.Body.Data)
	second := constitutionProposal(t, m, db, ctx)
	page, err := m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: "99", Limit: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Body.Meta.Page.Total)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, second.ID, page.Body.Data[0].ID)
	page, err = m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: "99", Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Equal(t, first.Body.Data.ID, page.Body.Data[0].ID)
	page, err = m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: "99", Limit: 50, RawQuery: url.Values{"state": {"eq:ACTIVE"}, "change_summary": {"like:First"}}})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	encoded, err := json.Marshal(page.Body.Data)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"body":`)
	for _, claims := range []*authn.Claims{
		{TenantID: 2, EntityID: 99, PrincipalID: 11},
		{TenantID: 1, EntityID: 98, PrincipalID: 11},
	} {
		_, err = m.getConstitutionHTTP(authn.WithClaims(ctx, claims), &constitutionGetInput{VersionID: id})
		assertWorkStatus(t, err, 404)
	}
	for _, constraint := range []authz.DataConstraint{{AllowAll: true, DeniedIDs: []string{"99"}}, {OwnerIDs: []string{"22"}}, {DepartmentIDs: []string{"3"}}} {
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return constraint, nil
		}
		_, err = m.getConstitutionHTTP(ctx, &constitutionGetInput{VersionID: id})
		require.Error(t, err)
		_, err = m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: "99", Limit: 50})
		require.Error(t, err)
	}
}

func TestConstitutionHTTPValidationAndRequiredVersion(t *testing.T) {
	m, _, ctx := constitutionHarness(t)
	v := constitutionProposal(t, m, m.runtime.Database().Write(), ctx)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "constitution-submit-test", Method: "POST", Path: constitutionVersionPath + "/submissions", DefaultStatus: 201}, func(_ context.Context, in *constitutionSubmitInput) (*deliveryCommandOutput[ConstitutionView], error) {
		return m.submitConstitutionHTTP(ctx, in)
	})
	path := "/constitution-versions/" + v.ID.String() + "/submissions"
	require.Equal(t, 422, api.Post(path).Code)
	require.Equal(t, 422, api.Post(path, "If-Match: 1").Code)
	require.Equal(t, 201, api.Post(path, `If-Match: "1"`).Code)
	huma.Register(api, huma.Operation{OperationID: "constitution-list-test", Method: "GET", Path: constitutionPath}, func(_ context.Context, in *constitutionListInput) (*struct{ Body int64 }, error) {
		result, err := m.listConstitutionsHTTP(ctx, in)
		if err != nil {
			return nil, err
		}
		return &struct{ Body int64 }{result.Body.Meta.Page.Total}, nil
	})
	for _, query := range []string{"?limit=0", "?limit=201", "?offset=-1", "?state=like:ACTIVE", "?sort=body", "?unknown=x"} {
		require.Equal(t, 422, api.Get("/entities/99/project/constitution-versions"+query).Code, query)
	}
}

func TestConstitutionHTTPChecksNormalizedBytes(t *testing.T) {
	m, _, ctx := constitutionHarness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "constitution-proposal-boundary", Method: "POST", Path: constitutionPath, DefaultStatus: 201}, func(_ context.Context, in *constitutionCreateInput) (*deliveryCommandOutput[ConstitutionView], error) {
		return m.proposeConstitutionHTTP(ctx, in)
	})
	for _, body := range []string{" " + strings.Repeat("x", 65536), strings.Repeat("x", 65536) + "\r\n", strings.Repeat("界", 21845) + "x"} {
		encoded, err := json.Marshal(constitutionProposalBody{Body: body, ChangeSummary: "Boundary"})
		require.NoError(t, err)
		response := api.Post("/entities/99/project/constitution-versions", strings.NewReader(string(encoded)))
		require.Equal(t, 201, response.Code, response.Body.String())
	}
	for _, body := range []string{strings.Repeat("x", 65537), strings.Repeat("界", 21846)} {
		encoded, err := json.Marshal(constitutionProposalBody{Body: body, ChangeSummary: "Boundary"})
		require.NoError(t, err)
		response := api.Post("/entities/99/project/constitution-versions", strings.NewReader(string(encoded)))
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `127050`)
	}
}

func TestConstitutionActiveAgentCanProposeAndSubmit(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
	}
	v := constitutionProposal(t, m, db, ctx)
	require.Equal(t, "AGENT", v.ProposedByKind)
	submitted := constitutionChange(t, m, db, ctx, v, nil)
	require.Equal(t, "IN_REVIEW", submitted.State)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeConstitutionOn(ctx, tx, 1, 99, v.ID, 11, submitted.Version, &constitutionDecisionBody{Result: "APPROVE"})
		return e
	})
	require.ErrorIs(t, err, ErrConstitutionHumanOnly)
}
func constitutionProposal(t *testing.T, m *Module, db *gorm.DB, ctx context.Context) ConstitutionView {
	t.Helper()
	var created ConstitutionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = m.proposeConstitutionOn(ctx, tx, 1, 99, 11, constitutionProposalBody{Body: " # Build\r\n\r\nRun real tests. ", ChangeSummary: " Initial rules "})
		return err
	}))
	return created
}
func constitutionChange(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, v ConstitutionView, decision *constitutionDecisionBody) ConstitutionView {
	t.Helper()
	var result ConstitutionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.changeConstitutionOn(ctx, tx, 1, 99, v.ID, 11, v.Version, decision)
		return err
	}))
	return result
}

func TestConstitutionExplicitLifecyclePreservesContentAndSingleActive(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	first := constitutionProposal(t, m, db, ctx)
	require.Equal(t, "DRAFT", first.State)
	require.Equal(t, 1, first.VersionNo)
	expectedBody := "# Build\n\nRun real tests."
	hash := sha256.Sum256([]byte(expectedBody))
	require.Equal(t, hex.EncodeToString(hash[:]), first.BodySHA256)
	submitted := constitutionChange(t, m, db, ctx, first, nil)
	require.Equal(t, "IN_REVIEW", submitted.State)
	active := constitutionChange(t, m, db, ctx, submitted, &constitutionDecisionBody{Result: "APPROVE", Comment: " Checked commands "})
	require.Equal(t, "ACTIVE", active.State)
	require.Equal(t, "Checked commands", *active.ReviewComment)
	second := constitutionProposal(t, m, db, ctx)
	require.Equal(t, 2, second.VersionNo)
	submitted2 := constitutionChange(t, m, db, ctx, second, nil)
	active2 := constitutionChange(t, m, db, ctx, submitted2, &constitutionDecisionBody{Result: "APPROVE"})
	require.Equal(t, "ACTIVE", active2.State)
	var stored []model.ProjectConstitution
	require.NoError(t, db.Table(m.service.names.Constitutions.String()).Order("version_no").Find(&stored).Error)
	require.Len(t, stored, 2)
	require.Equal(t, "SUPERSEDED", stored[0].State)
	require.Nil(t, stored[0].ActiveKey)
	require.Equal(t, active.Version+1, stored[0].Version)
	for _, row := range stored {
		require.Equal(t, expectedBody, row.Body)
		require.Equal(t, first.BodySHA256, row.BodySHA256)
	}
	require.Equal(t, guid.ID(99), *stored[1].ActiveKey)
	var project model.Project
	require.NoError(t, db.Table(m.service.names.Projects.String()).First(&project).Error)
	require.Equal(t, 2, *project.ConstitutionVersionNo)
	require.Equal(t, 3, project.Version)
	third := constitutionChange(t, m, db, ctx, constitutionProposal(t, m, db, ctx), nil)
	rejected := constitutionChange(t, m, db, ctx, third, &constitutionDecisionBody{Result: "REJECT", Comment: " Missing test command "})
	require.Equal(t, "REJECTED", rejected.State)
	require.Equal(t, "Missing test command", *rejected.ReviewComment)
	require.NoError(t, db.Table(m.service.names.Projects.String()).First(&project).Error)
	require.Equal(t, 2, *project.ConstitutionVersionNo)
}

func TestConstitutionByteLimitsAndNormalization(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 65536), strings.Repeat("界", 21845) + "x"} {
		v, err := validateConstitutionProposal(constitutionProposalBody{Body: body, ChangeSummary: strings.Repeat("界", 2000)})
		require.NoError(t, err)
		require.Len(t, v.Body, 65536)
	}
	_, err := validateConstitutionProposal(constitutionProposalBody{Body: strings.Repeat("界", 21846), ChangeSummary: "Rules"})
	require.ErrorIs(t, err, ErrConstitutionTooLarge)
	for _, body := range []constitutionProposalBody{{Body: " ", ChangeSummary: "Rules"}, {Body: "Rules", ChangeSummary: " "}, {Body: "Rules", ChangeSummary: strings.Repeat("x", 2001)}, {Body: string([]byte{255}), ChangeSummary: "Rules"}} {
		_, err := validateConstitutionProposal(body)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}

func TestConstitutionInvalidCommandsNeverChangeContentOrReview(t *testing.T) {
	for _, scenario := range []string{"stale", "other_proposer", "non_review", "wrong_project", "invalid_result", "missing_rejection", "long_comment", "closed", "archived", "scope_denied", "different_owner", "inactive_member", "agent_review", "inactive_agent"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			v := constitutionProposal(t, m, db, ctx)
			actor, entity, version := guid.ID(11), guid.ID(99), v.Version
			var decision *constitutionDecisionBody
			want := ErrInvalidArgument
			switch scenario {
			case "stale":
				version++
				want = ErrConstitutionVersion
			case "other_proposer":
				actor = 22
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
				want = ErrWorkForbidden
			case "non_review":
				decision = &constitutionDecisionBody{Result: "APPROVE"}
				want = ErrConstitutionState
			case "wrong_project":
				entity = 98
				want = ErrProjectNotFound
			case "invalid_result":
				decision = &constitutionDecisionBody{Result: "MAYBE"}
			case "missing_rejection":
				decision = &constitutionDecisionBody{Result: "REJECT", Comment: " "}
			case "long_comment":
				decision = &constitutionDecisionBody{Result: "REJECT", Comment: strings.Repeat("界", 2001)}
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = ?", 99).Update("stage", "CLOSED").Error)
				want = ErrWorkProjectReadOnly
			case "archived":
				require.NoError(t, db.Exec("UPDATE d_delivery_projects SET stage='CLOSED', archived_at=CURRENT_TIMESTAMP").Error)
				want = ErrProjectNotFound
			case "scope_denied":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
				want = ErrWorkForbidden
			case "different_owner":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
				}
				want = ErrProjectNotFound
			case "inactive_member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
				want = ErrInvalidMember
			case "agent_review", "inactive_agent":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				if scenario == "agent_review" {
					decision = &constitutionDecisionBody{Result: "APPROVE"}
					want = ErrConstitutionHumanOnly
				} else {
					m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
						return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "PAUSED"}, true, nil
					}
					want = ErrInvalidMember
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeConstitutionOn(ctx, tx, 1, entity, v.ID, actor, version, decision)
				return e
			})
			require.ErrorIs(t, err, want)
			var stored model.ProjectConstitution
			require.NoError(t, db.Table(m.service.names.Constitutions.String()).First(&stored).Error)
			require.Equal(t, "DRAFT", stored.State)
			require.Equal(t, 1, stored.Version)
			require.Nil(t, stored.ReviewedBy)
			require.Equal(t, v.BodySHA256, stored.BodySHA256)
		})
	}
}

func TestConstitutionActivationFailureRollsBackSupersedingAndCanRetry(t *testing.T) {
	for _, failureStage := range []string{"audit", "event"} {
		t.Run(failureStage, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			first := constitutionChange(t, m, db, ctx, constitutionProposal(t, m, db, ctx), nil)
			first = constitutionChange(t, m, db, ctx, first, &constitutionDecisionBody{Result: "APPROVE"})
			second := constitutionChange(t, m, db, ctx, constitutionProposal(t, m, db, ctx), nil)
			auditBefore, eventsBefore := m.service.audit, m.service.events
			failure := errors.New("review storage failed")
			if failureStage == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			} else {
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeConstitutionOn(ctx, tx, 1, 99, second.ID, 11, second.Version, &constitutionDecisionBody{Result: "APPROVE"})
				return e
			})
			require.ErrorIs(t, err, failure)
			var rows []model.ProjectConstitution
			require.NoError(t, db.Table(m.service.names.Constitutions.String()).Order("version_no").Find(&rows).Error)
			require.Equal(t, "ACTIVE", rows[0].State)
			require.Equal(t, first.Version, rows[0].Version)
			require.Equal(t, "IN_REVIEW", rows[1].State)
			var project model.Project
			require.NoError(t, db.Table(m.service.names.Projects.String()).First(&project).Error)
			require.Equal(t, 1, *project.ConstitutionVersionNo)
			m.service.audit, m.service.events = auditBefore, eventsBefore
			result := constitutionChange(t, m, db, ctx, second, &constitutionDecisionBody{Result: "APPROVE"})
			require.Equal(t, "ACTIVE", result.State)
		})
	}
}

func TestConstitutionStorageFailuresPreserveActiveDocumentAndProjectPointer(t *testing.T) {
	for _, scenario := range []string{"proposal_read", "proposal_create", "version_read", "supersede_update", "project_update", "project_cas", "version_update", "version_cas"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			active := constitutionChange(t, m, db, ctx, constitutionChange(t, m, db, ctx, constitutionProposal(t, m, db, ctx), nil), &constitutionDecisionBody{Result: "APPROVE"})
			next := constitutionChange(t, m, db, ctx, constitutionProposal(t, m, db, ctx), nil)
			var before model.Project
			require.NoError(t, db.Table(m.service.names.Projects.String()).First(&before).Error)
			target, kind, skip := m.service.names.Constitutions.String(), "update", 0
			switch scenario {
			case "proposal_read", "version_read":
				kind = "query"
			case "proposal_create":
				kind = "create"
			case "project_update", "project_cas":
				target = m.service.names.Projects.String()
			case "version_update", "version_cas":
				skip = 1 // superseding the previous document precedes the new version CAS
			}
			fired := false
			sentinel := errors.New("constitution storage unavailable")
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table != target || fired {
					return
				}
				if skip > 0 {
					skip--
					return
				}
				fired = true
				_ = tx.AddError(sentinel)
			}
			if strings.HasSuffix(scenario, "_cas") {
				condition := ""
				if scenario == "version_cas" {
					condition = " WHEN OLD.id=" + next.ID.String()
				}
				require.NoError(t, db.Exec("CREATE TRIGGER constitution_ignore BEFORE UPDATE ON "+target+condition+" BEGIN SELECT RAISE(IGNORE); END").Error)
			} else {
				switch kind {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("constitution_fault", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("constitution_fault", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("constitution_fault", hook))
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(scenario, "proposal_") {
					_, e := m.proposeConstitutionOn(ctx, tx, 1, 99, 11, constitutionProposalBody{Body: "New rules", ChangeSummary: "New proposal"})
					return e
				}
				_, e := m.changeConstitutionOn(ctx, tx, 1, 99, next.ID, 11, next.Version, &constitutionDecisionBody{Result: "APPROVE"})
				return e
			})
			require.Error(t, err)
			if !strings.HasSuffix(scenario, "_cas") {
				require.True(t, fired)
				require.ErrorIs(t, err, sentinel)
			}
			var after model.Project
			require.NoError(t, db.Table(m.service.names.Projects.String()).First(&after).Error)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.ConstitutionVersionNo, after.ConstitutionVersionNo)
			var rows []model.ProjectConstitution
			require.NoError(t, db.Table(m.service.names.Constitutions.String()).Order("version_no").Find(&rows).Error)
			require.Len(t, rows, 2)
			require.Equal(t, "ACTIVE", rows[0].State)
			require.Equal(t, active.Version, rows[0].Version)
			require.Equal(t, "IN_REVIEW", rows[1].State)
			require.Nil(t, rows[1].ReviewedAt)
		})
	}
}
