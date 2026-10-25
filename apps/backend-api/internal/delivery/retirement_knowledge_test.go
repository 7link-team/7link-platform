package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestAcceptedSkillSourceFailsClosedAtEveryPersistedRead(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 12)
	require.NoError(t, e)
	h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("source_execution_id", 401).Error)
	injected := errors.New("accepted source unavailable")
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("distillation-read-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("distillation-read-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.acceptedSkillSourceOn(h.ctx, tx, 1, h.work); return e })
	}
	require.NoError(t, read())
	total := reads
	require.Greater(t, total, 6)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.ErrorIs(t, read(), injected, "accepted source read %d", i)
	}
	failAt = 0
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("source_execution_id", nil).Error)
	require.ErrorIs(t, read(), knowledge.ErrUnavailable)
	_, e = h.m.acceptedSkillSourceOn(h.ctx, h.db, 1, h.work)
	require.ErrorIs(t, e, knowledge.ErrInvalid)
}

func TestKnowledgeConnectorsExposeOnlyCanonicalResultAndDecision(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	var reader knowledge.ResultExecutionReader
	var decision func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (knowledge.FeedbackDecisionMetadata, error)
	s := module.NewServices()
	require.NoError(t, module.Provide(s, knowledge.DistillationConnector{BindSource: func(v knowledge.DistillationSource) error { require.NotNil(t, v.ReadOn); return nil }}))
	require.NoError(t, module.Provide(s, knowledge.ResultExecutionConnector{Bind: func(v knowledge.ResultExecutionReader) error { reader = v; return nil }}))
	require.NoError(t, module.Provide(s, knowledge.ExecutionKnowledgeViewConnector{BindDecision: func(v func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (knowledge.FeedbackDecisionMetadata, error)) error {
		decision = v
		return nil
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, h.m.bindKnowledgeResults(s))
	read := func() (id guid.ID, err error) {
		err = h.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = reader.ReadOn(h.ctx, tx, 1, result.ID); return e })
		return
	}
	id, e := read()
	require.NoError(t, e)
	require.Zero(t, id)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Update("source_execution_id", 401).Error)
	id, e = read()
	require.NoError(t, e)
	require.EqualValues(t, 401, id)
	actions, e := query.BindResultActions(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, e)
	require.NotEmpty(t, actions)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		metadata, e := decision(h.ctx, tx, 1, 99, h.work, actions[0].ID)
		require.NoError(t, e)
		require.Equal(t, actions[0].ActorID, metadata.ActorID)
		_, e = decision(h.ctx, tx, 1, 99, h.work, 999)
		require.ErrorIs(t, e, knowledge.ErrNotFound)
		return nil
	}))
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	_, e = read()
	require.ErrorIs(t, e, ErrWorkForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := decision(h.ctx, tx, 1, 99, h.work, actions[0].ID)
		require.ErrorIs(t, e, knowledge.ErrForbidden)
		return nil
	}))
}

func TestRetirementManifestRejectsOversizedOwnershipInsteadOfTruncating(t *testing.T) {
	h := newResultFixture(t)
	base, e := query.BindWorkItems(h.db, h.m.service.names).WithContext(h.ctx).Take()
	require.NoError(t, e)
	assignment, e := query.BindAssignments(h.db, h.m.service.names).WithContext(h.ctx).Take()
	require.NoError(t, e)
	works := make([]model.WorkItem, 1001)
	assignments := make([]model.Assignment, 1001)
	for i := range works {
		w := *base
		w.ID = guid.ID(20000 + i)
		w.Number = 20000 + i
		works[i] = w
		a := *assignment
		a.ID = guid.ID(40000 + i)
		a.WorkItemID = w.ID
		slot := w.ID
		a.ActiveSlot = &slot
		assignments[i] = a
	}
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).CreateInBatches(works, 25).Error)
	require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).CreateInBatches(assignments, 50).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.retirementPrincipalIDsOn(h.ctx, tx, 1, assignment.AssigneePrincipalID)
		require.ErrorIs(t, e, ErrUnavailable)
		_, e = h.m.retirementAssignmentsOn(h.ctx, tx, 1, assignment.AssigneePrincipalID)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

