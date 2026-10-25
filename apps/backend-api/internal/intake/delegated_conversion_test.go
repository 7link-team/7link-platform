package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDelegatedNewExactPersistedCardAndRollback(t *testing.T) {
	m, db, ctx, _, pub := changeSourceHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 7, EntityID: 20, PrincipalID: 30, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "version": 1, "raw_content": "immutable original"}).Error)
	var card TriageSource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft())
		return err
	}))
	allowed := true
	failure := errors.New("authority unavailable")
	var authorityErr error
	m.delegatedNewAuthority = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, current TriageSource) (string, error) {
		require.Equal(t, card.ActorID, current.ActorID)
		require.Equal(t, card.Hash, current.Hash)
		if authorityErr != nil {
			return "", authorityErr
		}
		if !allowed {
			return "", nil
		}
		return "DELEGATION:500", nil
	}
	call := func(version, revision int, hash string, requirement guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.convertDelegatedNewOn(ctx, tx, 7, 20, 10, version, revision, hash, requirement)
		})
	}
	for _, bad := range []struct {
		version, revision int
		hash              string
		requirement       guid.ID
	}{{3, 1, card.Hash, 100}, {2, 2, card.Hash, 100}, {2, 1, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 100}, {2, 1, card.Hash, 0}} {
		require.Error(t, call(bad.version, bad.revision, bad.hash, bad.requirement))
	}
	allowed = false
	require.ErrorIs(t, call(2, 1, card.Hash, 100), ErrForbidden)
	allowed = true
	authorityErr = failure
	require.ErrorIs(t, call(2, 1, card.Hash, 100), failure)
	authorityErr = nil
	pub.fail = true
	require.Error(t, call(2, 1, card.Hash, 100))
	pub.fail = false
	var state string
	require.NoError(t, db.Table(m.names.Records.String()).Select("status").Where("id=10").Scan(&state).Error)
	require.Equal(t, "TRIAGED", state)
	require.NoError(t, call(2, 1, card.Hash, 100))
	require.ErrorIs(t, call(2, 1, card.Hash, 101), ErrConflict)
	var stored struct {
		RawContent, ResolvedActorKind, ResolvedAuthoritySource string
		RequirementID                                          guid.ID
		Version                                                int
	}
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&stored).Error)
	require.Equal(t, "immutable original", stored.RawContent)
	require.Equal(t, "AGENT", stored.ResolvedActorKind)
	require.Equal(t, "DELEGATION:500", stored.ResolvedAuthoritySource)
	require.EqualValues(t, 100, stored.RequirementID)
	require.Equal(t, 3, stored.Version)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, _, err := m.readDelegatedNewOn(ctx, tx, 7, 20, 10); return err }))
	allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, _, err := m.readDelegatedNewOn(ctx, tx, 7, 20, 10); return err }), ErrForbidden, "converted source cannot retain revoked authority for replay")
	_, _, err := m.readDelegatedNewOn(ctx, db, 7, 20, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestDelegatedNewConnectorAndSourceBoundaries(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideDelegatedNew(services))
	require.NoError(t, services.Seal())
	c, err := module.Resolve[DelegatedNewConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	check := DelegatedNewAuthority(func(context.Context, *gorm.DB, guid.ID, guid.ID, TriageSource) (string, error) {
		return "DELEGATION:5", nil
	})
	require.NoError(t, c.Bind(check))
	require.ErrorIs(t, c.Bind(check), ErrInvalidArgument)
	for _, claims := range []*authn.Claims{{TenantID: 8, EntityID: 20, PrincipalID: 30}, {TenantID: 7, EntityID: 21, PrincipalID: 30}, {TenantID: 7, EntityID: 20}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.readDelegatedNewOn(authn.WithClaims(ctx, claims), tx, 7, 20, 10)
			return err
		}))
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 7, EntityID: 20, PrincipalID: 30})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, _, err := m.readDelegatedNewOn(ctx, tx, 7, 20, 10); return err }), ErrConflict, "CHANGE is never delegated NEW")
	m.delegatedNewAuthority = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, _, err := m.readDelegatedNewOn(ctx, tx, 7, 20, 10); return err }), ErrUnavailable)
}
