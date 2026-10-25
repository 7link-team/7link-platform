package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestRuntimeDefectDeploymentThroughAgentQAAndHumanClosure(t *testing.T) {
	for _, scenario := range []string{"PASSED", "FAILED", "REJECTED"} {
		t.Run(scenario, func(t *testing.T) {
			verdict := "PASSED"
			if scenario == "FAILED" {
				verdict = "FAILED"
			}
			h, producer, deployment := deploymentFixture(t)
			resultID := deployment.ResultID
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_qa_required", true).Error)
			criterion, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).First()
			require.NoError(t, err)
			start := func(in verificationStartBody) {
				_, e := h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
					return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, resultID, 12, 11, version, in)
				})
				require.NoError(t, e)
			}
			collect := func() {
				_, e := h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
					return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, resultID, 12, 11, version)
				})
				require.NoError(t, e)
			}
			start(verificationStartBody{CriterionID: criterion.ID, Method: "COMMAND", Argv: []string{"go", "version"}, TimeoutSec: 30})
			collect()
			require.Equal(t, "VERIFYING", h.stored().State, "AC41: passed checks cannot skip deployment")
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				h.tx = tx
				defer func() { h.tx = nil }()
				_, e := h.m.recordDeploymentOn(producer, tx, 1, 99, h.work, deployment)
				return e
			}))
			require.Equal(t, "INTERNAL_QA", h.stored().State)
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
			h.m.verificationExecutor.LaunchQualityOn = func(ctx context.Context, tx *gorm.DB, request QualityLaunchRequest) (guid.ID, error) {
				require.Zero(t, authn.PrincipalIDFromContext(ctx))
				a := query.BindResultActions(tx, h.m.service.names)
				action, e := a.WithContext(ctx).Where(a.ID.Eq(request.SourceDecisionID)).First()
				require.NoError(t, e)
				require.Equal(t, "SYSTEM:internal_qa", action.AuthoritySource)
				require.Equal(t, resultID, action.ResultID)
				return 99001, nil
			}
			tasks := map[guid.ID]VerificationRequest{}
			h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, request VerificationRequest) (guid.ID, error) {
				id := guid.ID(1000 + len(tasks))
				tasks[id] = request
				return id, nil
			}
			h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, _, _, _, result, task guid.ID) (VerificationReceipt, error) {
				request := tasks[task]
				return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: request.ExecutionID, CriterionID: request.CriterionID, ResultVersion: request.ResultVersion, Method: request.Method, Stage: request.Stage, ReviewerID: request.ReviewerPrincipalID, Model: "review-model", Family: "independent-family", Verdict: verdict, ReceiptHash: strings.Repeat("b", 64), HeadCommit: request.HeadCommit}, nil
			}
			judge := verificationStartBody{CriterionID: criterion.ID, Method: "AGENT_JUDGE", Stage: "AGENT_TEST", ReviewerPrincipalID: 21, TimeoutSec: 30}
			start(judge)
			collect()
			if verdict == "FAILED" {
				require.Equal(t, "REWORK", h.stored().State, "AC43")
				require.Nil(t, h.stored().DefectStage)
				var escalations int64
				require.NoError(t, h.db.Table("provision_probe").Where("step LIKE ?", "event:work_item.escalated%").Count(&escalations).Error)
				require.Zero(t, escalations, "an internal failure must not notify Human review")
				return
			}
			judge.Stage, judge.ReviewerPrincipalID = "AGENT_ACCEPTANCE", 22
			start(judge)
			collect()
			require.Equal(t, "NEEDS_HUMAN_REVIEW", h.stored().State)
			require.Equal(t, "VERIFIED", *h.stored().DefectStage)
			if scenario == "REJECTED" {
				body := defectStageBody{ResultID: resultID, Stage: "REWORK", Reason: "The agreed functional behavior still fails", Feedback: []resultFeedbackBody{{Kind: "FEEDBACK", Category: "SCOPE_CHANGE", Detail: "This is a new requirement, not an escaped defect"}}}
				reject := func() error {
					return h.db.Transaction(func(tx *gorm.DB) error {
						_, e := h.m.defectStageOn(h.actor(12), tx, 1, 99, h.work, 12, 11, h.stored().Version, body)
						return e
					})
				}
				require.ErrorIs(t, reject(), ErrInvalidArgument, "scope changes must use their canonical command and never count as functional escapes")
				require.Equal(t, "NEEDS_HUMAN_REVIEW", h.stored().State)
				body.Feedback[0].Category, body.Feedback[0].Detail = "QUALITY_REJECTION", "The agreed functional behavior still fails"
				before := h.stored()
				h.fail = "audit"
				require.Error(t, reject())
				require.Equal(t, before, h.stored(), "audit failure must roll back the escape and rejection")
				h.fail = ""
				require.NoError(t, reject())
				require.Equal(t, "REWORK", h.stored().State)
				require.Nil(t, h.stored().DefectStage)
				require.Equal(t, "HUMAN_REVIEW", *h.stored().EscapedFromStage)
				require.Equal(t, 1, h.stored().EscapeCount)
				q := query.BindReportQualityReceipts(h.db, h.m.service.names)
				n, e := q.WithContext(h.ctx).Where(q.WorkItemID.Eq(h.work), q.Kind.Eq("ESCAPE_HUMAN")).Count()
				require.NoError(t, e)
				require.EqualValues(t, 1, n)
				return
			}
			_, err = h.check(resultID, 12, "PASSED")
			require.NoError(t, err)
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
			}
			_, err = h.command(14, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
				return h.m.acceptResultOn(ctx, tx, 1, 99, h.work, resultID, 14, 11, version, resultAcceptBody{Reason: "Attempted Agent closure", SeenSeq: new(h.stored().ActivitySeq)})
			})
			require.ErrorIs(t, err, ErrDefectAgentAccept, "AC44: Agent cannot close defects")
			h.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 50, nil }
			h.m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
				return []core.EntityRoleBinding{{PrincipalID: 12, RoleID: 50, Effect: "allow"}}, nil
			}
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"introduced_stage": "DEVELOPMENT", "defect_details": `{"resolution":"FIXED"}`}).Error)
			_, err = h.accept(resultID, 12)
			require.NoError(t, err, "AC44: current Human tester may close")
			require.Equal(t, "ACCEPTED", h.stored().State)
			require.Equal(t, "CLOSED", *h.stored().DefectStage)
		})
	}
}

