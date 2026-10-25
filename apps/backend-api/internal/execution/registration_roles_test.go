package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestFleetRegistrationRolesBridgeUsesCurrentSeededGrantsOnCallerTransaction(t *testing.T) {
	for _, fault := range []string{"", "unknown_role", "revoked", "seed_error", "grant_error"} {
		t.Run(fault, func(t *testing.T) {
			s := module.NewServices()
			tx := &gorm.DB{}
			failure := errors.New("owned role provider unavailable")
			var check func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error
			require.NoError(t, module.Provide[fleet.AgentRegistrationRolesConnector](s, fleet.AgentRegistrationRolesConnector{Bind: func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error) error {
				check = fn
				return nil
			}}))
			require.NoError(t, module.Provide[governance.SeededRoles](s, governance.SeededRoles{RoleIDOn: func(_ context.Context, db *gorm.DB, tenant guid.ID, key string) (guid.ID, error) {
				require.Same(t, tx, db)
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, "agent_executor", key)
				if fault == "seed_error" {
					return 0, failure
				}
				if fault == "unknown_role" {
					return 0, nil
				}
				return 77, nil
			}}))
			require.NoError(t, module.Provide[core.RoleGrantStore](s, core.RoleGrantStore{RoleGranted: func(_ context.Context, db *gorm.DB, tenant, role, principal guid.ID, now time.Time) (bool, error) {
				require.Same(t, tx, db)
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, guid.ID(77), role)
				require.Equal(t, guid.ID(202), principal)
				require.WithinDuration(t, time.Now().UTC(), now, time.Second)
				if fault == "grant_error" {
					return false, failure
				}
				return fault != "revoked", nil
			}}))
			require.NoError(t, s.Seal())
			require.NoError(t, bindFleetAgentRoles(s))
			require.NotNil(t, check)
			err := check(t.Context(), tx, 7, 202, []string{"agent_executor"})
			switch fault {
			case "":
				require.NoError(t, err)
			case "revoked":
				require.ErrorIs(t, err, fleet.ErrMachineForbidden)
			case "unknown_role":
				require.ErrorIs(t, err, fleet.ErrUnavailable)
			default:
				require.ErrorIs(t, err, failure)
			}
		})
	}
}
