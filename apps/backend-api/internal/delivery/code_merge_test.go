package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func codeMergeFixture(t *testing.T) (*resultFixture, ExecutionWorkLink) {
	t.Helper()
	h, link := executionWorkFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	c, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	plan, err := json.Marshal([]workVerificationStep{{CriterionID: c.ID, Method: "HUMAN", Detail: "Independent manual verification"}})
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"verification_plan": string(plan), "internal_qa_required": false}).Error)
	approveFixtureReviewPlan(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if err := h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE"); err != nil {
			return err
		}
		return h.m.startExecutionWorkOn(h.ctx, tx, link)
	}))
	completion := ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Kind: "FILE", Title: "Verified output", ContentHash: strings.Repeat("c", 64), Artifact: &ExecutionArtifact{SpaceID: 500, FileID: 501, VersionID: 502, FileVersion: 3, CriterionID: &c.ID, Coverage: "COVERED", SHA256: strings.Repeat("a", 64)}}}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.finishExecutionWorkOn(h.ctx, tx, link, completion) }))
	_, err = h.check(link.ExecutionID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(link.ExecutionID, 12)
	require.NoError(t, err)
	at := time.Now().UTC()
	repo := model.ProjectRepository{TenantID: 1, ID: 700, ProjectID: 99, Name: "Source", RemoteURL: "https://example.invalid/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, h.db.Table(h.m.service.names.Repositories.String()).Create(&repo).Error)
	return h, link
}

func TestCodeMergeRequiresCurrentAcceptedExecutionAndAllHumanVerifications(t *testing.T) {
	for _, mode := range []string{"success", "unaccepted", "stale", "wrong_execution", "revoked", "stale_upstream", "upstream_andon", "missing_human", "failed_other_human", "human_missing_source", "archived_repository"} {
		t.Run(mode, func(t *testing.T) {
			h, link := codeMergeFixture(t)
			execution := link.ExecutionID
			switch mode {
			case "unaccepted":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "NEEDS_HUMAN_REVIEW").Error)
			case "stale":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "wrong_execution":
				execution++
			case "revoked":
				h.deniedAuthority = true
			case "stale_upstream", "upstream_andon":
				_, _, _, body := workHTTPHarness(t)
				var upstream workDetailView
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					upstream, err = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
					return err
				}))
				require.NoError(t, query.BindDependencies(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: h.work, DependsOnID: upstream.ID}))
				if mode == "upstream_andon" {
					require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", upstream.ID).Updates(map[string]any{"paused_at": time.Now().UTC(), "pause_reason": "MANUAL", "block_reasons": `["ANDON"]`}).Error)
				} else {
					consumed := model.ArtifactConsumption{TenantID: 1, ID: 808, ProjectID: 99, WorkItemID: h.work, UpstreamWorkItemID: upstream.ID, AcceptedArtifactID: 809, ResultID: 810, FileID: 811, VersionID: 812, SHA256: strings.Repeat("a", 64), State: "STALE", CreatedBy: 11, CreatedAt: time.Now().UTC()}
					require.NoError(t, h.db.Table(h.m.service.names.ArtifactConsumptions.String()).Create(&consumed).Error)
				}
			case "missing_human":
				q := query.BindVerificationRuns(h.db, h.m.service.names)
				unrelated, err := q.WithContext(h.ctx).Where(q.ResultID.Eq(execution)).First()
				require.NoError(t, err)
				require.NoError(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("result_id=?", execution).Delete(&model.VerificationRun{}).Error)
				// An unrelated successful automatic receipt must not substitute for
				// the frozen Human criterion, even though a receipt exists.
				unrelated.CriterionID++
				unrelated.Method, unrelated.Stage = "COMMAND", "AUTOMATED"
				task := guid.ID(901)
				unrelated.TaskID, unrelated.CheckID = &task, nil
				unrelated.ReceiptHash = strings.Repeat("d", 64)
				require.NoError(t, q.WithContext(h.ctx).Create(unrelated))
			case "human_missing_source":
				require.Error(t, h.db.Table(h.m.service.names.VerificationRuns.String()).Where("result_id=?", execution).Update("check_id", nil).Error)
			case "failed_other_human":
				// A later passing reviewer cannot hide another reviewer's failure.
				q := query.BindVerificationRuns(h.db, h.m.service.names)
				pass, err := q.WithContext(h.ctx).Where(q.ResultID.Eq(execution)).First()
				require.NoError(t, err)
				failed := *pass
				failed.ID--
				failed.ActorID = 13
				failed.Verdict = "FAILED"
				require.NoError(t, q.WithContext(h.ctx).Create(&failed))
			case "archived_repository":
				require.NoError(t, h.db.Table(h.m.service.names.Repositories.String()).Where("id=?", 700).Update("archived_at", time.Now().UTC()).Error)
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				facts, err := h.m.codeMergeOn(h.actor(12), tx, 1, 99, h.work, execution, 700, 11)
				if mode == "success" || mode == "human_missing_source" {
					require.NoError(t, err)
					require.Equal(t, strings.Repeat("a", 40), facts.HeadCommit)
					require.Equal(t, "main", facts.DefaultBranch)
					require.Equal(t, "ROLE:reviewer", facts.AuthoritySource)
				}
				return err
			})
			if mode != "success" && mode != "human_missing_source" {
				require.Error(t, err)
			}
		})
	}
}

