package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScheduledDelegationActivatesOnlyWhenDueAndRollsBackBindingsWithEvent(t *testing.T) {
	m, db, ctx, pub, in := delegationHarness(t)
	starts := m.seed.now().Add(time.Hour)
	in.StartsAt = &starts
	view, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	require.Equal(t, "SCHEDULED", view.State)
	require.Equal(t, starts.Add(time.Hour), view.EndsAt)
	require.Zero(t, seedCount(t, db, "binding_probe"))
	require.NoError(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
	require.Zero(t, seedCount(t, db, "binding_probe"))
	m.seed.now = func() time.Time { return starts }
	pub.Fail = true
	require.Error(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
	require.Zero(t, seedCount(t, db, "binding_probe"))
	q := query.BindDelegations(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).Take()
	require.NoError(t, err)
	require.Nil(t, row.ActivatedAt)
	require.Equal(t, 1, row.Version)
	pub.Fail = false
	require.NoError(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(view.ID)).Take()
	require.NoError(t, err)
	require.NotNil(t, row.ActivatedAt)
	require.Equal(t, 2, row.Version)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"))
	var event string
	require.NoError(t, db.Table("event_probe").Order("rowid DESC").Limit(1).Pluck("detail", &event).Error)
	require.Contains(t, event, "delegation.activated")
	require.Contains(t, event, "SYSTEM:governance.delegation_activation")
	require.NoError(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"))
}

func TestScheduledDelegationRechecksEligibilityBeforeInstallingAnyCarrier(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Module)
	}{
		{"grantor permission", func(m *Module) {
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: false}, nil
			}
		}},
		{"grantee inactive", func(m *Module) {
			m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "INACTIVE"}, true, nil
			}
		}},
		{"project closed", func(m *Module) {
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Stage: "CLOSED"}, nil
			}
		}},
		{"project holds delegation", func(m *Module) {
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Stage: "ACTIVE", Mode: "AUTONOMOUS"}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			starts := m.seed.now().Add(time.Hour)
			in.StartsAt = &starts
			_, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			m.seed.now = func() time.Time { return starts }
			tc.mutate(m)
			require.NoError(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
			require.Zero(t, seedCount(t, db, "binding_probe"))
			require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
		})
	}
}

func TestScheduledDelegationCancellationExpiryAndInvalidStarts(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "expired before activation"}[cancel], func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			starts := m.seed.now().Add(time.Hour)
			in.StartsAt = &starts
			view, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			m.delegations.grants.RemoveEntityRoleBinding = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
				t.Fatal("pending cancellation must not delete an uninstalled carrier")
				return false, nil
			}
			if cancel {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.revokeDelegationOn(ctx, tx, 1, view.ID, "Cancel future delegation")
					return err
				}))
			} else {
				end := view.EndsAt
				m.seed.now = func() time.Time { return end }
				require.NoError(t, m.expireDelegations(t.Context(), jobs.Job{}))
			}
			m.seed.now = func() time.Time { return starts }
			require.NoError(t, m.activateScheduledDelegations(t.Context(), jobs.Job{}))
			require.Zero(t, seedCount(t, db, "binding_probe"))
		})
	}
	m, db, ctx, _, in := delegationHarness(t)
	past := m.seed.now()
	in.StartsAt = &past
	_, err := grantDelegation(t, m, db, ctx, in)
	require.ErrorIs(t, err, ErrInvalidArgument)
	future := past.Add(time.Hour)
	in.StartsAt = &future
	id := guid.ID(123)
	in.ReplacesDelegationID = &id
	_, err = grantDelegation(t, m, db, ctx, in)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestScheduledDelegationHTTPCreateFilterAndCancel(t *testing.T) {
	m, _, h, in := delegationHTTPHarness(t)
	starts := m.seed.now().Add(time.Hour)
	in.StartsAt = &starts
	w := delegationRequest(t, h, http.MethodPost, "/tenants/1/delegations", "future-grant", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	var envelope struct{ Data DelegationView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "SCHEDULED", envelope.Data.State)
	w = delegationRequest(t, h, http.MethodGet, "/tenants/1/delegations?state=SCHEDULED", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), envelope.Data.ID.String())
	w = delegationRequest(t, h, http.MethodGet, "/tenants/1/delegations?state=ACTIVE", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), envelope.Data.ID.String())
	w = delegationRequest(t, h, http.MethodPost, "/tenants/1/delegations/"+envelope.Data.ID.String()+"/revocations", "cancel-future", map[string]string{"reason": "Cancel future delegation"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"state":"REVOKED"`)
}
