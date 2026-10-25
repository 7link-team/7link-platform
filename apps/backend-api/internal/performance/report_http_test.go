package performance

import (
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestReportHTTPRealRoundtripAndRequestValidation(t *testing.T) {
	m, _, ctx, scheme, _ := reportFixture(t)
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "performance", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Owner assessments"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) {
		next(huma.WithContext(c, authn.WithClaims(c.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})))
	})
	require.NoError(t, m.RegisterHTTP(ctx, api))
	body := GenerateReportBody{SchemeID: scheme.SchemeID, SubjectID: 21, Cadence: "WEEK", Timezone: "America/New_York", AnchorDate: "2024-02-14", Reason: "Generate frozen report through HTTP"}
	response := api.Post("/tenants/1/performance-reports", "Idempotency-Key: http-generate", body)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"total_score_bps":null`)
	require.Contains(t, response.Body.String(), `"status":"PENDING"`)
	response = api.Get("/tenants/1/performance-periods?limit=1")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"has_more":false`)
	response = api.Get("/tenants/1/performance-period-preview?cadence=MONTH&timezone=UTC&anchor_date=2024-02-14")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"period_end":"2024-02-29"`)
	response = api.Post("/tenants/1/performance-reports", body)
	require.Equal(t, 422, response.Code)
	body.Cadence = "DAY"
	response = api.Post("/tenants/1/performance-reports", "Idempotency-Key: invalid-cadence", body)
	require.Equal(t, 422, response.Code)
	for _, path := range []string{
		"/tenants/1/performance-period-preview?cadence=WEEK&timezone=UTC&anchor_date=2024-02-30",
		"/tenants/1/performance-period-preview?cadence=WEEK&timezone=Local&anchor_date=2024-02-14",
		"/tenants/1/performance-periods?offset=-1",
	} {
		response = api.Get(path)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
}

func TestReportHTTPReadPaginationAndInvalidCommands(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	nextWindow, err := reportWindowForDate("MONTH", "UTC", "2024-03-12")
	require.NoError(t, err)
	second := generateFixtureReport(t, m, db, ctx, scheme, nextWindow)
	page, err := m.listReportsHTTP(ctx, &reportListInput{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, second.ReportID, page.Body.Data.Items[0].ReportID)
	require.True(t, page.Body.Data.HasMore)
	tail, err := m.listReportsHTTP(ctx, &reportListInput{Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, first.ReportID, tail.Body.Data.Items[0].ReportID)
	require.False(t, tail.Body.Data.HasMore)
	maximum, err := m.listReportsHTTP(ctx, &reportListInput{Limit: 200})
	require.NoError(t, err)
	require.Len(t, maximum.Body.Data.Items, 2)
	history, err := m.reportHistoryHTTP(ctx, &reportHistoryInput{ReportID: first.ReportID.String(), Limit: 200})
	require.NoError(t, err)
	require.Len(t, history.Body.Data.Items, 1)
	_, err = m.reportHistoryHTTP(ctx, &reportHistoryInput{ReportID: first.ReportID.String(), Limit: 201})
	status(t, err, 422)
	for _, in := range []*reportListInput{{Limit: 0}, {Limit: 201}, {Limit: 1, Offset: -1}} {
		_, err = m.listReportsHTTP(ctx, in)
		status(t, err, 422)
	}
	_, err = m.getReportHTTP(ctx, &reportReadInput{ReportID: "bad"})
	status(t, err, 422)
	_, err = m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String(), Version: 100})
	status(t, err, 404)
	_, err = m.reportHistoryHTTP(ctx, &reportHistoryInput{ReportID: "bad", Limit: 1})
	status(t, err, 422)
	_, err = m.recalculateReportHTTP(ctx, &reportRecalculateInput{ReportID: "bad"})
	status(t, err, 422)
	_, err = m.calibrateReportHTTP(ctx, &reportCalibrateInput{ReportID: "bad"})
	status(t, err, 422)
	_, err = m.confirmReportHTTP(ctx, &reportConfirmInput{ReportID: "bad"})
	status(t, err, 422)
	for _, date := range []string{"2024-02-30", "2011-12-30"} {
		_, err = reportWindowForDate("MONTH", "Pacific/Apia", date)
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = m.generateReportHTTP(ctx, &reportGenerateInput{Body: GenerateReportBody{SchemeID: scheme.SchemeID, SubjectID: 21, Cadence: "YEAR", Timezone: "UTC", AnchorDate: "9998-01-01", Reason: "Future cutoff"}, IdempotencyKey: "future"})
	status(t, err, 422)
	_, err = m.calibrateReportHTTP(ctx, &reportCalibrateInput{ReportID: first.ReportID.String(), IdempotencyKey: "automatic-edit", Body: CalibrationBody{BaseVersion: 1, DimensionKey: "quality", RuleKey: "automatic", Value: 9000, EvidenceRefs: []string{"https://example.invalid/review"}, Reason: "Cannot edit automatic evidence"}})
	status(t, err, 422)
}