func TestAgentInboxProjectionRechecksEveryCurrentSourceBeforePublishing(t *testing.T) {
	for _, fault := range []string{"ok", "agent_missing", "agent_error", "policy_missing", "policy_error", "denied", "hidden", "scope", "self", "human", "result_missing", "wrong_work", "marshal", "decode", "read"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			r, e := h.submit()
			require.NoError(t, e)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("actor_id", 21).Error)
			injected := errors.New("inbox source unavailable")
			want := error(nil)
			called := 0
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 121, PrincipalID: 21, State: "ACTIVE"}, true, nil
			}
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			source := events.Event{Type: "work_item.state_changed", TenantID: 1, ProjectID: 99, Subject: events.Subject{ID: h.work}, Data: map[string]string{"reason_code": "RESULT_ACCEPTED", "result_id": r.ID.String()}}
			switch fault {
			case "agent_missing":
				h.m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			case "agent_error":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
				want = injected
			case "policy_missing":
				h.m.workPolicy = nil
				want = ErrUnavailable
			case "policy_error":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
				want = injected
			case "denied":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "hidden":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
				}
			case "scope":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			case "self":
				source.Actor = events.Actor{Kind: "AGENT", ID: 21}
			case "human":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
			case "result_missing":
				source.Data = map[string]string{"reason_code": "RESULT_ACCEPTED", "result_id": "999"}
				want = gorm.ErrRecordNotFound
			case "wrong_work":
				source.Subject.ID = 999
				want = ErrInvalidArgument
			case "marshal":
				source.Data = func() {}
			case "decode":
				source.Data = []string{"invalid"}
			case "read":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("inbox-read-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.service.names.WorkItems.String() {
						tx.AddError(injected)
					}
				}))
				want = injected
			}
			e = h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.projectAgentInboxOn(h.ctx, tx, source, 901, func(_ context.Context, _ *gorm.DB, ref AgentInboxReference) error {
					called++
					require.Equal(t, r.ID, ref.SubjectID)
					require.EqualValues(t, 21, ref.PrincipalID)
					return nil
				})
			})
			if fault == "scope" || fault == "marshal" || fault == "decode" {
				require.Error(t, e)
			} else {
				require.ErrorIs(t, e, want)
			}
			if fault == "ok" {
				require.Equal(t, 1, called)
			} else {
				require.Zero(t, called)
			}
		})
	}
}

