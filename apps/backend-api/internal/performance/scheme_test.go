package performance

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/performance/model"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

type publisher func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f publisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return f(ctx, tx, e)
}

type eventSink struct {
	ID      string `gorm:"primaryKey"`
	Payload string
}

func harness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	router, e := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{"main": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "perf.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("p_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	db := router.Write()
	raw, e := db.DB()
	require.NoError(t, e)
	source := m.MigrationSource()
	source.Prefix = "p_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	require.NoError(t, db.AutoMigrate(&eventSink{}))
	m.ids, e = guid.New(1)
	require.NoError(t, e)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals = identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		return tx.Create(&eventSink{ID: "audit:" + e.TargetID.String() + ":" + e.Detail["revision_id"].(string), Payload: string(b)}).Error
	}
	m.events = publisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		b, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		return 1, tx.Create(&eventSink{ID: "outbox:" + e.Data.(map[string]any)["revision_id"].(string), Payload: string(b)}).Error
	})
	store := idempotency.NewModule(rt)
	source = store.MigrationSource()
	source.Prefix = "p_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idem = store
	return m, db, ctx
}
func definition() SchemeDefinition {
	return SchemeDefinition{Name: "Delivery review", SubjectKind: "HUMAN", Scope: "Engineers in this tenant", Enabled: true, Dimensions: []Dimension{{Key: "quality", Title: "Delivery quality", WeightBPS: 6000, MaximumPoints: 100, Rules: []Rule{{Key: "accepted", Description: "Independent quality evidence", Condition: Condition{Metric: "QUALITY_SCORE", Operator: "GTE", Threshold: 80}, Effect: "ADD", Points: 90, EvidenceKinds: []string{"RESULT", "HUMAN_REVIEW"}}}}, {Key: "progress", Title: "Collaboration progress", WeightBPS: 4000, MaximumPoints: 100, Rules: []Rule{{Key: "late", Description: "Accountable delay with source", Condition: Condition{Metric: "ON_TIME_RATIO", Operator: "LTE", Threshold: 60}, Effect: "SUBTRACT", Points: 10, EvidenceKinds: []string{"PLAN"}}}}}, Template: ReportTemplate{Title: "Assessment", Summary: "Evidence and justified adjustments", DimensionOrder: []string{"quality", "progress"}}}
}
func TestSchemeImmutableRevisionsCASAndAtomicAuditOutbox(t *testing.T) {
	m, db, ctx := harness(t)
	in := &createInput{IdempotencyKey: "create", Body: schemeBody{Definition: definition(), Reason: "Initial rules"}}
	created, e := m.createHTTP(ctx, in)
	require.NoError(t, e)
	v := created.Body.Data
	replay, e := m.createHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, v, replay.Body.Data)
	revision := definition()
	revision.Enabled = false
	revision.Template.Title = "Revised title"
	revision.Dimensions[0].Rules[0].Points = 80
	changed, e := m.reviseHTTP(ctx, &reviseInput{SchemeID: v.SchemeID.String(), IdempotencyKey: "revise", Body: schemeBody{BaseVersion: 1, Definition: revision, Reason: "Refined thresholds"}})
	require.NoError(t, e)
	require.Equal(t, 2, changed.Body.Data.Version)
	require.NotEqual(t, v.SHA256, changed.Body.Data.SHA256)
	old, e := m.getHTTP(ctx, &getInput{SchemeID: v.SchemeID.String(), Version: 1})
	require.NoError(t, e)
	require.Equal(t, v, old.Body.Data)
	latest, e := m.getHTTP(ctx, &getInput{SchemeID: v.SchemeID.String()})
	require.NoError(t, e)
	require.False(t, latest.Body.Data.Definition.Enabled)
	_, e = m.reviseHTTP(ctx, &reviseInput{SchemeID: v.SchemeID.String(), IdempotencyKey: "stale", Body: schemeBody{BaseVersion: 1, Definition: revision, Reason: "stale"}})
	status(t, e, 409)
	in.Body.Reason = "different"
	_, e = m.createHTTP(ctx, in)
	status(t, e, 409)
	var count int64
	require.NoError(t, db.Model(&eventSink{}).Count(&count).Error)
	require.EqualValues(t, 4, count)
	// A failed outbox write rolls back head, immutable revision, audit and idem.
	m.events = publisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
		return 0, errors.New("outbox unavailable")
	})
	_, e = m.reviseHTTP(ctx, &reviseInput{SchemeID: v.SchemeID.String(), IdempotencyKey: "rollback", Body: schemeBody{BaseVersion: 2, Definition: definition(), Reason: "Failed change"}})
	status(t, e, 500)
	latest, e = m.getHTTP(ctx, &getInput{SchemeID: v.SchemeID.String()})
	require.NoError(t, e)
	require.Equal(t, 2, latest.Body.Data.Version)
	r := query.BindRevisions(db, m.names)
	n, e := r.WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 2, n)
	require.NoError(t, db.Model(&eventSink{}).Count(&count).Error)
	require.EqualValues(t, 4, count)
}
func TestSchemeOwnerAuthorizationReplayAndTenantHistory(t *testing.T) {
	m, _, ctx := harness(t)
	in := &createInput{IdempotencyKey: "owner", Body: schemeBody{Definition: definition(), Reason: "Initial"}}
	created, e := m.createHTTP(ctx, in)
	require.NoError(t, e)
	m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "performance_manage"}, nil
	}
	_, e = m.createHTTP(ctx, in)
	status(t, e, 403)
	_, e = m.getHTTP(ctx, &getInput{SchemeID: created.Body.Data.SchemeID.String()})
	status(t, e, 403)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	_, e = m.getHTTP(other, &getInput{SchemeID: created.Body.Data.SchemeID.String()})
	status(t, e, 404)
	page, e := m.listHTTP(other, &listInput{Limit: 10})
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = m.createHTTP(ctx, in)
	status(t, e, 403)
}
func TestSchemeValidationRejectsVanityMetricsAmbiguousWeightsAndBrokenTemplate(t *testing.T) {
	valid := definition()
	require.NoError(t, valid.validate())
	for name, mutate := range map[string]func(*SchemeDefinition){
		"subject": func(d *SchemeDefinition) { d.SubjectKind = "TOKEN" }, "name": func(d *SchemeDefinition) { d.Name = " " }, "scope": func(d *SchemeDefinition) { d.Scope = "" }, "weight": func(d *SchemeDefinition) { d.Dimensions[0].WeightBPS = 5999 }, "duplicate dimension": func(d *SchemeDefinition) { d.Dimensions[1].Key = "quality" }, "missing template dimension": func(d *SchemeDefinition) { d.Template.DimensionOrder = []string{"quality"} }, "duplicate template dimension": func(d *SchemeDefinition) { d.Template.DimensionOrder = []string{"quality", "quality"} }, "vanity": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].Condition.Metric = "TOKEN_COUNT" }, "points": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].Points = -1 }, "evidence": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].EvidenceKinds = []string{"MESSAGE_COUNT"} }, "duplicate evidence": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].EvidenceKinds = []string{"RESULT", "RESULT"} }, "operator": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].Condition.Operator = "EXEC" }, "negative threshold": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].Condition.Threshold = -1 }, "duplicate subject": func(d *SchemeDefinition) { d.SubjectIDs = []guid.ID{11, 11} }, "empty rules": func(d *SchemeDefinition) { d.Dimensions[0].Rules = nil }, "effect": func(d *SchemeDefinition) { d.Dimensions[0].Rules[0].Effect = "SET" },
	} {
		t.Run(name, func(t *testing.T) { d := definition(); mutate(&d); require.ErrorIs(t, d.validate(), ErrInvalid) })
	}
	_, e := revisionView(&model.Revision{Definition: "{}", SHA256: "bad"})
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = revisionView(&model.Revision{Definition: "invalid"})
	require.ErrorIs(t, e, ErrUnavailable)
}
