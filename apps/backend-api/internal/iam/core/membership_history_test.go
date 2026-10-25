package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
)

func TestMembershipHistoryClosesOnPrincipalErasure(t *testing.T) {
	h := newRepositoryHarness(t, "")
	seedTenantAndPrincipal(t, h.db, "", 1, 2)
	_, err := h.repository.PutMember(t.Context(), TenantMember{TenantID: 1, PrincipalID: 2, Status: MemberActive})
	require.NoError(t, err)
	end := h.now.Add(time.Hour)
	require.NoError(t, ErasePrincipalData(t.Context(), h.db, "", authz.DefaultRegistry(), 2, end))
	checker := NewMembershipChecker(h.runtime)
	periods, err := checker.MembershipIntervalsOn(t.Context(), h.db, 1, *h.now, end.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, periods, 1)
	require.NotNil(t, periods[0].End)
	require.Equal(t, end, *periods[0].End)
	periods, err = checker.MembershipIntervalsOn(t.Context(), h.db, 1, end, end.Add(24*time.Hour))
	require.NoError(t, err)
	require.Empty(t, periods, "erased admission cannot manufacture later report eligibility")
}

func TestMembershipHistoryPreservesReactivationAndRollsBack(t *testing.T) {
	h := newRepositoryHarness(t, "history_")
	seedTenantAndPrincipal(t, h.db, "history_", 1, 2)
	member := TenantMember{TenantID: 1, PrincipalID: 2, Status: MemberActive}
	_, err := h.repository.PutMember(t.Context(), member)
	require.NoError(t, err)
	first := *h.now
	*h.now = first.Add(time.Hour)
	member.Status = MemberDisabled
	_, err = h.repository.PutMember(t.Context(), member)
	require.NoError(t, err)
	end := *h.now
	*h.now = first.Add(48 * time.Hour)
	member.Status = MemberActive
	_, err = h.repository.PutMember(t.Context(), member)
	require.NoError(t, err)
	_, err = h.repository.PutMember(t.Context(), member)
	require.NoError(t, err, "idempotent active profile update does not open another interval")
	checker := NewMembershipChecker(h.runtime)
	periods, err := checker.MembershipIntervalsOn(t.Context(), h.db, 1, first.Add(-time.Hour), h.now.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, periods, 2)
	require.Equal(t, first, periods[0].Start)
	require.Equal(t, end, *periods[0].End)
	require.Equal(t, *h.now, periods[1].Start)
	require.Nil(t, periods[1].End)
	periods, err = checker.MembershipIntervalsOn(t.Context(), h.db, 1, end, first.Add(47*time.Hour))
	require.NoError(t, err)
	require.Empty(t, periods, "inactive gap cannot count as membership")
	periods, err = checker.MembershipIntervalsOn(t.Context(), h.db, 3, first, h.now.Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, periods, "tenant isolation")
	require.NoError(t, h.db.Migrator().DropTable(query.BindNames("history_").MembershipPeriods.String()))
	member.Status = MemberDisabled
	_, err = h.repository.PutMember(t.Context(), member)
	require.Error(t, err)
	stored, err := h.repository.GetMember(t.Context(), 1, 2)
	require.NoError(t, err)
	require.Equal(t, MemberActive, stored.Status, "history failure rolls back admission change")
}

func TestMembershipHistoryObservationCutoffAndUnavailableInputs(t *testing.T) {
	h := newRepositoryHarness(t, "observed_")
	checker := NewMembershipChecker(h.runtime)
	at, err := checker.MembershipObservedSinceOn(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Nil(t, at, "empty history cannot prove an observation boundary")
	seedTenantAndPrincipal(t, h.db, "observed_", 1, 2)
	_, err = h.repository.PutMember(t.Context(), TenantMember{TenantID: 1, PrincipalID: 2, Status: MemberActive})
	require.NoError(t, err)
	at, err = checker.MembershipObservedSinceOn(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, *h.now, *at)
	at, err = checker.MembershipObservedSinceOn(t.Context(), h.db, 3)
	require.NoError(t, err)
	require.Nil(t, at, "another tenant's history cannot establish this tenant's cutoff")
	_, err = checker.MembershipObservedSinceOn(t.Context(), h.db, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = checker.MembershipObservedSinceOn(t.Context(), nil, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = checker.MembershipIntervalsOn(t.Context(), h.db, 1, *h.now, *h.now)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = checker.MembershipIntervalsOn(t.Context(), nil, 1, *h.now, h.now.Add(time.Hour))
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, h.db.Migrator().DropTable(query.BindNames("observed_").MembershipPeriods.String()))
	_, err = checker.MembershipObservedSinceOn(t.Context(), h.db, 1)
	require.Error(t, err, "database errors cannot be reported as absent history")
	_, err = checker.MembershipIntervalsOn(t.Context(), h.db, 1, *h.now, h.now.Add(time.Hour))
	require.Error(t, err)
}
