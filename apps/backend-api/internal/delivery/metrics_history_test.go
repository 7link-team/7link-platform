package delivery

import (
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryQualityHistoricalReceiptsNotCurrentStateOrMissingZeros(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for i, source := range []struct {
		tenant, project guid.ID
		kind, verdict   string
		at              time.Time
	}{
		{1, 99, "RECEIPT", "PASSED", at}, {1, 99, "RETURN", "FAILED", at},
		{1, 99, "CHECK", "PASSED", at},
		{1, 99, "RECEIPT", "PASSED", at.Add(48 * time.Hour)},
	} {
		row := model.ResultAction{TenantID: source.tenant, ID: guid.ID(800 + i), ProjectID: source.project, WorkItemID: h.work, ResultID: result.ID, Kind: source.kind, Verdict: source.verdict, ActorID: 12, AuthoritySource: "permission:delivery_send", Reason: "Actual reply", CreatedAt: source.at}
		require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Create(&row).Error)
	}
	for i, verdict := range []string{"PASSED", "FAILED", "INCONCLUSIVE"} {
		row := model.VerificationRun{TenantID: 1, ID: guid.ID(900 + i), ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, Method: "AGENT_JUDGE", Round: 1, Stage: "AGENT_ACCEPTANCE", State: "DONE", Verdict: verdict, CompletedAt: &at, ReceiptHash: strings.Repeat("a", 64)}
		require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Create(&row).Error)
	}
	var out DeliveryQualityHistory
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.deliveryQualityOn(h.actor(12), tx, 1, "2026-10-19", "2026-10-21")
		return err
	}))
	require.Len(t, out.Days, 2)
	require.Equal(t, 1, out.Days[0].Received)
	require.Equal(t, 1, out.Days[0].Returned)
	require.InDelta(t, 0.5, *out.Days[0].AcceptanceRate, 0.00001)
	require.Equal(t, 1, out.Days[0].VerificationPassed)
	require.Equal(t, 1, out.Days[0].VerificationFailed)
	require.InDelta(t, 0.5, *out.Days[0].VerificationPassRate, 0.00001)
	require.Nil(t, out.Days[1].AcceptanceRate)
	require.Nil(t, out.Days[1].VerificationPassRate)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.deliveryQualityOn(h.actor(12), tx, 2, "2026-10-19", "2026-10-21")
		return err
	}))
}

func TestDeliveryQualityPublicHistoryRetainsRealFailuresAndWindowValidation(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	registry, e := authz.NewRegistry(authz.Action{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, e)
	guard := resultHTTPGuard{ctx: h.ctx}
	h.m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	_, api := humatest.New(t)
	h.m.registerDeliveryQualityHTTP(api)
	r := api.Get("/tenants/1/metrics/delivery-quality")
	require.Equal(t, 200, r.Code, r.Body.String())
	for _, query := range []string{"from=2026-02-30", "to=2026-02-30", "from=2026-10-21&to=2026-10-19", "from=2026-01-01&to=2026-10-21"} {
		r := api.Get("/tenants/1/metrics/delivery-quality?" + query)
		require.Equal(t, 422, r.Code, r.Body.String())
	}
	failure := errors.New("quality history unavailable")
	calls, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("quality-history-source", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.deliveryQualityOn(h.ctx, tx, 1, "", ""); return e })
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 2)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure)
	}
	failAt = 0
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	r = api.Get("/tenants/1/metrics/delivery-quality")
	require.Equal(t, 500, r.Code, r.Body.String())
	require.NoError(t, h.db.Callback().Query().Remove("quality-history-source"))
	h.m.runtime = nil
	r = api.Get("/tenants/1/metrics/delivery-quality")
	require.Equal(t, 503, r.Code, r.Body.String())
}
