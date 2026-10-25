package execution

import (
	"7link.com/platform/internal/infra/events"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementPrecheckReadsActualExecutionIdentityAndCurrentAuthority(t *testing.T) {
	f := executionHarness(t)
	created := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(f.human).Where(q.ID.Eq(created.ID)).First()
	require.NoError(t, err)
	var plan ExecutionPlan
	require.NoError(t, json.Unmarshal([]byte(row.PlanJSON), &plan))
	plan.ExecutorPrincipalID = 21
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(row.ID)).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)})
	require.NoError(t, err)
	read := func(tenant, agent guid.ID) ([]agents.RetirementExecution, error) {
		var out []agents.RetirementExecution
		e := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.retirementExecutionsOn(f.human, tx, tenant, agent)
			return e
		})
		return out, e
	}
	items, err := read(7, 21)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, created.ID, items[0].ID)
	require.Equal(t, "CREATED", items[0].State)
	require.Equal(t, created.Version, items[0].Version)
	items, err = read(7, 22)
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = read(8, 21)
	require.NoError(t, err)
	require.Empty(t, items)
	f.authorized = false
	_, err = read(7, 21)
	require.ErrorIs(t, err, ErrForbidden)
	f.authorized = true
	_, err = q.WithContext(f.human).Where(q.ID.Eq(row.ID)).Update(q.State, "SUCCEEDED")
	require.NoError(t, err)
	items, err = read(7, 21)
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = f.m.retirementExecutionsOn(f.human, f.db, 7, 21)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestSkillDistillationReusesRunnerWithoutWorkCallbacksOrCompletionClaim(t *testing.T) {
	for _, scenario := range []string{"success", "failed", "cancelled", "revoked"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		t.Run(scenario, func(t *testing.T) {
			f := executionHarness(t)
			parent := f.create(t)
			q := query.BindExecutions(f.db, f.m.names)
			_, err := q.WithContext(t.Context()).Where(q.ID.Eq(parent.ID)).Update(q.State, "SUCCEEDED")
			require.NoError(t, err)
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "observed-family", Roles: []string{"EXECUTOR"}}, true, nil
			}
			f.m.work.SettleBudgetOn = func(_ context.Context, tx *gorm.DB, l WorkLink, r WorkResult) error {
				require.Zero(t, l.AssignmentID)
				return tx.Exec("INSERT INTO effects(kind) VALUES ('distillation.budget_settled')").Error
			}
			s := module.NewServices()
			var runtime knowledge.DistillationRuntime
			completed := 0
			require.NoError(t, module.Provide[knowledge.DistillationConnector](s, knowledge.DistillationConnector{BindRuntime: func(value knowledge.DistillationRuntime) error { runtime = value; return nil }, CompleteOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
				completed++
				output, e := f.m.skillDistillationOutputOn(ctx, tx, tenant, id)
				if e != nil {
					return e
				}
				expected := map[string]string{"success": "SUCCEEDED", "failed": "FAILED", "cancelled": StateCanceled}[scenario] //nolint:misspell // Preserve the published domain or serialized contract spelling.
				require.Equal(t, expected, output.State)
				if scenario == "success" {
					require.Contains(t, output.Content, "name: actual-guidance")
				} else {
					require.Empty(t, output.Content)
				}
				require.Equal(t, guid.ID(21), output.AgentPrincipalID)
				return nil
			}}))
			require.NoError(t, module.Provide[delivery.SkillDistillationWork](s, delivery.SkillDistillationWork{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, work guid.ID) (knowledge.AcceptedSkillSource, error) {
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, guid.ID(3), work)
				return knowledge.AcceptedSkillSource{ProjectID: 9, WorkItemID: work, ResultID: parent.ID, ExecutionID: parent.ID, HeadCommit: strings.Repeat("b", 40), Content: "Accepted canonical delivery"}, nil
			}}))
			require.NoError(t, s.Seal())
			require.NoError(t, f.m.bindSkillDistillation(s))
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				id, e = runtime.LaunchOn(f.human, tx, 7, knowledge.DistillationLaunch{DistillationID: 900, ProjectID: 9, WorkItemID: 3, AgentPrincipalID: 21, SourceExecutionID: parent.ID, HeadCommit: strings.Repeat("b", 40), Objective: "Produce a valid SKILL.md from accepted facts"})
				return e
			}))
			created := f.read(t, id)
			require.Zero(t, created.AssignmentID)
			require.Equal(t, "SKILL_DISTILLATION", created.BranchReason)
			require.Equal(t, "READ_ONLY", created.Plan.AuthorizationMode)
			require.Equal(t, guid.ID(21), created.Plan.ExecutorPrincipalID)
			require.Nil(t, created.Claim)
			if scenario == "cancelled" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, id, created.Version); return e }))
				final := f.read(t, id)
				require.Equal(t, StateCanceled, final.State)
				require.Nil(t, final.Claim)
				require.Equal(t, 1, completed)
				var count int64
				require.NoError(t, f.db.Table("effects").Where("kind LIKE ?", "work.%").Count(&count).Error)
				require.Zero(t, count)
				require.NoError(t, f.db.Table("effects").Where("kind = ?", "distillation.budget_settled").Count(&count).Error)
				require.EqualValues(t, 1, count)
				return
			}
			f.pull(t, id)
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			if scenario == "failed" || scenario == "revoked" {
				before := f.read(t, id)
				if scenario == "revoked" {
					policy := f.m.policy
					f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
						if permission == "knowledge_write" {
							return authz.DataConstraint{}, nil
						}
						return policy(ctx, tx, tenant, project, permission, actor)
					}
				}
				_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "FAILED"})
				if scenario == "revoked" {
					require.ErrorIs(t, err, ErrForbidden)
					require.Equal(t, before, f.read(t, id))
					require.Zero(t, completed)
				} else {
					require.NoError(t, err)
					require.Equal(t, "FAILED", f.read(t, id).State)
					require.Nil(t, f.read(t, id).Claim)
					require.Equal(t, 1, completed)
				}
				var count int64
				require.NoError(t, f.db.Table("effects").Where("kind LIKE ?", "work.%").Count(&count).Error)
				require.Zero(t, count)
				require.NoError(t, f.db.Table("effects").Where("kind = ?", "distillation.budget_settled").Count(&count).Error)
				if scenario == "revoked" {
					require.Zero(t, count)
				} else {
					require.EqualValues(t, 1, count)
				}
				return
			}
			document := "---\nname: actual-guidance\ndescription: Preserve verified evidence\n---\nReview the actual receipts."
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Runtime skill distillation output", Content: document})
			require.NoError(t, err)
			head := strings.Repeat("b", 40)
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Execution head commit", Content: head})
			require.NoError(t, err)
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: head})
			require.NoError(t, err)
			final := f.read(t, id)
			require.Equal(t, "SUCCEEDED", final.State)
			require.Equal(t, "SKILL_DISTILLATION_COMPLETED", final.StateReason)
			require.Nil(t, final.Claim)
			require.Equal(t, 1, completed)
			var writes int64
			require.NoError(t, f.db.Table("effects").Where("kind LIKE ?", "work.%").Count(&writes).Error)
			require.Zero(t, writes, "distillation must never call work START/FINISH")
			require.NoError(t, f.db.Table("effects").Where("kind = ?", "distillation.budget_settled").Count(&writes).Error)
			require.EqualValues(t, 1, writes)
			// An ordinary public Create still cannot omit its genuine assignment or
			// inject a private purpose through JSON plan fields.
			input := validCreate()
			input.AssignmentID = 0
			input.Plan.Distillation = &DistillationPlan{ID: 901, AgentPrincipalID: 21, SourceExecutionID: parent.ID}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, input); return e }), ErrInvalidArgument)
		})
	}
}

func TestSourceOwnedInboxDoesNotProjectProjectlessHandoverAsProjectActivity(t *testing.T) {
	f, _, _ := handoverFixture(t)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		t.Error("a projectless handover has no project execution-read source")
		return authz.DataConstraint{}, ErrInvalidArgument
	}
	services := module.NewServices()
	require.NoError(t, module.Provide(services, agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
		t.Error("handover lifecycle is delivered by its retirement protocol, not a project activity receipt")
		return 0, ErrInvalidArgument
	}}))
	require.NoError(t, services.Seal())
	require.NoError(t, f.m.bindInteractionInbox(services))
	require.Positive(t, launchTestHandover(t, f))
}

func TestSourceOwnedManualEvidenceInboxExcludesOwnEventsAndRetainsStoppedRecipients(t *testing.T) {
	for _, state := range []string{"ACTIVE", "STOPPED", "CRASHED", "RETIRING", "DRAFT", "RETIRED"} {
		t.Run(state, func(t *testing.T) {
			f, agent, v := manualEvidenceFixture(t)
			v.Plan.ExecutorPrincipalID = 22
			plan, err := json.Marshal(v.Plan)
			require.NoError(t, err)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Updates(map[string]any{"plan_json": string(plan), "plan_hash": hash(plan)}).Error)
			f.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 220, TenantID: tenant, PrincipalID: principal, State: state}, principal == 22, nil
			}
			var receipts []agents.InboxReference
			services := module.NewServices()
			require.NoError(t, module.Provide(services, agents.InboxAppender{AppendOn: func(_ context.Context, _ *gorm.DB, tenant, recipient guid.ID, ref agents.InboxReference) (int64, error) {
				require.EqualValues(t, 7, tenant)
				require.EqualValues(t, 22, recipient)
				require.Equal(t, "work_item:3", ref.StreamKey)
				receipts = append(receipts, ref)
				return int64(len(receipts)), nil
			}}))
			require.NoError(t, services.Seal())
			require.NoError(t, f.m.bindInteractionInbox(services))
			create := func(ctx context.Context) error {
				return f.db.Transaction(func(tx *gorm.DB) error {
					_, err := f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: new(int64(1)), EvidenceType: "OBSERVATION", Title: "Observed output", ScopeText: "Current screen", Text: "Actual source observation"})
					return err
				})
			}
			if state == "ACTIVE" {
				require.NoError(t, create(agent))
				require.Empty(t, receipts, "an Agent's own evidence must not loop back into its inbox")
			}
			require.NoError(t, create(f.human))
			if state == "RETIRED" {
				require.Empty(t, receipts)
			} else {
				require.Len(t, receipts, 1, "non-running hosts retain others' source events")
				require.Equal(t, "EXECUTION", receipts[0].Kind)
				require.Equal(t, v.ID, receipts[0].SubjectID)
			}
		})
	}
}

