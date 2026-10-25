package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPipelineSourceFailuresNeverBecomeSuccessfulEmptyHistory(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 12)
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	injected := errors.New("pipeline source unavailable")
	h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		return nil, nil
	}
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("pipeline-read-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("pipeline-read-failure")
	read := func() (PipelinePage, error) { return h.m.pipelineOn(h.actor(12), h.db, 1, 99, h.work, 0, 100) }
	out, e := read()
	require.NoError(t, e)
	require.NotEmpty(t, out.Items)
	found := false
	for _, r := range out.Items {
		if r.Stage == "HUMAN_REVIEW" {
			found = true
			require.Equal(t, "PASSED", r.State)
		}
	}
	require.True(t, found)
	total := reads
	require.Greater(t, total, 4)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		_, e = read()
		require.ErrorIs(t, e, injected, "pipeline read %d", i)
	}
	failAt = 0
	h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		return nil, injected
	}
	_, e = read()
	require.ErrorIs(t, e, injected)
	for _, rows := range [][]PipelineStageRun{{{ID: "duplicate", ProjectID: 99, WorkItemID: h.work, Stage: "DELIVERY", State: "PENDING"}, {ID: "duplicate", ProjectID: 99, WorkItemID: h.work, Stage: "DELIVERY", State: "PASSED"}}, {{ID: "bad-stage", ProjectID: 99, WorkItemID: h.work, Stage: "FAKE", State: "PASSED"}}} {
		h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
			return rows, nil
		}
		_, e = read()
		require.ErrorIs(t, e, ErrUnavailable)
	}
}

func TestPipelineSourceRebuildSixStagesAttemptsScopeAndMissingFacts(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for i, stage := range []string{"AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE", "HUMAN", "AGENT_TEST"} {
		row := model.VerificationRun{TenantID: 1, ID: guid.ID(810 + i), ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, Method: "AGENT_JUDGE", Stage: stage, Round: 1 + i/4, State: "DONE", Verdict: "PASSED", ReceiptHash: strings.Repeat("a", 64), CreatedAt: at.Add(time.Duration(i) * time.Second), CompletedAt: &at}
		if i == 4 {
			row.ReceiptHash = ""
		}
		require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Create(&row).Error)
	}
	calls := 0
	h.m.pipelineClient = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, works []guid.ID, _ []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		calls++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, []guid.ID{h.work}, works)
		return []PipelineStageRun{{ID: "hidden:900", ProjectID: project, WorkItemID: h.work, ResultID: result.ID, Stage: "HIDDEN_SUITE", State: "FAILED", CreatedAt: at}, {ID: "package:901", ProjectID: project, WorkItemID: h.work, ResultID: result.ID, Stage: "DELIVERY", State: "RUNNING", CreatedAt: at}}, nil
	}
	read := func(offset, limit int) (PipelinePage, error) {
		return h.m.pipelineOn(h.actor(12), h.db, 1, 99, 0, offset, limit)
	}
	first, err := read(0, 100)
	require.NoError(t, err)
	require.Equal(t, 7, first.Total)
	stages := map[string]int{}
	for _, row := range first.Items {
		stages[row.Stage]++
		require.Nil(t, row.StartedAt)
	}
	require.Len(t, stages, 6)
	require.Equal(t, "INCONCLUSIVE", first.Items[6].State)
	require.Equal(t, 2, first.Items[6].Attempt)
	require.Equal(t, 2, *first.Items[6].InternalRound)
	second, err := read(0, 100)
	require.NoError(t, err)
	require.Equal(t, first, second)
	page, err := read(2, 2)
	require.NoError(t, err)
	require.Equal(t, first.Items[2:4], page.Items)
	require.Equal(t, 7, page.Total)
	empty, err := read(100, 1)
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	require.Equal(t, 7, empty.Total)
	_, err = h.m.pipelineOn(h.actor(12), h.db, 2, 99, 0, 0, 50)
	require.ErrorIs(t, err, ErrWorkForbidden)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	priorCalls := calls
	empty, err = read(0, 50)
	require.NoError(t, err)
	require.Zero(t, empty.Total)
	require.Equal(t, priorCalls, calls)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		return []PipelineStageRun{{ID: "foreign", ProjectID: 99, WorkItemID: h.work + 1, ResultID: result.ID, Stage: "DELIVERY", State: "PASSED"}}, nil
	}
	_, err = read(0, 50)
	require.ErrorIs(t, err, ErrUnavailable)
	h.m.pipelineClient = nil
	_, err = read(0, 50)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestPipelineHTTPDefaultsLimitsCurrentScopeAndUnavailableSource(t *testing.T) {
	h := newResultFixture(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: h.actor(12)}
	h.m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	h.m.entityOwner = func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: id}, true, nil
	}
	calls := 0
	h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		calls++
		return []PipelineStageRun{}, nil
	}
	_, api := humatest.New(t)
	h.m.registerPipelineHTTP(api)
	before := map[string]int64{}
	for _, table := range []string{h.m.service.names.ResultVersions.String(), h.m.service.names.VerificationRuns.String(), "provision_probe"} {
		var count int64
		require.NoError(t, h.db.Table(table).Count(&count).Error)
		before[table] = count
	}
	response := api.Get("/entities/99/pipeline-runs")
	require.Equal(t, http.StatusOK, response.Code)
	var envelope respx.Envelope[[]PipelineStageRun]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Empty(t, envelope.Data)
	require.NotNil(t, envelope.Meta.Page)
	require.EqualValues(t, 0, envelope.Meta.Page.Total)
	require.Equal(t, 50, envelope.Meta.Page.Limit)
	require.Equal(t, 0, envelope.Meta.Page.Offset)
	require.Equal(t, 0, envelope.Meta.Page.Count)
	require.Equal(t, 1, calls)
	require.Equal(t, http.StatusUnprocessableEntity, api.Get("/entities/99/pipeline-runs?limit=201").Code)
	require.Equal(t, http.StatusUnprocessableEntity, api.Get("/entities/99/pipeline-runs?work_item_id=0").Code)
	require.Equal(t, http.StatusForbidden, api.Get("/entities/98/pipeline-runs").Code)
	h.m.pipelineClient = nil
	require.Equal(t, http.StatusServiceUnavailable, api.Get("/entities/99/pipeline-runs").Code)
	for table, expected := range before {
		var count int64
		require.NoError(t, h.db.Table(table).Count(&count).Error)
		require.Equal(t, expected, count)
	}
}

