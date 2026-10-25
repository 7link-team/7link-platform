package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"testing"
	"time"
)

func TestQualityEscapeSummaryUsesWindowAndSourceVisibilityAndUnknownZeroDenominator(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	m.seed.now = func() time.Time { return now }
	q := query.BindQualityEscapes(db, m.seed.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.CreatedAt, now)
	require.NoError(t, err)
	count := 25
	m.qualityReviews = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, start, end time.Time) (QualityReviewWindow, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), start)
		require.Equal(t, start.AddDate(0, 0, 7), end)
		return QualityReviewWindow{WorkIDs: []guid.ID{44}, Entered: count}, nil
	}
	in := &escapeSummaryInput{TenantID: "1", ProjectID: "99"}
	out, err := m.qualityEscapeSummaryHTTP(ctx, in)
	require.NoError(t, err)
	require.EqualValues(t, 1, out.Body.Data.EscapedProblems)
	require.Equal(t, 25, out.Body.Data.EnteredHumanReview)
	require.NotNil(t, out.Body.Data.Rate)
	require.InEpsilon(t, .04, *out.Body.Data.Rate, .00001)
	count = 0
	out, err = m.qualityEscapeSummaryHTTP(ctx, in)
	require.NoError(t, err)
	require.Nil(t, out.Body.Data.Rate)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.CreatedAt, now.AddDate(0, 0, 7))
	require.NoError(t, err)
	out, err = m.qualityEscapeSummaryHTTP(ctx, in)
	require.NoError(t, err)
	require.Zero(t, out.Body.Data.EscapedProblems)
	m.qualityReviews = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
		return QualityReviewWindow{WorkIDs: []guid.ID{45}, Entered: 1}, nil
	}
	out, err = m.qualityEscapeSummaryHTTP(ctx, in)
	require.NoError(t, err)
	require.Zero(t, out.Body.Data.EscapedProblems)
	m.delegations.members = delegationMembers{false}
	_, err = m.qualityEscapeSummaryHTTP(ctx, in)
	require.Error(t, err)
}

func TestQualityEscapeHTTPActualReaderFiltersAndTenantBoundary(t *testing.T) {
	m, _, ctx, _, id := escapeHarness(t)
	page, e := m.listQualityEscapesHTTP(ctx, &escapeListInput{TenantID: "1", ProjectID: "99", LearningState: "PENDING", Limit: 50})
	require.NoError(t, e)
	require.NotNil(t, page)
	_, e = m.getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "2", ID: id.String()})
	require.Error(t, e)
	_, e = m.getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "1", ID: id.String()})
	require.NoError(t, e)
	_, e = m.decideQualityEscapeHTTP(ctx, &escapeDecisionInput{TenantID: "1", ID: id.String(), IfMatch: "1"})
	require.Error(t, e)
	require.False(t, query.BindNames("x_").QualityEscapes.IsZero())
}

func TestQualityEscapeHTTPRuleDraftAtomicReplayAndCurrentKnowledgeAuthority(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	store := idempotency.NewModule(m.runtime)
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.seed.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	source := store.MigrationSource()
	source.Prefix = "g_"
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	m.delegations.replay = store
	require.NoError(t, db.Exec("CREATE TABLE learning_probe (id BIGINT NOT NULL)").Error)
	deny, fail := false, false
	m.learningRules = knowledge.LearningRules{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
		if deny {
			return knowledge.ErrForbidden
		}
		return nil
	}, ProposeOn: func(_ context.Context, tx *gorm.DB, tenant, project, escape guid.ID, draft knowledge.LearningRuleDraft) (knowledge.EntryView, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, id, escape)
		require.Equal(t, "Use exact evidence", draft.Title)
		if e := tx.Exec("INSERT INTO learning_probe VALUES (77)").Error; e != nil {
			return knowledge.EntryView{}, e
		}
		if fail {
			return knowledge.EntryView{}, errors.New("knowledge outbox unavailable")
		}
		return knowledge.EntryView{ID: 77, State: "PENDING_REVIEW"}, nil
	}}
	in := &escapeDecisionInput{TenantID: "1", ID: id.String(), IfMatch: `"1"`, Key: "escape-learning-rule"}
	in.Body.State = "RECORDED"
	in.Body.RuleDraft = &knowledge.LearningRuleDraft{Title: "Use exact evidence", Body: "Check the criterion against exact result receipts."}
	fail = true
	_, e := m.decideQualityEscapeHTTP(ctx, in)
	require.Error(t, e)
	require.Zero(t, seedCount(t, db, "learning_probe"))
	q := query.BindQualityEscapes(db, m.seed.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "PENDING", r.LearningState)
	require.Equal(t, "[]", r.MemoryIDs)
	fail = false
	first, e := m.decideQualityEscapeHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, []guid.ID{77}, first.Body.Data.MemoryIDs)
	again, e := m.decideQualityEscapeHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.EqualValues(t, 1, seedCount(t, db, "learning_probe"))
	deny = true
	_, e = m.decideQualityEscapeHTTP(ctx, in)
	require.Error(t, e, "cached response must recheck Knowledge authority")
}

