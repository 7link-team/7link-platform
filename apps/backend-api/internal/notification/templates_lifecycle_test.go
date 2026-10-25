package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTemplateLifecycleSubscriberRejectsMissingCatalogAndDispatchContext(t *testing.T) {
	f := newFixture(t)
	s := module.NewServices()
	require.Error(t, f.m.injectTemplateLifecycle(s))
	s = module.NewServices()
	require.NoError(t, module.Provide[templates.LifecycleFacts](s, templates.LifecycleFacts{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, f.m.injectTemplateLifecycle(s), ErrInvalid)
	for _, mode := range []string{"missing_catalog", "registration", "source", "context"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			subs := []events.Subscriber{}
			require.NoError(t, module.Provide[templates.LifecycleFacts](s, templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
				if mode == "source" {
					return templates.LifecycleView{}, false, ErrUnavailable
				}
				return templates.LifecycleView{}, false, nil
			}}))
			if mode != "missing_catalog" {
				catalog := fixtureCatalog{subscribers: &subs}
				if mode == "registration" {
					catalog.err = ErrUnavailable
				}
				require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
			}
			require.NoError(t, s.Seal())
			err := f.m.injectTemplateLifecycle(s)
			if mode == "missing_catalog" || mode == "registration" {
				require.Error(t, err)
				require.Empty(t, subs)
				return
			}
			require.NoError(t, err)
			require.Len(t, subs, 1)
			require.Equal(t, events.Transactional, subs[0].Mode)
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return subs[0].Handle(t.Context(), tx, events.Envelope{EventID: 1001, TenantID: 7, Subject: events.Subject{ID: 900, Kind: "template_lifecycle"}})
			})
			if mode == "source" {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, events.ErrInvalidEvent)
			}
		})
	}
}

func TestTemplateLifecycleNotificationConditionsAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		typ, event, state string
		project           guid.ID
		want              int
	}{
		{"verifier.calibration_drifted", "verifier.calibration_changed", "DRIFTED", 0, 2},
		{"provider.unavailable", "provider.state_changed", "UNAVAILABLE", 81, 2},
		{"eval.below_target", "eval.run_completed", "COMPLETED", 0, 1},
		{"rubric.divergence_detected", "rubric.divergence_detected", "RECORDED", 81, 2},
		{"verifier.tool_unavailable", "verifier.tool_availability_changed", "UNAVAILABLE", 0, 2},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			f := newFixture(t)
			found := true
			fact := templates.LifecycleView{ID: 900, TemplateVersionID: 91, ProjectID: tc.project, ObjectKey: "stable-condition", Reference: "subject:501", EventType: tc.event, State: tc.state, NoticeType: tc.typ, InitiatorID: 11, Params: map[string]string{"verifier": "visual", "agreement": "0.5", "provider": "generator", "n": "2", "template": "91", "failed": "8", "subject": "501", "dimensions": "narrative", "tool": "ffmpeg"}}
			f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (templates.LifecycleView, bool, error) {
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, fact.ID, id)
				return fact, found, nil
			}}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: tc.project, SubjectKind: "template_lifecycle", SubjectID: 900, Type: tc.event}
			for range 2 {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectTemplateLifecycleOn(t.Context(), tx, e) }))
			}
			q := query.BindNotifications(f.db, f.m.names)
			rows, err := q.WithContext(t.Context()).Find()
			require.NoError(t, err)
			require.Len(t, rows, tc.want)
			for _, row := range rows {
				require.Equal(t, tc.typ, row.Type)
				if tc.typ == "eval.below_target" {
					require.Equal(t, guid.ID(11), row.RecipientPrincipalID)
				}
				visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
				require.NoError(t, err)
				require.True(t, visible)
			}
			// Current IAM revocation removes content and counts, even though the
			// immutable source observation and notification still exist.
			f.allow = false
			for _, row := range rows {
				visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
				require.NoError(t, err)
				require.False(t, visible)
			}
			f.allow = true
			found = false
			for _, row := range rows {
				visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
				require.NoError(t, err)
				require.False(t, visible)
			}
		})
	}
}

