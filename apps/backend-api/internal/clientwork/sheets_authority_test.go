package clientwork

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type unavailableIDs struct {
	source    guid.Source
	at, count int
}

func (s *unavailableIDs) Next() (int64, error) {
	s.count++
	if s.count == s.at {
		return 0, ErrUnavailable
	}
	return s.source.Next()
}

type zeroIDs struct{}

func (zeroIDs) Next() (int64, error) { return 0, nil }

func TestAuthorityRequiresCurrentHumanScopeAndTransaction(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	_, e := m.authorize(ctx, db, 3, "project_read", false)
	require.ErrorIs(t, e, ErrInvalid)
	for _, in := range []struct {
		ctx     context.Context
		project guid.ID
	}{{context.Background(), 3}, {ctx, 0}, {ctx, 99}, {testContext(t, 7), 3}, {testContext(t, 9), 3}, {testContext(t, 4), 3}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorize(in.ctx, tx, in.project, "project_read", false); return e }))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.authorize(authn.WithClaims(ctx, nil), tx, 3, "project_read", false)
		return e
	}), ErrNotFound)
	state := m.principals
	m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("principal store failed") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, 3, "project_read", false); return e }))
	m.principals = state
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, ErrUnavailable
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, 3, "project_read", false); return e }), ErrUnavailable)
	m.principals = state
	m.members = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, 3, "project_read", false); return e }), ErrUnavailable)
}

func TestIDsAndAuditFailuresCannotManufactureReceipts(t *testing.T) {
	m, db, _, _ := harness(t)
	source := m.ids
	for _, ids := range []guid.Source{nil, zeroIDs{}, &unavailableIDs{source: source, at: 1}} {
		m.ids = ids
		_, e := m.nextID()
		require.Error(t, e)
	}
	m.ids = source
	appendEvent := m.audit
	for _, auditFn := range []audit.Appender{nil, func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }} {
		m.audit = auditFn
		before := collaborationSnapshot(t, m, db)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.draftSheetOn(testContext(t, 2), tx, 3, 0, 0, SheetDraft{Title: "Scope", SignerID: 4, RequirementIDs: []guid.ID{10}})
			return e
		}), ErrUnavailable)
		require.Equal(t, before, collaborationSnapshot(t, m, db))
	}
	m.audit = appendEvent
}