func deploymentFixture(t *testing.T) (*resultFixture, context.Context, DeploymentEvent) {
	h, r, _ := verificationFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"type": "BUG", "severity": "NORMAL", "priority": "MEDIUM", "reproducibility": "ALWAYS", "state": "VERIFYING", "defect_stage": "MERGED"}).Error)
	h.m.principals.ActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 14, nil
	}
	h.m.defectMerge = func(_ context.Context, tx *gorm.DB, tenant, project, work, execution guid.ID, head string) (DefectMergeFact, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, h.work, work)
		require.Equal(t, guid.ID(880), execution)
		require.Equal(t, strings.Repeat("a", 40), head)
		return DefectMergeFact{MergeID: 901, SourceCommit: head, MergeCommit: strings.Repeat("b", 40), TargetBranch: "main", ReceiptHash: strings.Repeat("c", 64)}, nil
	}
	// The canonical service-account client-credentials issuer deliberately does
	// not set OAuth client assurance on a service-account subject.
	ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 14, SubjectType: authn.SubjectTypeServiceAccount})
	return h, ctx, DeploymentEvent{ResultID: r.ID, EventKey: "pipeline-run-1", Environment: "staging", Commit: strings.Repeat("a", 40), ArtifactHash: strings.Repeat("d", 64), FixedVersion: "v2", OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
}

func TestDeploymentTrustedArtifactIdempotencyAndCurrentAuthority(t *testing.T) {
	h, ctx, in := deploymentFixture(t)
	var first DeploymentReceipt
	command := func(body DeploymentEvent) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			first, err = h.m.recordDeploymentOn(ctx, tx, 1, 99, h.work, body)
			return err
		})
	}
	require.NoError(t, command(in))
	id := first.ID
	require.Equal(t, "DEPLOYED", *h.stored().DefectStage)
	require.Equal(t, "v2", *h.stored().FixedVersion)
	require.NoError(t, command(in))
	require.Equal(t, id, first.ID)
	changed := in
	changed.ArtifactHash = strings.Repeat("e", 64)
	require.ErrorIs(t, command(changed), ErrDefectDeployment)
	q := query.BindDeployments(h.db, h.m.service.names)
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, command(in), "even a matching stored event must reauthorize the current producer")
}

