package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func manifestTestOperation(name string) Operation {
	return Operation{Name: name, Summary: "Read self", Method: "GET", Path: "/tenants/{tenant_id}/agents/{agent_principal_id}/inbox-events", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), SideEffects: []string{"READ_ONLY"}, Via: "SELF"}
}

func TestManifestPublicationRejectsInvalidSnowflakeWithoutHistory(t *testing.T) {
	m, db, _ := agentHarness(t)
	m.ids = failedAgentIDs{id: 0}
	require.ErrorIs(t, m.publishManifest(agentContext(t, 7)), ErrUnavailable)
	q := query.BindManifestVersion(db, m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
func TestManifestPublicationRetainsHistoryHashesFinalRegistryAndReusesUnchanged(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, m.registry.Register(manifestTestOperation("inbox_events.list")))
	require.NoError(t, m.publishManifest(ctx))
	require.NoError(t, m.publishManifest(ctx))
	first, err := m.manifestOn(ctx, db, "")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", first.ManifestVersion)
	require.NoError(t, m.registry.Register(manifestTestOperation("agent_operations.list")))
	require.NoError(t, m.publishManifest(ctx))
	latest, err := m.manifestOn(ctx, db, "")
	require.NoError(t, err)
	require.Equal(t, "1.1.0", latest.ManifestVersion)
	require.Len(t, latest.Operations, 2)
	historical, err := m.manifestOn(ctx, db, "1.0.0")
	require.NoError(t, err)
	require.Equal(t, first, historical)
	require.NotEqual(t, latest.SHA256, first.SHA256)
	q := query.BindManifestVersion(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	m.registry.mu.Lock()
	delete(m.registry.operations, "agent_operations.list")
	m.registry.mu.Unlock()
	require.NoError(t, m.publishManifest(ctx))
	rolledBack, err := m.manifestOn(ctx, db, "")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", rolledBack.ManifestVersion)
	require.Equal(t, first.SHA256, rolledBack.SHA256)
	_, err = m.manifestOn(ctx, db, "99.0.0")
	require.ErrorIs(t, err, ErrNotFound)
	require.True(t, etagMatches(`W/"other", "`+latest.SHA256+`"`, latest.SHA256))
	require.False(t, etagMatches(`"old"`, latest.SHA256))
}
func TestManifestVersionClassifiesRemovalAndSchemaChangeAsMajorSummaryAsPatch(t *testing.T) {
	op := manifestTestOperation("inbox_events.list")
	require.Equal(t, "2.0.0", nextManifestVersion("1.2.3", []Operation{op}, nil))
	changed := op
	changed.Summary = "New copy"
	require.Equal(t, "1.2.4", nextManifestVersion("1.2.3", []Operation{op}, []Operation{changed}))
	optional := op
	optional.InputSchema = json.RawMessage(`{"type":"object","properties":{"optional":{"type":"string"}}}`)
	require.Equal(t, "1.3.0", nextManifestVersion("1.2.3", []Operation{op}, []Operation{optional}))
	changed.InputSchema = json.RawMessage(`{"type":"object","required":["x"]}`)
	require.Equal(t, "2.0.0", nextManifestVersion("1.2.3", []Operation{op}, []Operation{changed}))
}
func TestCapabilitiesUseCurrentSubjectIAMWithoutClaimingUnresolvedResourceOrQuota(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	self := manifestTestOperation("inbox_events.list")
	require.NoError(t, m.registry.Register(self))
	guarded := manifestTestOperation("channel.post")
	guarded.Via = "ROLE"
	guarded.RequiredPermission = "channel_post"
	guarded.Quota = "RATE_LIMIT"
	guarded.Parameters = []OperationParameter{{In: "path", Name: "channel_id"}}
	require.NoError(t, m.registry.Register(guarded))
	require.NoError(t, m.publishManifest(ctx))
	allowed := false
	m.capabilityExplain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.EqualValues(t, 7, r.PrincipalID)
		return authz.Explanation{Allowed: allowed, Reason: "explicit_deny"}, nil
	}
	var view EffectiveCapabilities
	err := db.Transaction(func(tx *gorm.DB) error { var err error; view, err = m.capabilitiesOn(ctx, tx, 1, 7, 0, ""); return err })
	require.NoError(t, err)
	require.Equal(t, "CAPABILITY_NOT_AUTHORIZED", view.Operations[0].Status)
	require.Equal(t, "explicit_deny", view.Operations[0].ReasonCode)
	require.Nil(t, view.Operations[0].QuotaState.Remaining)
	require.Equal(t, "AVAILABLE", view.Operations[1].Status)
	allowed = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; view, err = m.capabilitiesOn(ctx, tx, 1, 7, 3, ""); return err }))
	require.Equal(t, "NOT_APPLICABLE", view.Operations[0].Status)
	require.Equal(t, "CONTEXT_REQUIRED", view.Operations[0].QuotaState.State)
}