func TestRetirementTransferGuardsCurrentAssignmentBeforeCanonicalDispatch(t *testing.T) {
	for _, fault := range []string{"", "human", "kind", "stale_work", "stale_assignment", "holder", "not_retiring", "execution", "claim", "provider_missing", "provider_error", "tenant", "artifact"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			claimed := assignmentClaim(t, m, db, ctx, work, false)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				if principal == 12 {
					state := "RETIRING"
					if fault == "not_retiring" {
						state = "ACTIVE"
					}
					return fleet.AgentIdentity{ID: 77, PrincipalID: 12, State: state}, true, nil
				}
				if principal == 13 {
					return fleet.AgentIdentity{ID: 78, PrincipalID: 13, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			a := query.BindAssignments(db, m.service.names)
			_, err := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": guid.ID(12), "agent_id": guid.ID(77)})
			require.NoError(t, err)
			assignment := RetirementAssignment{AssignmentID: claimed.Assignment.ID, ProjectID: 99, WorkItemID: work.ID, WorkVersion: claimed.WorkItem.Version, AssignmentVersion: claimed.Assignment.Version}
			in := RetirementTransferInput{RetirementID: 900, ArtifactID: 901, AssigneePrincipalID: 13, AssigneeKind: "AGENT"}
			w := query.BindWorkItems(db, m.service.names)
			switch fault {
			case "human":
				in.AssigneePrincipalID, in.AssigneeKind = 11, "HUMAN"
			case "kind":
				in.AssigneeKind = "HUMAN"
			case "stale_work":
				assignment.WorkVersion++
			case "stale_assignment":
				assignment.AssignmentVersion++
			case "holder":
				_, err = a.WithContext(ctx).Where(a.ID.Eq(assignment.AssignmentID)).Update(a.AssigneePrincipalID, guid.ID(13))
			case "execution":
				_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.CurrentExecutionID, guid.ID(500))
			case "claim":
				_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.CurrentClaimID, guid.ID(500))
			case "artifact":
				in.ArtifactID = 0
			}
			require.NoError(t, err)
			s := module.NewServices()
			require.NoError(t, m.provideReminderWork(s))
			require.NoError(t, s.Seal())
			connector, err := module.Resolve[RetirementDispatchConnector](s)
			require.NoError(t, err)
			require.Error(t, connector.Bind(RetirementDispatch{}))
			called := 0
			provider := RetirementDispatch{TransferOn: func(_ context.Context, tx *gorm.DB, tenant guid.ID, got RetirementAssignment, body RetirementTransferInput) (guid.ID, error) {
				called++
				require.EqualValues(t, 1, tenant)
				require.Equal(t, assignment, got)
				require.Equal(t, in, body)
				_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
				require.True(t, ok)
				if fault == "provider_error" {
					return 0, errors.New("canonical dispatch unavailable")
				}
				return 800, nil
			}}
			if fault != "provider_missing" {
				require.NoError(t, connector.Bind(provider))
				require.Error(t, connector.Bind(provider))
			}
			access, err := module.Resolve[RetirementWork](s)
			require.NoError(t, err)
			tenant := guid.ID(1)
			if fault == "tenant" {
				tenant = 99
			}
			var next guid.ID
			err = db.Transaction(func(tx *gorm.DB) error {
				var e error
				next, e = access.TransferOn(ctx, tx, tenant, 12, assignment, in)
				return e
			})
			if fault == "" || fault == "human" {
				require.NoError(t, err)
				require.EqualValues(t, 800, next)
				require.Equal(t, 1, called)
			} else {
				require.Error(t, err)
				require.Zero(t, next)
				if fault == "provider_error" {
					require.Equal(t, 1, called)
				} else {
					require.Zero(t, called)
				}
			}
		})
	}
}

