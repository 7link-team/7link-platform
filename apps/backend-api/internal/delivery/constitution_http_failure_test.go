package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConstitutionHTTPFailsClosedOnInvalidIDsAndMissingStorage(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	_, found, err := m.constitutionOwner(ctx, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.False(t, found)
	_, found, err = m.constitutionOwner(ctx, 999)
	require.NoError(t, err)
	require.False(t, found)
	for _, raw := range []string{"0", "invalid", "9223372036854775808"} {
		_, err = m.proposeConstitutionHTTP(ctx, &constitutionCreateInput{EntityID: raw})
		assertWorkStatus(t, err, 422)
		_, err = m.getConstitutionHTTP(ctx, &constitutionGetInput{VersionID: raw})
		assertWorkStatus(t, err, 422)
		_, err = m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: raw})
		assertWorkStatus(t, err, 422)
	}
	failure := errors.New("authorization dependency failed")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	require.ErrorIs(t, m.constitutionReadProjectOn(ctx, db, 1, 99, 11), failure)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
	}
	require.ErrorIs(t, m.constitutionReadProjectOn(ctx, db, 1, 99, 11), ErrUnavailable)
	m.workPolicy = nil
	require.ErrorIs(t, m.constitutionReadProjectOn(ctx, db, 1, 99, 11), ErrUnavailable)
	require.NoError(t, db.Exec("ALTER TABLE "+m.service.names.Constitutions.String()+" RENAME TO unavailable_constitutions").Error)
	_, found, err = m.constitutionOwner(ctx, 999)
	require.Error(t, err)
	require.False(t, found)
	m.runtime = nil
	_, _, err = m.constitutionOwner(ctx, 999)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.getConstitutionHTTP(ctx, &constitutionGetInput{VersionID: "999"})
	assertWorkStatus(t, err, 503)
	_, err = m.listConstitutionsHTTP(ctx, &constitutionListInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
	assertWorkStatus(t, constitutionHTTPError(ctx, ErrConstitutionState), 409)
	assertWorkStatus(t, constitutionHTTPError(ctx, ErrConstitutionHumanOnly), 403)
}
