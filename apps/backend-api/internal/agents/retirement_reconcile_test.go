package agents

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementCompletedReconciliationRetriesCurrentSourceAndCommitsResultAtomically(t *testing.T) {
	for _, fault := range []string{"", "pending_execution", "release", "audit", "authority"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			now := m.now()
			r := model.Retirement{CompletedAt: &now, ID: 900, TenantID: 1, AgentPrincipalID: 7, Mode: "FORCED", State: "COMPLETED", Step: "DONE", Reason: "Real completed forced retirement", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now()}
			q := query.BindRetirement(db, m.names)
			require.NoError(t, q.WithContext(ctx).Create(&r))
			m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{9}, nil }
			prepared := false
			m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { prepared = true; return nil }
			m.retirementHandover.ReconcileOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
				require.True(t, prepared)
				return fault != "pending_execution", nil
			}
			hasWork := true
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				if hasWork {
					return []delivery.RetirementAssignment{{AssignmentID: 90, ProjectID: 30, WorkItemID: 40, WorkVersion: 2, AssignmentVersion: 3}}, nil
				}
				return nil, nil
			}
			failure := errors.New("source release failed")
			m.retirementWork.ReleaseOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, item delivery.RetirementAssignment) error {
				require.EqualValues(t, 90, item.AssignmentID)
				if fault == "release" {
					return failure
				}
				return tx.Exec("INSERT INTO audit_probe VALUES ('source_release')").Error
			}
			if fault == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			if fault == "authority" {
				m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.reconcileCompletedRetirementOn(ctx, tx, 1, 900, 1) })
			stored, e := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(900))).First()
			require.NoError(t, e)
			require.Equal(t, "COMPLETED", stored.State)
			var count int64
			require.NoError(t, db.Table("audit_probe").Where("event_type='source_release'").Count(&count).Error)
			if fault == "pending_execution" {
				require.NoError(t, err)
				require.Equal(t, 1, stored.Version)
				require.Zero(t, count)
				return
			}
			if fault != "" {
				require.Error(t, err)
				require.Equal(t, 1, stored.Version)
				require.Zero(t, count)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
			require.Equal(t, "PENDING", stored.Precheck.Progress.ReconciliationStatus)
			require.Equal(t, "POST_RETIREMENT_RECLAIM", stored.Precheck.Progress.ReassignResult[0].Code)
			hasWork = false
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reconcileCompletedRetirementOn(ctx, tx, 1, 900, stored.Version) }))
			stored, e = q.WithContext(ctx).Where(q.ID.Eq(guid.ID(900))).First()
			require.NoError(t, e)
			require.Equal(t, "COMPLETE", stored.Precheck.Progress.ReconciliationStatus)
		})
	}
}

func TestRetirementReconciliationCurrentSourceFailuresPreserveTerminalRevocation(t *testing.T) {
	for _, mode := range []string{"missing", "principals", "prepare", "leads", "reconcile", "list", "write"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			fault := errors.New("reconciliation source failed")
			now := m.now()
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: 7, Mode: "FORCED", State: "COMPLETED", Step: "DONE", Reason: "Preserve real runtime revocation", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: now, StepStartedAt: now, CompletedAt: &now}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{7}, nil }
			m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return nil }
			m.retirementHandover.ReconcileOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				return nil, nil
			}
			m.retirementWork.ReleaseOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, delivery.RetirementAssignment) error {
				return nil
			}
			switch mode {
			case "missing":
				m.retirementWork.PrincipalIDsOn = nil
			case "principals":
				m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, fault }
			case "prepare":
				m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return fault }
			case "leads":
				m.retirementWork.ClearLeadsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return fault }
			case "reconcile":
				m.retirementHandover.ReconcileOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, fault }
			case "list":
				m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
					return nil, fault
				}
			case "write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reconciliation-write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Retirement.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Update().Remove("reconciliation-write")
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.reconcileCompletedRetirementOn(ctx, tx, 1, r.ID, 1) }))
			q := query.BindRetirement(db, m.names)
			stored, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "COMPLETED", stored.State)
			require.Equal(t, 1, stored.Version)
			require.Nil(t, stored.ActiveAgentID)
		})
	}
}
