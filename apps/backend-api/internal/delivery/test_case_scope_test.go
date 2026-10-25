package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTestCaseScopeUsesActualWorkRequirementAndCurrentReadAuthority(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	var requirement, work guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		require.NoError(t, err)
		requirement = r.ID
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.baselineRequirementOn(ctx, tx, 1, 99, requirement, 11, 1)
		return err
	}))
	version := 1
	workBody := workCreateBody{Title: "Linked test work", Type: "TASK", Objective: "Exercise import scope", AccountablePrincipalID: 11, RequirementID: &requirement, BaselineVersionNo: &version, Criteria: []workCriterionInput{{Text: "Acceptance", VerificationMethod: "Exercise actual workflow"}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := m.createWorkOn(ctx, tx, 1, 99, 11, workBody)
		work = r.ID
		return err
	}))
	a := clientWork{m: m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := a.TestCaseScopeOn(ctx, tx, 1, 99, work, requirement)
		require.NoError(t, err)
		require.Equal(t, requirement, r.ID)
		require.Len(t, r.Criteria, 2)
		return err
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := a.TestCaseScopeOn(ctx, tx, 1, 99, work, requirement+1); return err }))
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == "requirement_read" {
			return authz.DataConstraint{}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := a.TestCaseScopeOn(ctx, tx, 1, 99, work, requirement); return err }))
}
