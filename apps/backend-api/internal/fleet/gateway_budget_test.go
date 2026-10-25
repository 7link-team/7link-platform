package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectGatewayBudgetAdmissionAndKnownUnusedSettlementAreAtomic(t *testing.T) {
	for _, fault := range []string{"", "missing", "reserve", "settle", "ledger"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, gatewayWrite()); return err }))
			require.NoError(t, db.Exec("CREATE TABLE budget_effects(kind TEXT)").Error)
			var reservedID guid.ID
			m.gatewayBudgets = GatewayBudgetConsumer{
				ReserveOn: func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, cost int64, limit *int64) error {
					require.Equal(t, guid.ID(7), tenant)
					require.Equal(t, guid.ID(20), project)
					require.Equal(t, int64(7), cost)
					require.NotNil(t, limit)
					require.Equal(t, int64(123456), *limit)
					reservedID = id
					if fault == "reserve" {
						return ErrVersionConflict
					}
					return tx.Exec("INSERT INTO budget_effects VALUES ('reserved')").Error
				},
				SettleOn: func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
					require.Equal(t, reservedID, id)
					if fault == "settle" {
						return errors.New("settle failed")
					}
					return tx.Exec("INSERT INTO budget_effects VALUES ('settled_zero')").Error
				},
			}
			if fault == "missing" {
				m.gatewayBudgets = GatewayBudgetConsumer{}
			}
			if fault == "ledger" {
				require.NoError(t, db.Exec("DROP TABLE "+m.names.GatewayRequests.String()).Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				v, err := m.QueueProjectGatewayRequestOn(ctx, tx, 7, 20, "gpt-test", 1, 1)
				if err == nil {
					require.Equal(t, "FAILED", v.State)
					require.Equal(t, "RUNNER_GATEWAY_UNAVAILABLE", v.Reason)
					require.Equal(t, guid.ID(20), *v.ProjectID)
				}
				return err
			})
			var count int64
			require.NoError(t, db.Table("budget_effects").Count(&count).Error)
			if fault == "" {
				require.NoError(t, err)
				require.Equal(t, int64(2), count)
			} else {
				require.Error(t, err)
				require.Zero(t, count)
			}
		})
	}
}

func TestProjectGatewayRequestRequiresProjectAndBoundedTokenPlan(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	for _, in := range []struct {
		project       guid.ID
		input, output int64
	}{{0, 1, 1}, {20, 0, 1}, {20, 1, 0}, {20, 1000001, 1}, {20, 1, 1000001}} {
		_, err := m.QueueProjectGatewayRequestOn(ctx, db, 7, in.project, "model", in.input, in.output)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	router := fleetBusinessRouter(t, m)
	for _, body := range []string{`{"model":"gpt-test"}`, `{"model":"gpt-test","project_id":"20","max_input_tokens":0,"max_output_tokens":1}`} {
		r := fleetHTTPRequest(router, "POST", "/tenants/7/model-gateway/requests", body, nil)
		require.Equal(t, 422, r.Code, r.Body.String())
	}
}
