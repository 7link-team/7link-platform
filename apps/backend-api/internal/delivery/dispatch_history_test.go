package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestDecisionHistoryRetainsFirstAcceptanceAndLaterReturn(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	read := func() DispatchHistoryWork {
		cutoff := time.Now().UTC()
		if created := h.stored().CreatedAt; created.After(cutoff) {
			cutoff = created
		}
		var got []DispatchHistoryWork
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			got, err = h.m.dispatchDecisionHistoryOn(h.ctx, tx, 1, h.stored().Type, cutoff.Add(time.Hour))
			return err
		}))
		require.Len(t, got, 1)
		return got[0]
	}
	got := read()
	require.Len(t, got.Claims, 1)
	require.Equal(t, r.ID, got.Claims[0].ID)
	require.Len(t, got.Decisions, 1)
	require.Equal(t, "APPROVE", got.Decisions[0].Action)
	require.Equal(t, r.ID, *got.AcceptedClaimID)
	handoff, err := h.handoff(r.ID)
	require.NoError(t, err)
	_, err = h.receive(r.ID, handoff.Handoffs[0].ID, 13, false)
	require.NoError(t, err)
	got = read()
	require.Len(t, got.Decisions, 2)
	require.Equal(t, "APPROVE", got.Decisions[0].Action)
	require.Equal(t, "REQUEST_CHANGES", got.Decisions[1].Action)
	require.Nil(t, got.AcceptedClaimID)
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		rows, err := h.m.dispatchDecisionHistoryOn(h.ctx, tx, 1, h.stored().Type, time.Now())
		require.Empty(t, rows)
		return err
	}))
	_, err = h.m.dispatchDecisionHistoryOn(h.ctx, h.db, 1, "TASK", time.Now())
	require.ErrorIs(t, err, ErrInvalidArgument)
	s := module.NewServices()
	require.NoError(t, h.m.provideDispatchHistory(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[DispatchDecisionHistory](s)
	require.NoError(t, err)
	require.NotNil(t, p.ReadOn)
}

func TestQuestionDraftRequiresCurrentAgentMembershipScopeAndSourceVersion(t *testing.T) {
	h := newResultFixture(t)
	ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 901, TenantID: 1, PrincipalID: 25, State: "ACTIVE"}, true, nil
	}
	h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		p := guid.ID(99)
		return intake.IntakeRef{ID: 50, TenantID: 1, ProjectID: &p, Kind: "NEW", Status: "RECEIVED", Version: 2}, true, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		kind, err := h.m.clarificationDraftAuthorityOn(ctx, tx, 1, 99)
		require.NoError(t, err)
		require.Equal(t, "AGENT", kind)
		require.NoError(t, h.m.clarificationDraftSourceOn(ctx, tx, 1, 99, 50, 2))
		require.Error(t, h.m.clarificationDraftSourceOn(ctx, tx, 1, 99, 50, 1))
		require.Error(t, h.m.clarificationDraftSourceOn(ctx, tx, 1, 98, 50, 2))
		return nil
	}))
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.clarificationDraftAuthorityOn(ctx, tx, 1, 99); return err }))
}

func TestSignerRequiresActualCurrentAcceptingContact(t *testing.T) {
	h := newResultFixture(t)
	principal := guid.ID(11)
	h.m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		return []intake.ContactReference{{ID: 4, TenantID: 1, PrincipalID: &principal, State: "ACTIVE"}}, nil
	}
	at := time.Now().UTC()
	require.NoError(t, h.db.Table(h.m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 4, "role": "CC", "can_accept": true, "receives_deliveries": true, "created_at": at, "updated_at": at}).Error)
	read := func() (guid.ID, error) {
		var id guid.ID
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			id, err = h.m.clientSignerContactOn(h.ctx, tx, 1, 99, 11)
			return err
		})
		return id, err
	}
	id, err := read()
	require.NoError(t, err)
	require.EqualValues(t, 4, id)
	require.NoError(t, h.db.Table(h.m.service.names.Contacts.String()).Where("project_id=99").Update("can_accept", false).Error)
	_, err = read()
	require.ErrorIs(t, err, ErrWorkForbidden)
	require.NoError(t, h.db.Table(h.m.service.names.Contacts.String()).Where("project_id=99").Update("can_accept", true).Error)
	principal = 12
	_, err = read()
	require.ErrorIs(t, err, ErrWorkForbidden)
}