func TestSourceOwnedArtifactCommentInboxSharesSourceTransactionAndCurrentAuthority(t *testing.T) {
	f := executionHarness(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	f.m.commentActivities = delivery.WorkItemActivityRecorder{RecordOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, in delivery.WorkActivityInput) (int64, error) {
		return 1, tx.Exec("INSERT INTO effects(kind) VALUES (?)", in.Kind).Error
	}}
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Source", Upload: &ArtifactUpload{Name: "source.go", Data: []byte("package sample\nfunc value() bool { return false }\n"), Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	evidence, err := q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
	require.NoError(t, err)
	executions := query.BindExecutions(f.db, f.m.names)
	v.Plan.ExecutorPrincipalID = 21
	plan, err := json.Marshal(v.Plan)
	require.NoError(t, err)
	_, err = executions.WithContext(f.human).Where(executions.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(plan), "plan_hash": hash(plan)})
	require.NoError(t, err)
	state, readable := "ACTIVE", true
	sourcePolicy := f.m.policy
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if actor == 21 && permission == "execution_read" && !readable {
			return authz.DataConstraint{}, nil
		}
		return sourcePolicy(ctx, tx, tenant, project, permission, actor)
	}
	f.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: principal, State: state}, principal == 21, nil
	}
	require.NoError(t, f.db.Exec("CREATE TABLE comment_inbox_probe (source TEXT PRIMARY KEY, subject BIGINT, kind TEXT)").Error)
	failure := errors.New("inbox insert failed")
	fail := true
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, EntityID: 9, SubjectType: authn.SubjectTypePrincipal})
	s := module.NewServices()
	require.NoError(t, module.Provide(s, agents.InboxAppender{AppendOn: func(_ context.Context, tx *gorm.DB, tenant, agent guid.ID, ref agents.InboxReference) (int64, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 21, agent)
		require.Equal(t, "MESSAGE", ref.Kind)
		require.NotEmpty(t, ref.SourceEventID)
		if fail {
			return 0, failure
		}
		return 1, tx.Exec("INSERT INTO comment_inbox_probe VALUES (?, ?, ?)", ref.SourceEventID, ref.SubjectID, ref.Kind).Error
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, f.m.bindInteractionInbox(s))
	create := func(ctx context.Context) (out ArtifactCommentView, err error) {
		err = f.db.Transaction(func(tx *gorm.DB) error {
			out, err = f.m.CreateArtifactCommentOn(ctx, tx, 7, v.ID, ArtifactCommentCreate{SeenSeq: new(int64), EvidenceID: evidence.ID, StartLine: 2, EndLine: 2, Body: "Please check this result"})
			return err
		})
		return
	}
	_, err = create(f.human)
	require.ErrorIs(t, err, failure)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Count(&count).Error)
	require.Zero(t, count, "receipt failure rolls back the original comment")
	fail = false
	comment, err := create(f.human)
	require.NoError(t, err)
	require.NoError(t, f.db.Table("comment_inbox_probe").Where("subject = ?", comment.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	readable = false
	_, err = create(f.human)
	require.NoError(t, err)
	readable, state = true, "RETIRED"
	_, err = create(f.human)
	require.NoError(t, err)
	state = "ACTIVE"
	_, err = create(authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 21, EntityID: 9, SubjectType: authn.SubjectTypePrincipal}))
	require.NoError(t, err)
	require.NoError(t, f.db.Table("comment_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count, "unreadable, retired and self-authored comments emit no receipt")
}

func TestSourceOwnedInteractionInboxDecisionRollbackExpiryCancellationAndReplay(t *testing.T) {
	f, v, input := approvalFixture(t)
	// Source-owner fixture supplies a frozen executor identity. Runner approval
	// creation/decision/cancel still use their real leased domain commands.
	q := query.BindExecutions(f.db, f.m.names)
	v.Plan.ExecutorPrincipalID = 21
	plan, err := json.Marshal(v.Plan)
	require.NoError(t, err)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(plan), "plan_hash": hash(plan)})
	require.NoError(t, err)
	state := "ACTIVE"
	readable := true
	sourcePolicy := f.m.policy
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if actor == 21 && permission == "execution_read" && !readable {
			return authz.DataConstraint{}, nil
		}
		return sourcePolicy(ctx, tx, tenant, project, permission, actor)
	}
	f.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: id, State: state}, id == 21, nil
	}
	require.NoError(t, f.db.Exec("CREATE TABLE interaction_inbox_probe (source TEXT PRIMARY KEY, subject BIGINT, kind TEXT)").Error)
	require.NoError(t, f.db.Exec("CREATE TABLE execution_inbox_probe (source TEXT PRIMARY KEY, subject BIGINT, kind TEXT)").Error)
	fail := false
	failure := errors.New("receipt insertion failed")
	s := module.NewServices()
	require.NoError(t, module.Provide(s, agents.InboxAppender{AppendOn: func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, r agents.InboxReference) (int64, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 21, agent)
		require.Equal(t, "work_item:"+v.WorkItemID.String(), r.StreamKey)
		require.EqualValues(t, 0, r.StreamSeq, "executionHarness supplies the current work activity head")
		if fail {
			return 0, failure
		}
		switch r.Kind {
		case "INTERACTION":
			require.Equal(t, "interaction:"+r.SubjectID.String()+":terminal", r.SourceEventID)
			return 1, tx.Exec("INSERT INTO interaction_inbox_probe VALUES (?, ?, ?) ON CONFLICT(source) DO NOTHING", r.SourceEventID, r.SubjectID, r.Kind).Error
		case "EXECUTION":
			require.Equal(t, "1", r.SourceEventID, "testPublisher returns the source event ID")
			require.Equal(t, v.ID, r.SubjectID)
			executions := query.BindExecutions(tx, f.m.names)
			stored, err := executions.WithContext(ctx).Where(executions.ID.Eq(v.ID)).First()
			require.NoError(t, err)
			require.NotNil(t, stored.CancelRequestedAt)
			require.Equal(t, "USER_CANCEL", stored.StateReason)
			return 1, tx.Exec("INSERT INTO execution_inbox_probe VALUES (?, ?, ?)", r.SourceEventID, r.SubjectID, r.Kind).Error
		default:
			t.Fatalf("unexpected inbox receipt kind %q", r.Kind)
			return 0, ErrInvalidArgument
		}
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, f.m.bindInteractionInbox(s))
	a := requestApproval(t, f, v, input)
	decide := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "ALLOW", "Reviewed exact tool input")
			return err
		})
	}
	fail = true
	require.ErrorIs(t, decide(), failure)
	approvals := query.BindApprovals(f.db, f.m.names)
	stored, err := approvals.WithContext(f.human).Where(approvals.ID.Eq(a.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", stored.State)
	fail = false
	require.NoError(t, decide())
	require.NoError(t, decide())
	f.authorized = false
	require.ErrorIs(t, decide(), ErrForbidden)
	f.authorized = true
	var count int64
	require.NoError(t, f.db.Table("interaction_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	input.RequestID = "runtime-request-expiring"
	expiring := requestApproval(t, f, v, input)
	_, err = approvals.WithContext(f.human).Where(approvals.ID.Eq(expiring.ID)).Update(approvals.ExpiresAt, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, err)
	state = "RETIRED"
	require.NoError(t, f.m.expireInteractions(f.human, f.db, 7, time.Now().UTC()))
	require.NoError(t, f.db.Table("interaction_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	state = "ACTIVE"
	readable = false
	require.NoError(t, f.m.expireInteractions(f.human, f.db, 7, time.Now().UTC()))
	require.NoError(t, f.db.Table("interaction_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count, "loss of source read authority emits no receipt")
	readable = true
	require.NoError(t, f.m.expireInteractions(f.human, f.db, 7, time.Now().UTC()))
	require.NoError(t, f.m.expireInteractions(f.human, f.db, 7, time.Now().UTC()))
	require.NoError(t, f.db.Table("interaction_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 2, count)
	input.RequestID = "runtime-request-cancelling" //nolint:misspell // Preserve the published domain or serialized contract spelling.
	pending := requestApproval(t, f, v, input)
	current, err := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CancelOn(f.human, tx, 7, v.ID, current.Version); return err }))
	require.NoError(t, f.db.Table("interaction_inbox_probe").Where("subject=?", pending.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.db.Table("interaction_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 3, count, "decision, expiry and cancellation each retain one interaction receipt")
	require.NoError(t, f.db.Table("execution_inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count, "cancellation also emits its distinct execution activity receipt")
}

type expiryCatalog struct {
	handlers      []jobs.Handler
	schedules     []jobs.Schedule
	handlerError  error
	scheduleError error
}

func (c *expiryCatalog) AddHandlers(_ string, h ...jobs.Handler) error {
	c.handlers = append(c.handlers, h...)
	return c.handlerError
}
func (c *expiryCatalog) AddSchedules(_ string, s ...jobs.Schedule) error {
	c.schedules = append(c.schedules, s...)
	return c.scheduleError
}

func TestInteractionExpiryJobRequiresWriterAndPropagatesCatalogFailure(t *testing.T) {
	for _, fault := range []string{"handlers", "schedules", "writer", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("job registration failed")
			catalog := &expiryCatalog{}
			if fault == "handlers" {
				catalog.handlerError = failure
			}
			if fault == "schedules" {
				catalog.scheduleError = failure
			}
			require.NoError(t, module.Provide[jobs.Catalog](s, catalog))
			require.NoError(t, s.Seal())
			e := f.m.registerInteractionExpiry(s)
			if fault == "handlers" || fault == "schedules" {
				require.ErrorIs(t, e, failure)
				return
			}
			require.NoError(t, e)
			require.Len(t, catalog.handlers, 1)
			if fault == "writer" {
				f.m.runtime = nil
			}
			e = catalog.handlers[0].Run(f.human, jobs.Job{TenantID: 7})
			if fault == "writer" {
				require.ErrorIs(t, e, ErrUnavailable)
			} else {
				require.NoError(t, e)
			}
		})
	}
	for _, fault := range []string{"appender", "publisher", "duplicate"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			appender := agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) { return 1, nil }}
			if fault == "appender" {
				appender.AppendOn = nil
			}
			if fault == "publisher" {
				f.m.events = nil
			}
			require.NoError(t, module.Provide(s, appender))
			require.NoError(t, s.Seal())
			if fault == "duplicate" {
				require.NoError(t, f.m.bindInteractionInbox(s))
				require.ErrorIs(t, f.m.bindInteractionInbox(s), ErrConflict)
			} else {
				require.ErrorIs(t, f.m.bindInteractionInbox(s), ErrCapabilityUnavailable)
			}
		})
	}
}

func TestSourceOwnedInteractionExpiryRegistersTenantJobAndRejectsTamperedRecipient(t *testing.T) {
	f, v, input := approvalFixture(t)
	a := requestApproval(t, f, v, input)
	catalog := &expiryCatalog{}
	s := module.NewServices()
	require.NoError(t, module.Provide[jobs.Catalog](s, catalog))
	require.NoError(t, s.Seal())
	require.NoError(t, f.m.registerInteractionExpiry(s))
	require.Len(t, catalog.handlers, 1)
	require.Len(t, catalog.schedules, 1)
	require.Equal(t, jobs.ScopePerTenant, catalog.schedules[0].TenantScope)
	require.Equal(t, catalog.handlers[0].Kind, catalog.schedules[0].Kind)
	require.Error(t, catalog.handlers[0].Run(t.Context(), jobs.Job{}))
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, err)
	approval := query.BindApprovals(f.db, f.m.names)
	r, err := approval.WithContext(t.Context()).Where(approval.ID.Eq(a.ID)).First()
	require.NoError(t, err)
	row.PlanHash = "corrupted"
	p := interactionPublisher{m: f.m, inbox: agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
		t.Fatal("corrupt source must never emit a receipt")
		return 0, nil
	}}}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return p.projectInteractionOn(f.human, tx, row, r, 0) }), ErrConflict)
}