func TestDeploymentUntrustedMismatchAndAtomicFailures(t *testing.T) {
	for _, scenario := range []string{"human", "wrong_commit", "wrong_result", "missing_merge", "bad_hash", "future", "audit", "outbox", "readiness_storage", "stale", "paused", "foreign_project", "foreign_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			h, ctx, in := deploymentFixture(t)
			tenant, project := guid.ID(1), guid.ID(99)
			switch scenario {
			case "human":
				ctx = h.actor(12)
			case "wrong_commit":
				in.Commit = strings.Repeat("e", 40)
			case "wrong_result":
				in.ResultID++
			case "missing_merge":
				h.m.defectMerge = nil
			case "bad_hash":
				in.ArtifactHash = strings.Repeat("z", 64)
			case "future":
				in.OccurredAt = time.Now().Add(time.Hour)
			case "audit", "outbox":
				h.fail = scenario
			case "readiness_storage":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("deployment-readiness-storage", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.service.names.VerificationRuns.String() {
						tx.AddError(errors.New("verification storage unavailable"))
					}
				}))
				defer h.db.Callback().Query().Remove("deployment-readiness-storage")
			case "stale":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now()).Error)
			case "foreign_project":
				project = 100
			case "foreign_tenant":
				tenant = 2
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.recordDeploymentOn(ctx, tx, tenant, project, h.work, in)
				return e
			})
			require.Error(t, err)
			require.Equal(t, "MERGED", *h.stored().DefectStage)
			q := query.BindDeployments(h.db, h.m.service.names)
			n, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, n)
		})
	}
}

func TestDefectMergedWaitsForDeploymentBeforeInternalQA(t *testing.T) {
	h, ctx, in := deploymentFixture(t)
	result, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, in.ResultID)
	require.NoError(t, err)
	var plan []workVerificationStep
	require.NoError(t, json.Unmarshal([]byte(result.VerificationPlan), &plan))
	require.Len(t, plan, 1)
	start := verificationStartBody{CriterionID: plan[0].CriterionID, Method: "COMMAND", Argv: []string{"go", "version"}, TimeoutSec: 30}
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, in.ResultID, 12, 11, v, start)
	})
	require.NoError(t, err)
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, in.ResultID, 12, 11, v)
	})
	require.NoError(t, err)
	require.Equal(t, "VERIFYING", h.stored().State)
	require.Equal(t, "MERGED", *h.stored().DefectStage)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		h.tx = tx
		_, e := h.m.recordDeploymentOn(ctx, tx, 1, 99, h.work, in)
		return e
	}))
	require.Equal(t, "INTERNAL_QA", h.stored().State)
	require.Equal(t, "DEPLOYED", *h.stored().DefectStage)
}

func TestDeploymentAutomaticallyQueuesCurrentInternalQATester(t *testing.T) {
	h, ctx, in := deploymentFixture(t)
	criterion, err := query.BindWorkCriteria(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, in.ResultID, 12, 11, version, verificationStartBody{CriterionID: criterion.ID, Method: "COMMAND", Argv: []string{"go", "version"}, TimeoutSec: 30})
	})
	require.NoError(t, err)
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, in.ResultID, 12, 11, version)
	})
	require.NoError(t, err)
	// Runtime results use the execution identity. Match that production binding
	// so this exercises the automatic participant discovery, not a manual start.
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", in.ResultID).Update("source_execution_id", in.ResultID).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("internal_qa_required", true).Error)
	h.m.defectMerge = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) (DefectMergeFact, error) {
		return DefectMergeFact{SourceCommit: in.Commit, ReceiptHash: strings.Repeat("c", 64)}, nil
	}
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.verificationExecutor.ReviewersOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, execution guid.ID, stage string) ([]guid.ID, error) {
		require.Equal(t, in.ResultID, execution)
		if stage == "AGENT_TEST" {
			return []guid.ID{21}, nil
		}
		return []guid.ID{22}, nil
	}
	var queued []VerificationRequest
	h.m.verificationExecutor.QueueOn = func(_ context.Context, _ *gorm.DB, request VerificationRequest) (guid.ID, error) {
		queued = append(queued, request)
		return 901, nil
	}
	var locked []guid.ID
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.recordDeploymentOn(ctx, tx, 1, 99, h.work, in)
		return e
	}))
	require.Equal(t, "INTERNAL_QA", h.stored().State)
	require.Len(t, queued, 1)
	require.Equal(t, "AGENT_TEST", queued[0].Stage)
	require.Equal(t, guid.ID(21), queued[0].ReviewerPrincipalID)
	require.Equal(t, in.ResultID, queued[0].ExecutionID)
	for _, principal := range []guid.ID{11, 14, 21, 22} {
		require.Contains(t, locked, principal)
	}
}