func TestTemplateLifecycleNotificationDedupeSchedules(t *testing.T) {
	for _, typ := range []string{"verifier.calibration_drifted", "verifier.tool_unavailable", "rubric.divergence_detected"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			fact := templates.LifecycleView{ID: 900, TemplateVersionID: 91, ObjectKey: "fixed", Reference: "WORK_ITEM:501", NoticeType: typ, InitiatorID: 11, Params: map[string]string{}}
			switch typ {
			case "verifier.calibration_drifted":
				fact.EventType = "verifier.calibration_changed"
				fact.Params = map[string]string{"verifier": "visual", "agreement": "0.5000"}
			case "verifier.tool_unavailable":
				fact.EventType = "verifier.tool_availability_changed"
				fact.Params = map[string]string{"tool": "ffmpeg"}
			case "rubric.divergence_detected":
				fact.EventType = typ
				fact.ProjectID = 81
				fact.Params = map[string]string{"subject": "WORK_ITEM:501", "dimensions": "narrative"}
			}
			f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
				return fact, true, nil
			}}
			publish := func() {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.projectTemplateLifecycleOn(t.Context(), tx, events.RecordedEvent{ID: fact.ID + 100, TenantID: 7, ProjectID: fact.ProjectID, SubjectID: fact.ID, SubjectKind: "template_lifecycle", Type: fact.EventType})
				}))
			}
			publish()
			fact.ID++
			publish()
			q := query.BindNotifications(f.db, f.m.names)
			n, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.EqualValues(t, 2, n)
			start := f.m.now()
			f.m.now = func() time.Time { return start.Add(24 * time.Hour) }
			fact.ID++
			publish()
			n, err = q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			expected := int64(2)
			if typ == "verifier.tool_unavailable" {
				expected = 4
			}
			require.Equal(t, expected, n)
			f.m.now = func() time.Time { return start.Add(7*24*time.Hour + time.Second) }
			fact.ID++
			publish()
			n, err = q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			if typ == "verifier.calibration_drifted" {
				require.EqualValues(t, 4, n)
			}
			if typ == "rubric.divergence_detected" {
				require.EqualValues(t, 2, n)
			}
		})
	}
}

func TestTemplateLifecycleAudienceRequiresSeededOwnerOrCurrentLead(t *testing.T) {
	f := newFixture(t)
	fact := templates.LifecycleView{ID: 900, TemplateVersionID: 91, NoticeType: "verifier.tool_unavailable"}
	f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
		return fact, true, nil
	}}
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "other-role"}}}, nil
	}
	q := query.BindNotifications(f.db, f.m.names)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.projectTemplateLifecycleOn(t.Context(), tx, events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 900, SubjectKind: "template_lifecycle", Type: ""})
	}))
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n, "broad permission alone is not the team-owner audience")
	// Missing source capabilities fail closed instead of falling back to broad
	// template_manage permission.
	f.m.templateLifecycle = templates.LifecycleFacts{}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.projectTemplateLifecycleOn(t.Context(), tx, events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 900, SubjectKind: "template_lifecycle"})
	}), ErrUnavailable)
}

func TestTemplateLifecycleSubscriberRegistrationAndChannelDefaults(t *testing.T) {
	f := newFixture(t)
	services := module.NewServices()
	require.NoError(t, services.Seal())
	require.NoError(t, f.m.injectTemplateLifecycle(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[templates.LifecycleFacts](services, templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
		return templates.LifecycleView{}, false, nil
	}}))
	require.NoError(t, module.Provide[events.SubscriberCatalog](services, fixtureCatalog{}))
	require.NoError(t, services.Seal())
	require.NoError(t, f.m.injectTemplateLifecycle(services))
	require.Equal(t, "DAILY_DIGEST", defaultEmail("verifier.calibration_drifted", Action))
	for _, typ := range []string{"provider.unavailable", "eval.below_target", "rubric.divergence_detected", "verifier.tool_unavailable"} {
		require.Equal(t, "OFF", defaultEmail(typ, Action))
		require.Zero(t, mergeWindow(typ))
	}
}

func TestTemplateLifecycleSubscriberAcceptsRealCatalogAlongsideExistingSubscriptions(t *testing.T) {
	f := newFixture(t)
	catalog := events.NewModule(f.m.runtime)
	// Use the real validator and duplicate-name registry: the fixture catalog
	// deliberately cannot prove either contract during composed service wiring.
	require.NoError(t, catalog.AddSubscribers(Name, f.m.subscriber(), f.m.messageEditedSubscriber()))
	services := module.NewServices()
	require.NoError(t, module.Provide[templates.LifecycleFacts](services, templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
		return templates.LifecycleView{}, false, nil
	}}))
	require.NoError(t, module.Provide[events.SubscriberCatalog](services, catalog))
	require.NoError(t, services.Seal())
	require.NoError(t, f.m.injectTemplateLifecycle(services))
}

