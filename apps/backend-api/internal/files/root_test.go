package files

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOrganizationRootProvisioningIsIdempotentAndOrganizationOwned(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, appender: h.service.appender,
		references: h.service.references,
	}
	input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
	now := time.Now().UTC()
	var first, second guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.provisionOrganizationRoot(t.Context(), tx, input, now)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = m.provisionOrganizationRoot(t.Context(), tx, input, now.Add(time.Second))
		return err
	}))
	require.Equal(t, first, second)

	row := treeNode(t, h.db, h.tree.prefix, first)
	require.NotNil(t, row.RootScope)
	require.Equal(t, "entity", *row.RootScope)
	require.NotNil(t, row.RootOwnerID)
	require.Equal(t, guid.ID(80), *row.RootOwnerID)
	require.True(t, row.ACLStopInheritance)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.NodeACL.WithContext(t.Context()).Where(q.NodeACL.NodeID.Eq(first)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "organization roots must not grant their creator a personal ACL")

	policy := nodeAuthorizationPolicy(map[string]authz.DataConstraint{"file_read": {ResourceIDs: []string{"80"}}})
	authorizer, err := newNodeAuthorizer(h.tree, policy, nodeAuthorizationReferences(true, nil), 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return authorizer.access(t.Context(), tx, row.SpaceID, 70, row.ID, "read")
	}))

	events, total, err := h.audit.List(t.Context(), audit.Filter{TenantID: 90, EventType: "file_root_created", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, events, 1)
}

func TestOrganizationRootProvisioningRollsBackWithCallerTransaction(t *testing.T) {
	h := newOperationServiceHarness(t)
	want := errors.New("audit unavailable")
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, references: h.service.references,
		appender: func(context.Context, *gorm.DB, audit.DomainEvent) error { return want },
	}
	before := immediateDatabaseSnapshot(t, h.db, h.tree)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.provisionOrganizationRoot(t.Context(), tx, core.FileRootProvisionInput{
			Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 50,
		}, time.Now())
		return err
	})
	require.ErrorIs(t, err, want)
	require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
}

func TestOrganizationRootContentCheckIncludesRetainedDescendants(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, appender: h.service.appender,
		references: h.service.references,
	}
	input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
	var rootID guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		rootID, err = m.provisionOrganizationRoot(t.Context(), tx, input, time.Now())
		return err
	}))
	check := func() bool {
		var retained bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			retained, err = m.organizationRootHasContent(t.Context(), tx, input)
			return err
		}))
		return retained
	}
	require.False(t, check())
	root := treeNode(t, h.db, h.tree.prefix, rootID)
	_, err := h.tree.createDirectory(t.Context(), h.db, root.SpaceID, 9997, rootID, "Retained", time.Now())
	require.NoError(t, err)
	require.True(t, check())
	_, err = m.organizationRootHasContent(t.Context(), h.db, input)
	require.ErrorIs(t, err, ErrAccountTransaction)
}