func TestNewProjectUsesCanonicalProvisioningAndDraftRequirement(t *testing.T) {
	h := newResultFixture(t)
	m := h.m
	m.roles = governance.SeededRoles{EnsureTenant: func(context.Context, guid.ID) error { return nil }, RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 6, nil }}
	m.createAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:team_owner"}, nil
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.sources = intake.IntakeReader{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		p := guid.ID(99)
		return intake.IntakeRef{ID: 50, TenantID: 1, ContactID: 4, Kind: "NEW", Status: "TRIAGED", ProjectID: &p}, true, nil
	}, ContactsOn: func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		return []intake.ContactReference{{ID: 4, TenantID: 1}}, nil
	}}
	m.service.entities = core.EntityProvisioner{CreateOn: func(_ context.Context, _ *gorm.DB, e core.Entity) (core.Entity, error) { e.ID = 1001; return e, nil }, GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) { return core.Entity{}, nil }}
	m.service.roles = core.EntityRoleBindingStore{PutOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, core.BindingEffect, time.Time) (core.EntityRoleBinding, bool, error) {
		return core.EntityRoleBinding{}, true, nil
	}}
	m.service.channels = collab.ChannelProvisioner{CreateDefaultOn: func(_ context.Context, _ *gorm.DB, in collab.DefaultChannelInput) (guid.ID, error) {
		require.Equal(t, "ROLE:team_owner", in.Audit.AuthoritySource)
		return 1002, nil
	}}
	verified := false
	m.scopeReauthentication = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.EqualValues(t, 11, actor)
		require.Equal(t, "intake_convert_privileged", purpose)
		require.Equal(t, "intake:50", target)
		require.Equal(t, "proof", proof)
		require.Equal(t, "session", cookie)
		verified = true
		return nil
	}
	in := IntakeProjectInput{Name: "New project", Code: "NP"}
	ids, err := m.prepareIntakeProject(h.ctx, h.db, 1, in)
	require.NoError(t, err)
	var result IntakeProjectResult
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{Summary: "Export", RequirementDraft: "Export rows", Criteria: []string{"CSV"}}, ids, "session", "proof")
		return err
	}))
	require.True(t, verified)
	require.EqualValues(t, 1001, result.ProjectID)
	require.EqualValues(t, 1002, result.ChannelID)
	var project struct {
		Stage          string
		OriginIntakeID guid.ID
	}
	require.NoError(t, h.db.Table(m.service.names.Projects.String()).Where("project_id=1001").Take(&project).Error)
	require.Equal(t, "INTAKE", project.Stage)
	require.EqualValues(t, 50, project.OriginIntakeID)
	var contact struct {
		Role      string
		CanAccept bool
	}
	require.NoError(t, h.db.Table(m.service.names.Contacts.String()).Where("project_id=1001").Take(&contact).Error)
	require.Equal(t, "PRIMARY", contact.Role)
	require.True(t, contact.CanAccept)
	var requirement struct {
		Status         string
		SourceIntakeID guid.ID
	}
	require.NoError(t, h.db.Table(m.service.names.Requirements.String()).Where("id=?", result.RequirementID).Take(&requirement).Error)
	require.Equal(t, "DRAFT", requirement.Status)
	require.EqualValues(t, 50, requirement.SourceIntakeID)
}

