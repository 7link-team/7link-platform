package agents

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementSuccessorTransfersEachCurrentAssignmentWithIndependentFailure(t *testing.T) {
	for _, kind := range []string{"HUMAN", "LATER"} {
		t.Run(kind, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			agent, artifact := guid.ID(7), guid.ID(800)
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "SUCCESSOR", Reason: "Transfer actual source responsibilities", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{HandoverArtifactID: &artifact, HandoverSource: "SYSTEM"}, WorkItems: []model.RetirementWorkItem{{AssignmentID: 90, WorkItemID: 40, ProjectID: 30, WorkVersion: 1, AssignmentVersion: 1}, {AssignmentID: 91, WorkItemID: 41, ProjectID: 30, WorkVersion: 1, AssignmentVersion: 1}}}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			prepared := false
			m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{9}, nil }
			m.retirementHandover.PrepareOn = func(_ context.Context, _ *gorm.DB, tenant, target guid.ID, extra ...guid.ID) error {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, agent, target)
				require.Contains(t, extra, guid.ID(9))
				if kind == "HUMAN" {
					require.Contains(t, extra, guid.ID(3))
				}
				prepared = true
				return nil
			}
			lock := m.principals.LockActiveRow
			m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				require.True(t, prepared, "all source principals precede protocol authority locks")
				return lock(ctx, tx, id)
			}
			m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
				return HandoverArtifact{ID: artifact, Source: "SYSTEM"}, nil
			}
			live := []delivery.RetirementAssignment{{AssignmentID: 90, WorkItemID: 40, ProjectID: 30, WorkVersion: 9, AssignmentVersion: 3}, {AssignmentID: 91, WorkItemID: 41, ProjectID: 30, WorkVersion: 10, AssignmentVersion: 4}}
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				return live, nil
			}
			mutations := 0
			apply := func(tx *gorm.DB, source delivery.RetirementAssignment) error {
				mutations++
				require.True(t, prepared)
				require.Greater(t, source.WorkVersion, 1, "use source's current fence, preserving the original precheck")
				if err := tx.Exec("INSERT INTO audit_probe VALUES (?)", "transfer:"+source.WorkItemID.String()).Error; err != nil {
					return err
				}
				if source.WorkItemID == 40 {
					return delivery.ErrWorkVersion
				}
				return nil
			}
			m.retirementWork.TransferOn = func(_ context.Context, tx *gorm.DB, tenant, target guid.ID, source delivery.RetirementAssignment, in delivery.RetirementTransferInput) (guid.ID, error) {
				require.Equal(t, artifact, in.ArtifactID)
				require.Equal(t, r.ID, in.RetirementID)
				require.Equal(t, guid.ID(3), in.AssigneePrincipalID)
				return 1000, apply(tx, source)
			}
			m.retirementWork.ReleaseOn = func(_ context.Context, tx *gorm.DB, tenant, target, retirement guid.ID, source delivery.RetirementAssignment) error {
				return apply(tx, source)
			}
			var successor *guid.ID
			if kind == "HUMAN" {
				id := guid.ID(3)
				successor = &id
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				next, err := m.selectRetirementSuccessorOn(ctx, tx, 1, r.ID, r.Version, kind, successor)
				if err == nil {
					r = next
				}
				return err
			}))
			require.Equal(t, "REASSIGN", r.Step)
			for range 3 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					next, err := m.advanceRetirementTransferOn(ctx, tx, 1, r.ID, r.Version)
					if err == nil {
						r = next
					}
					return err
				}))
			}
			require.Equal(t, "CLEANUP", r.Step)
			require.Len(t, r.Precheck.Progress.ReassignResult, 2)
			require.Equal(t, "FAILED", r.Precheck.Progress.ReassignResult[0].State)
			require.Nil(t, r.Precheck.Progress.ReassignResult[0].AssignmentID)
			if kind == "HUMAN" {
				require.Equal(t, "REASSIGNED", r.Precheck.Progress.ReassignResult[1].State)
			} else {
				require.Equal(t, "RELEASED", r.Precheck.Progress.ReassignResult[1].State)
			}
			require.Equal(t, 2, mutations)
			var failedEffects, passedEffects int64
			require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "transfer:40").Count(&failedEffects).Error)
			require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "transfer:41").Count(&passedEffects).Error)
			require.Zero(t, failedEffects)
			require.EqualValues(t, 1, passedEffects)
			require.Equal(t, 1, r.Precheck.WorkItems[0].WorkVersion)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.cancelRetirementOn(ctx, tx, 1, r.ID, r.Version, 6); return err }), ErrConflict)
		})
	}
}