func TestTemplateLifecycleProviderAffectedProjectsUseCurrentLeadAudience(t *testing.T) {
	f := newFixture(t)
	f.m.roles.RoleIDOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, key string) (guid.ID, error) {
		if key == "team_owner" {
			return 10, nil
		}
		return 20, nil
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		role := ""
		if r.EntityID == 0 && r.PrincipalID == 11 {
			role = "10"
		}
		if r.EntityID == 81 && r.PrincipalID == 12 {
			role = "20"
		}
		return authz.Explanation{Allowed: role != "", Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: role}}}, nil
	}
	fact := templates.LifecycleView{ID: 900, TemplateVersionID: 91, ObjectKey: "provider:global", Reference: "generator:v1", EventType: "provider.state_changed", NoticeType: "provider.unavailable", AffectedProjectIDs: []guid.ID{81}, Params: map[string]string{"provider": "generator:v1", "n": "2"}}
	f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
		return fact, true, nil
	}}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 900, SubjectKind: "template_lifecycle", Type: fact.EventType}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectTemplateLifecycleOn(t.Context(), tx, e) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.RecipientPrincipalID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID)
	require.Zero(t, rows[0].ProjectID)
	require.Equal(t, guid.ID(12), rows[1].RecipientPrincipalID)
	require.Equal(t, guid.ID(81), rows[1].ProjectID)
	fact.AffectedProjectIDs = nil
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[1], 12)
	require.NoError(t, err)
	require.False(t, visible)
}

func TestTemplateLifecycleFailuresRollbackAllRecipients(t *testing.T) {
	for _, mode := range []string{"invalid", "source", "mismatch", "candidates", "order", "visibility", "missing_guard", "lock", "guard", "read", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			fact := templates.LifecycleView{ID: 900, TemplateVersionID: 91, ObjectKey: "verifier", EventType: "verifier.calibration_changed", NoticeType: "verifier.calibration_drifted", QualityOwnerID: 11, Params: map[string]string{"verifier": "visual"}}
			f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
				if mode == "source" {
					return fact, false, ErrUnavailable
				}
				return fact, true, nil
			}}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 900, SubjectKind: "template_lifecycle", Type: fact.EventType}
			switch mode {
			case "invalid":
				e.ID = 0
			case "mismatch":
				e.SubjectID = 901
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "missing_guard":
				f.m.guard = nil
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("template_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectTemplateLifecycleOn(t.Context(), tx, e) }))
			if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Remove("template_read"))
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestTemplateAudienceAndOwnerFailuresFailClosed(t *testing.T) {
	f := newFixture(t)
	fact := templates.LifecycleView{ID: 900, NoticeType: "verifier.calibration_drifted", QualityOwnerID: 11}
	f.m.templateLifecycle = templates.LifecycleFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (templates.LifecycleView, bool, error) {
		return fact, true, nil
	}}
	row := &model.Notification{TenantID: 7, Type: fact.NoticeType, Params: `{"subject_id":"900"}`}
	yes, err := f.m.templateLifecycleAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.True(t, yes)
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`} {
		row.Params = raw
		yes, err = f.m.templateLifecycleAudienceOn(t.Context(), f.db, row, 11)
		require.ErrorIs(t, err, ErrInvalid)
		require.False(t, yes)
	}
	row.Params = `{"subject_id":"900"}`
	fact.NoticeType = "unknown"
	row.Type = fact.NoticeType
	yes, err = f.m.templateLifecycleAudienceOn(t.Context(), f.db, row, 11)
	require.ErrorIs(t, err, ErrInvalid)
	require.False(t, yes)
	for _, mode := range []string{"missing_role", "missing_explain", "unseeded", "role_failure", "explain_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			switch mode {
			case "missing_role":
				f.m.roles.RoleIDOn = nil
			case "missing_explain":
				f.m.explain = nil
			case "unseeded":
				f.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) {
					return 0, governance.ErrRoleNotSeeded
				}
			case "role_failure":
				f.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 0, ErrUnavailable }
			case "explain_failure":
				f.err = ErrUnavailable
			}
			yes, err := f.m.templateTeamOwnerOn(t.Context(), f.db, 7, 11)
			require.False(t, yes)
			if mode == "unseeded" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