func TestRetirementWorkReleaseUsesCurrentAuthorityFencesAndAtomicHistory(t *testing.T) {
	for _, fault := range []string{"", "denied", "stale_work", "stale_assignment", "changed_holder", "execution", "claim", "audit", "not_retiring"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			claimed := assignmentClaim(t, m, db, ctx, work, false)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				if principal == 12 {
					state := "RETIRING"
					if fault == "not_retiring" {
						state = "ACTIVE"
					}
					return fleet.AgentIdentity{ID: 77, PrincipalID: 12, State: state}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Equal(t, "work_item_assign", in.Code)
				return governance.AuthorityDecision{Allowed: fault != "denied", AuthoritySource: "ROLE:owner"}, nil
			}
			a := query.BindAssignments(db, m.service.names)
			_, err := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": guid.ID(12), "agent_id": guid.ID(77)})
			require.NoError(t, err)
			var manifest []RetirementAssignment
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				manifest, e = m.retirementAssignmentsOn(ctx, tx, 1, 12)
				return e
			}))
			require.Len(t, manifest, 1)
			in := manifest[0]
			w := query.BindWorkItems(db, m.service.names)
			switch fault {
			case "stale_work":
				in.WorkVersion++
			case "stale_assignment":
				in.AssignmentVersion++
			case "changed_holder":
				_, err = a.WithContext(ctx).Where(a.ID.Eq(in.AssignmentID)).Update(a.AssigneePrincipalID, guid.ID(11))
			case "execution":
				_, err = w.WithContext(ctx).Where(w.ID.Eq(in.WorkItemID)).Update(w.CurrentExecutionID, guid.ID(300))
			case "claim":
				_, err = w.WithContext(ctx).Where(w.ID.Eq(in.WorkItemID)).Update(w.CurrentClaimID, guid.ID(301))
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failure") }
			}
			require.NoError(t, err)
			before, e := a.WithContext(ctx).Where(a.ID.Eq(in.AssignmentID)).First()
			require.NoError(t, e)
			workBefore, e := w.WithContext(ctx).Where(w.ID.Eq(in.WorkItemID)).First()
			require.NoError(t, e)
			err = db.Transaction(func(tx *gorm.DB) error { return m.releaseRetirementAssignmentOn(ctx, tx, 1, 12, 900, in) })
			after, e := a.WithContext(ctx).Where(a.ID.Eq(in.AssignmentID)).First()
			require.NoError(t, e)
			workAfter, e := w.WithContext(ctx).Where(w.ID.Eq(in.WorkItemID)).First()
			require.NoError(t, e)
			if fault != "" {
				require.Error(t, err)
				require.Equal(t, before, after)
				require.Equal(t, workBefore, workAfter)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "RELEASED", after.Status)
			require.Nil(t, after.ActiveSlot)
			require.Nil(t, after.LeaseExpiresAt)
			require.NotNil(t, after.EndedAt)
			require.Equal(t, "AGENT_RETIREMENT:900", *after.EndReason)
			require.Equal(t, "READY", workAfter.State)
			require.Equal(t, workBefore.Version+1, workAfter.Version)
			require.Greater(t, workAfter.ActivitySeq, workBefore.ActivitySeq)
			history, e := query.BindWorkTransitions(db, m.service.names).WithContext(ctx).Where(query.BindWorkTransitions(db, m.service.names).WorkItemID.Eq(in.WorkItemID)).Order(query.BindWorkTransitions(db, m.service.names).ID.Desc()).First()
			require.NoError(t, e)
			require.Equal(t, "ROLE:owner", history.AuthoritySource)
			require.Equal(t, "HUMAN", history.ActorKind)
			require.Equal(t, "AGENT_RETIREMENT:900", history.ReasonCode)
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseRetirementAssignmentOn(ctx, tx, 1, 12, 900, in) }), "fresh replay key must not release twice")
		})
	}
}

func TestRetirementWorkRejectsNontransactionAndInventedDispatchReferences(t *testing.T) {
	m, db, ctx, _ := assignmentHarness(t, true)
	_, err := m.retirementAssignmentsOn(ctx, db, 1, 12)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reassignRetirementAssignmentOn(ctx, tx, 1, 12, RetirementAssignment{}, DispatchAssignmentInput{})
		return err
	}), ErrInvalidArgument)
}

