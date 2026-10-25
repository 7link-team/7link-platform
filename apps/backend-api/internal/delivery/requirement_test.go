package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func requirementHarness(t *testing.T) (*Module, *gorm.DB, context.Context, requirementCreateBody) {
	m, db, ctx, _ := workHTTPHarness(t)
	m.requirementCases = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]RequirementCaseSnapshot, error) {
		return []RequirementCaseSnapshot{}, nil
	}
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, entity guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Contains(t, []string{"requirement_read", "requirement_write", "work_item_write", "work_item_read"}, permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	return m, db, ctx, requirementCreateBody{Title: " Requirement ", Description: "Business objective", Priority: 1, Criteria: []requirementCriterionInput{{Text: " Acceptance ", Required: true}, {Text: "Optional", Required: false}}}
}

func TestRequirementMutationsRecheckCurrentAuthorityAndMembership(t *testing.T) {
	failure := errors.New("authority dependency failed")
	for _, scenario := range []string{"principal_lock", "policy_error", "explicit_deny", "invalid_owner_scope", "other_owner", "inactive_human", "wrong_human", "human_lookup", "inactive_member", "member_lookup", "missing_project", "closed_project", "missing_policy"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, body := requirementHarness(t)
			want := failure
			switch scenario {
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "policy_error":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "explicit_deny":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
				want = ErrWorkForbidden
			case "invalid_owner_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				want = ErrUnavailable
			case "other_owner":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
				}
				want = ErrWorkForbidden
			case "inactive_human", "wrong_human", "human_lookup":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					if scenario == "human_lookup" {
						return identity.Principal{}, false, failure
					}
					return identity.Principal{ID: 22}, scenario != "inactive_human", nil
				}
				if scenario != "human_lookup" {
					want = ErrInvalidMember
				}
			case "inactive_member", "member_lookup":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "member_lookup" {
						return false, failure
					}
					return false, nil
				})
				if scenario == "inactive_member" {
					want = ErrInvalidMember
				}
			case "missing_project":
				require.NoError(t, db.Exec("DELETE FROM d_delivery_projects").Error)
				want = ErrProjectNotFound
			case "closed_project":
				require.NoError(t, db.Exec("UPDATE d_delivery_projects SET stage='CLOSED'").Error)
				want = ErrRequirementStage
			case "missing_policy":
				m.workPolicy = nil
				want = ErrUnavailable
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.createRequirementOn(ctx, tx, 1, 99, 11, body); return e })
			require.ErrorIs(t, err, want)
			for _, table := range []string{"d_delivery_requirements", "d_delivery_acceptance_criteria", "d_delivery_project_counters"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count, table)
			}
		})
	}
	m, db, ctx, body := requirementHarness(t)
	_, err := m.createRequirementOn(ctx, nil, 1, 99, 11, body)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.createRequirementOn(ctx, db, 1, 99, 11, body)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createRequirementOn(ctx, tx, 1, 99, 12, body); return e }), ErrInvalidArgument)
}

func TestRequirementCreateFailureRollsBackRowsCounterAndRetryReceipt(t *testing.T) {
	for _, stage := range []string{"requirement", "criterion", "audit", "event"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := requirementHarness(t)
			auditBefore, eventsBefore := m.service.audit, m.service.events
			switch stage {
			case "requirement", "criterion":
				table := "d_delivery_requirements"
				if stage == "criterion" {
					table = "d_delivery_acceptance_criteria"
				}
				require.NoError(t, db.Exec("CREATE TRIGGER reject_create BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'creation refused'); END").Error)
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit refused") }
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("event refused") })
			}
			input := &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "atomic-create"}
			_, err := m.createRequirementHTTP(ctx, input)
			assertWorkStatus(t, err, 500)
			for _, table := range []string{"d_delivery_requirements", "d_delivery_acceptance_criteria", "d_delivery_project_counters"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count, table)
			}
			require.NoError(t, db.Exec("DROP TRIGGER IF EXISTS reject_create").Error)
			m.service.audit, m.service.events = auditBefore, eventsBefore
			// Failed commands leave no successful receipt; the exact request can commit once.
			created, err := m.createRequirementHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, 1, created.Body.Data.Number)
			replayed, err := m.createRequirementHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, created.Body.Data.ID, replayed.Body.Data.ID)
			require.Equal(t, "true", replayed.Replayed)
		})
	}
}