func TestSchedulerUsesStoredAgePriorityRiskAndCurrentLead(t *testing.T) {
	h := newResultFixture(t)
	lead := guid.ID(901)
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id = ?", 99).Updates(map[string]any{"lead_agent_id": lead, "dispatch_policy": "AUTO"}).Error)
	h.m.agents.ActiveByRoleOn = func(_ context.Context, _ *gorm.DB, tenant guid.ID, role string) ([]fleet.AgentIdentity, error) {
		require.EqualValues(t, 1, tenant)
		require.Equal(t, "LEAD", role)
		return []fleet.AgentIdentity{{ID: lead, PrincipalID: 902, State: "ACTIVE"}}, nil
	}
	at := time.Now().UTC()
	base := *h.stored()
	for i, item := range []struct {
		hours          int
		priority, risk string
	}{{1, "URGENT", "HIGH"}, {3, "LOW", "LOW"}, {3, "HIGH", "LOW"}, {3, "HIGH", "HIGH"}, {3, "HIGH", "HIGH"}, {4, "HIGH", "HIGH"}, {3, "LOW", "LOW"}, {1, "HIGH", "LOW"}, {1, "URGENT", "HIGH"}} {
		work := base
		work.ID = guid.ID(100 + i)
		work.Number = 100 + i
		work.State = "READY"
		work.Version = 1
		work.StateChangedAt = at.Add(-time.Duration(item.hours) * time.Hour)
		work.Priority = &item.priority
		work.Risk = item.risk
		require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Create(&work).Error)
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		projects, err := h.m.dispatchScheduleProjectsOn(h.ctx, tx, 1, 0)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{99}, projects)
		batch, err := h.m.dispatchScheduleReadyOn(h.ctx, tx, 1, 99, at)
		require.NoError(t, err)
		require.EqualValues(t, 902, batch.PrincipalID)
		ids := []guid.ID{}
		for _, work := range batch.Items {
			ids = append(ids, work.ID)
		}
		require.Equal(t, []guid.ID{105, 103, 104, 102, 101, 106, 100, 108, 107}, ids)
		h.m.agents.ActiveByRoleOn = nil
		_, err = h.m.dispatchScheduleReadyOn(h.ctx, tx, 1, 99, at)
		require.ErrorIs(t, err, ErrUnavailable)
		h.m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) {
			return nil, gorm.ErrInvalidDB
		}
		_, err = h.m.dispatchScheduleReadyOn(h.ctx, tx, 1, 99, at)
		require.ErrorIs(t, err, gorm.ErrInvalidDB)
		h.m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) { return nil, nil }
		batch, err = h.m.dispatchScheduleReadyOn(h.ctx, tx, 1, 99, at)
		require.NoError(t, err)
		require.Empty(t, batch.Items)
		require.Zero(t, batch.PrincipalID, "a missing current lead cannot borrow a historical principal")
		return nil
	}))
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id = ?", 99).Update("stage", "INTAKE").Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		batch, err := h.m.dispatchScheduleReadyOn(h.ctx, tx, 1, 99, at)
		require.NoError(t, err)
		require.Empty(t, batch.Items)
		require.Zero(t, batch.PrincipalID)
		return nil
	}))
}

func TestReopenAcceptedWorkPreservesHistoryAndRollsBack(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	// This fixture was created directly; attach the genuine requirement linkage
	// needed by the immutable package rejection contract.
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var requirement guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		row, err := h.m.createRequirementOn(h.actor(13), tx, 1, 99, 13, requirementCreateBody{Title: "Export contract", Description: "Preserve export semantics"})
		requirement = row.ID
		return err
	}))
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"requirement_id": requirement, "quality_rework_count": 2}).Error)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var evidence struct{ ID guid.ID }
	require.NoError(t, h.db.Table(h.m.service.names.ResultEvidence.String()).Where("result_id=?", r.ID).Take(&evidence).Error)
	item := intake.ReworkItem{ItemID: "rejected", WorkItemID: h.work, ResultID: r.ID, EvidenceID: evidence.ID, RequirementID: requirement}
	before := h.stored()
	accepted := *before.AcceptedDecisionID
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.reopenRejectedWorkOn(h.ctx, tx, 1, 99, item, before.Version, "The client reproduced a defect")
	}), ErrResultIndependent)
	for _, scenario := range []string{"missing-evidence", "wrong-requirement", "empty-reason", "missing-work"} {
		invalid, reason := item, "The client reproduced a defect"
		switch scenario {
		case "missing-evidence":
			invalid.EvidenceID = 999999
		case "wrong-requirement":
			invalid.RequirementID = 999999
		case "empty-reason":
			reason = " "
		case "missing-work":
			invalid.WorkItemID = 999999
		}
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
			return h.m.reopenRejectedWorkOn(h.actor(13), tx, 1, 99, invalid, before.Version, reason)
		}), scenario)
		require.Equal(t, accepted, *h.stored().AcceptedDecisionID)
	}
	run := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return h.m.reopenRejectedWorkOn(h.actor(13), tx, 1, 99, item, before.Version, "The client reproduced a defect")
		})
	}
	baseAudit, baseEvents := h.m.service.audit, h.m.service.events
	for _, failure := range []string{"escalation-audit", "escalation-outbox"} {
		h.m.service.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
			if failure == "escalation-audit" && event.EventType == "delivery.work_item_escalated" {
				return gorm.ErrInvalidDB
			}
			return baseAudit(ctx, tx, event)
		}
		h.m.service.events = projectPublisher(func(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
			if failure == "escalation-outbox" && event.Type == "work_item.escalated" {
				return 0, gorm.ErrInvalidDB
			}
			return baseEvents.Publish(ctx, tx, event)
		})
		require.ErrorIs(t, run(), gorm.ErrInvalidDB)
		require.Equal(t, "ACCEPTED", h.stored().State)
		require.Equal(t, before.QualityReworkCount, h.stored().QualityReworkCount)
		require.Equal(t, accepted, *h.stored().AcceptedDecisionID)
	}
	h.m.service.audit, h.m.service.events = baseAudit, baseEvents
	h.fail = "outbox"
	require.Error(t, run())
	require.Equal(t, "ACCEPTED", h.stored().State)
	require.Equal(t, accepted, *h.stored().AcceptedDecisionID)
	h.fail = ""
	require.NoError(t, run())
	after := h.stored()
	require.Equal(t, "REWORK", after.State)
	require.Nil(t, after.AcceptedAt)
	require.Nil(t, after.AcceptedDecisionID)
	require.Equal(t, before.QualityReworkCount+1, after.QualityReworkCount)
	require.NotNil(t, after.PausedAt)
	require.Equal(t, "AUTONOMY_REWORK_LIMIT", *after.PauseReason)
	require.True(t, after.RequireHumanDecision)
	var n int64
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("id=? AND kind='ACCEPT'", accepted).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Where("result_id=? AND kind='RETURN' AND handoff_id IS NULL", r.ID).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.Error(t, run())
}