func TestRetirementWorkReassignmentDelegatesCanonicalDispatchAndRollsBack(t *testing.T) {
	for _, failAudit := range []bool{false, true} {
		t.Run(map[bool]string{false: "reassign", true: "audit_rollback"}[failAudit], func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			old := assignmentClaim(t, m, db, ctx, work, false)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (fleet.AgentIdentity, bool, error) {
				if id == 12 {
					return fleet.AgentIdentity{ID: 77, PrincipalID: 12, State: "RETIRING"}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
			}
			a := query.BindAssignments(db, m.service.names)
			_, err := a.WithContext(ctx).Where(a.ID.Eq(old.Assignment.ID)).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": guid.ID(12), "agent_id": guid.ID(77)})
			require.NoError(t, err)
			in := RetirementAssignment{AssignmentID: old.Assignment.ID, ProjectID: 99, WorkItemID: work.ID, WorkVersion: old.WorkItem.Version, AssignmentVersion: old.Assignment.Version}
			before, err := a.WithContext(ctx).Where(a.ID.Eq(in.AssignmentID)).First()
			require.NoError(t, err)
			if failAudit {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failure") }
			}
			var next guid.ID
			err = db.Transaction(func(tx *gorm.DB) error {
				var e error
				next, e = m.reassignRetirementAssignmentOn(ctx, tx, 1, 12, in, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 500, DecisionID: 501, AssigneePrincipalID: 11, ExpectedWorkVersion: in.WorkVersion, Reassign: true, Reason: "Agent retirement handover: 900; artifact: 901"})
				return e
			})
			after, e := a.WithContext(ctx).Where(a.ID.Eq(in.AssignmentID)).First()
			require.NoError(t, e)
			active, e := a.WithContext(ctx).Where(a.Status.Eq("ACTIVE"), a.WorkItemID.Eq(work.ID)).Find()
			require.NoError(t, e)
			require.Len(t, active, 1)
			if failAudit {
				require.Error(t, err)
				require.Equal(t, before, after)
				require.Equal(t, in.AssignmentID, active[0].ID)
				return
			}
			require.NoError(t, err)
			require.NotEqual(t, in.AssignmentID, next)
			require.Equal(t, "RELEASED", after.Status)
			require.Nil(t, after.ActiveSlot)
			require.NotNil(t, after.EndedAt)
			require.Equal(t, next, active[0].ID)
			require.Equal(t, guid.ID(11), active[0].AssigneePrincipalID)
			w := query.BindWorkItems(db, m.service.names)
			stored, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "CLAIMED", stored.State)
			require.Nil(t, stored.CurrentExecutionID)
			require.Nil(t, stored.CurrentClaimID)
			tr := query.BindWorkTransitions(db, m.service.names)
			transition, e := tr.WithContext(ctx).Where(tr.WorkItemID.Eq(work.ID)).Order(tr.ID.Desc()).First()
			require.NoError(t, e)
			require.Equal(t, "DECISION", transition.Trigger)
			require.Equal(t, guid.ID(501), *transition.DecisionID)
			require.Equal(t, "ROLE:owner", transition.AuthoritySource)
		})
	}
}

func TestSkillDistillationReadsAcceptedCanonicalSourceWithoutChangingWorkOrClaims(t *testing.T) {
	h := newResultFixture(t)
	policy := h.m.workPolicy
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "knowledge_write" {
			require.EqualValues(t, 1, tenant)
			require.EqualValues(t, 99, project)
			return authz.DataConstraint{AllowAll: true}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	result, err := h.submit()
	require.NoError(t, err)
	read := func() (knowledge.AcceptedSkillSource, error) {
		var value knowledge.AcceptedSkillSource
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			value, e = h.m.acceptedSkillSourceOn(h.ctx, tx, 1, h.work)
			return e
		})
		return value, e
	}
	_, err = read()
	require.ErrorIs(t, err, knowledge.ErrDistillationNotAccepted)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 12)
	require.NoError(t, err)
	// This isolated owner-provider fixture supplies genuine-source linkage;
	// Execution's separate Runner test verifies the linked runtime lifecycle.
	r := query.BindResultVersions(h.db, h.m.service.names)
	_, err = r.WithContext(h.ctx).Where(r.ID.Eq(result.ID)).Updates(map[string]any{"source_execution_id": guid.ID(401), "head_commit": strings.Repeat("a", 40)})
	require.NoError(t, err)
	w := query.BindWorkItems(h.db, h.m.service.names)
	before, err := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).First()
	require.NoError(t, err)
	counts := map[string]int64{}
	for _, table := range []string{h.m.service.names.Assignments.String(), h.m.service.names.ResultVersions.String(), h.m.service.names.ResultActions.String()} {
		var count int64
		require.NoError(t, h.db.Table(table).Count(&count).Error)
		counts[table] = count
	}
	source, err := read()
	require.NoError(t, err)
	require.Equal(t, result.ID, source.ResultID)
	require.Equal(t, guid.ID(401), source.ExecutionID)
	require.Contains(t, source.Content, "Human result")
	require.Contains(t, source.Content, "evidence.txt")
	require.Contains(t, source.Content, "Passed verification")
	require.NotContains(t, source.Content, "exact")
	after, err := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).First()
	require.NoError(t, err)
	require.Equal(t, before, after)
	for table, count := range counts {
		var after int64
		require.NoError(t, h.db.Table(table).Count(&after).Error)
		require.Equal(t, count, after, table)
	}
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	_, err = read()
	require.ErrorIs(t, err, knowledge.ErrForbidden)
}