func TestRequirementLimitExcludesArchivedRowsAndPreservesNumbers(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	now := time.Now().UTC()
	rows := make([]*model.Requirement, 500)
	for i := range rows {
		rows[i] = &model.Requirement{TenantID: 1, ProjectID: 99, ID: guid.ID(1000 + i), Number: i + 1, Title: "Existing", Status: "DRAFT", Version: 1, CreatedAt: now, UpdatedAt: now}
	}
	r := query.BindRequirements(db, m.service.names)
	require.NoError(t, r.WithContext(ctx).CreateInBatches(rows, 50))
	_, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	assertWorkStatus(t, err, 422)
	require.NoError(t, db.Exec("UPDATE d_delivery_requirements SET archived_at=? WHERE id=1000", now).Error)
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	require.Equal(t, 501, created.Body.Data.Number)
	count, err := r.WithContext(ctx).Where(r.ArchivedAt.IsNull()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 500, count)
}

func TestRequirementCriterionRestoreHonorsActiveLimitAndStableIdentity(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	body.Criteria = make([]requirementCriterionInput, 50)
	for i := range body.Criteria {
		body.Criteria[i] = requirementCriterionInput{Text: "Original", Required: true}
	}
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	id := created.Body.Data.ID
	detail, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	first, second := detail.Criteria[0].ID, detail.Criteria[1].ID
	archived := true
	patch := &criterionPatchInput{EntityID: "99", RequirementID: id.String(), CriterionID: first.String(), IfMatch: "\"1\"", Body: criterionPatchBody{Archived: &archived}}
	_, err = m.patchCriterionHTTP(ctx, patch)
	require.NoError(t, err)
	_, err = m.createCriterionHTTP(ctx, &requirementEditInput[criterionCreateBody]{EntityID: "99", RequirementID: id.String(), IfMatch: "\"2\"", Body: criterionCreateBody{Text: "Replacement", Required: true}})
	require.NoError(t, err)
	archived = false
	patch.IfMatch = "\"3\""
	_, err = m.patchCriterionHTTP(ctx, patch)
	assertWorkStatus(t, err, 422)
	detail, err = m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Equal(t, 3, detail.Version)
	require.Len(t, detail.Criteria, 50)
	archived = true
	patch.CriterionID = second.String()
	_, err = m.patchCriterionHTTP(ctx, patch)
	require.NoError(t, err)
	archived = false
	text, required, position := "Restored and revised", false, 49
	patch.CriterionID, patch.IfMatch = first.String(), "\"4\""
	patch.Body.Text, patch.Body.Required, patch.Body.Position = &text, &required, &position
	_, err = m.patchCriterionHTTP(ctx, patch)
	require.NoError(t, err)
	detail, err = m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Len(t, detail.Criteria, 50)
	found := false
	for _, criterion := range detail.Criteria {
		if criterion.ID == first {
			found = true
			require.Equal(t, "C1", criterion.Code)
			require.Equal(t, text, criterion.Text)
			require.False(t, criterion.Required)
			require.Equal(t, position, criterion.Position)
		}
	}
	require.True(t, found, "restored clause must retain its original stable ID")
}

