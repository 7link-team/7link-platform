package delivery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func scopeHarness(t *testing.T) (*Module, *gorm.DB, context.Context, RequirementView, scopeChangeBody) {
	t.Helper()
	m, db, ctx, body := requirementHarness(t)
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	baseline, err := m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: created.Body.Data.ID.String(), IfMatch: fmtETag(1)})
	require.NoError(t, err)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	m.scopeReauthentication = func(_ context.Context, _ *gorm.DB, actor guid.ID, _ string, proof, purpose, target string) error {
		require.EqualValues(t, 11, actor)
		require.Equal(t, "scope_change", purpose)
		require.Equal(t, "requirement:"+created.Body.Data.ID.String(), target)
		if proof != "verified" {
			return ErrWorkForbidden
		}
		return nil
	}
	title := "Revised business objective"
	return m, db, ctx, baseline.Body.Data.Requirement, scopeChangeBody{BaseVersionNo: 1, Summary: "Approved revised requirement", Title: &title, ReauthProof: "verified"}
}

func TestScopeChangeValidationAndDatabaseFailuresRollbackEntireAggregate(t *testing.T) {
	for _, scenario := range []string{"short_summary", "empty_diff", "invalid_title", "same_title", "no_authority", "no_stepup", "frozen", "authority_error", "bad_scope", "id_change", "id_version", "id_transition", "insert_change", "insert_impact", "update_work", "cas_work", "insert_transition", "insert_version", "update_requirement", "cas_requirement"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, row, body := scopeHarness(t)
			version := 1
			created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workCreateBody{Title: "Linked task", Type: "TASK", Objective: "Deliver requirement", AccountablePrincipalID: 11, RequirementID: &row.ID, BaselineVersionNo: &version, Criteria: []workCriterionInput{{Text: "Task-owned criterion", VerificationMethod: "Manual check"}}}})
			require.NoError(t, err)
			switch scenario {
			case "short_summary":
				body.Summary = "bad"
			case "empty_diff":
				body.Title = nil
			case "invalid_title":
				title := ""
				body.Title = &title
			case "same_title":
				title := row.Title
				body.Title = &title
			case "no_authority":
				m.deliveryAuthority = nil
			case "no_stepup":
				m.scopeReauthentication = nil
			case "frozen":
				require.NoError(t, db.Exec("UPDATE d_delivery_requirements SET status='DRAFT' WHERE id=?", row.ID).Error)
			case "authority_error":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, errors.New("authority unavailable")
				}
			case "bad_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			case "id_change", "id_version", "id_transition":
				failAt := map[string]int{"id_change": 1, "id_version": 2, "id_transition": 3}[scenario]
				original, count := m.ids, 0
				m.ids = draftIDSource(func() (int64, error) {
					count++
					if count == failAt {
						return 0, errors.New("id unavailable")
					}
					return original.Next()
				})
			default:
				tables := map[string]string{"insert_change": "d_delivery_change_records", "insert_impact": "d_delivery_change_impacts", "update_work": "d_delivery_work_items", "cas_work": "d_delivery_work_items", "insert_transition": "d_delivery_work_item_transitions", "insert_version": "d_delivery_requirement_versions", "update_requirement": "d_delivery_requirements", "cas_requirement": "d_delivery_requirements"}
				operation, effect := "INSERT", "RAISE(ABORT,'write failed')"
				if scenario == "update_work" || scenario == "cas_work" || scenario == "update_requirement" || scenario == "cas_requirement" {
					operation = "UPDATE"
				}
				if scenario == "cas_work" || scenario == "cas_requirement" {
					effect = "RAISE(IGNORE)"
				}
				require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER reject_scope BEFORE %s ON %s BEGIN SELECT %s; END", operation, tables[scenario], effect)).Error)
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				_, e := m.scopeChangeOn(ctx, tx, 1, 99, row.ID, 11, row.Version, "", body)
				return e
			})
			require.Error(t, err)
			var actual struct {
				Version       int
				BaselineStale bool
			}
			require.NoError(t, db.Table("d_delivery_work_items").Where("id=?", created.Body.Data.ID).Take(&actual).Error)
			require.Equal(t, created.Body.Data.Version, actual.Version)
			require.False(t, actual.BaselineStale)
			for _, table := range []string{"d_delivery_change_records", "d_delivery_change_impacts"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestScopeChangeFailureRollsBackBaselineAndRecords(t *testing.T) {
	for _, scenario := range []string{"proof", "delegation", "version", "audit", "missing_work", "denied_scope"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, row, body := scopeHarness(t)
			switch scenario {
			case "proof":
				body.ReauthProof = "wrong"
			case "delegation":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DELEGATION:1", DelegationID: 1}, nil
				}
			case "version":
				body.BaseVersionNo = 2
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "missing_work":
				body.StaleWorkIDs = []guid.ID{333}
			case "denied_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.scopeChangeOn(ctx, tx, 1, 99, row.ID, 11, row.Version, "", body)
				return e
			})
			require.Error(t, err)
			q := query.BindRequirements(db, m.service.names)
			actual, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, err)
			require.Equal(t, row.Version, actual.Version)
			require.Equal(t, 1, actual.CurrentVersionNo)
			var n int64
			require.NoError(t, db.Table("d_delivery_change_records").Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, db.Table("d_delivery_requirement_versions").Count(&n).Error)
			require.EqualValues(t, 1, n)
		})
	}
}