func TestToolRelayExecutionPinsAndRejectsLeasePlanAndDispatchChanges(t *testing.T) {
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	pin := agents.RelayToolPin{ToolID: 61, EntryKey: "catalog-test", VersionNo: 1, GrantID: 62, GrantVersion: 1}
	f.m.knowledge.toolPins = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID) ([]agents.RelayToolPin, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Positive(t, int64(agent))
		return []agents.RelayToolPin{pin}, nil
	}
	v := f.create(t)
	require.Equal(t, []agents.RelayToolPin{pin}, v.Plan.Toolset)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindExecutions(tx, f.m.names)
		row, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
		if e != nil {
			return e
		}
		now := time.Now().UTC().Add(time.Minute)
		row.State = "RUNNING"
		row.DispatchEpoch = 1
		row.LeaseExpiresAt = &now
		require.NoError(t, q.WithContext(t.Context()).Save(row))
		got, e := f.m.toolRelayExecutionOn(f.human, tx, 7, row.ID)
		require.NoError(t, e)
		require.Equal(t, []agents.RelayToolPin{pin}, got.Tools)
		row.PlanHash = "tampered"
		require.NoError(t, q.WithContext(t.Context()).Save(row))
		_, e = f.m.toolRelayExecutionOn(f.human, tx, 7, row.ID)
		require.Error(t, e)
		row.PlanHash = fmt.Sprintf("%x", sha256.Sum256([]byte(row.PlanJSON)))
		past := time.Now().UTC().Add(-time.Second)
		row.LeaseExpiresAt = &past
		require.NoError(t, q.WithContext(t.Context()).Save(row))
		_, e = f.m.toolRelayExecutionOn(f.human, tx, 7, row.ID)
		require.Error(t, e)
		return nil
	}))
}

func TestAutomaticKnowledgePinsApprovedRulesAndBudgetedSnapshotWithoutExplicitRefs(t *testing.T) {
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	f.m.knowledge.work.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	rule := knowledge.ContextItem{EntryID: 501, Version: 2, Label: "[R1]", Title: "Evidence rule", Body: "Keep independent evidence.", ContentHash: "canonical-rule"}
	item := knowledge.ContextItem{EntryID: 502, Version: 3, Label: "[K1]", Title: "Prior lesson", Body: "Budgeted prefix", ContentHash: "canonical-original", SnapshotHash: "budgeted-snapshot"}
	calls := 0
	f.m.knowledge.context.BuildInjectionOn = func(ctx context.Context, tx *gorm.DB, in knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
		calls++
		require.Equal(t, guid.ID(21), authn.PrincipalIDFromContext(ctx))
		require.Equal(t, guid.ID(7), in.TenantID)
		require.Equal(t, guid.ID(9), in.ProjectID)
		require.Equal(t, guid.ID(3), in.WorkItemID)
		require.Positive(t, int64(in.ExecutionID))
		require.Empty(t, in.ExplicitIDs)
		require.NotEmpty(t, in.Query.Title)
		return knowledge.InjectionResult{Rules: []knowledge.InjectionItem{{ContextItem: rule}}, Items: []knowledge.InjectionItem{{ContextItem: item}}}, nil
	}
	f.m.knowledge.context.AuthorizeSnapshotOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID, items []knowledge.ContextItem) error {
		require.EqualValues(t, 11, authn.PrincipalIDFromContext(ctx))
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 9, project)
		require.Equal(t, []knowledge.ContextItem{rule, item}, items)
		return nil
	}
	v := f.create(t)
	require.Equal(t, 1, calls)
	require.Equal(t, []knowledge.ContextItem{rule, item}, v.Plan.Knowledge)
	require.Equal(t, []ContextRef{{Kind: "KNOWLEDGE", Ref: 501}, {Kind: "KNOWLEDGE", Ref: 502}}, v.Plan.ContextRefs)
	stored, err := query.BindExecutions(f.db, f.m.names).WithContext(t.Context()).Where(query.BindExecutions(f.db, f.m.names).ID.Eq(v.ID)).First()
	require.NoError(t, err)
	var plan ExecutionPlan
	require.NoError(t, json.Unmarshal([]byte(stored.PlanJSON), &plan))
	require.Equal(t, v.Plan.Knowledge, plan.Knowledge)
	require.Equal(t, hash([]byte(stored.PlanJSON)), stored.PlanHash)
}

func TestMemoryExecutionRequiresOwnedExactRecentTerminalReceipt(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	v.Plan.ExecutorPrincipalID = 17
	plan, err := json.Marshal(v.Plan)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	project := guid.ID(9)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Updates(map[string]any{"state": "SUCCEEDED", "plan_json": string(plan), "plan_hash": hash(plan)}).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.memoryExecutionOn(f.human, tx, 7, v.Plan.ExecutorPrincipalID, &project, v.ID, now)
		require.ErrorIs(t, err, knowledge.ErrMemoryProvenance, "updated_at is not terminal evidence")
		r := model.Receipt{ID: 78001, TenantID: 7, ExecutionID: v.ID, DispatchEpoch: v.DispatchEpoch, Seq: 1, Kind: "COMPLETED", State: "APPLIED", ContentHash: "hash", CreatedAt: now.Add(-time.Hour)}
		require.NoError(t, query.BindReceipts(tx, f.m.names).WithContext(f.human).Create(&r))
		work, err := f.m.memoryExecutionOn(f.human, tx, 7, v.Plan.ExecutorPrincipalID, &project, v.ID, now)
		require.NoError(t, err)
		require.Equal(t, v.WorkItemID, work)
		wrong := guid.ID(10)
		for _, test := range []struct {
			agent   guid.ID
			project *guid.ID
			at      time.Time
		}{{v.Plan.ExecutorPrincipalID + 1, &project, now}, {v.Plan.ExecutorPrincipalID, &wrong, now}, {v.Plan.ExecutorPrincipalID, nil, now}, {v.Plan.ExecutorPrincipalID, &project, now.Add(25 * time.Hour)}, {v.Plan.ExecutorPrincipalID, &project, now.Add(-2 * time.Hour)}} {
			_, err = f.m.memoryExecutionOn(f.human, tx, 7, test.agent, test.project, v.ID, test.at)
			require.ErrorIs(t, err, knowledge.ErrMemoryProvenance)
		}
		return nil
	}))
}

func TestKnowledgeMetricProjectionUsesFrozenPlanAndRealDecision(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	project := guid.ID(9)
	from, to := v.CreatedAt.Add(-time.Hour), v.CreatedAt.Add(time.Hour)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		facts, err := f.m.knowledgeMetricProjectionOn(f.human, tx, 7, &project, from, to, nil)
		require.NoError(t, err)
		require.Empty(t, facts, "empty frozen plans do not prove a retrieval attempt")
		foreign, err := f.m.knowledgeMetricProjectionOn(f.human, tx, 8, &project, from, to, nil)
		require.NoError(t, err)
		require.Empty(t, foreign)
		plan := v.Plan
		plan.ContextRefs = []ContextRef{{Kind: "KNOWLEDGE", Ref: 555}}
		plan.Knowledge = []knowledge.ContextItem{{EntryID: 555, Label: "[K1]", ContentHash: "frozen"}}
		body, err := json.Marshal(plan)
		require.NoError(t, err)
		require.NoError(t, tx.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Updates(map[string]any{"plan_json": string(body), "plan_hash": hash(body)}).Error)
		evidence := model.Evidence{ID: 78002, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "OUTPUT", Title: "Output", Content: "Uses [K1] reference", ContentHash: "hash", CreatedAt: v.CreatedAt}
		require.NoError(t, query.BindEvidence(tx, f.m.names).WithContext(f.human).Create(&evidence))
		decisions := []delivery.KnowledgeDecisionFact{{ExecutionID: v.ID, ProjectID: 9, EvidenceID: 78003, Kind: "ACCEPT", CreatedAt: v.CreatedAt}}
		facts, err = f.m.knowledgeMetricProjectionOn(f.human, tx, 7, &project, from, to, decisions)
		require.NoError(t, err)
		require.Len(t, facts, 2)
		require.True(t, facts[0].Hit)
		require.Nil(t, facts[0].LatencyMS, "a frozen assembly proves no measured latency")
		require.Equal(t, "CITATION_OUTCOME", facts[1].Kind)
		require.Equal(t, "ACCEPTED", facts[1].Outcome)
		require.EqualValues(t, 555, facts[1].EntryID)
		decisions[0].ProjectID = 10
		_, err = f.m.knowledgeMetricProjectionOn(f.human, tx, 7, &project, from, to, decisions)
		require.ErrorIs(t, err, knowledge.ErrConflict)
		return nil
	}))
}