func TestStarvationFactsExcludeAssignedAndPausedWork(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"state_changed_at": at.Add(-5 * time.Hour), "state": "READY"}).Error)
	read := func(tenant guid.ID) []DispatchStarvedWork {
		var rows []DispatchStarvedWork
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = m.dispatchStarvedListOn(ctx, tx, tenant, 0, at)
			return err
		}))
		return rows
	}
	require.Len(t, read(1), 1)
	require.Empty(t, read(2))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		yes, err := m.dispatchStarvedCurrentOn(ctx, tx, 1, 99, work.ID, at)
		require.True(t, yes)
		return err
	}))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("paused_at", at).Error)
	require.Empty(t, read(1))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("paused_at", nil).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11})
		return err
	}))
	require.Empty(t, read(1))
	_, err := m.dispatchStarvedListOn(ctx, nil, 1, 0, at)
	require.Error(t, err)
}

func TestReworkRequirementPreservesSourceAndRollsBack(t *testing.T) {
	h := newResultFixture(t)
	project := guid.ID(99)
	source := intake.IntakeRef{ID: 50, TenantID: 1, ProjectID: &project, Kind: "REWORK", Status: "RECEIVED"}
	h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return source, true, nil
	}
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	draft := intake.TriageDraft{Summary: "Correct export", RequirementDraft: "Preserve the original rejected export contract", Criteria: []string{"CSV keeps Unicode", "CSV keeps column order"}}
	run := func() (guid.ID, error) {
		var id guid.ID
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			id, err = h.m.newReworkRequirementOn(h.ctx, tx, 1, 99, 50, draft)
			return err
		})
		return id, err
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, h.db.Table(table).Count(&n).Error)
		return n
	}
	beforeRequirements := count(h.m.service.names.Requirements.String())
	beforeCriteria := count(h.m.service.names.Criteria.String())
	beforeEffects := count("provision_probe")
	for _, failure := range []string{"audit", "outbox"} {
		h.fail = failure
		_, err := run()
		require.Error(t, err)
		require.Equal(t, beforeRequirements, count(h.m.service.names.Requirements.String()))
		require.Equal(t, beforeCriteria, count(h.m.service.names.Criteria.String()))
		require.Equal(t, beforeEffects, count("provision_probe"))
	}
	h.fail = ""
	source.Kind = "NEW"
	_, err := run()
	require.ErrorIs(t, err, ErrResultState)
	source.Kind, project = "REWORK", 98
	_, err = run()
	require.ErrorIs(t, err, ErrResultState)
	project = 99
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	_, err = run()
	require.Error(t, err, "revoked membership must prevent requirement creation")
	require.Equal(t, beforeRequirements, count(h.m.service.names.Requirements.String()))
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	source.Status = "CONVERTED" // A later item may independently create another draft.
	id, err := run()
	require.NoError(t, err)
	var row struct {
		SourceIntakeID   guid.ID
		Status           string
		Title            string
		CurrentVersionNo int
	}
	require.NoError(t, h.db.Table(h.m.service.names.Requirements.String()).Where("id=?", id).Take(&row).Error)
	require.EqualValues(t, 50, row.SourceIntakeID)
	require.Equal(t, "DRAFT", row.Status)
	require.Equal(t, draft.Summary, row.Title)
	require.Zero(t, row.CurrentVersionNo, "creating a rework draft must not baseline it")
	var criteria []struct {
		Text     string
		Required bool
	}
	require.NoError(t, h.db.Table(h.m.service.names.Criteria.String()).Where("requirement_id=?", id).Order("position").Find(&criteria).Error)
	require.Len(t, criteria, 2)
	for i, criterion := range criteria {
		require.Equal(t, draft.Criteria[i], criterion.Text)
		require.True(t, criterion.Required)
	}
}