func TestSourceOwnedDispatchInboxCommitsWithAssignmentAndRollsBackOnProjectionFailure(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	// This exercises dispatch inbox atomicity for work without a planning phase.
	// Prepare that scope through the canonical command rather than inventing a
	// planning Execution or bypassing the runtime required by higher-risk work.
	preparation := work.Preparation.WorkPreparationBody
	preparation.Risk, preparation.AutonomyLevel = "LOW", "LOW"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.prepareWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, &preparation)
		return err
	}))
	var err error
	work, err = m.readWork(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, err)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, id == 21, nil
	}
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE inbox_probe (source_event_id TEXT PRIMARY KEY, kind TEXT, subject_id BIGINT, stream_seq BIGINT)").Error)
	failure := errors.New("inbox unavailable")
	fail := true
	s := module.NewServices()
	require.NoError(t, m.provideRetirementKnowledge(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AgentInboxConnector](s)
	require.NoError(t, err)
	require.NoError(t, c.Bind(func(ctx context.Context, tx *gorm.DB, r AgentInboxReference) error {
		require.EqualValues(t, 21, r.PrincipalID)
		require.Equal(t, "work_item:"+work.ID.String(), r.StreamKey)
		if fail {
			return failure
		}
		return tx.Exec("INSERT INTO inbox_probe VALUES (?, ?, ?, ?)", r.SourceEventID, r.Kind, r.SubjectID, r.StreamSeq).Error
	}))
	require.Error(t, c.Bind(func(context.Context, *gorm.DB, AgentInboxReference) error { return nil }))
	apply := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 1000, AssigneePrincipalID: 21, ExpectedWorkVersion: work.Version, Reason: "Reviewed candidate"})
			return err
		})
	}
	require.ErrorIs(t, apply(), failure)
	var count int64
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
	require.Zero(t, count)
	fail = false
	require.NoError(t, apply())
	var receipt struct {
		Kind      string
		SubjectID guid.ID
		StreamSeq int64
	}
	require.NoError(t, db.Table("inbox_probe").Take(&receipt).Error)
	require.Equal(t, "DISPATCH", receipt.Kind)
	require.Equal(t, work.ID, receipt.SubjectID)
	w := query.BindWorkItems(db, m.service.names)
	current, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, current.ActivitySeq, receipt.StreamSeq)
	require.Error(t, apply())
	require.NoError(t, db.Table("inbox_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSourceOwnedReviewInboxUsesExactResultAuthorAndCurrentAgentState(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	result, err := h.submit()
	require.NoError(t, err)
	q := query.BindResultVersions(h.db, h.m.service.names)
	_, err = q.WithContext(h.ctx).Where(q.ID.Eq(result.ID)).Update(q.ActorID, guid.ID(21))
	require.NoError(t, err)
	state := "ACTIVE"
	h.m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: id, State: state}, id == 21, nil
	}
	var receipts []AgentInboxReference
	s := module.NewServices()
	require.NoError(t, h.m.provideRetirementKnowledge(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AgentInboxConnector](s)
	require.NoError(t, err)
	require.NoError(t, c.Bind(func(_ context.Context, _ *gorm.DB, r AgentInboxReference) error {
		receipts = append(receipts, r)
		return nil
	}))
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, "REVIEW_FEEDBACK", receipts[0].Kind)
	require.Equal(t, result.ID, receipts[0].SubjectID)
	require.EqualValues(t, 21, receipts[0].PrincipalID)
	for _, inactive := range []string{"DRAFT", "STOPPED", "CRASHED", "RETIRING"} {
		state = inactive
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.m.appendResultFeedbackOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, resultFeedbackBody{Kind: "FEEDBACK", Category: "QUALITY_REJECTION", Detail: "Source feedback while host is not running"})
			return err
		}))
	}
	require.Len(t, receipts, 5, "source feedback must survive every nonterminal host state")
	state = "ACTIVE"
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if actor == 21 && permission == "work_item_read" {
			return authz.DataConstraint{}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.appendResultFeedbackOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, resultFeedbackBody{Kind: "FEEDBACK", Category: "QUALITY_REJECTION", Detail: "Current source feedback"})
		return err
	}))
	require.Len(t, receipts, 5, "revoked source read scope must not receive an inbox reference")
	state = "RETIRED"
	_, err = h.accept(result.ID, 12)
	require.NoError(t, err)
	require.Len(t, receipts, 5, "retired Agents get no new receipt")
}