func TestHubSessionsHTTPUsesCurrentProjectAuthorityAndBoundedCursor(t *testing.T) {
	f, h := executionRoutesFixture(t)
	created := f.create(t)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", created.ID).Update("state", "PAUSED").Error)
	path := "/tenants/7/execution-sessions?project_id=9&limit=1"
	page := executionRouteData[HubExecutionSessionPage](t, executionRouteRequest(t, h, http.MethodGet, path, nil, 0, false), 200)
	require.Len(t, page.Items, 1)
	require.Equal(t, created.ID, page.Items[0].ID)
	require.True(t, page.Items[0].ViewerOwned)
	for _, invalid := range []string{"/tenants/7/execution-sessions?project_id=9&limit=101", "/tenants/7/execution-sessions?project_id=9&cursor=bad"} {
		response := executionRouteRequest(t, h, http.MethodGet, invalid, nil, 0, false)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	f.authorized = false
	response := executionRouteRequest(t, h, http.MethodGet, path, nil, 0, false)
	require.Equal(t, 403, response.Code, response.Body.String())
}

func TestHubSessionsOwnOrCurrentlyRespondableBeforePagination(t *testing.T) {
	f := executionHarness(t)
	base := f.create(t)
	ctx := f.human
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindExecutions(tx, f.m.names)
		seed, e := q.WithContext(ctx).Where(q.ID.Eq(base.ID)).First()
		if e != nil {
			return e
		}
		for _, test := range []struct {
			id, initiator guid.ID
			state         string
			approval      bool
			epoch         int64
			expiry        time.Time
			lease         *time.Time
		}{
			{100, 12, "RUNNING", false, 1, future, &future},
			{101, 12, "RUNNING", true, 1, past, &future},
			{102, 12, "RUNNING", true, 2, future, &future},
			{103, 12, "RUNNING", true, 1, future, &past},
			{104, 11, "CREATED", false, 1, future, nil},
			{105, 11, "SUCCEEDED", false, 1, future, nil},
			{106, 11, "PAUSED", false, 1, future, nil},
			{107, 12, "RUNNING", true, 1, future, &future},
			{108, 11, "RUNNING", true, 1, future, &future},
		} {
			row := *seed
			row.ID = test.id
			row.InitiatorID = test.initiator
			row.State = test.state
			row.LeaseExpiresAt = test.lease
			if e = q.WithContext(ctx).Create(&row); e != nil {
				return e
			}
			if test.approval {
				a := model.Approval{ID: test.id + 1000, TenantID: 7, ExecutionID: test.id, DispatchEpoch: test.epoch, SessionID: 2, ConnEpoch: 1, RequestID: test.id.String(), Tool: "Bash", InputJSON: "{}", RequestHash: "hash", State: "PENDING", ExpiresAt: test.expiry, CreatedAt: time.Now().UTC()}
				if e = query.BindApprovals(tx, f.m.names).WithContext(ctx).Create(&a); e != nil {
					return e
				}
			}
		}
		page, e := f.m.hubSessionsOn(ctx, tx, 7, 9, 0, 1)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 106, page.Items[0].ID)
		require.True(t, page.Items[0].ViewerOwned)
		require.False(t, page.Items[0].CurrentResponder)
		require.Zero(t, page.Items[0].PendingInteractionCount)
		require.True(t, page.HasMore)
		require.Equal(t, "106", page.NextCursor)
		page, e = f.m.hubSessionsOn(ctx, tx, 7, 9, 106, 1)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 107, page.Items[0].ID)
		require.False(t, page.Items[0].ViewerOwned)
		require.True(t, page.Items[0].CurrentResponder)
		require.Equal(t, 1, page.Items[0].PendingInteractionCount)
		require.True(t, page.HasMore)
		page, e = f.m.hubSessionsOn(ctx, tx, 7, 9, 107, 1)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 108, page.Items[0].ID)
		require.True(t, page.Items[0].ViewerOwned)
		require.True(t, page.Items[0].CurrentResponder)
		require.False(t, page.HasMore)
		return nil
	}))
	// Revoking only interaction_respond removes other people's approvals, while
	// retaining the viewer's current execution under execution_read.
	original := f.m.policy
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "interaction_respond" {
			return authz.DataConstraint{}, nil
		}
		return original(ctx, tx, tenant, project, permission, actor)
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.hubSessionsOn(ctx, tx, 7, 9, 0, 100)
		require.NoError(t, e)
		require.Len(t, page.Items, 2)
		for _, item := range page.Items {
			require.True(t, item.ViewerOwned)
			require.False(t, item.CurrentResponder)
			require.Zero(t, item.PendingInteractionCount)
		}
		return nil
	}))
	f.authorized = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.hubSessionsOn(ctx, tx, 7, 9, 0, 50); return e }), ErrForbidden)
}

func TestHubSessionsExcludeUnauthorizedExternalAndFencedApprovalWork(t *testing.T) {
	f, v, in := approvalFixture(t)
	requestApproval(t, f, v, in)
	// A different current Human may see the genuine pending request only while
	// the canonical decision authority and work assignment remain valid.
	actor := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 12})
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.hubSessionsOn(actor, tx, 7, 9, 0, 50)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.Equal(t, 1, page.Items[0].PendingInteractionCount)
		require.False(t, page.Items[0].ViewerOwned)
		return nil
	}))
	original := f.m.work.AuthorizeOn
	f.m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
		if l.ActorID == 12 {
			return ErrForbidden
		}
		return original(ctx, tx, l, op)
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.hubSessionsOn(actor, tx, 7, 9, 0, 50)
		require.NoError(t, e)
		require.Empty(t, page.Items)
		return nil
	}))
	f.humanActive = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.hubSessionsOn(actor, tx, 7, 9, 0, 50); return e }), ErrForbidden)
	f.humanActive = true
	// External shared-channel access does not convey execution_read.
	policy := f.m.policy
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		if principal == 12 && permission == "execution_read" {
			return authz.DataConstraint{}, nil
		}
		return policy(ctx, tx, tenant, project, permission, principal)
	}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.hubSessionsOn(actor, tx, 7, 9, 0, 50); return e }), ErrForbidden)
	foreign := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.hubSessionsOn(foreign, tx, 7, 9, 0, 50); return e }), ErrForbidden)
}

func TestUsedKnowledgeExecutionViewUsesFrozenFeedbackAndVerifiedOutput(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, err)
	source := guid.ID(900)
	row.SourceDecisionID = &source
	parent := guid.ID(899)
	row.ParentExecutionID = &parent
	row.FeedbackSnapshot = `{"feedback":[{"source_decision_id":"900","category":"FUNCTIONAL_DEFECT","location":"api","expected":"PASSED","detail":"Retain evidence"}]}`
	require.NoError(t, q.WithContext(t.Context()).Save(row))
	e := query.BindEvidence(f.db, f.m.names)
	body := "Resolved [F1] with source evidence"
	require.NoError(t, e.WithContext(t.Context()).Create(&model.Evidence{ID: 901, TenantID: 7, ExecutionID: v.ID, Kind: "OBSERVATION", Content: body, ContentHash: hash([]byte(body)), CreatedAt: time.Now()}))
	var out knowledge.ExecutionKnowledgeSnapshot
	err = f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.executionKnowledgeViewOn(f.human, tx, 7, v.ID)
		return e
	})
	require.NoError(t, err)
	require.Len(t, out.Feedback, 1)
	require.True(t, out.Feedback[0].Cited)
	require.Equal(t, "api", out.Feedback[0].Location)
	require.Equal(t, source, out.Feedback[0].SourceDecisionID)
	row.PlanHash = "tampered"
	require.NoError(t, q.WithContext(t.Context()).Save(row))
	err = f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.executionKnowledgeViewOn(f.human, tx, 7, v.ID); return e })
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestAutomaticKnowledgeRejectsCreatorWithoutSelectedSourcePermission(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	f.m.knowledge.work.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	item := knowledge.ContextItem{EntryID: 501, Title: "Private memory", Body: "Private source bytes"}
	f.m.knowledge.context.BuildInjectionOn = func(ctx context.Context, _ *gorm.DB, in knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
		require.EqualValues(t, 21, authn.PrincipalIDFromContext(ctx))
		return knowledge.InjectionResult{Items: []knowledge.InjectionItem{{ContextItem: item}}}, nil
	}
	f.m.knowledge.context.AuthorizeSnapshotOn = func(ctx context.Context, _ *gorm.DB, _, _ guid.ID, items []knowledge.ContextItem) error {
		require.EqualValues(t, 11, authn.PrincipalIDFromContext(ctx))
		require.Equal(t, []knowledge.ContextItem{item}, items)
		return knowledge.ErrForbidden
	}
	response := executionRouteRequest(t, handler, http.MethodPost, "/tenants/7/executions", validCreate(), 0, false)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), item.Body)
	for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestHumanExecutionResponsesRecheckCurrentKnowledgeSourcePermission(t *testing.T) {
	for _, endpoint := range []string{"get", "list", "sessions", "cancel", "recover"} {
		t.Run(endpoint, func(t *testing.T) {
			f, handler := executionRoutesFixture(t)
			created := f.create(t)
			plan := created.Plan
			secret := knowledge.ContextItem{EntryID: 501, Version: 1, Label: "[K1]", Title: "Private source title", Body: "Private retained source bytes"}
			plan.Knowledge = []knowledge.ContextItem{secret}
			encoded, err := json.Marshal(plan)
			require.NoError(t, err)
			q := query.BindExecutions(f.db, f.m.names)
			updates := map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)}
			if endpoint == "sessions" {
				updates["state"] = "PAUSED"
			}
			if endpoint == "recover" {
				updates["state"] = "INTERRUPTED"
				require.NoError(t, f.db.Exec("DELETE FROM slots").Error)
			}
			_, err = q.WithContext(t.Context()).Where(q.ID.Eq(created.ID)).Updates(updates)
			require.NoError(t, err)
			denied := false
			calls := 0
			f.m.knowledge.context.AuthorizeSnapshotOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID, items []knowledge.ContextItem) error {
				calls++
				require.EqualValues(t, 11, authn.PrincipalIDFromContext(ctx))
				require.EqualValues(t, 7, tenant)
				require.EqualValues(t, 9, project)
				require.Equal(t, []knowledge.ContextItem{secret}, items)
				if denied {
					return knowledge.ErrForbidden
				}
				return nil
			}
			method, path := http.MethodGet, "/executions/"+created.ID.String()
			var body any
			version := 0
			switch endpoint {
			case "list":
				path = "/tenants/7/executions?project_id=9"
			case "sessions":
				path = "/tenants/7/execution-sessions?project_id=9"
			case "cancel":
				method = http.MethodPut
				path += "/cancellation"
				version = 1
				body = map[string]any{"acknowledge_unknown_result": false}
			case "recover":
				method = http.MethodPut
				path += "/recovery"
				version = 1
				body = map[string]any{"acknowledge_unknown_result": true}
			}
			// GET authorization succeeds with unchanged frozen source bytes.
			if endpoint == "get" || endpoint == "list" || endpoint == "sessions" {
				allowed := executionRouteRequest(t, handler, method, path, body, version, false)
				require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
				require.Contains(t, allowed.Body.String(), secret.Body)
			}
			before, err := q.WithContext(t.Context()).Where(q.ID.Eq(created.ID)).First()
			require.NoError(t, err)
			denied = true
			response := executionRouteRequest(t, handler, method, path, body, version, false)
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), secret.Title)
			require.NotContains(t, response.Body.String(), secret.Body)
			require.Positive(t, calls)
			after, err := q.WithContext(t.Context()).Where(q.ID.Eq(created.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after, "denied view does not mutate immutable plans or commit a control operation")
		})
	}
}