func TestCodeMergeAgentUsesActualCostReworkAndExactDelegationSource(t *testing.T) {
	for _, mode := range []string{"success", "unknown_cost", "negative_cost", "missing_cost", "forged_source", "zero_delegation", "revoked", "self", "medium", "rework_limit"} {
		t.Run(mode, func(t *testing.T) {
			h, link := codeMergeFixture(t)
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE", Roles: []string{"REVIEWER"}}, true, nil
			}
			h.m.acceptanceExecutions = func(_ context.Context, tx *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
				require.NotNil(t, tx)
				require.Equal(t, h.work, work)
				return []guid.ID{201, 202}, nil
			}
			h.m.acceptanceSettlements = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
				require.Equal(t, []guid.ID{201, 202}, ids)
				if mode == "negative_cost" {
					return -1, true, nil
				}
				return 42, mode != "unknown_cost", nil
			}
			if mode == "missing_cost" {
				h.m.acceptanceSettlements = nil
			}
			risk := "LOW"
			if mode == "medium" {
				risk = "MEDIUM"
			}
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"risk": risk, "quality_rework_count": 2}).Error)
			producer := guid.ID(11)
			if mode == "self" {
				producer = 14
			}
			h.m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Equal(t, "AGENT", req.ActorKind)
				require.Equal(t, "code_merge", req.Code)
				require.Equal(t, link.ExecutionID, req.Facts.ClaimID)
				require.EqualValues(t, 42, req.Facts.CostMicros)
				require.Equal(t, 2, req.Facts.QualityReworkCount)
				require.Equal(t, risk, req.Facts.Risk)
				require.Equal(t, mode == "self", req.Facts.SelfProduced)
				source, id := "DELEGATION:901", guid.ID(901)
				if mode == "forged_source" {
					source = "ROLE:reviewer"
				}
				if mode == "zero_delegation" {
					id, source = 0, "DELEGATION:0"
				}
				return governance.AuthorityDecision{Allowed: mode != "revoked" && mode != "self" && mode != "medium" && mode != "rework_limit", DelegationID: id, AuthoritySource: source}, nil
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, err := h.m.codeMergeOn(h.actor(14), tx, 1, 99, h.work, link.ExecutionID, 700, producer)
				return err
			})
			if mode == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestCodeMergePreservesEveryCurrentStorageFailure(t *testing.T) {
	h, link := codeMergeFixture(t)
	failure := errors.New("current merge source unavailable")
	count, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("merge-current-source", func(tx *gorm.DB) {
		count++
		if count == failAt {
			tx.AddError(failure)
		}
	}))
	defer h.db.Callback().Query().Remove("merge-current-source")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			facts, e := h.m.codeMergeOn(h.actor(12), tx, 1, 99, h.work, link.ExecutionID, 700, 11)
			if e != nil {
				require.Empty(t, facts.HeadCommit)
				require.Empty(t, facts.AuthoritySource)
			}
			return e
		})
	}
	require.NoError(t, read())
	total := count
	require.Greater(t, total, 5)
	for i := 1; i <= total; i++ {
		count, failAt = 0, i
		require.ErrorIs(t, read(), failure, "current read %d", i)
	}
	failAt = 0
	h.m.deliveryAuthority = nil
	require.ErrorIs(t, read(), ErrUnavailable)
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{}, failure
	}
	require.ErrorIs(t, read(), failure)
}