func TestReminderWorkAccessUsesCurrentWorkReadPolicy(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	s := module.NewServices()
	require.NoError(t, m.provideReminderWork(s))
	require.NoError(t, s.Seal())
	access, e := module.Resolve[ReminderWorkAccess](s)
	require.NoError(t, e)
	q := query.BindWorkItems(db, m.service.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(work.ID)).Update(q.ActivitySeq, 27)
	require.NoError(t, e)
	seq, e := access.CheckOn(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, e)
	require.EqualValues(t, 27, seq)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	_, e = access.CheckOn(ctx, db, 1, 99, work.ID, 11)
	require.ErrorIs(t, e, ErrWorkForbidden)
	_, e = access.CheckOn(ctx, db, 1, 98, work.ID, 11)
	require.Error(t, e)
}

func TestMemoryWorkSourceFollowsRequirementAndCurrentAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	s := module.NewServices()
	calls := 0
	require.NoError(t, module.Provide[intake.MemorySourceText](s, intake.MemorySourceText{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, project, source guid.ID) (string, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.EqualValues(t, 501, source)
		calls++
		return "Original client source", nil
	}}))
	require.NoError(t, m.provideReminderWork(s))
	require.NoError(t, s.Seal())
	source, err := module.Resolve[MemoryWorkSource](s)
	require.NoError(t, err)
	w := query.BindWorkItems(db, m.service.names)
	requirement := guid.ID(502)
	intakeID := guid.ID(501)
	now := time.Now().UTC()
	r := query.BindRequirements(db, m.service.names)
	require.NoError(t, r.WithContext(ctx).Create(&model.Requirement{ID: requirement, TenantID: 1, ProjectID: 99, Number: 1, Title: "Linked source", Description: "Original source requirement", Priority: 2, Status: "DRAFT", ReviewState: "NEEDS_WORK", CurrentVersionNo: 1, SourceIntakeID: &intakeID, Version: 1, CreatedAt: now, UpdatedAt: now}))
	_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.RequirementID, requirement)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		text, e := source.ReadOn(ctx, tx, 1, 99, work.ID)
		require.Equal(t, "Original client source", text)
		return e
	}))
	require.Equal(t, 1, calls)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := source.ReadOn(ctx, tx, 1, 99, work.ID); return e }))
	require.Equal(t, 1, calls)
}
func TestKnowledgeDecisionsUseExactResultExecutionAndWindow(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	at := time.Now().UTC().Truncate(time.Second)
	execution := guid.ID(701)
	r := query.BindResultVersions(db, m.service.names)
	require.NoError(t, r.WithContext(ctx).Create(&model.ResultVersion{ID: 601, TenantID: 1, ProjectID: 99, WorkItemID: work.ID, Sequence: 1, Summary: "Actual result", ActorID: 11, BaselineVersionNo: 1, SourceExecutionID: &execution, CreatedAt: at}))
	a := query.BindResultActions(db, m.service.names)
	require.NoError(t, a.WithContext(ctx).Create(&model.ResultAction{ID: 602, TenantID: 1, ProjectID: 99, WorkItemID: work.ID, ResultID: 601, Kind: "ACCEPT", Verdict: "PASSED", ActorID: 12, AuthoritySource: "PROJECT", Reason: "Accepted exact result", CreatedAt: at}))
	require.ErrorIs(t, func() error {
		_, e := m.knowledgeDecisionsOn(ctx, db, 1, nil, at.Add(-time.Hour), at.Add(time.Hour))
		return e
	}(), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, e := m.knowledgeDecisionsOn(ctx, tx, 1, nil, at.Add(-time.Hour), at.Add(time.Hour))
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.Equal(t, execution, rows[0].ExecutionID)
		require.EqualValues(t, 602, rows[0].EvidenceID)
		other := guid.ID(98)
		rows, e = m.knowledgeDecisionsOn(ctx, tx, 1, &other, at.Add(-time.Hour), at.Add(time.Hour))
		require.NoError(t, e)
		require.Empty(t, rows)
		rows, e = m.knowledgeDecisionsOn(ctx, tx, 2, nil, at.Add(-time.Hour), at.Add(time.Hour))
		require.NoError(t, e)
		require.Empty(t, rows)
		rows, e = m.knowledgeDecisionsOn(ctx, tx, 1, nil, at.Add(-time.Hour), at)
		require.NoError(t, e)
		require.Empty(t, rows)
		return nil
	}))
}