func TestStrategyHumanKnowledgeNeverImpersonatesAgentZero(t *testing.T) {
	f := executionHarness(t)
	f.m.knowledge.context.BuildInjectionOn = func(context.Context, *gorm.DB, knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
		t.Fatal("Human assignment must not invoke Agent automatic retrieval")
		return knowledge.InjectionResult{}, nil
	}
	v := f.create(t)
	require.Zero(t, v.Plan.ExecutorPrincipalID)
	require.Empty(t, v.Plan.Knowledge)
}

func TestDistillationCapabilityRechecksCanonicalSourceAndPreservesOrdinaryCommands(t *testing.T) {
	for _, fault := range []string{"none", "invalid_link", "invalid_operation", "source_forbidden", "source_missing", "source_unavailable", "source_invalid", "source_error", "source_scope", "source_execution", "agent_error", "agent_inactive", "authority", "ordinary_missing"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			parent := f.create(t)
			finishBranchSource(t, f, parent)
			failure := errors.New("canonical source failed")
			if fault == "ordinary_missing" {
				f.m.work = WorkItems{}
			}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "agent_error" {
					return fleet.AgentIdentity{}, false, failure
				}
				return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE"}, fault != "agent_inactive", nil
			}
			s := module.NewServices()
			var runtime knowledge.DistillationRuntime
			require.NoError(t, module.Provide(s, knowledge.DistillationConnector{BindRuntime: func(v knowledge.DistillationRuntime) error { runtime = v; return nil }, CompleteOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}))
			require.NoError(t, module.Provide(s, delivery.SkillDistillationWork{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (knowledge.AcceptedSkillSource, error) {
				switch fault {
				case "source_forbidden":
					return knowledge.AcceptedSkillSource{}, knowledge.ErrForbidden
				case "source_missing":
					return knowledge.AcceptedSkillSource{}, knowledge.ErrNotFound
				case "source_unavailable":
					return knowledge.AcceptedSkillSource{}, knowledge.ErrUnavailable
				case "source_invalid":
					return knowledge.AcceptedSkillSource{}, knowledge.ErrInvalid
				case "source_error":
					return knowledge.AcceptedSkillSource{}, failure
				}
				fact := knowledge.AcceptedSkillSource{ProjectID: parent.ProjectID, ExecutionID: parent.ID}
				if fault == "source_scope" {
					fact.ProjectID++
				}
				if fault == "source_execution" {
					fact.ExecutionID++
				}
				return fact, nil
			}}))
			require.NoError(t, s.Seal())
			require.NoError(t, f.m.bindSkillDistillation(s))
			require.NotNil(t, runtime.PrincipalsOn)
			l := WorkLink{TenantID: 7, ProjectID: parent.ProjectID, WorkItemID: parent.WorkItemID, InitiatorID: 11, Distillation: &DistillationPlan{ID: 901, AgentPrincipalID: 21, SourceExecutionID: parent.ID}}
			op := "CREATE"
			if fault == "invalid_link" {
				l.AssignmentID = 6
			}
			if fault == "invalid_operation" {
				op = "MERGE"
			}
			if fault == "authority" {
				f.authorized = false
			}
			before := f.read(t, parent.ID)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "ordinary_missing" {
					ordinary := WorkLink{}
					_, e := f.m.work.PrincipalsOn(f.human, tx, ordinary)
					require.ErrorIs(t, e, ErrCapabilityUnavailable)
					_, e = f.m.work.AssignedAgentOn(f.human, tx, ordinary)
					require.ErrorIs(t, e, ErrCapabilityUnavailable)
					require.ErrorIs(t, f.m.work.AuthorizeOn(f.human, tx, ordinary, op), ErrCapabilityUnavailable)
					require.ErrorIs(t, f.m.work.StartedOn(f.human, tx, ordinary), ErrCapabilityUnavailable)
					return f.m.work.FinishedOn(f.human, tx, ordinary, WorkResult{})
				}
				ids, e := f.m.work.PrincipalsOn(f.human, tx, l)
				require.NoError(t, e)
				require.Equal(t, []guid.ID{21}, ids)
				agent, e := f.m.work.AssignedAgentOn(f.human, tx, l)
				require.NoError(t, e)
				require.EqualValues(t, 21, agent)
				e = f.m.work.AuthorizeOn(f.human, tx, l, op)
				if fault == "none" {
					require.NoError(t, e)
					require.NoError(t, f.m.work.StartedOn(f.human, tx, l))
					require.ErrorIs(t, f.m.work.FinishedOn(f.human, tx, l, WorkResult{State: "FAILED"}), ErrCapabilityUnavailable)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, f.read(t, parent.ID))
		})
	}
}

func TestSkillDistillationParticipantDiscoveryRejectsMissingCurrentRuntime(t *testing.T) {
	for _, fault := range []string{"none", "capability", "runtime_error", "runtime_absent", "machine_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			failure := errors.New("distillation runtime unavailable")
			switch fault {
			case "capability":
				f.m.agentRuntime.ByPrincipalOn = nil
			case "runtime_error":
				f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					return fleet.AgentRuntimeFact{}, false, failure
				}
			case "runtime_absent":
				f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					return fleet.AgentRuntimeFact{}, false, nil
				}
			case "machine_error":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				ids, e := f.m.skillDistillationPrincipalsOn(f.human, tx, 7, 21)
				if fault == "none" {
					require.Equal(t, []guid.ID{21, 101}, ids)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMemoryBindingsVerifyActualTerminalReceiptAndCurrentSource(t *testing.T) {
	for _, fault := range []string{"none", "projection_invalid", "projection_bind_error", "memory_invalid", "memory_bind_error", "source_missing", "source_invalid", "source_error", "provenance"} {
		t.Run(fault, func(t *testing.T) {
			f, item := executionKnowledgeFixture(t)
			v := f.create(t)
			finishBranchSource(t, f, v)
			v = f.read(t, v.ID)
			now := time.Now().UTC().Add(time.Second)
			project := v.ProjectID
			from, to := v.CreatedAt.Add(-time.Hour), now.Add(time.Hour)
			s := module.NewServices()
			failure := errors.New("memory source unavailable")
			var projection knowledge.RuntimeMetricProjection
			var evidence knowledge.MemoryExecutionEvidence
			pc := knowledge.RuntimeMetricProjectionConnector{Bind: func(v knowledge.RuntimeMetricProjection) error {
				projection = v
				if fault == "projection_bind_error" {
					return failure
				}
				return nil
			}}
			if fault == "projection_invalid" {
				pc.Bind = nil
			}
			require.NoError(t, module.Provide(s, pc))
			mc := knowledge.MemoryExecutionEvidenceConnector{Bind: func(v knowledge.MemoryExecutionEvidence) error {
				evidence = v
				if fault == "memory_bind_error" {
					return failure
				}
				return nil
			}}
			if fault == "memory_invalid" {
				mc.Bind = nil
			}
			require.NoError(t, module.Provide(s, mc))
			source := delivery.MemoryWorkSource{ReadOn: func(_ context.Context, tx *gorm.DB, tenant, gotProject, work guid.ID) (string, error) {
				require.EqualValues(t, 7, tenant)
				require.Equal(t, project, gotProject)
				require.Equal(t, v.WorkItemID, work)
				if fault == "source_error" {
					return "", failure
				}
				return "Canonical accepted source", nil
			}, DecisionsOn: func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]delivery.KnowledgeDecisionFact, error) {
				if fault == "source_error" {
					return nil, failure
				}
				return nil, nil
			}}
			if fault == "source_invalid" {
				source = delivery.MemoryWorkSource{}
			}
			if fault != "source_missing" {
				require.NoError(t, module.Provide(s, source))
			}
			require.NoError(t, s.Seal())
			err := f.m.bindMemory(s)
			if fault == "projection_invalid" || fault == "memory_invalid" {
				require.ErrorIs(t, err, ErrCapabilityUnavailable)
				return
			}
			if fault == "projection_bind_error" || fault == "memory_bind_error" {
				require.ErrorIs(t, err, failure)
				return
			}
			require.NoError(t, err)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				items, e := projection.ReadOn(f.human, tx, 7, &project, from, to)
				if fault == "source_missing" || fault == "source_invalid" {
					require.ErrorIs(t, e, knowledge.ErrUnavailable)
				} else if fault == "source_error" {
					require.ErrorIs(t, e, failure)
				} else {
					require.NoError(t, e)
					require.Len(t, items, 1)
					require.Equal(t, "INJECTION", items[0].Kind)
					require.True(t, items[0].Hit)
				}
				agent := guid.ID(21)
				if fault == "provenance" {
					agent++
				}
				content, e := evidence.VerifyOn(f.human, tx, 7, agent, &project, v.ID, now)
				switch fault {
				case "source_missing", "source_invalid":
					require.ErrorIs(t, e, knowledge.ErrUnavailable)
				case "source_error":
					require.ErrorIs(t, e, failure)
				case "provenance":
					require.ErrorIs(t, e, knowledge.ErrMemoryProvenance)
				default:
					require.NoError(t, e)
					require.Equal(t, "Canonical accepted source", content)
				}
				return nil
			}))
			require.Equal(t, []knowledge.ContextItem{item}, v.Plan.Knowledge)
		})
	}
}