func TestCapabilitiesReminderQuotaReadsActualUsageWithoutReservation(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, m.registry.Register(manifestTestOperation("agent_reminders.create")))
	require.NoError(t, m.publishManifest(ctx))
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Check", DueAt: m.now().Add(time.Hour), MaxFires: 1}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.reminderOn(ctx, tx, 1, 7, in); return err }))
	q := query.BindReminder(db, m.names)
	template, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	for i := 1; i < reminderLimit; i++ {
		row := *template
		id, err := m.ids.Next()
		require.NoError(t, err)
		row.ID = guid.ID(id)
		require.NoError(t, q.WithContext(ctx).Create(&row))
	}
	var view EffectiveCapabilities
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; view, err = m.capabilitiesOn(ctx, tx, 1, 7, 0, ""); return err }))
		require.Equal(t, "QUOTA_EXHAUSTED", view.Operations[0].Status)
		require.NotNil(t, view.Operations[0].QuotaState.Remaining)
		require.Zero(t, *view.Operations[0].QuotaState.Remaining)
	}
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, reminderLimit, count)
}

func TestManifestPublicationStorageAuditAndCorruptHistoryNeverReplacePublishedContract(t *testing.T) {
	for _, mode := range []string{"missing-ids", "missing-audit", "missing-events", "query", "create", "audit", "events", "corrupt", "version", "hash"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 7)
			require.NoError(t, m.registry.Register(manifestTestOperation("inbox_events.list")))
			require.NoError(t, m.publishManifest(ctx))
			q := query.BindManifestVersion(db, m.names)
			row, err := q.WithContext(ctx).First()
			require.NoError(t, err)
			fault := errors.New("publication storage unavailable")
			require.NoError(t, m.registry.Register(manifestTestOperation("protocol.live")))
			switch mode {
			case "missing-ids":
				m.ids = nil
			case "missing-audit":
				m.audit = nil
			case "missing-events":
				m.events = nil
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("manifest-read", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ManifestVersion.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("manifest-read")
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("manifest-write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ManifestVersion.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("manifest-write")
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "events":
				m.events = badPublisher{}
			case "corrupt":
				_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Update(q.Document, "not json")
				require.NoError(t, err)
			case "version":
				_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Update(q.ManifestVersion, "invalid")
				require.NoError(t, err)
			case "hash":
				_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Update(q.SHA256, "wrong")
				require.NoError(t, err)
				_, err = m.manifestOn(ctx, db, "")
				require.ErrorIs(t, err, ErrInvalid)
				return
			}
			require.Error(t, m.publishManifest(ctx))
			if mode == "query" {
				require.NoError(t, db.Callback().Query().Remove("manifest-read"))
			}
			count, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestManifestCompatibilityDetectsOutputBreaksAndChangedRequiredProperties(t *testing.T) {
	op := manifestTestOperation("protocol.live")
	for _, tc := range []struct {
		schema  string
		version string
	}{
		{`{"type":"object","properties":{"extra":{"type":"string"}}}`, "1.3.0"},
		{`{"type":"object","required":["extra"],"properties":{"extra":{"type":"string"}}}`, "2.0.0"},
		{`{"type":"array"}`, "2.0.0"},
	} {
		next := op
		next.OutputSchema = json.RawMessage(tc.schema)
		require.Equal(t, tc.version, nextManifestVersion("1.2.3", []Operation{op}, []Operation{next}))
	}
	next := op
	next.RequiredPermission = "changed"
	require.Equal(t, "2.0.0", nextManifestVersion("1.2.3", []Operation{op}, []Operation{next}))
	require.Empty(t, nextManifestVersion("invalid", []Operation{op}, []Operation{op}))
	require.False(t, optionalSchemaExtension(json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`), json.RawMessage(`{"type":"object","properties":{"name":{"type":"integer"}}}`)))
}

func TestMountedCatalogFinalizationPublishesActualManifestOnlyAfterSuccessfulSeal(t *testing.T) {
	m, db, _, api := agentHTTPHarness(t)
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	require.NoError(t, FinalizeMountedOperations(services, api))
	doc, err := m.manifestOn(agentContext(t, 7), db, "")
	require.NoError(t, err)
	require.NotEmpty(t, doc.Operations)
	for _, mode := range []string{"unsealed", "missing", "nil-finalize", "failed-finalize", "missing-publisher", "nil-publisher"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			if mode != "missing" && mode != "unsealed" {
				finalize := func(api huma.API) error { return nil }
				if mode == "nil-finalize" {
					finalize = nil
				}
				if mode == "failed-finalize" {
					finalize = func(huma.API) error { return ErrConflict }
				}
				require.NoError(t, module.Provide(s, MountedOperationCatalog{Finalize: finalize}))
				if mode == "nil-publisher" {
					require.NoError(t, module.Provide(s, ManifestPublisher{}))
				}
			}
			if mode != "unsealed" {
				require.NoError(t, s.Seal())
			}
			e := FinalizeMountedOperations(s, api)
			if mode == "missing" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}
