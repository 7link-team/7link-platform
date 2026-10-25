package core

import (
	"7link.com/platform/internal/infra/guid"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestTeamRosterSearchReturnsCurrentRoleNamesAndPropagatesReadFailures(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.repository.CreateRole(t.Context(), 1, "Reviewer", "")
	require.NoError(t, err)
	_, err = h.repository.AddMember(t.Context(), 1, role.ID, 11, nil)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.repository.names.TenantMembers.String()).Where("principal_id=11").Update("display_name", "Roster subject").Error)
	m := &Module{repository: h.repository}
	_, err = m.teamRosterOn(t.Context(), h.db, 1, 10, 0, "")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.teamRosterOn(t.Context(), nil, 1, 10, 0, "")
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		page, err := m.teamRosterOn(t.Context(), tx, 1, 10, 0, "subject")
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, []string{"Reviewer"}, page.Items[0].Roles)
		empty, err := m.teamRosterOn(t.Context(), tx, 1, 10, 0, "absent")
		require.NoError(t, err)
		require.Empty(t, empty.Items)
		return nil
	}))
	for _, table := range []string{h.repository.names.TenantMembers.String(), h.repository.names.RoleMembers.String(), h.repository.names.Roles.String()} {
		failure := errors.New("roster read failed: " + table)
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("roster_read_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(failure)
			}
		}))
		err = h.db.Transaction(func(tx *gorm.DB) error { _, err := m.teamRosterOn(t.Context(), tx, 1, 10, 0, ""); return err })
		require.ErrorIs(t, err, failure)
		require.NoError(t, h.db.Callback().Query().Remove("roster_read_failure"))
	}
}

func TestProjectRosterOmitsDeniedExpiredAndForeignProjectBindings(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "project", Name: "Roster project"})
	require.NoError(t, err)
	other, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "project", Name: "Other roster project"})
	require.NoError(t, err)
	role, err := h.service.CreateRole(t.Context(), 1, "Worker", "")
	require.NoError(t, err)
	deny, err := h.service.CreateRole(t.Context(), 1, "Denied", "")
	require.NoError(t, err)
	expired, err := h.service.CreateRole(t.Context(), 1, "Expired", "")
	require.NoError(t, err)
	for _, in := range []struct {
		role, principal guid.ID
		effect          BindingEffect
		expires         time.Time
	}{{role.ID, 11, BindingAllow, time.Time{}}, {deny.ID, 11, BindingDeny, time.Time{}}, {expired.ID, 12, BindingAllow, time.Now().UTC().Add(time.Hour)}} {
		_, _, err = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, in.role, in.principal, in.effect, in.expires)
		require.NoError(t, err)
	}
	require.NoError(t, h.db.Table(h.repository.names.RoleBindings.String()).Where("role_id=?", expired.ID).Update("expires_at", time.Now().UTC().Add(-time.Hour)).Error)
	m := &Module{repository: h.repository}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		roles, err := m.teamRosterProjectOn(t.Context(), tx, 1, entity.ID)
		require.NoError(t, err)
		require.Equal(t, map[guid.ID][]string{11: {"Worker"}}, roles)
		empty, err := m.teamRosterProjectOn(t.Context(), tx, 1, other.ID)
		require.NoError(t, err)
		require.Empty(t, empty)
		_, err = m.teamRosterProjectOn(t.Context(), tx, 0, entity.ID)
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	_, err = m.teamRosterProjectOn(t.Context(), nil, 1, entity.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	failure := errors.New("project roster storage failed")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("project_roster_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
	_, err = m.teamRosterProjectOn(t.Context(), h.db, 1, entity.ID)
	require.ErrorIs(t, err, failure)
	require.NoError(t, h.db.Callback().Query().Remove("project_roster_failure"))
}

func TestTeamRosterReadsCurrentMembershipWithTenantAndPageBounds(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedAdditionalMember(t, 1, 12, MemberDisabled)
	m := &Module{repository: h.repository}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		page, e := m.teamRosterOn(t.Context(), tx, 1, 1, 0, "")
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.True(t, page.HasMore)
		require.Equal(t, guid.ID(11), page.Items[0].PrincipalID)
		last, e := m.teamRosterOn(t.Context(), tx, 1, 1, 1, "")
		require.NoError(t, e)
		require.False(t, last.HasMore)
		require.Equal(t, "disabled", last.Items[0].State)
		other, e := m.teamRosterOn(t.Context(), tx, 2, 50, 0, "")
		require.NoError(t, e)
		require.Empty(t, other.Items)
		_, e = m.teamRosterOn(t.Context(), tx, 1, 201, 0, "")
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
}

func TestEntitySubjectRetirementRemovesOnlySubjectEntityBindingsAndRollsBack(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "project", Name: "Project"})
	require.NoError(t, err)
	role, err := h.service.CreateRole(t.Context(), 1, "Worker", "")
	require.NoError(t, err)
	for _, principal := range []guid.ID{11, 12} {
		_, _, err = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, role.ID, principal, BindingAllow, time.Time{})
		require.NoError(t, err)
	}
	m := &Module{service: h.service, repository: h.repository}
	before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	abort := errors.New("retirement outbox failed")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.revokeEntitySubjectBindingsOn(t.Context(), tx, 1, 11))
		return abort
	}), abort)
	rows, err := h.repository.ListEntityRoleBindings(t.Context(), 1, entity.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, before, after)
	for range 2 {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return m.revokeEntitySubjectBindingsOn(t.Context(), tx, 1, 11) }))
	}
	rows, err = h.repository.ListEntityRoleBindings(t.Context(), 1, entity.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].PrincipalID)
	after, err = h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Greater(t, after, before)
	require.Error(t, m.revokeEntitySubjectBindingsOn(t.Context(), h.db, 1, 11))
}