func TestKnowledgeViewConnectorMapsCurrentSourceFailuresWithoutReturningPins(t *testing.T) {
	for _, fault := range []string{"none", "missing", "forbidden", "capability", "hash", "plan", "query"} {
		t.Run(fault, func(t *testing.T) {
			f, _ := executionKnowledgeFixture(t)
			v := f.create(t)
			s := module.NewServices()
			var read func(context.Context, *gorm.DB, guid.ID, guid.ID) (knowledge.ExecutionKnowledgeSnapshot, error)
			require.NoError(t, module.Provide(s, knowledge.ExecutionKnowledgeViewConnector{BindExecution: func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID) (knowledge.ExecutionKnowledgeSnapshot, error)) error {
				read = fn
				return nil
			}}))
			require.NoError(t, s.Seal())
			require.NoError(t, f.m.bindKnowledgeView(s))
			id := v.ID
			want := error(nil)
			failure := errors.New("knowledge view storage unavailable")
			switch fault {
			case "missing":
				id++
				want = knowledge.ErrNotFound
			case "forbidden":
				f.authorized = false
				want = knowledge.ErrForbidden
			case "capability":
				f.m.policy = nil
				want = knowledge.ErrUnavailable
			case "hash":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("plan_hash", "tampered").Error)
				want = knowledge.ErrUnavailable
			case "plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("plan_json", "{").Error)
			case "query":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("knowledge_view_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
				want = failure
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				out, e := read(f.human, tx, 7, id)
				if fault == "none" {
					require.Equal(t, v.Plan.Knowledge, out.Items)
					require.Equal(t, v.ProjectID, out.ProjectID)
				} else {
					require.Empty(t, out.Items)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, err)
			} else if want != nil {
				require.ErrorIs(t, err, want)
			} else {
				require.Error(t, err)
			}
			if fault == "query" {
				require.NoError(t, f.db.Callback().Query().Remove("knowledge_view_failure"))
			}
		})
	}
}

func TestBindSkillDistillationRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindSkillDistillation(module.NewServices()), module.ErrServicesProviding)
}

func TestBindRetirementPrecheckRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindRetirementPrecheck(module.NewServices()), module.ErrServicesProviding)
}

func TestBindInteractionInboxRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindInteractionInbox(module.NewServices()), module.ErrServicesProviding)
}

func TestRegisterInteractionExpiryRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.registerInteractionExpiry(module.NewServices()), module.ErrServicesProviding)
}

func TestBindToolRelayRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindToolRelay(module.NewServices()), module.ErrServicesProviding)
}

func TestBindMemoryRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindMemory(module.NewServices()), module.ErrServicesProviding)
}

func TestBindKnowledgeViewRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindKnowledgeView(module.NewServices()), module.ErrServicesProviding)
}

func TestExecutionInboxProjectionRejectsUnavailableCurrentDependencies(t *testing.T) {
	for _, fault := range []string{"none", "agent_missing", "agent_error", "policy_missing", "policy_error", "policy_forbidden", "head_missing", "head_error", "inbox_error", "plan_hash", "plan_json"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			q := query.BindExecutions(f.db, f.m.names)
			r, e := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).First()
			require.NoError(t, e)
			var plan ExecutionPlan
			require.NoError(t, json.Unmarshal([]byte(r.PlanJSON), &plan))
			plan.ExecutorPrincipalID = 21
			raw, e := json.Marshal(plan)
			require.NoError(t, e)
			r.PlanJSON = string(raw)
			r.PlanHash = hash(raw)
			failure := errors.New("projection dependency unavailable")
			appended := 0
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "agent_error" {
					return fleet.AgentIdentity{}, false, failure
				}
				return fleet.AgentIdentity{TenantID: 7, PrincipalID: 21, State: "ACTIVE"}, true, nil
			}
			f.m.commentContext.HeadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) {
				if fault == "head_error" {
					return 0, failure
				}
				return 8, nil
			}
			if fault == "agent_missing" {
				f.m.agents.ByPrincipalOn = nil
			}
			if fault == "policy_missing" {
				f.m.policy = nil
			}
			if fault == "policy_error" || fault == "policy_forbidden" {
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if fault == "policy_forbidden" {
						return authz.DataConstraint{}, ErrForbidden
					}
					return authz.DataConstraint{}, failure
				}
			}
			if fault == "head_missing" {
				f.m.commentContext.HeadOn = nil
			}
			if fault == "plan_hash" {
				r.PlanHash = "changed"
			}
			if fault == "plan_json" {
				r.PlanJSON = "{"
				r.PlanHash = hash([]byte(r.PlanJSON))
			}
			publisher := interactionPublisher{m: f.m, inbox: agents.InboxAppender{AppendOn: func(_ context.Context, _ *gorm.DB, tenant, target guid.ID, ref agents.InboxReference) (int64, error) {
				appended++
				require.EqualValues(t, 7, tenant)
				require.EqualValues(t, 21, target)
				require.Equal(t, "work_item:3", ref.StreamKey)
				require.EqualValues(t, 8, ref.StreamSeq)
				if fault == "inbox_error" {
					return 0, failure
				}
				return 1, nil
			}}}
			e = f.db.Transaction(func(tx *gorm.DB) error {
				return publisher.projectExecutionInboxOn(f.human, tx, r, 11, agents.InboxReference{Kind: "EXECUTION", SubjectID: v.ID})
			})
			if fault == "none" {
				require.NoError(t, e)
				require.Equal(t, 1, appended)
			} else if fault == "policy_forbidden" {
				require.NoError(t, e)
				require.Zero(t, appended)
			} else {
				require.Error(t, e)
				if fault != "inbox_error" {
					require.Zero(t, appended)
				}
			}
		})
	}
}
func TestDistillationErrorMappingPreservesCanonicalProviderClasses(t *testing.T) {
	for _, pair := range [][2]error{{ErrForbidden, knowledge.ErrForbidden}, {ErrNotFound, knowledge.ErrNotFound}, {ErrInvalidArgument, knowledge.ErrInvalid}, {ErrConflict, knowledge.ErrConflict}, {ErrBudgetExceeded, knowledge.ErrConflict}, {ErrUnavailable, knowledge.ErrUnavailable}, {ErrCapabilityUnavailable, knowledge.ErrUnavailable}} {
		require.ErrorIs(t, distillationKnowledgeError(pair[0]), pair[1])
	}
}

func TestSkillDistillationLaunchRejectsChangedCurrentAgentAndSource(t *testing.T) {
	for _, fault := range []string{"invalid", "missing", "source_state", "source_scope", "source_json", "agent_error", "agent_missing", "agent_role", "runtime_error", "runtime_missing", "runtime_machine", "runtime_kind"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "SUCCEEDED").Error)
			in := knowledge.DistillationLaunch{DistillationID: 901, ProjectID: 9, WorkItemID: 3, AgentPrincipalID: 21, SourceExecutionID: v.ID, HeadCommit: strings.Repeat("a", 40), Objective: "Generate frozen guidance"}
			failure := errors.New("current runtime failed")
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "agent_error" {
					return fleet.AgentIdentity{}, false, failure
				}
				roles := []string{"EXECUTOR"}
				if fault == "agent_role" {
					roles = nil
				}
				return fleet.AgentIdentity{State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", Roles: roles}, fault != "agent_missing", nil
			}
			f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
				if fault == "runtime_error" {
					return fleet.AgentRuntimeFact{}, false, failure
				}
				machine := guid.ID(1)
				r := fleet.AgentRuntimeFact{MachineID: &machine, RuntimeKind: "CODEX"}
				if fault == "runtime_machine" {
					r.MachineID = nil
				}
				if fault == "runtime_kind" {
					r.RuntimeKind = "OTHER"
				}
				return r, fault != "runtime_missing", nil
			}
			switch fault {
			case "invalid":
				in.DistillationID = 0
			case "missing":
				in.SourceExecutionID = 999
			case "source_state":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "RUNNING").Error)
			case "source_scope":
				in.WorkItemID = 99
			case "source_json":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{").Error)
			}
			before := int64(0)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&before).Error)
			err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.launchSkillDistillationOn(f.human, tx, 7, in); return e })
			require.Error(t, err)
			var after int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}