func TestRetirementSuccessorCurrentFencesAndProviderFailuresNeverAdvanceProgress(t *testing.T) {
	for _, mode := range []string{"missing-prepare", "principals", "prepare", "stale", "self", "inactive", "unknown-human", "unknown-agent", "missing-read", "read", "changed-artifact", "changed-source"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			agent, artifact := guid.ID(7), guid.ID(800)
			successor := guid.ID(3)
			kind := "HUMAN"
			version := 1
			fault := errors.New("handover source failed")
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "SUCCESSOR", Reason: "Transfer current responsibilities", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{HandoverArtifactID: &artifact, HandoverSource: "SYSTEM"}}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
			m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return nil }
			m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
				return HandoverArtifact{ID: artifact, Source: "SYSTEM"}, nil
			}
			switch mode {
			case "missing-prepare":
				m.retirementHandover.PrepareOn = nil
			case "principals":
				m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, fault }
			case "prepare":
				m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return fault }
			case "stale":
				version = 2
			case "self":
				successor = 7
			case "inactive":
				successor = 8
			case "unknown-human":
				m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{ID: 2}, true, nil
				}
			case "unknown-agent":
				kind = "AGENT"
				successor = 9
			case "missing-read":
				m.retirementHandover.ReadOn = nil
			case "read":
				m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
					return HandoverArtifact{}, fault
				}
			case "changed-artifact":
				m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
					return HandoverArtifact{ID: 801, Source: "SYSTEM"}, nil
				}
			case "changed-source":
				m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
					return HandoverArtifact{ID: artifact, Source: "AGENT"}, nil
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.selectRetirementSuccessorOn(ctx, tx, 1, r.ID, version, kind, &successor)
				return e
			}))
			q := query.BindRetirement(db, m.names)
			stored, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
			require.NoError(t, e)
			require.Equal(t, 1, stored.Version)
			require.Equal(t, "SUCCESSOR", stored.Step)
			require.Empty(t, stored.Precheck.Progress.SuccessorKind)
		})
	}
}

func TestRetirementTransferRecordsDomainFailuresButRollsBackUnavailableSourceMutations(t *testing.T) {
	for _, mode := range []string{"source-changed", "ineligible", "denied", "unavailable", "zero-assignment", "list", "not-ready", "reconcile"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			agent, artifact, successor := guid.ID(7), guid.ID(800), guid.ID(3)
			fault := errors.New("transfer unavailable")
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "REASSIGN", Reason: "Transfer current responsibilities", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{HandoverArtifactID: &artifact, HandoverSource: "SYSTEM", SuccessorKind: "HUMAN", SuccessorPrincipalID: &successor}, WorkItems: []model.RetirementWorkItem{{AssignmentID: 90, WorkItemID: 40, ProjectID: 30}}}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{3}, nil }
			m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return nil }
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				if mode == "list" {
					return nil, fault
				}
				if mode == "source-changed" {
					return nil, nil
				}
				return []delivery.RetirementAssignment{{AssignmentID: 90, WorkItemID: 40, ProjectID: 30, AssignmentVersion: 2, WorkVersion: 3}}, nil
			}
			m.retirementWork.ReleaseOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, delivery.RetirementAssignment) error {
				return nil
			}
			m.retirementWork.TransferOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ delivery.RetirementAssignment, _ delivery.RetirementTransferInput) (guid.ID, error) {
				require.NoError(t, tx.Exec("INSERT INTO audit_probe VALUES ('source-transfer')").Error)
				switch mode {
				case "ineligible":
					return 0, delivery.ErrInvalidMember
				case "denied":
					return 0, delivery.ErrWorkForbidden
				case "unavailable":
					return 0, fault
				default:
					return 0, nil
				}
			}
			if mode == "not-ready" || mode == "reconcile" {
				m.retirementHandover.ReconcileOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
					if mode == "reconcile" {
						return false, fault
					}
					return false, nil
				}
			}
			var next model.Retirement
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				next, e = m.advanceRetirementTransferOn(ctx, tx, 1, r.ID, 1)
				return e
			})
			switch mode {
			case "source-changed":
				require.NoError(t, err)
				require.Equal(t, "SOURCE_CHANGED", next.Precheck.Progress.ReassignResult[0].State)
			case "ineligible", "denied":
				require.NoError(t, err)
				require.Equal(t, "FAILED", next.Precheck.Progress.ReassignResult[0].State)
			case "not-ready":
				require.NoError(t, err)
				require.Equal(t, 1, next.Version)
				require.Empty(t, next.Precheck.Progress.ReassignResult)
			default:
				require.Error(t, err)
				q := query.BindRetirement(db, m.names)
				stored, e := q.WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, 1, stored.Version)
				require.Empty(t, stored.Precheck.Progress.ReassignResult)
			}
			var effects int64
			require.NoError(t, db.Table("audit_probe").Where("event_type='source-transfer'").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}