type escapeReadHTTPAuth struct{ governanceHTTPAuthority }

func (a *escapeReadHTTPAuth) Authenticate(ctx context.Context, authorization, cookie string) (*authn.Claims, error) {
	scope, ok := authn.PersonalTokenScopeFromContext(ctx)
	if !ok || scope.Permission != "audit_read" {
		return nil, authn.ErrInvalidToken
	}
	return a.governanceHTTPAuthority.Authenticate(ctx, authorization, cookie)
}
func TestEscapeHTTPProjectGrantDoesNotRequireTenantAuditGrant(t *testing.T) {
	m, _, _, _, id := escapeHarness(t)
	authority := &escapeReadHTTPAuth{governanceHTTPAuthority{member: true, allow: false}}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), authority, authority, authority)
	handler := chi.NewRouter()
	api := humax.New(handler, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{{Resource: "policy", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	m.registerQualityEscapeHTTP(api)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.TenantID == 1 && req.EntityID == 99 && req.Permission == "audit_read"}, nil
	}
	path := "/tenants/1/quality-escapes?project_id=99"
	require.Equal(t, 200, governanceRequest(t, handler, http.MethodGet, path, nil, "").Code)
	require.Equal(t, 200, governanceRequest(t, handler, http.MethodGet, "/tenants/1/quality-escapes/"+id.String(), nil, "").Code)
	m.qualityReviews = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
		return QualityReviewWindow{WorkIDs: []guid.ID{}, Entered: 0}, nil
	}
	summary := governanceRequest(t, handler, http.MethodGet, "/tenants/1/quality-escape-summary?project_id=99", nil, "")
	require.Equal(t, 200, summary.Code, summary.Body.String())
	require.Contains(t, summary.Body.String(), `"rate":null`)
	for _, invalid := range []string{"", "0", "abc", "-1"} {
		require.Equal(t, 422, governanceRequest(t, handler, http.MethodGet, "/tenants/1/quality-escape-summary?project_id="+invalid, nil, "").Code)
	}
	for _, denied := range []string{"/tenants/1/quality-escapes", "/tenants/1/quality-escapes?project_id=100", "/tenants/2/quality-escapes?project_id=99"} {
		require.Equal(t, 403, governanceRequest(t, handler, http.MethodGet, denied, nil, "").Code, denied)
	}
	authority.member = false
	require.Equal(t, 403, governanceRequest(t, handler, http.MethodGet, path, nil, "").Code)
}

func TestQualitySummarySourceFailureNeverReportsSuccessfulRate(t *testing.T) {
	for _, scenario := range []string{"tenant", "project", "missing", "provider", "negative", "query"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := escapeHarness(t)
			in := &escapeSummaryInput{TenantID: "1", ProjectID: "99"}
			fault := errors.New("quality summary source failed")
			m.qualityReviews = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
				return QualityReviewWindow{Entered: 1, WorkIDs: []guid.ID{44}}, nil
			}
			switch scenario {
			case "tenant":
				in.TenantID = "bad"
			case "project":
				in.ProjectID = "bad"
			case "missing":
				m.qualityReviews = nil
			case "provider":
				m.qualityReviews = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
					return QualityReviewWindow{}, fault
				}
			case "negative":
				m.qualityReviews = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
					return QualityReviewWindow{Entered: -1}, nil
				}
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:quality-summary", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.QualityEscapes.String() {
						tx.AddError(fault)
					}
				}))
			}
			out, err := m.qualityEscapeSummaryHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, out)
		})
	}
}