func TestInteractionExpiryCurrentPrincipalAndStorageFailuresNeverWakeWrongAgent(t *testing.T) {
	for _, fault := range []string{"none", "no_executor", "lock_missing", "lock_error", "query_1", "query_2", "query_3", "query_4", "query_5"} {
		t.Run(fault, func(t *testing.T) {
			f, v, input := approvalFixture(t)
			approval := requestApproval(t, f, v, input)
			q := query.BindExecutions(f.db, f.m.names)
			r, e := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).First()
			require.NoError(t, e)
			var plan ExecutionPlan
			require.NoError(t, json.Unmarshal([]byte(r.PlanJSON), &plan))
			plan.ExecutorPrincipalID = 21
			if fault == "no_executor" {
				plan.ExecutorPrincipalID = 0
			}
			raw, e := json.Marshal(plan)
			require.NoError(t, e)
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
			require.NoError(t, f.db.Table(f.m.names.Approvals.String()).Where("id=?", approval.ID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error)
			failure := errors.New("expiry storage unavailable")
			if fault == "lock_missing" {
				f.m.principals.Lock = nil
			}
			if fault == "lock_error" {
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{PrincipalID: 21, TenantID: 7, State: "ACTIVE"}, true, nil
			}
			appended := 0
			f.m.events = interactionPublisher{m: f.m, Publisher: f.m.events, inbox: agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
				appended++
				return 1, nil
			}}}
			at := 0
			if strings.HasPrefix(fault, "query_") {
				_, e = fmt.Sscanf(fault, "query_%d", &at)
				require.NoError(t, e)
			}
			calls := 0
			const callback = "expiry_query_failure"
			require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				calls++
				if calls == at {
					tx.AddError(failure)
				}
			}))
			e = f.m.expireInteractions(f.human, f.db, 7, time.Now().UTC())
			require.NoError(t, f.db.Callback().Query().Remove(callback))
			if fault == "none" {
				require.NoError(t, e)
				require.Equal(t, 1, appended)
			} else if fault == "no_executor" {
				require.NoError(t, e)
				require.Zero(t, appended)
			} else {
				require.Error(t, e)
				if at < 5 {
					require.Zero(t, appended)
				}
			}
		})
	}
}

func TestArtifactCommentProjectionReadsExactMessageAndRejectsForgedSource(t *testing.T) {
	for _, fault := range []string{"none", "json", "marshal", "thread", "missing", "message", "message_missing", "actor", "scope", "plan"} {
		t.Run(fault, func(t *testing.T) {
			f, _, thread := replyFixture(t)
			var r model.ArtifactCommentThread
			require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id=?", thread).Take(&r).Error)
			event := events.Event{TenantID: 7, ProjectID: 9, Subject: events.Subject{Kind: "execution", ID: r.ExecutionID}, Actor: events.Actor{ID: 11}, Data: map[string]any{"thread_id": thread.String(), "message_id": "703", "thread_seq": int64(1)}}
			switch fault {
			case "json":
				event.Data = "invalid"
			case "marshal":
				event.Data = make(chan int)
			case "thread":
				event.Data = map[string]any{"thread_id": "invalid"}
			case "missing":
				event.Subject.ID = 999
			case "message":
				event.Data = map[string]any{"thread_id": thread.String(), "message_id": "invalid"}
			case "message_missing":
				event.Data = map[string]any{"thread_id": thread.String(), "message_id": "999", "thread_seq": 1}
			case "actor":
				event.Actor.ID = 12
			case "scope":
				require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id=?", thread).Update("work_item_id", 99).Error)
			case "plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", r.ExecutionID).Update("plan_json", "{").Error)
			}
			p := interactionPublisher{m: f.m, inbox: agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
				t.Fatal("fixture has no executor recipient")
				return 0, nil
			}}}
			e := f.db.Transaction(func(tx *gorm.DB) error { return p.projectArtifactCommentOn(f.human, tx, event, 88) })
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}

func TestRetirementConnectorPreservesDomainErrorsAndRequiresBinding(t *testing.T) {
	for _, fault := range []string{"missing_bind", "bind_error", "invalid", "principal", "forbidden", "unavailable", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("retirement dependency failed")
			var read func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]agents.RetirementExecution, error)
			connector := agents.RetirementExecutionConnector{Bind: func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]agents.RetirementExecution, error)) error {
				read = fn
				if fault == "bind_error" {
					return failure
				}
				return nil
			}}
			if fault == "missing_bind" {
				connector.Bind = nil
			}
			require.NoError(t, module.Provide(s, connector))
			require.NoError(t, s.Seal())
			e := f.m.bindRetirementPrecheck(s)
			if fault == "missing_bind" {
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
				return
			}
			if fault == "bind_error" {
				require.ErrorIs(t, e, failure)
				return
			}
			require.NoError(t, e)
			switch fault {
			case "principal":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "forbidden":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrForbidden }
			case "unavailable":
				f.m.principals.Lock = nil
			}
			e = f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "invalid" {
					tx = f.db
				}
				items, e := read(f.human, tx, 7, 21)
				require.Empty(t, items)
				return e
			})
			switch fault {
			case "none":
				require.NoError(t, e)
			case "invalid":
				require.ErrorIs(t, e, ErrInvalidArgument)
			case "principal":
				require.ErrorIs(t, e, failure)
			case "forbidden":
				require.ErrorIs(t, e, agents.ErrForbidden)
			case "unavailable":
				require.ErrorIs(t, e, agents.ErrUnavailable)
			}
		})
	}
}

func TestDistillationBindingPreservesOrdinaryWorkAndCanonicalReadErrors(t *testing.T) {
	for _, fault := range []string{"none", "work_missing", "work_invalid", "runtime_invalid", "complete_invalid", "bind_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			calls := 0
			failure := errors.New("ordinary work failed")
			f.m.work = WorkItems{
				PrincipalsOn:    func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { calls++; return nil, failure },
				AssignedAgentOn: func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { calls++; return 0, failure },
				AuthorizeOn:     func(context.Context, *gorm.DB, WorkLink, string) error { calls++; return failure },
				StartedOn:       func(context.Context, *gorm.DB, WorkLink) error { calls++; return failure },
				FinishedOn:      func(context.Context, *gorm.DB, WorkLink, WorkResult) error { calls++; return failure },
			}
			s := module.NewServices()
			var runtime knowledge.DistillationRuntime
			connector := knowledge.DistillationConnector{BindRuntime: func(v knowledge.DistillationRuntime) error {
				runtime = v
				if fault == "bind_error" {
					return failure
				}
				return nil
			}, CompleteOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }}
			if fault == "runtime_invalid" {
				connector.BindRuntime = nil
			}
			if fault == "complete_invalid" {
				connector.CompleteOn = nil
			}
			require.NoError(t, module.Provide(s, connector))
			work := delivery.SkillDistillationWork{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (knowledge.AcceptedSkillSource, error) {
				return knowledge.AcceptedSkillSource{}, failure
			}}
			if fault == "work_invalid" {
				work.ReadOn = nil
			}
			if fault != "work_missing" {
				require.NoError(t, module.Provide(s, work))
			}
			require.NoError(t, s.Seal())
			e := f.m.bindSkillDistillation(s)
			if fault == "work_missing" {
				require.ErrorIs(t, e, module.ErrServiceMissing)
				return
			}
			if fault == "work_invalid" || fault == "runtime_invalid" || fault == "complete_invalid" {
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
				return
			}
			if fault == "bind_error" {
				require.ErrorIs(t, e, failure)
				return
			}
			require.NoError(t, e)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.work.PrincipalsOn(f.human, tx, WorkLink{})
				require.ErrorIs(t, e, failure)
				_, e = f.m.work.AssignedAgentOn(f.human, tx, WorkLink{})
				require.ErrorIs(t, e, failure)
				require.ErrorIs(t, f.m.work.AuthorizeOn(f.human, tx, WorkLink{}, "CREATE"), failure)
				require.ErrorIs(t, f.m.work.StartedOn(f.human, tx, WorkLink{}), failure)
				require.ErrorIs(t, f.m.work.FinishedOn(f.human, tx, WorkLink{}, WorkResult{}), failure)
				_, e = runtime.ReadOn(f.human, tx, 7, 999)
				require.ErrorIs(t, e, knowledge.ErrNotFound)
				f.m.agentRuntime.ByPrincipalOn = nil
				_, e = runtime.PrincipalsOn(f.human, tx, 7, 21)
				require.ErrorIs(t, e, knowledge.ErrUnavailable)
				return nil
			}))
			require.Equal(t, 5, calls)
		})
	}
}

