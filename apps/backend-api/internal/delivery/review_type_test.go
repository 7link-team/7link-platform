package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func reviewTypeFixture() RequirementReviewTypeSettings {
	return RequirementReviewTypeSettings{Name: "Requirement review", AppliesTo: RequirementReviewAppliesTo{Requirement: true, WorkItemTypes: []string{}}, ReviewNodes: []string{"DRAFT"}, DefaultReviewerIDs: []guid.ID{11}, MinReviewers: 1, ContentFields: []string{"title", "description", "criteria"}, PerACRequired: true, Propagate: true}
}
func TestReviewTypeConfigurationDefaultsSnapshotsNodesAndPropagation(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	settings := reviewTypeFixture()
	created, e := m.createReviewTypeHTTP(ctx, &reviewTypeCreateInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, Key: "type-create", Body: settings})
	require.NoError(t, e)
	typ := created.Body.Data
	replay, e := m.createReviewTypeHTTP(ctx, &reviewTypeCreateInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, Key: "type-create", Body: settings})
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, typ.ID, replay.Body.Data.ID)
	listed, e := m.listReviewTypesHTTP(ctx, &reviewTypeListInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, Limit: 50})
	require.NoError(t, e)
	require.Len(t, listed.Body.Data, 1)
	create := RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewTypeID: &typ.ID}
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; sheet, e = m.createSheetOn(ctx, tx, 99, create); return e }))
	require.Equal(t, settings.Name, sheet.Document.ReviewType)
	require.Equal(t, []guid.ID{11}, sheet.Document.Reviewers)
	require.Equal(t, 1, sheet.Document.Minimum)
	require.Equal(t, "DRAFT", sheet.Document.ReviewNode)
	require.Equal(t, 1, sheet.Document.ReviewTypeSnapshot.Version)
	changed := settings
	changed.Name = "Changed"
	changed.ReviewNodes = []string{"BASELINED"}
	changed.MinReviewers = 2
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 1, changed); return e }))
	frozen := readSheetTest(t, m, db, ctx, sheet.ID)
	require.Equal(t, settings.Name, frozen.Document.ReviewTypeSnapshot.Settings.Name)
	require.Equal(t, 1, frozen.Document.Minimum)
	// The new type configuration immediately applies in an already-existing project.
	second, e := m.createRequirementOnInTest(ctx, db, 99)
	require.NoError(t, e)
	create.RequirementID = second.ID
	create.RequirementVersion = second.Version
	create.Reviewers = []guid.ID{11, 12}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, create); return e }), ErrRequirementStage)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 1, settings); return e }), ErrWorkVersion)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 2, typ.ID, 2, settings); return e }), ErrWorkForbidden)
}
func (m *Module) createRequirementOnInTest(ctx context.Context, db *gorm.DB, project guid.ID) (RequirementView, error) {
	var out RequirementView
	e := db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.createRequirementOn(ctx, tx, 1, project, 11, requirementCreateBody{Title: "Second", Criteria: []requirementCriterionInput{{Text: "Expected output", Required: true}}})
		return e
	})
	return out, e
}
func TestReviewTypeValidationAuthorizationAndRollback(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	for _, mutate := range []func(*RequirementReviewTypeSettings){func(v *RequirementReviewTypeSettings) { v.MinReviewers = 11 }, func(v *RequirementReviewTypeSettings) { v.ReviewNodes = []string{"invented"} }, func(v *RequirementReviewTypeSettings) { v.DefaultReviewerIDs = []guid.ID{11, 11} }, func(v *RequirementReviewTypeSettings) { v.ContentFields = []string{"unknown"} }} {
		bad := reviewTypeFixture()
		mutate(&bad)
		require.ErrorIs(t, validateReviewType(bad), ErrInvalidArgument)
	}
	failure := errors.New("outbox failed")
	m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture()); return e }), failure)
	q := query.BindReviewTypes(db, m.service.names)
	count, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture()); return e }), ErrWorkForbidden)
	d := RequirementReviewDocument{ReviewTypeSnapshot: &RequirementReviewTypeView{Settings: RequirementReviewTypeSettings{PerACRequired: false}}}
	require.NoError(t, validateRequirementReviewOpinion(d, RequirementReviewOpinion{Decision: "REJECT"}))
	d.ReviewTypeSnapshot.Settings.PerACRequired = true
	require.ErrorIs(t, validateRequirementReviewOpinion(d, RequirementReviewOpinion{Decision: "REJECT"}), ErrInvalidArgument)
}
func TestReviewTypeHTTPUsesExportedTenantAndTypePaths(t *testing.T) {
	_, api := humatest.New(t)
	path := "/tenants/{tenant_id}/review-types/{type_id}"
	huma.Register(api, huma.Operation{OperationID: "review-type-path", Method: "PUT", Path: path}, func(_ context.Context, in *reviewTypeUpdateInput) (*struct{ Body string }, error) {
		require.Equal(t, "1", in.TenantID)
		require.Equal(t, "77", in.TypeID)
		return &struct{ Body string }{Body: in.TypeID}, nil
	})
	names := map[string]bool{}
	for _, p := range api.OpenAPI().Paths[path].Put.Parameters {
		names[p.Name] = true
	}
	require.True(t, names["tenant_id"])
	require.True(t, names["type_id"])
}