func TestPipelineBindingAndHandoffProjectionRetainRealDeliveryLinksAndRejectDuplicateReceipts(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 13)
	require.NoError(t, e)
	handoff, e := h.handoff(result.ID)
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	services := module.NewServices()
	require.NoError(t, h.m.providePipeline(services))
	require.NoError(t, services.Seal())
	connector, e := module.Resolve[PipelineClientConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
	duplicate, overflow := false, false
	source := PipelineClientSource(func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, works []guid.ID, links []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		require.Equal(t, []guid.ID{h.work}, works)
		require.Equal(t, []PipelineDeliveryLink{{DeliveryID: handoff.Handoffs[0].DeliveryID, WorkItemID: h.work, ResultID: result.ID}}, links)
		if overflow {
			return make([]PipelineStageRun, 20001), nil
		}
		rows := []PipelineStageRun{{ID: "client:receipt", ProjectID: project, WorkItemID: h.work, ResultID: result.ID, Stage: "DELIVERY", State: "PENDING", CreatedAt: time.Now().UTC()}}
		if duplicate {
			rows = append(rows, rows[0])
		}
		return rows, nil
	})
	require.NoError(t, connector.Bind(source))
	require.ErrorIs(t, connector.Bind(source), ErrInvalidArgument)
	read := func() (PipelinePage, error) {
		var out PipelinePage
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.pipelineOn(h.ctx, tx, 1, 99, h.work, 0, 200)
			return e
		})
		return out, e
	}
	out, e := read()
	require.NoError(t, e)
	require.GreaterOrEqual(t, out.Total, 2)
	var found bool
	for _, row := range out.Items {
		if row.ID == "client:receipt" {
			found = true
			require.Equal(t, "PENDING", row.State)
			require.Equal(t, 1, row.Attempt)
		}
	}
	require.True(t, found)
	duplicate = true
	_, e = read()
	require.ErrorIs(t, e, ErrUnavailable)
	duplicate = false
	overflow = true
	_, e = read()
	require.ErrorIs(t, e, ErrUnavailable)
	for _, check := range []struct{ state, verdict, want string }{{"RUNNING", "", "RUNNING"}, {"PENDING", "", "PENDING"}, {"FAILED", "", "INCONCLUSIVE"}, {"DONE", "UNKNOWN", "INCONCLUSIVE"}} {
		require.Equal(t, check.want, pipelineVerdict(check.state, check.verdict))
	}
}

func TestPipelineCurrentStorageFailuresNeverReturnPartialHistory(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 13)
	require.NoError(t, e)
	_, e = h.handoff(result.ID)
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.pipelineClient = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error) {
		return nil, nil
	}
	failure := errors.New("pipeline history unavailable")
	for _, table := range []string{h.m.service.names.WorkItems.String(), h.m.service.names.ResultVersions.String(), h.m.service.names.VerificationRuns.String(), h.m.service.names.ResultActions.String(), h.m.service.names.ResultHandoffs.String()} {
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("pipeline-source-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(failure)
			}
		}))
		e := h.db.Transaction(func(tx *gorm.DB) error {
			out, e := h.m.pipelineOn(h.ctx, tx, 1, 99, h.work, 0, 200)
			require.Empty(t, out.Items)
			return e
		})
		require.ErrorIs(t, e, failure)
		require.NoError(t, h.db.Callback().Query().Remove("pipeline-source-failure"))
	}
}