func TestAutomaticKnowledgeAssemblyFailsClosedOnCurrentSourceErrors(t *testing.T) {
	for _, fault := range []string{"work_missing", "work_error", "foreign_ref", "resolver_missing", "resolver_error", "injection_error", "snapshot_missing", "snapshot_error"} {
		t.Run(fault, func(t *testing.T) {
			f, item := executionKnowledgeFixture(t)
			f.m.knowledge.context.BuildInjectionOn = func(context.Context, *gorm.DB, knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
				return knowledge.InjectionResult{Items: []knowledge.InjectionItem{{ContextItem: item}}}, nil
			}
			f.m.knowledge.context.AuthorizeSnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []knowledge.ContextItem) error { return nil }
			failure := errors.New("knowledge source unavailable")
			expected := ErrCapabilityUnavailable
			in := validCreate()
			switch fault {
			case "work_missing":
				f.m.knowledge.work.ReadOn = nil
			case "work_error":
				f.m.knowledge.work.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
				expected = failure
			case "foreign_ref":
				in.Plan.ContextRefs = []ContextRef{{Kind: "KNOWLEDGE", Ref: 91}}
				expected = ErrInvalidArgument
			case "resolver_missing":
				f.m.knowledge.context.ResolveOn = nil
			case "resolver_error":
				f.m.knowledge.context.ResolveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]knowledge.ContextItem, error) {
					return nil, failure
				}
				expected = failure
			case "injection_error":
				f.m.knowledge.context.BuildInjectionOn = func(context.Context, *gorm.DB, knowledge.InjectionRequest) (knowledge.InjectionResult, error) {
					return knowledge.InjectionResult{}, failure
				}
				expected = failure
			case "snapshot_missing":
				f.m.knowledge.context.AuthorizeSnapshotOn = nil
			case "snapshot_error":
				f.m.knowledge.context.AuthorizeSnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []knowledge.ContextItem) error { return failure }
				expected = failure
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, in); return e }), expected)
			var n int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestInteractionPublisherRejectsMalformedOrUncommittedTerminalFacts(t *testing.T) {
	for _, fault := range []string{"publisher", "unrelated", "marshal", "decode", "execution", "approval_id", "approval_missing", "approval_state", "evidence_execution"} {
		t.Run(fault, func(t *testing.T) {
			f, v, input := approvalFixture(t)
			approval := requestApproval(t, f, v, input)
			publisher := interactionPublisher{Publisher: &testPublisher{}, m: f.m, inbox: agents.InboxAppender{AppendOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, agents.InboxReference) (int64, error) {
				t.Fatal("invalid terminal fact cannot wake an agent")
				return 0, nil
			}}}
			event := events.Event{Type: "execution.state_changed", TenantID: 7, ProjectID: 9, Subject: events.Subject{Kind: "execution", ID: v.ID}, Actor: events.Actor{ID: 11}, Data: map[string]any{"approval_id": approval.ID.String(), "approval_state": "ALLOW"}}
			switch fault {
			case "publisher":
				publisher.Publisher = &testPublisher{fail: true}
			case "unrelated":
				event.Type = "execution.other"
			case "marshal":
				event.Data = func() {}
			case "decode":
				event.Data = []string{"invalid event payload"}
			case "execution":
				event.Subject.ID++
			case "approval_id":
				event.Data = map[string]any{"approval_id": "bad", "approval_state": "ALLOW"}
			case "approval_missing":
				event.Data = map[string]any{"approval_id": (approval.ID + 1).String(), "approval_state": "ALLOW"}
			case "evidence_execution":
				event.Type = "execution.evidence_recorded"
				event.Subject.ID++
			}
			var before int64
			require.NoError(t, f.db.Table("effects").Count(&before).Error)
			e := f.db.Transaction(func(tx *gorm.DB) error { _, e := publisher.Publish(f.human, tx, event); return e })
			if fault == "unrelated" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
				var after int64
				require.NoError(t, f.db.Table("effects").Count(&after).Error)
				require.Equal(t, before, after)
			}
		})
	}
}

func TestInteractionPublisherStorageFailuresRollBackCanonicalDecisionAndWake(t *testing.T) {
	for _, command := range []string{"decision", "cancel", "evidence"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, v, input := approvalFixture(t)
				approval := requestApproval(t, f, v, input)
				v = f.read(t, v.ID)
				v.Plan.ExecutorPrincipalID = 21
				raw, e := json.Marshal(v.Plan)
				require.NoError(t, e)
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
				f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{TenantID: 7, PrincipalID: 21, State: "ACTIVE"}, true, nil
				}
				require.NoError(t, f.db.Exec("CREATE TABLE inbox_commit_probe (kind TEXT, subject BIGINT)").Error)
				f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
				f.m.events = interactionPublisher{Publisher: f.m.events, m: f.m, inbox: agents.InboxAppender{AppendOn: func(_ context.Context, tx *gorm.DB, tenant, agent guid.ID, r agents.InboxReference) (int64, error) {
					require.EqualValues(t, 7, tenant)
					require.EqualValues(t, 21, agent)
					return 1, tx.Exec("INSERT INTO inbox_commit_probe VALUES (?,?)", r.Kind, r.SubjectID).Error
				}}}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "decision":
						out, e := f.m.DecideApprovalOn(f.human, tx, 7, v.ID, approval.ID, "DENY", "Current command denied")
						if e == nil {
							require.Equal(t, "DENY", out.State)
						}
						return e
					case "cancel":
						_, e := f.m.CancelOn(f.human, tx, 7, v.ID, v.Version)
						return e
					default:
						_, e := f.m.CreateEvidenceOn(f.human, tx, 7, v.ID, EvidenceCreate{Title: "Actual observation", EvidenceType: "OBSERVATION", ScopeText: "Current state", Text: "Observed permission request"})
						return e
					}
				}
			})
		})
	}
}

func TestToolRelayRequiresAuthenticRunningExecutorAndCurrentWorkAuthority(t *testing.T) {
	for _, fault := range []string{"none", "id", "missing", "executor", "json", "authorize_missing", "authorize_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			v = f.read(t, v.ID)
			v.Plan.ExecutorPrincipalID = 21
			raw, e := json.Marshal(v.Plan)
			require.NoError(t, e)
			if fault == "executor" {
				v.Plan.ExecutorPrincipalID = 0
				raw, e = json.Marshal(v.Plan)
				require.NoError(t, e)
			}
			if fault == "json" {
				raw = []byte("{")
			}
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
			if fault == "authorize_missing" {
				f.m.work.AuthorizeOn = nil
			}
			if fault == "authorize_error" {
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrForbidden }
			}
			id := v.ID
			if fault == "id" {
				id = 0
			}
			if fault == "missing" {
				id++
			}
			e = f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.toolRelayExecutionOn(f.human, tx, 7, id)
				if fault == "none" && e == nil {
					require.EqualValues(t, 21, out.AgentID)
					require.EqualValues(t, 1, out.DispatchEpoch)
					require.Equal(t, hash(raw), out.PlanHash)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, e)
			} else if fault == "id" {
				require.ErrorIs(t, e, ErrInvalidArgument)
			} else if fault == "authorize_missing" {
				require.ErrorIs(t, e, agents.ErrUnavailable)
			} else {
				require.ErrorIs(t, e, agents.ErrForbidden)
			}
		})
	}
}

func TestExecutionKnowledgeViewRejectsForgedFeedbackAndIgnoresMalformedLegacyContext(t *testing.T) {
	for _, fault := range []string{"legacy", "too_many", "source", "hash", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			feedback := []map[string]any{{"source_decision_id": 77, "detail": "Current feedback"}}
			if fault == "too_many" {
				for range 100 {
					feedback = append(feedback, feedback[0])
				}
			}
			if fault == "source" {
				feedback[0]["source_decision_id"] = 78
			}
			raw, e := json.Marshal(map[string]any{"feedback": feedback})
			require.NoError(t, e)
			if fault == "legacy" {
				raw = []byte("{")
			}
			var parent model.Execution
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).First(&parent).Error)
			parent.ID += 1000
			parent.State = "SUCCEEDED"
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&parent).Error)
			updates := map[string]any{"source_decision_id": 77, "parent_execution_id": parent.ID, "feedback_snapshot": string(raw)}
			if fault == "hash" {
				updates["plan_hash"] = "tampered"
			}
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(updates).Error)
			e = f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.executionKnowledgeViewOn(f.human, tx, 7, v.ID)
				if e == nil {
					if fault == "legacy" {
						require.Empty(t, out.Feedback)
					} else {
						require.Len(t, out.Feedback, 1)
						require.False(t, out.Feedback[0].Cited)
					}
				}
				return e
			})
			switch fault {
			case "legacy", "none":
				require.NoError(t, e)
			case "too_many":
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
			case "hash":
				require.ErrorIs(t, e, ErrUnavailable)
			default:
				require.ErrorIs(t, e, ErrConflict)
			}
		})
	}
}

func TestToolRelayBindingRequiresBothCallbacksAndPropagatesPinFailures(t *testing.T) {
	for _, fault := range []string{"bind_missing", "pins_missing", "bind_error", "pins_error", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("tool relay source failed")
			bound := false
			connector := agents.ToolRelayExecutionConnector{Bind: func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.RelayExecution, error)) error {
				require.NotNil(t, fn)
				bound = true
				if fault == "bind_error" {
					return failure
				}
				return nil
			}, PinsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]agents.RelayToolPin, error) {
				if fault == "pins_error" {
					return nil, failure
				}
				return nil, nil
			}}
			if fault == "bind_missing" {
				connector.Bind = nil
			}
			if fault == "pins_missing" {
				connector.PinsOn = nil
			}
			require.NoError(t, module.Provide(s, connector))
			require.NoError(t, s.Seal())
			e := f.m.bindToolRelay(s)
			if fault == "bind_missing" || fault == "pins_missing" {
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
				return
			}
			if fault == "bind_error" {
				require.ErrorIs(t, e, failure)
				return
			}
			require.NoError(t, e)
			require.True(t, bound)
			e = f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.pinAgentToolsOn(f.human, tx, &model.Execution{TenantID: 7}, &ExecutionPlan{ExecutorPrincipalID: 21})
			})
			if fault == "pins_error" {
				require.ErrorIs(t, e, failure)
			} else {
				require.NoError(t, e)
			}
		})
	}
}

func TestHubAndKnowledgeMetricReadStorageFailuresAreExplicit(t *testing.T) {
	for _, command := range []string{"hub", "metrics", "knowledge"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				v := f.create(t)
				f.pull(t, v.ID)
				_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, e)
				project := guid.ID(9)
				now := time.Now().UTC()
				decisions := []delivery.KnowledgeDecisionFact{{ExecutionID: v.ID, ProjectID: 9, Kind: "ACCEPT"}}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "hub":
						out, e := f.m.hubSessionsOn(f.human, tx, 7, 9, 0, 50)
						if e == nil {
							require.Len(t, out.Items, 1)
						}
						return e
					case "metrics":
						_, e := f.m.knowledgeMetricProjectionOn(f.human, tx, 7, &project, now.Add(-time.Hour), now.Add(time.Hour), decisions)
						return e
					default:
						out, e := f.m.executionKnowledgeViewOn(f.human, tx, 7, v.ID)
						if e == nil {
							require.EqualValues(t, 9, out.ProjectID)
						}
						return e
					}
				}
			})
		})
	}
}