func TestRequirementBaselineAndWorkLink(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "requirement-create"})
	require.NoError(t, err)
	id := created.Body.Data.ID
	read, err := m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: id.String()})
	require.NoError(t, err)
	require.Equal(t, "\"1\"", read.ETag)
	require.Equal(t, "DRAFT", read.Body.Data.Status)
	require.Empty(t, read.Body.Data.Versions)
	require.Len(t, read.Body.Data.Criteria, 2)
	require.Equal(t, "C1", read.Body.Data.Criteria[0].Code)
	require.Equal(t, "Acceptance", read.Body.Data.Criteria[0].Text)
	in := &requirementBaselineInput{EntityID: "99", RequirementID: id.String(), IfMatch: read.ETag, IdempotencyKey: "baseline-once"}
	baseline, err := m.baselineRequirementHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 2, baseline.Body.Data.Requirement.Version)
	replayed, err := m.baselineRequirementHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, baseline.Body.Data, replayed.Body.Data)
	_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: id.String(), IfMatch: "\"1\""})
	assertWorkStatus(t, err, 409)
	_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: id.String(), IfMatch: "\"2\""})
	assertWorkStatus(t, err, 422)
	detail, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Len(t, detail.Versions, 1)
	v := detail.Versions[0]
	encoded, err := json.Marshal(v.Snapshot)
	require.NoError(t, err)
	hash := sha256.Sum256(encoded)
	require.Equal(t, hex.EncodeToString(hash[:]), v.ContentHash)
	require.Equal(t, baseline.Body.Data.Version.ContentHash, v.ContentHash)
	require.Equal(t, read.Body.Data.Criteria[0].ID, v.Snapshot.Criteria[0].ID)
	require.Equal(t, 1, v.Snapshot.Criteria[0].IntroducedInVersion)
	_, err = m.readRequirement(ctx, db, 1, 100, id, 11)
	require.ErrorIs(t, err, ErrProjectNotFound)
	work := workCreateBody{Title: "Task", Type: "TASK", Objective: "Deliver", AccountablePrincipalID: 11, RequirementID: &id, BaselineVersionNo: &v.VersionNo, Criteria: []workCriterionInput{{Text: "Test", VerificationMethod: "Run"}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, work); return e }))
	require.NoError(t, db.Exec("UPDATE d_delivery_requirements SET title='Later' WHERE id=?", id).Error)
	later, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Equal(t, "Requirement", later.Versions[0].Snapshot.Title)
}

func TestRequirementReplayFiltersAndDataScopes(t *testing.T) {
	m, _, ctx, body := requirementHarness(t)
	in := &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "same-draft"}
	one, err := m.createRequirementHTTP(ctx, in)
	require.NoError(t, err)
	two, err := m.createRequirementHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, one.Body.Data, two.Body.Data)
	require.Equal(t, "true", two.Replayed)
	changed := *in
	changed.Body.Title = "Different"
	_, err = m.createRequirementHTTP(ctx, &changed)
	assertWorkStatus(t, err, 422)
	list, err := m.listRequirementsHTTP(ctx, &requirementListInput{EntityID: "99", Limit: 20, RawQuery: url.Values{"title": {"like:Require"}, "priority": {"lte:1"}}})
	require.NoError(t, err)
	require.EqualValues(t, 1, list.Body.Meta.Page.Total)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
	}
	list, err = m.listRequirementsHTTP(ctx, &requirementListInput{EntityID: "99", Limit: 20})
	require.NoError(t, err)
	require.Zero(t, list.Body.Meta.Page.Total)
	_, err = m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: one.Body.Data.ID.String()})
	assertWorkStatus(t, err, 404)
	_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: one.Body.Data.ID.String(), IfMatch: "\"1\""})
	assertWorkStatus(t, err, 403)
	_, err = m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "denied-create"})
	assertWorkStatus(t, err, 403)
}