func TestReviewTypeOptionalCriterionRejectionStillRejectsSheet(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	settings := reviewTypeFixture()
	settings.PerACRequired = false
	var typ RequirementReviewTypeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		typ, e = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, settings)
		return e
	}))
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sheet, e = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewTypeID: &typ.ID})
		return e
	}))
	action := &sheetActionInput{}
	action.Body.Version = sheet.Version
	action.Body.Action = "OPINION"
	action.Body.Opinion = RequirementReviewOpinion{Decision: "REJECT", Comment: "Needs revision"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sheet, e = m.actionSheetOn(ctx, tx, 99, sheet.ID, action)
		return e
	}))
	action.Body.Version = sheet.Version
	action.Body.Action = "CLOSE"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sheet, e = m.actionSheetOn(ctx, tx, 99, sheet.ID, action)
		return e
	}))
	require.Equal(t, "REJECTED", sheet.State)
	require.Empty(t, sheet.Document.ReworkIDs, "no criterion was rejected so no criterion-specific task is invented")
}

func TestReviewTypeFalsePropagationProjectOverridesAndGlobalReset(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var typ RequirementReviewTypeView
	settings := reviewTypeFixture()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		typ, e = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, settings)
		return e
	}))
	settings.Propagate = false
	settings.Name = "New projects only"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 1, settings); return e }))
	create := RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewTypeID: &typ.ID}
	var frozen *RequirementReviewTypeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		_, frozen, e = m.prepareReviewCreateOn(ctx, tx, 99, create)
		return e
	}))
	require.Equal(t, "Requirement review", frozen.Settings.Name)
	require.Equal(t, 1, frozen.Version)
	local := reviewTypeFixture()
	local.Name = "Project override"
	local.Propagate = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveProjectReviewTypeOn(ctx, tx, 1, 99, typ.ID, 1, local); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		_, frozen, e = m.prepareReviewCreateOn(ctx, tx, 99, create)
		return e
	}))
	require.Equal(t, "Project override", frozen.Settings.Name)
	require.Equal(t, 2, frozen.Version)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveProjectReviewTypeOn(ctx, tx, 1, 99, typ.ID, 1, local); return e }), ErrWorkVersion)
	settings.Name = "Propagated"
	settings.Propagate = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 3, settings); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		_, frozen, e = m.prepareReviewCreateOn(ctx, tx, 99, create)
		return e
	}))
	require.Equal(t, "Propagated", frozen.Settings.Name)
	require.Equal(t, 4, frozen.Version)
}

func TestReviewTypeDefaultsRequireCurrentHumanOrAgentMembershipAndAtomicSave(t *testing.T) {
	for _, mode := range []string{"lock", "actor_read", "actor_inactive", "actor_member", "actor_removed", "policy", "reviewer_read", "agent_missing", "agent_read", "agent_inactive", "reviewer_member", "reviewer_removed", "save", "audit", "id", "project_read"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			failure := errors.New("review type source unavailable")
			settings := reviewTypeFixture()
			settings.DefaultReviewerIDs = []guid.ID{20}
			settings.Propagate = mode != "project_read"
			m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if mode == "policy" {
					return authz.DataConstraint{}, failure
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if mode == "actor_read" && id == 11 || mode == "reviewer_read" && id == 20 {
					return identity.Principal{}, false, failure
				}
				return identity.Principal{ID: id}, id == 11 && mode != "actor_inactive", nil
			}
			m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _, id guid.ID) (bool, error) {
				if mode == "actor_member" && id == 11 || mode == "reviewer_member" && id == 20 {
					return false, failure
				}
				return !(mode == "actor_removed" && id == 11 || mode == "reviewer_removed" && id == 20), nil
			})
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if mode == "agent_read" {
					return fleet.AgentIdentity{}, false, failure
				}
				state := "ACTIVE"
				if mode == "agent_inactive" {
					state = "REVOKED"
				}
				return fleet.AgentIdentity{ID: 120, TenantID: 1, PrincipalID: 20, State: state}, true, nil
			}
			if mode == "agent_missing" {
				m.agents.ByPrincipalOn = nil
			}
			if mode == "lock" {
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			}
			if mode == "id" {
				m.ids = unavailableActivityIDs{err: failure}
			}
			if mode == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			if mode == "save" {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("review-type-save", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.ReviewTypes.String() {
						tx.AddError(failure)
					}
				}))
			}
			if mode == "project_read" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("review-type-project-source", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(failure)
					}
				}))
			}
			e := db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, 0, 0, settings); return e })
			require.Error(t, e)
			if mode != "actor_inactive" && mode != "actor_removed" && mode != "agent_missing" && mode != "agent_inactive" && mode != "reviewer_removed" {
				require.ErrorIs(t, e, failure)
			}
			var count int64
			require.NoError(t, db.Table(m.service.names.ReviewTypes.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
