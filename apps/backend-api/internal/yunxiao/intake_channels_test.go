package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func intakeSettingsFixture() IntakeChannelSettings {
	return IntakeChannelSettings{DisplayName: "Client workspace", Language: "en-US", DailyLimit: 20, ReminderHours: 48, TimeoutHours: 168}
}
func TestIntakeChannelTriageBindingPersistsAndRechecksCurrentBinding(t *testing.T) {
	m, db, ctx := workHarness(t)
	id := guid.ID(9007199254740993)
	in := intakeSettingsFixture()
	in.TriageAgentID = &id
	valid := true
	m.projects.ValidateTriageAgentOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID) error {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, id, agent)
		if !valid {
			return ErrForbidden
		}
		return nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "PORTAL", 0, in)
		require.Equal(t, &id, v.Settings.TriageAgentID)
		return e
	}))
	valid = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "PORTAL", 1, in); return e }), ErrForbidden)
}
func TestIntakeChannelSettingsPersistVersionsAndRollback(t *testing.T) {
	m, db, ctx := workHarness(t)
	in := intakeSettingsFixture()
	var v IntakeChannelView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.saveIntakeChannelOn(ctx, tx, 1, 99, "EMAIL", 0, in)
		return e
	}))
	require.Equal(t, "UNAVAILABLE", v.Availability)
	require.Equal(t, 1, v.Version)
	in.DisplayName = "Updated workspace"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "EMAIL", 0, in); return e }), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.saveIntakeChannelOn(ctx, tx, 1, 99, "EMAIL", 1, in)
		return e
	}))
	require.Equal(t, 2, v.Version)
	failure := errors.New("outbox failed")
	m.events = testPublisher{err: failure}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, in); return e }), failure)
	q := query.BindIntakeChannels(db, m.names)
	n, e := q.WithContext(ctx).Where(q.Kind.Eq("API")).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "EMAIL", 2, in); return e }), ErrForbidden)
}
func TestIntakeChannelValidatesLimitsLocaleAndEmail(t *testing.T) {
	in := intakeSettingsFixture()
	require.NoError(t, validateIntakeSettings(in))
	for _, mutate := range []func(*IntakeChannelSettings){func(v *IntakeChannelSettings) { v.DailyLimit = 0 }, func(v *IntakeChannelSettings) { v.TimeoutHours = v.ReminderHours }, func(v *IntakeChannelSettings) { v.Language = "xx" }, func(v *IntakeChannelSettings) { v.SupportEmail = "not an email" }, func(v *IntakeChannelSettings) { v.DisplayName = " " }} {
		bad := in
		mutate(&bad)
		require.ErrorIs(t, validateIntakeSettings(bad), ErrInvalid)
	}
}

func TestIntakeChannelHTTPExtractsTenantProjectAndKind(t *testing.T) {
	_, api := humatest.New(t)
	path := "/tenants/{tenant_id}/projects/{project_id}/intake-channels/{kind}"
	huma.Register(api, huma.Operation{OperationID: "intake-policy-contract", Method: "PUT", Path: path}, func(_ context.Context, in *intakeChannelSave) (*struct{ Body string }, error) {
		require.Equal(t, "1", in.TenantID)
		require.Equal(t, "99", in.ProjectID)
		require.Equal(t, "EMAIL", in.Kind)
		return &struct{ Body string }{Body: in.Kind}, nil
	})
	names := map[string]bool{}
	for _, param := range api.OpenAPI().Paths[path].Put.Parameters {
		names[param.Name] = true
	}
	for _, name := range []string{"tenant_id", "project_id", "kind"} {
		require.True(t, names[name], name)
	}
	response := api.Put("/tenants/1/projects/99/intake-channels/EMAIL", "Idempotency-Key: config-key", strings.NewReader(`{"enabled":false,"display_name":"Workspace","support_email":"","language":"en-US","daily_limit":20,"receipt_enabled":false,"reminder_hours":48,"timeout_hours":168}`))
	require.Equal(t, 200, response.Code, response.Body.String())
}

