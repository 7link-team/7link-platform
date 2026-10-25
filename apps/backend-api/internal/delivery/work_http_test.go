package delivery

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWorkHTTPQueryAndBodyContracts(t *testing.T) {
	m, _, ctx, body := workHTTPHarness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "work-list-contract", Method: "GET", Path: workPath}, func(_ context.Context, in *workListInput) (*struct{ Body int64 }, error) {
		page, err := m.listWorkHTTP(ctx, in)
		if err != nil {
			return nil, err
		}
		return &struct{ Body int64 }{Body: page.Body.Meta.Page.Total}, nil
	})
	for _, query := range []string{"?limit=0", "?limit=201", "?offset=-1", "?title=eq:x"} {
		require.Equal(t, 422, api.Get("/entities/99/work-items"+query).Code, query)
	}
	require.Equal(t, 200, api.Get("/entities/99/work-items?title=like:x&limit=200&sort=title").Code)
	huma.Register(api, huma.Operation{OperationID: "work-create-contract", Method: "POST", Path: workPath, DefaultStatus: 201}, func(_ context.Context, in *workCreateInput) (*workCreateOutput, error) {
		return m.createWorkHTTP(ctx, in)
	})
	valid, err := json.Marshal(body)
	require.NoError(t, err)
	require.Equal(t, 201, api.Post("/entities/99/work-items", strings.NewReader(string(valid))).Code)
	for _, mutate := range []func(*workCreateBody){
		func(b *workCreateBody) { b.Criteria = nil },
		func(b *workCreateBody) { b.Title = strings.Repeat("a", 201) },
		func(b *workCreateBody) { b.Type = "UNKNOWN" },
		func(b *workCreateBody) { b.Criteria = []workCriterionInput{{Text: "x", VerificationMethod: ""}} },
	} {
		bad := body
		mutate(&bad)
		encoded, err := json.Marshal(bad)
		require.NoError(t, err)
		require.Equal(t, 422, api.Post("/entities/99/work-items", strings.NewReader(string(encoded))).Code)
	}
	_, err = m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", IdempotencyKey: "invalid key", Body: body})
	assertWorkStatus(t, err, 400)
}

func TestWorkDefectFiltersIntersectCurrentScopeBeforeCountsAndPaging(t *testing.T) {
	m, db, ctx, body := workHTTPHarness(t)
	body.Type = "BUG"
	severity, priority, repro := "CRITICAL", "HIGH", "ALWAYS"
	body.Severity, body.Priority, body.Reproducibility = &severity, &priority, &repro
	first, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	severity = "MINOR"
	body.AccountablePrincipalID = 12
	second, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", second.Body.Data.ID).Update("created_by", 12).Error)
	check := func(raw string, want ...guid.ID) {
		t.Helper()
		q, e := url.ParseQuery(raw)
		require.NoError(t, e)
		page, e := m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 1, RawQuery: q})
		require.NoError(t, e)
		require.EqualValues(t, len(want), page.Body.Meta.Page.Total)
		if len(want) == 0 {
			require.Empty(t, page.Body.Data)
		} else {
			require.Equal(t, want[0], page.Body.Data[0].ID)
		}
	}
	check("severity=eq:CRITICAL", first.Body.Data.ID)
	check("accountable_principal_id=eq:12", second.Body.Data.ID)
	check("created_by=eq:12", second.Body.Data.ID)
	check("severity=eq:MINOR&accountable_principal_id=eq:11")
	check("severity=in:MINOR,CRITICAL", second.Body.Data.ID, first.Body.Data.ID)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	check("severity=in:MINOR,CRITICAL", first.Body.Data.ID)
	check("created_by=eq:12")
	for _, raw := range []string{"severity=eq:UNKNOWN", "severity=like:MINOR", "accountable_principal_id=eq:0", "created_by=in:11,nope", "created_by=eq:90071992547409930000"} {
		q, e := url.ParseQuery(raw)
		require.NoError(t, e)
		_, e = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 10, RawQuery: q})
		assertWorkStatus(t, e, 422)
	}
}