func TestRetirementReleaseStorageFailuresKeepCurrentOwnershipAndWork(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 77, PrincipalID: id, State: "RETIRING"}, true, nil
	}
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	a := query.BindAssignments(db, m.service.names)
	w := query.BindWorkItems(db, m.service.names)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id=?", claimed.Assignment.ID).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 12, "agent_id": 77}).Error)
	assignment, e := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).First()
	require.NoError(t, e)
	current, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, e)
	in := RetirementAssignment{AssignmentID: assignment.ID, ProjectID: 99, WorkItemID: work.ID, WorkVersion: current.Version, AssignmentVersion: assignment.Version}
	rollback := errors.New("rollback successful retirement release")
	failure := errors.New("retirement storage unavailable")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("retirement-release-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("retirement-release-storage", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retirement-release-storage", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if e := m.releaseRetirementAssignmentOn(ctx, tx, 1, 12, 900, in); e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 5)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		e := run()
		require.Error(t, e)
		require.NotErrorIs(t, e, rollback, "retirement stage %d must retain ownership", i)
	}
	require.NoError(t, db.Callback().Query().Remove("retirement-release-storage"))
	require.NoError(t, db.Callback().Create().Remove("retirement-release-storage"))
	require.NoError(t, db.Callback().Update().Remove("retirement-release-storage"))
	after, e := a.WithContext(ctx).Where(a.ID.Eq(assignment.ID)).First()
	require.NoError(t, e)
	require.Equal(t, assignment, after)
	workAfter, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, e)
	require.Equal(t, current, workAfter)
}

func TestRetirementOwnerDiscoveryReturnsDistinctCurrentAccountablePrincipals(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	assignmentClaim(t, m, db, ctx, work, false)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, e := m.retirementPrincipalIDsOn(ctx, tx, 1, 11)
		require.NoError(t, e)
		require.Equal(t, []guid.ID{work.AccountablePrincipalID}, ids)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := (&Module{}).retirementPrincipalIDsOn(ctx, tx, 1, 11)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
	_, e := m.retirementPrincipalIDsOn(ctx, db, 1, 11)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestRetirementCapabilityPublicationPreservesExistingOwnerServices(t *testing.T) {
	m := &Module{}
	for _, provide := range []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[RetirementWork](s, RetirementWork{}) },
		func(s *module.Services) error {
			return module.Provide[SkillDistillationWork](s, SkillDistillationWork{})
		},
		func(s *module.Services) error { return module.Provide[ReminderWorkAccess](s, ReminderWorkAccess{}) },
	} {
		s := module.NewServices()
		require.NoError(t, provide(s))
		require.ErrorIs(t, m.provideReminderWork(s), module.ErrServiceDuplicate)
	}
}