func TestRequirementRollbackAndRequiredCriterion(t *testing.T) {
	for _, stage := range []string{"criteria", "audit", "event", "version", "update"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := requirementHarness(t)
			created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
			require.NoError(t, err)
			switch stage {
			case "criteria":
				require.NoError(t, db.Exec("CREATE TRIGGER no_criteria BEFORE UPDATE ON d_delivery_acceptance_criteria BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("failed audit") }
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("failed event") })
			case "version":
				require.NoError(t, db.Exec("CREATE TRIGGER no_version BEFORE INSERT ON d_delivery_requirement_versions BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER no_update BEFORE UPDATE ON d_delivery_requirements BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			}
			_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: created.Body.Data.ID.String(), IfMatch: "\"1\"", IdempotencyKey: "failing-baseline"})
			assertWorkStatus(t, err, 500)
			r := query.BindRequirements(db, m.service.names)
			row, err := r.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "DRAFT", row.Status)
			require.Equal(t, 1, row.Version)
			vq := query.BindRequirementVersions(db, m.service.names)
			n, err := vq.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, n)
			cq := query.BindCriteria(db, m.service.names)
			clauses, err := cq.WithContext(ctx).Find()
			require.NoError(t, err)
			for _, c := range clauses {
				require.Zero(t, c.IntroducedInVersion)
			}
		})
	}
	m, _, ctx, body := requirementHarness(t)
	body.Criteria[0].Required = false
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: created.Body.Data.ID.String(), IfMatch: "\"1\""})
	assertWorkStatus(t, err, 422)
}

func TestRequirementHTTPContracts(t *testing.T) {
	m, _, ctx, _ := requirementHarness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "test-req-create", Method: "POST", Path: requirementPath, DefaultStatus: 201}, func(_ context.Context, in *requirementCreateInput) (*deliveryCommandOutput[RequirementView], error) {
		return m.createRequirementHTTP(ctx, in)
	})
	for _, body := range []string{`{"title":""}`, `{"title":"a","priority":4}`, `{"title":"a","criteria":[{"text":""}]}`, `{"title":"a","criteria":[{"text":"` + strings.Repeat("a", 2001) + `"}]}`} {
		require.Equal(t, 422, api.Post("/entities/99/requirements", strings.NewReader(body)).Code)
	}
	valid := `{"title":"` + strings.Repeat("😀", 200) + `","criteria":[{"text":"ok","required":true}]}`
	require.Equal(t, 201, api.Post("/entities/99/requirements", strings.NewReader(valid)).Code)
	huma.Register(api, huma.Operation{OperationID: "test-req-list", Method: "GET", Path: requirementPath}, func(_ context.Context, in *requirementListInput) (*respx.Body[[]RequirementView], error) {
		return m.listRequirementsHTTP(ctx, in)
	})
	for _, q := range []string{"limit=101", "limit=0", "offset=-1", "priority=eq:bad", "title=eq:bad", "unknown=eq:x", "sort=title"} {
		require.Equal(t, 422, api.Get("/entities/99/requirements?"+q).Code, q)
	}
	for _, tag := range []string{"1", "W/\"1\"", "*", "\"0\"", "\"-1\""} {
		_, err := m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: "8", IfMatch: tag})
		assertWorkStatus(t, err, 422)
	}
}