func TestWorkPreparationHTTPBoundariesAndVersionContract(t *testing.T) {
	m, db, ctx, detail, body := readinessHarness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "work-prepare-contract", Method: "PATCH", Path: workPath + "/{work_item_id}"}, func(_ context.Context, in *workPrepareInput) (*deliveryCommandOutput[workPreparationReceipt], error) {
		return m.prepareWorkHTTP(ctx, in)
	})
	path := "/entities/99/work-items/" + detail.ID.String()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.Equal(t, 422, api.Patch(path, strings.NewReader(string(encoded))).Code)
	for _, minutes := range []int{1, 14, 4801, 2147483647} {
		body.EstimateMinutes = &minutes
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
		response := api.Patch(path, "If-Match: \"1\"", strings.NewReader(string(encoded)))
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	assertReadinessHistoryCount(t, db, 0)
	for i, minutes := range []int{15, 4800} {
		body.EstimateMinutes = &minutes
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
		response := api.Patch(path, "If-Match: "+fmtETag(i+1), strings.NewReader(string(encoded)))
		require.Equal(t, 200, response.Code, response.Body.String())
		var receipt struct {
			Data workPreparationReceipt `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &receipt))
		require.Equal(t, i+2, receipt.Data.Version)
		require.Equal(t, "READY", receipt.Data.State)
	}
	assertReadinessHistoryCount(t, db, 2)
}

func TestWorkCreationRespectsTerminalProjectReadOnlyState(t *testing.T) {
	for _, stage := range []string{"INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING", "DELIVERED", "CLOSED", "CANCELLED"} { //nolint:misspell // Frozen business state.
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := workHTTPHarness(t)
			require.NoError(t, db.Exec("UPDATE d_delivery_projects SET stage=?, blocked_reason='Awaiting input'", stage).Error)
			created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body, IdempotencyKey: "phase-check"})
			if stage == "CLOSED" || stage == "CANCELLED" { //nolint:misspell // Frozen business state.
				assertWorkStatus(t, err, 409)
				for _, table := range []string{"d_delivery_work_items", "d_delivery_work_item_criteria", "d_delivery_work_item_contracts", "d_delivery_project_counters"} {
					var count int64
					require.NoError(t, db.Table(table).Count(&count).Error)
					require.Zero(t, count, table)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, "DRAFT", created.Body.Data.State)
		})
	}
}

func workHTTPHarness(t *testing.T) (*Module, *gorm.DB, context.Context, workCreateBody) {
	t.Helper()
	m, db, ctx, input := workHarness(t)
	router, err := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: db.Dialector.(*sqlite.Dialector).DSN, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("d_"), appenv.WithDatabase(router))
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "d_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	require.NoError(t, platformi18n.NewModule(m.runtime, platformi18n.Base).Initialize(ctx))
	require.NoError(t, store.Initialize(ctx))
	m.idempotency = store
	return m, db, ctx, input
}

func TestWorkHTTPCreateReplayAndFailureRollback(t *testing.T) {
	for _, key := range []string{"", "work-create-once"} {
		t.Run("key_"+key, func(t *testing.T) {
			m, db, ctx, body := workHTTPHarness(t)
			in := &workCreateInput{EntityID: "99", IdempotencyKey: key, Body: body}
			first, err := m.createWorkHTTP(ctx, in)
			require.NoError(t, err)
			require.Equal(t, 201, first.Status)
			require.Equal(t, "DRAFT", first.Body.Data.State)
			require.Equal(t, 1, first.Body.Data.ContractVersionNo)
			if key != "" {
				replay, err := m.createWorkHTTP(ctx, in)
				require.NoError(t, err)
				require.Equal(t, "true", replay.Replayed)
				require.Equal(t, first.Body.Data, replay.Body.Data)
				changed := *in
				changed.Body.Title = "Changed request"
				_, err = m.createWorkHTTP(ctx, &changed)
				assertWorkStatus(t, err, 422)
			}
			var count int64
			require.NoError(t, db.Table("d_delivery_work_items").Count(&count).Error)
			require.EqualValues(t, 1, count)
			bad := *in
			bad.IdempotencyKey = key + "-bad"
			bad.Body.Objective = " "
			for i := 0; i < 2; i++ {
				_, err = m.createWorkHTTP(ctx, &bad)
				assertWorkStatus(t, err, 422)
			}
			require.NoError(t, db.Table("d_delivery_work_items").Count(&count).Error)
			require.EqualValues(t, 1, count)
			// A business failure without a key must roll back rather than commit.
			bad.IdempotencyKey = ""
			_, err = m.createWorkHTTP(ctx, &bad)
			assertWorkStatus(t, err, 422)
		})
	}
}

func assertWorkStatus(t *testing.T, err error, status int) {
	t.Helper()
	var actual huma.StatusError
	require.ErrorAs(t, err, &actual)
	require.Equal(t, status, actual.GetStatus())
}

func TestWorkHTTPListFiltersPageAndScopedCount(t *testing.T) {
	m, db, ctx, input := workHTTPHarness(t)
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		input.Title = title
		_, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: input})
		require.NoError(t, err)
	}
	page, err := m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Offset: 1, Limit: 1, Sort: "title"})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, "Beta", page.Body.Data[0].Title)
	require.EqualValues(t, 3, page.Body.Meta.Page.Total)
	require.Equal(t, 1, page.Body.Meta.Page.Offset)
	filtered, err := m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20, RawQuery: url.Values{"title": {"like:Alpha"}, "type": {"eq:TASK"}, "state": {"eq:DRAFT"}}})
	require.NoError(t, err)
	require.Len(t, filtered.Body.Data, 1)
	require.Equal(t, "Alpha", filtered.Body.Data[0].Title)
	require.EqualValues(t, 1, filtered.Body.Meta.Page.Total)
	view, err := m.getWorkHTTP(ctx, &workGetInput{EntityID: "99", WorkItemID: filtered.Body.Data[0].ID.String()})
	require.NoError(t, err)
	require.Equal(t, "Alpha", view.Body.Data.Title)
	require.Len(t, view.Body.Data.Criteria, 1)
	require.NotNil(t, view.Body.Data.Contract)
	for _, raw := range []url.Values{{"unknown": {"x"}}, {"title": {"eq:Alpha"}}} {
		_, err = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20, RawQuery: raw})
		assertWorkStatus(t, err, 422)
	}
	_, err = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20, Sort: "unknown"})
	assertWorkStatus(t, err, 422)
	for _, constraint := range []authz.DataConstraint{{OwnerIDs: []string{"22"}}, {AllowAll: true, DeniedIDs: []string{"99"}}, {ResourceIDs: []string{"98"}}, {DepartmentIDs: []string{"3"}}} {
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return constraint, nil
		}
		page, err = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20})
		require.NoError(t, err)
		require.Empty(t, page.Body.Data)
		require.Zero(t, page.Body.Meta.Page.Total)
		_, err = m.getWorkHTTP(ctx, &workGetInput{EntityID: "99", WorkItemID: view.Body.Data.ID.String()})
		assertWorkStatus(t, err, 404)
		_, err = m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: input})
		assertWorkStatus(t, err, 403)
	}
	// Archived work is excluded from both the count and the detail.
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='CANCELLED', archived_at=CURRENT_TIMESTAMP WHERE id=?", view.Body.Data.ID).Error)
	page, err = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Body.Meta.Page.Total)
	_, err = m.getWorkHTTP(ctx, &workGetInput{EntityID: "99", WorkItemID: view.Body.Data.ID.String()})
	assertWorkStatus(t, err, 404)
}

func TestWorkHTTPRejectsInvalidIDsAndUnavailableRuntime(t *testing.T) {
	m := &Module{}
	for _, id := range []string{"invalid", "99"} {
		status := 422
		if id == "99" {
			status = 503
		}
		_, err := m.createWorkHTTP(t.Context(), &workCreateInput{EntityID: id})
		assertWorkStatus(t, err, status)
		_, err = m.listWorkHTTP(t.Context(), &workListInput{EntityID: id})
		assertWorkStatus(t, err, status)
		_, err = m.getWorkHTTP(t.Context(), &workGetInput{EntityID: id, WorkItemID: "1"})
		assertWorkStatus(t, err, status)
	}
	_, err := m.getWorkHTTP(t.Context(), &workGetInput{EntityID: "99", WorkItemID: "invalid"})
	assertWorkStatus(t, err, 422)
}

func TestWorkHTTPPropagatesPolicyAndStorageFailures(t *testing.T) {
	for _, stage := range []string{"policy", "work", "criteria", "contract", "snapshot"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := workHTTPHarness(t)
			created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
			require.NoError(t, err)
			switch stage {
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrUnavailable
				}
			case "work":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_items RENAME TO hidden_work").Error)
			case "criteria":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_item_criteria RENAME TO hidden_criteria").Error)
			case "contract":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_item_contracts RENAME TO hidden_contracts").Error)
			case "snapshot":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_item_contracts SET snapshot=?", `{"criteria":true}`).Error)
			}
			_, err = m.getWorkHTTP(ctx, &workGetInput{EntityID: "99", WorkItemID: created.Body.Data.ID.String()})
			require.Error(t, err, "unavailable or corrupt evidence must never appear as a confirmed work item")
			if stage == "policy" || stage == "work" {
				_, err = m.listWorkHTTP(ctx, &workListInput{EntityID: "99", Limit: 20})
				require.Error(t, err)
			}
		})
	}
}

func TestWorkDetailSchemaIncludesEverySerializedProperty(t *testing.T) {
	registry := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	registry.Schema(reflect.TypeFor[workDetailView](), true, "")
	schema := registry.Map()["WorkDetailView"]
	require.NotNil(t, schema)
	encoded, err := json.Marshal(workDetailView{WorkView: WorkView{Title: "Task"}, Criteria: []workCriterionView{}})
	require.NoError(t, err)
	var actual map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &actual))
	for key := range actual {
		require.Contains(t, schema.Properties, key, "JSON property missing from OpenAPI")
	}
	for _, key := range []string{"id", "project_id", "title", "objective", "state", "criteria", "accountable_principal_id", "baseline_version_no", "version"} {
		require.Contains(t, schema.Properties, key)
	}
}

func TestWorkCreateReceiptStaysBelowIdempotencyStorageLimit(t *testing.T) {
	// Request text and criteria never enter the receipt. This bound therefore
	// holds even for 50 maximum-length multibyte clauses and descriptions.
	encoded, err := json.Marshal(workCreatedView{ID: 9223372036854775807, ProjectID: 9223372036854775807, Number: 2147483647, State: "DRAFT", Version: 1, ContractVersionNo: 1, ContentHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"})
	require.NoError(t, err)
	require.Less(t, len(encoded), 1024)
}
