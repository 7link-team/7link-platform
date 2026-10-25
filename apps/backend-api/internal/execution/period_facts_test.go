package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestPeriodFactsAreAuthorizedAndPeriodBound(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	s := module.NewServices()
	require.NoError(t, f.m.ProvideServices(s))
	require.NoError(t, s.Seal())
	r, e := module.Resolve[PeriodReader](s)
	require.NoError(t, e)
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC().AddDate(1, 0, 0)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		got, e := r.ReadOn(f.human, tx, guid.ID(7), v.ProjectID, []guid.ID{v.WorkItemID}, start, end)
		require.NoError(t, e)
		require.EqualValues(t, 1, got.Created)
		require.EqualValues(t, 1, got.States[v.State])
		return nil
	}))
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := r.ReadOn(f.human, tx, 999, v.ProjectID, []guid.ID{v.WorkItemID}, start, end)
		return e
	}))
	require.ErrorIs(t, func() error { _, e := r.ReadOn(f.human, f.db, guid.ID(7), v.ProjectID, nil, start, end); return e }(), ErrInvalidArgument)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		got, e := r.ReadOn(f.human, tx, guid.ID(7), v.ProjectID, []guid.ID{v.WorkItemID}, end, end.Add(time.Hour))
		require.Zero(t, got.Created)
		return e
	}))
}

type inboxCapture map[string]governance.InboxSource

func (c inboxCapture) Register(s governance.InboxSource) error {
	if c != nil {
		c[s.Kind] = s
	}
	return nil
}

func TestAttentionRegistration(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.ErrorIs(t, f.m.registerInbox(s), module.ErrServiceMissing)
	s = module.NewServices()
	capture := inboxCapture{}
	require.NoError(t, module.Provide[governance.InboxSources](s, capture))
	require.NoError(t, s.Seal())
	require.NoError(t, f.m.registerInbox(s))
	require.NotNil(t, capture["EXECUTION_ATTENTION"].Read)
}

func interrupt(t *testing.T, f *executionFixture) ExecutionView {
	t.Helper()
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOST"})
	require.NoError(t, err)
	return f.read(t, v.ID)
}

func TestAttentionCurrentRecoveryEligibilityAndNoSideEffects(t *testing.T) {
	f := executionHarness(t)
	v := interrupt(t, f)
	req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "EXECUTION_ATTENTION", Limit: 1}
	var before int64
	require.NoError(t, f.db.Table("effects").Count(&before).Error)
	got, err := f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, v.ID, got[0].ObjectID)
	require.Equal(t, "execution", got[0].ObjectType)
	require.Equal(t, "HIGH", got[0].Priority)
	require.Equal(t, v.Plan.Objective, got[0].Title)
	require.Equal(t, v, f.read(t, v.ID))
	var after int64
	require.NoError(t, f.db.Table("effects").Count(&after).Error)
	require.Equal(t, before, after)
	key := got[0].InboxKey
	req.After = &key
	got, err = f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Empty(t, got)
	req.After = nil
	req.ProjectID = 10
	got, err = f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Empty(t, got)
	req.ProjectID = 9
	for _, permission := range []string{"execution_read", "execution_stop"} {
		f.m.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, code string, _ guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{AllowAll: code != permission}, nil
		}
		got, err = f.m.attentionInbox(f.human, req)
		require.NoError(t, err)
		require.Empty(t, got)
	}
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	f.humanActive = false
	got, err = f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Empty(t, got)
	f.humanActive = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.RecoverOn(f.human, tx, 7, v.ID, v.Version, true)
		return e
	}))
	got, err = f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestAttentionFiltersBeforeLimitAndSurfacesFailures(t *testing.T) {
	f := executionHarness(t)
	first := interrupt(t, f)
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, err)
	second, third := first, first
	second.ID, third.ID = first.ID+1, first.ID+2
	for i, id := range []guid.ID{second.ID, third.ID} {
		copyRow := *row
		copyRow.ID = id
		copyRow.CreatedAt = row.CreatedAt.Add(time.Duration(i+1) * time.Second)
		require.NoError(t, q.WithContext(t.Context()).Create(&copyRow))
	}
	foreign := *row
	foreign.ID, foreign.TenantID = first.ID+3, 8
	require.NoError(t, q.WithContext(t.Context()).Create(&foreign))
	baseAuthorize := f.m.work.AuthorizeOn
	f.m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
		require.Equal(t, "RECOVER", op)
		if l.ExecutionID == first.ID {
			return ErrForbidden
		}
		return baseAuthorize(ctx, tx, l, op)
	}
	req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "EXECUTION_ATTENTION", Limit: 1}
	got, err := f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, second.ID, got[0].ID)
	req.After = &got[0].InboxKey
	got, err = f.m.attentionInbox(f.human, req)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, third.ID, got[0].ID)
	failure := errors.New("read failed")
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return failure }
	_, err = f.m.attentionInbox(f.human, req)
	require.ErrorIs(t, err, failure)
	f.m.work.AuthorizeOn = nil
	_, err = f.m.attentionInbox(f.human, req)
	require.ErrorIs(t, err, governance.ErrUnavailable)
}

func TestAttentionInvalidAndUnavailable(t *testing.T) {
	f := executionHarness(t)
	req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "EXECUTION_ATTENTION", Limit: 1}
	for _, bad := range []governance.InboxRequest{
		{TenantID: 8, PrincipalID: 11, Kind: req.Kind, Limit: 1},
		{TenantID: 7, PrincipalID: 12, Kind: req.Kind, Limit: 1},
		{TenantID: 7, PrincipalID: 11, Kind: "HUMAN_VERIFICATION", Limit: 1},
		{TenantID: 7, PrincipalID: 11, Kind: req.Kind, Limit: 0},
		{TenantID: 7, PrincipalID: 11, Kind: req.Kind, Limit: 102},
		{TenantID: 7, PrincipalID: 11, Kind: req.Kind, Limit: 1, ProjectID: -1},
	} {
		_, err := f.m.attentionInbox(f.human, bad)
		require.Error(t, err)
	}
	_, err := f.m.attentionInbox(authn.WithClaims(t.Context(), &authn.Claims{}), req)
	require.ErrorIs(t, err, governance.ErrForbidden)
	_, err = (*Module)(nil).attentionInbox(f.human, req)
	require.ErrorIs(t, err, governance.ErrUnavailable)
}

func TestRegisterInboxRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.registerInbox(module.NewServices()), module.ErrServicesProviding)
}

func TestExecutionProvisionRefusesExistingCapabilityOwner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		provide func(*Module, *module.Services) error
	}{
		{"client_threads", (*Module).provideClientThreads}, {"dispatch_history", (*Module).provideDispatchHistory}, {"heavy", (*Module).provideHeavy}, {"realtime", (*Module).provideRealtimeTopics}, {"forecast", (*Module).provideForecastFacts}, {"quality", (*Module).provideQualityLauncher}, {"loop", (*Module).provideLoopExecutionFacts}, {"verification", (*Module).provideVerificationExecutor}, {"notification", (*Module).provideNotificationFacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			require.NoError(t, tc.provide(f.m, s))
			require.ErrorIs(t, f.m.ProvideServices(s), module.ErrServiceDuplicate)
		})
	}
}