func TestRequirementDraftEditsPreserveCriterionIdentityAndFreezeAtBaseline(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	id := created.Body.Data.ID
	before, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	title := " Revised objective "
	priority := 0
	description := ""
	meta := &requirementEditInput[requirementPatchBody]{EntityID: "99", RequirementID: id.String(), IfMatch: "\"1\"", IdempotencyKey: "edit-metadata", Body: requirementPatchBody{Title: &title, Description: &description, Priority: &priority}}
	updated, err := m.patchRequirementHTTP(ctx, meta)
	require.NoError(t, err)
	require.Equal(t, 200, updated.Status)
	require.Equal(t, "Revised objective", updated.Body.Data.Title)
	require.Equal(t, 0, updated.Body.Data.Priority)
	replay, err := m.patchRequirementHTTP(ctx, meta)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, updated.Body.Data, replay.Body.Data)
	text := "Corrected acceptance"
	required := false
	position := 3
	edit := &criterionPatchInput{EntityID: "99", RequirementID: id.String(), CriterionID: before.Criteria[0].ID.String(), IfMatch: "\"2\"", Body: criterionPatchBody{Text: &text, Required: &required, Position: &position}}
	_, err = m.patchCriterionHTTP(ctx, edit)
	require.NoError(t, err)
	_, err = m.patchCriterionHTTP(ctx, edit)
	assertWorkStatus(t, err, 409)
	archived := true
	edit.IfMatch = "\"3\""
	edit.Body = criterionPatchBody{Archived: &archived}
	_, err = m.patchCriterionHTTP(ctx, edit)
	require.NoError(t, err)
	add := &requirementEditInput[criterionCreateBody]{EntityID: "99", RequirementID: id.String(), IfMatch: "\"4\"", IdempotencyKey: "new-criterion", Body: criterionCreateBody{Text: "New required criterion", Required: true, Position: 0}}
	added, err := m.createCriterionHTTP(ctx, add)
	require.NoError(t, err)
	require.Equal(t, 201, added.Status)
	_, err = m.createCriterionHTTP(ctx, add)
	require.NoError(t, err)
	draft, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Equal(t, 5, draft.Version)
	require.Empty(t, draft.Description)
	require.Len(t, draft.Criteria, 2)
	require.Equal(t, "C3", draft.Criteria[0].Code, "archived C1 must never be reused")
	baseline, err := m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: id.String(), IfMatch: "\"5\""})
	require.NoError(t, err)
	require.Equal(t, 6, baseline.Body.Data.Requirement.Version)
	meta.IfMatch = "\"6\""
	meta.IdempotencyKey = ""
	_, err = m.patchRequirementHTTP(ctx, meta)
	assertWorkStatus(t, err, 422)
	add.IfMatch = "\"6\""
	add.IdempotencyKey = ""
	_, err = m.createCriterionHTTP(ctx, add)
	assertWorkStatus(t, err, 422)
	edit.IfMatch = "\"6\""
	_, err = m.patchCriterionHTTP(ctx, edit)
	assertWorkStatus(t, err, 422)
	frozen, err := m.readRequirement(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Equal(t, draft.Criteria[0].ID, frozen.Criteria[0].ID)
	require.Equal(t, "Revised objective", frozen.Versions[0].Snapshot.Title)
}

func TestRequirementDraftLimitsScopeAndRollback(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	body.Criteria = make([]requirementCriterionInput, 50)
	for i := range body.Criteria {
		body.Criteria[i] = requirementCriterionInput{Text: strings.Repeat("😀", 2000), Required: true}
	}
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "large-requirement"})
	require.NoError(t, err)
	id := created.Body.Data.ID
	_, err = m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body, IdempotencyKey: "large-requirement"})
	require.NoError(t, err)
	add := &requirementEditInput[criterionCreateBody]{EntityID: "99", RequirementID: id.String(), IfMatch: "\"1\"", Body: criterionCreateBody{Text: "Too many", Required: true}}
	_, err = m.createCriterionHTTP(ctx, add)
	assertWorkStatus(t, err, 422)
	other, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: requirementCreateBody{Title: "Other", Criteria: []requirementCriterionInput{{Text: "Other criterion", Required: true}}}})
	require.NoError(t, err)
	otherDetail, err := m.readRequirement(ctx, db, 1, 99, other.Body.Data.ID, 11)
	require.NoError(t, err)
	changed := "Should not change"
	patch := &criterionPatchInput{EntityID: "99", RequirementID: id.String(), CriterionID: otherDetail.Criteria[0].ID.String(), IfMatch: "\"1\"", Body: criterionPatchBody{Text: &changed}}
	_, err = m.patchCriterionHTTP(ctx, patch)
	assertWorkStatus(t, err, 404)
	patch.CriterionID = otherDetail.Criteria[0].ID.String()
	patch.RequirementID = other.Body.Data.ID.String()
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit refused") }
	_, err = m.patchCriterionHTTP(ctx, patch)
	assertWorkStatus(t, err, 500)
	unchanged, err := m.readRequirement(ctx, db, 1, 99, other.Body.Data.ID, 11)
	require.NoError(t, err)
	require.Equal(t, 1, unchanged.Version)
	require.Equal(t, "Other criterion", unchanged.Criteria[0].Text)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
	}
	_, err = m.patchCriterionHTTP(ctx, patch)
	assertWorkStatus(t, err, 404)
}
