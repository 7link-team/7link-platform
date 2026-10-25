package fleet

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleRecordsActualApprovalAndSelectionSeparately(t *testing.T) {
	m, db, ctx, pub, _ := ensembleHarness(t)
	var plan EnsembleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Compare actual alternatives"})
		return err
	}))
	require.Nil(t, plan.ApprovedBy, "creation is not approval")
	change := func(actor guid.ID, action string, version int) (EnsembleView, error) {
		c := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
		var result EnsembleView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			result, e = m.ChangeEnsembleOn(c, tx, 7, 9, plan.ID, EnsembleDecision{Action: action, Version: version, Reason: "Human reviews actual alternatives", BranchID: plan.Branches[0].ID})
			return e
		})
		return result, err
	}
	_, err := change(101, "APPROVE", 1)
	require.ErrorIs(t, err, ErrVersionConflict, "no invented request")
	requested, err := change(101, "REQUEST_APPROVAL", 1)
	require.NoError(t, err)
	require.NotNil(t, requested.ApprovalRequestedAt)
	_, err = change(101, "COMPARE", 2)
	require.ErrorIs(t, err, ErrVersionConflict)
	original := m.ensemble.AuthorizeOn
	m.ensemble.AuthorizeOn = func(c context.Context, tx *gorm.DB, tenant, project, work guid.ID, permission string) error {
		if authn.PrincipalIDFromContext(c) == 102 {
			return nil
		}
		return original(c, tx, tenant, project, work, permission)
	}
	pub.fail = true
	_, err = change(102, "APPROVE", 2)
	require.Error(t, err)
	pub.fail = false
	approved, err := change(102, "APPROVE", 2)
	require.NoError(t, err)
	require.EqualValues(t, 102, *approved.ApprovedBy)
	require.NotNil(t, approved.ApprovedAt)
	_, err = change(101, "APPROVE", 2)
	require.ErrorIs(t, err, ErrVersionConflict)
	_, err = change(101, "COMPARE", 3)
	require.NoError(t, err)
	selected, err := change(101, "SELECT", 4)
	require.NoError(t, err)
	require.EqualValues(t, 101, *selected.SelectedBy)
	require.EqualValues(t, 102, *selected.ApprovedBy)
	require.NotNil(t, selected.SelectedAt)
	// Revoked/Agent identities cannot be recorded as a Human approver.
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	var other EnsembleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		other, e = m.CreateEnsembleOn(ctx, tx, 7, 9, EnsembleCreate{20, []guid.ID{11, 12}, "Another actual alternatives plan"})
		return e
	}))
	plan = other
	_, err = change(101, "REQUEST_APPROVAL", 1)
	require.ErrorIs(t, err, ErrMachineForbidden)
}