func TestIntakeChannelHTTPPersistsReplaysListsAndRechecksAuthority(t *testing.T) {
	m, db, ctx := workHarness(t)
	path := IntakeChannelPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, Kind: "EMAIL"}
	in := &intakeChannelSave{IntakeChannelPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "intake-policy-create"}, Body: intakeSettingsFixture()}
	first, e := m.saveIntakeChannelHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.saveIntakeChannelHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	list, e := m.listIntakeChannelsHTTP(ctx, &path.WorkProjectPath)
	require.NoError(t, e)
	require.Len(t, list.Body.Data, 1)
	require.Equal(t, first.Body.Data, list.Body.Data[0])
	in.IfMatch = `"1"`
	in.IdempotencyKey = "intake-policy-update"
	in.Body.DisplayName = "Changed"
	changed, e := m.saveIntakeChannelHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, changed.Body.Data.Version)
	in.IfMatch = "bad"
	_, e = m.saveIntakeChannelHTTP(ctx, in)
	require.Error(t, e)
	in.IfMatch = `"1"`
	in.IdempotencyKey = "intake-policy-stale"
	_, e = m.saveIntakeChannelHTTP(ctx, in)
	require.Error(t, e)
	in.ProjectID = "0"
	_, e = m.saveIntakeChannelHTTP(ctx, in)
	require.Error(t, e)
	in.ProjectID = "99"
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	_, e = m.listIntakeChannelsHTTP(ctx, &path.WorkProjectPath)
	require.Error(t, e)
	in.IfMatch = ""
	in.IdempotencyKey = "intake-policy-create"
	in.Body = intakeSettingsFixture()
	_, e = m.saveIntakeChannelHTTP(ctx, in)
	require.Error(t, e)
	require.NoError(t, db.Table(m.names.IntakeChannels.String()).Where("id=?", first.Body.Data.ID).Update("document", "[").Error)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
	_, e = m.listIntakeChannelsHTTP(ctx, &path.WorkProjectPath)
	require.Error(t, e)
	bad := path.WorkProjectPath
	bad.TenantID = "0"
	_, e = m.listIntakeChannelsHTTP(ctx, &bad)
	require.Error(t, e)
	_, e = (&Module{}).listIntakeChannelsHTTP(ctx, &path.WorkProjectPath)
	require.Error(t, e)
}

func TestIntakePolicyFailedStorageAndIdentifiersDoNotPublishChanges(t *testing.T) {
	for _, mode := range []string{"id", "read", "save", "audit", "invalid_kind", "invalid_policy", "new_version"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			fault := errors.New("policy persistence failed")
			in := intakeSettingsFixture()
			kind := "EMAIL"
			version := 0
			switch mode {
			case "id":
				m.ids = &testIDs{err: fault}
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("channel_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeChannels.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("channel_storage")
			case "save":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("channel_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeChannels.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("channel_storage")
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "invalid_kind":
				kind = "UNKNOWN"
			case "invalid_policy":
				in.DailyLimit = 0
			case "new_version":
				version = 1
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, kind, version, in); return e }))
			if mode == "read" {
				require.NoError(t, db.Callback().Query().Remove("channel_storage"))
			}
			var count int64
			require.NoError(t, db.Table(m.names.IntakeChannels.String()).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("test_effects").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestIntakeTriageBindingRejectsInvalidOrUnavailableCurrentAgent(t *testing.T) {
	m, db, ctx := workHarness(t)
	settings := intakeSettingsFixture()
	zero := guid.ID(0)
	settings.TriageAgentID = &zero
	require.ErrorIs(t, validateIntakeSettings(settings), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.intakeTriageAgentOn(ctx, tx, 1, settings) }), ErrInvalid)
	agent := guid.ID(77)
	settings.TriageAgentID = &agent
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.intakeTriageAgentOn(ctx, tx, 1, settings) }), ErrUnavailable)
	for _, tc := range []struct{ source, want error }{{delivery.ErrUnavailable, ErrUnavailable}, {delivery.ErrWorkForbidden, ErrForbidden}} {
		m.projects.ValidateTriageAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return tc.source }
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.intakeTriageAgentOn(ctx, tx, 1, settings) }), tc.want)
	}
	settings.TriageAgentID = nil
	settings.NotifyPrincipalIDs = make([]guid.ID, 51)
	require.ErrorIs(t, validateIntakeSettings(settings), ErrInvalid)
}
