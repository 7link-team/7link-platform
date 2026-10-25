package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type inboxRegistrar func(governance.InboxSource) error

func (f inboxRegistrar) Register(source governance.InboxSource) error { return f(source) }

func TestNotificationBindingStopsBeforePublishingIncompleteSources(t *testing.T) {
	h := newResultFixture(t)
	for _, failure := range []string{"quality", "PLAN_APPROVAL", "ANDON", "HUMAN_VERIFICATION", "ESCALATION_INTERNAL_EXHAUSTED", "none"} {
		s := module.NewServices()
		registered := []string{}
		require.NoError(t, module.Provide[governance.QualityReviewConnector](s, governance.QualityReviewConnector{Bind: func(read governance.QualityReviewSource) error {
			require.NotNil(t, read)
			if failure == "quality" {
				return errors.New("bind failed")
			}
			return nil
		}}))
		require.NoError(t, module.Provide[governance.InboxSources](s, inboxRegistrar(func(source governance.InboxSource) error {
			require.NotNil(t, source.Read)
			registered = append(registered, source.Kind)
			if failure == source.Kind {
				return errors.New("register failed")
			}
			return nil
		})))
		require.NoError(t, s.Seal())
		err := h.m.bindAutomatedDecisions(s)
		if failure == "none" {
			require.NoError(t, err)
			require.Equal(t, []string{"PLAN_APPROVAL", "ANDON", "HUMAN_VERIFICATION", "ESCALATION_INTERNAL_EXHAUSTED"}, registered)
		} else if failure == "quality" {
			require.EqualError(t, err, "bind failed")
			require.Empty(t, registered)
		} else {
			require.EqualError(t, err, "register failed")
			require.Equal(t, failure, registered[len(registered)-1])
		}
	}
	s := module.NewServices()
	require.NoError(t, module.Provide[governance.QualityReviewConnector](s, governance.QualityReviewConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, h.m.bindAutomatedDecisions(s), ErrUnavailable)
	bare := &Module{}
	_, err := bare.pendingHumanPlansOn(h.ctx, h.db, 1, 99)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = h.m.pendingHumanPlansOn(h.ctx, nil, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = h.m.pendingHumanPlansOn(h.ctx, h.db, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = bare.planApprovalInbox(h.ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "PLAN_APPROVAL", Limit: 10})
	require.ErrorIs(t, err, governance.ErrUnavailable)
	_, err = bare.verificationInboxWithLeads(h.ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "HUMAN_VERIFICATION", Limit: 10}, nil)
	require.ErrorIs(t, err, governance.ErrUnavailable)
	_, err = bare.reviewInbox(h.ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "WORK_ITEM_REVIEW", Limit: 10})
	require.ErrorIs(t, err, governance.ErrUnavailable)
}

func TestProviderBoundariesDoNotReturnPartialFacts(t *testing.T) {
	h := newResultFixture(t)
	m := h.m
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		return nil, nil
	}
	at := time.Now().UTC()
	_, err := m.clientSignerContactOn(h.ctx, h.db, 1, 99, 11)
	require.ErrorIs(t, err, ErrInvalidArgument, "signer authority must be read in the mutation transaction")
	_, err = m.dispatchDecisionHistoryOn(h.ctx, h.db, 2, "TASK", at)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.clarificationDraftAuthorityOn(h.ctx, h.db, 2, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.dispatchScheduleProjectsOn(h.ctx, nil, 1, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.dispatchScheduleReadyOn(h.ctx, h.db, 1, 99, time.Time{})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.dispatchStarvedCurrentOn(h.ctx, h.db, 1, 99, 0, at)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.clientSignerContactOn(h.ctx, tx, 1, 99, 12)
		require.ErrorIs(t, err, ErrInvalidArgument, "a signer cannot impersonate a different principal")
		m.sources.ContactsOn = nil
		_, err = m.clientSignerContactOn(h.ctx, tx, 1, 99, 11)
		require.ErrorIs(t, err, ErrUnavailable)
		m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
			return nil, errors.New("contact source offline")
		}
		_, err = m.clientSignerContactOn(h.ctx, tx, 1, 99, 11)
		require.EqualError(t, err, "contact source offline")
		m.sources.GetOn = nil
		require.ErrorIs(t, m.clarificationDraftSourceOn(h.ctx, tx, 1, 99, 50, 1), ErrUnavailable)
		m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{}, false, errors.New("source offline")
		}
		require.EqualError(t, m.clarificationDraftSourceOn(h.ctx, tx, 1, 99, 50, 1), "source offline")
		m.listConstraint = nil
		facts, err := m.dispatchDecisionHistoryOn(h.ctx, tx, 1, "TASK", at)
		require.Empty(t, facts)
		require.ErrorIs(t, err, ErrUnavailable)
		m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, errors.New("policy offline")
		}
		facts, err = m.dispatchDecisionHistoryOn(h.ctx, tx, 1, "TASK", at)
		require.Empty(t, facts)
		require.EqualError(t, err, "policy offline")
		m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		canceled, cancel := context.WithCancel(h.ctx)
		cancel()
		facts, err = m.dispatchDecisionHistoryOn(canceled, tx, 1, "TASK", at)
		require.Empty(t, facts)
		require.ErrorIs(t, err, context.Canceled)
		ids, err := m.dispatchScheduleProjectsOn(canceled, tx, 1, 0)
		require.Empty(t, ids)
		require.ErrorIs(t, err, context.Canceled)
		_, err = m.clientSignerContactOn(canceled, tx, 1, 99, 11)
		require.ErrorIs(t, err, context.Canceled)
		_, err = m.dispatchStarvedCurrentOn(canceled, tx, 1, 99, h.work, at)
		require.ErrorIs(t, err, context.Canceled)
		return nil
	}))
}

func TestDelegationProvidersBindRealSourcesAndPropagateFailure(t *testing.T) {
	h := newResultFixture(t)
	for _, fail := range []string{"ensemble", "project", "claim", "assignment", "none"} {
		t.Run(fail, func(t *testing.T) {
			s := module.NewServices()
			called := []string{}
			bind := func(name string) error {
				called = append(called, name)
				if fail == name {
					return errors.New(name + " bind failed")
				}
				return nil
			}
			require.NoError(t, module.Provide[fleet.EnsembleConnector](s, fleet.EnsembleConnector{BindWork: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error) error {
				require.NotNil(t, read)
				return bind("ensemble")
			}}))
			require.NoError(t, module.Provide[governance.DelegationProjectConnector](s, governance.DelegationProjectConnector{Bind: func(f governance.DelegationProjectFacts) error {
				require.NotNil(t, f.InspectOn)
				return bind("project")
			}}))
			require.NoError(t, module.Provide[governance.ClaimEvaluationConnector](s, governance.ClaimEvaluationConnector{Bind: func(f governance.ClaimEvaluationSource) error {
				require.NotNil(t, f.ReadOn)
				return bind("claim")
			}}))
			require.NoError(t, module.Provide[governance.SpawnCostConnector](s, governance.SpawnCostConnector{BindAssignment: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error {
				require.NotNil(t, read)
				return bind("assignment")
			}}))
			require.NoError(t, s.Seal())
			err := h.m.bindDelegationProjects(s)
			if fail == "none" {
				require.NoError(t, err)
				require.Equal(t, []string{"ensemble", "project", "claim", "assignment"}, called)
			} else {
				require.EqualError(t, err, fail+" bind failed")
				require.Equal(t, fail, called[len(called)-1], "binding must stop at the failed capability")
			}
		})
	}
	for _, capability := range []string{"ensemble", "project", "claim", "assignment"} {
		s := module.NewServices()
		if capability == "ensemble" {
			require.NoError(t, module.Provide[fleet.EnsembleConnector](s, fleet.EnsembleConnector{}))
		} else {
			project := governance.DelegationProjectConnector{Bind: func(governance.DelegationProjectFacts) error { return nil }}
			if capability == "project" {
				project.Bind = nil
			}
			require.NoError(t, module.Provide[governance.DelegationProjectConnector](s, project))
			if capability == "claim" {
				require.NoError(t, module.Provide[governance.ClaimEvaluationConnector](s, governance.ClaimEvaluationConnector{}))
			}
			if capability == "assignment" {
				require.NoError(t, module.Provide[governance.ClaimEvaluationConnector](s, governance.ClaimEvaluationConnector{Bind: func(governance.ClaimEvaluationSource) error { return nil }}))
				require.NoError(t, module.Provide[governance.SpawnCostConnector](s, governance.SpawnCostConnector{}))
			}
		}
		require.NoError(t, s.Seal())
		require.ErrorIs(t, h.m.bindDelegationProjects(s), ErrUnavailable, capability)
	}
}

func TestNotificationProvidersRejectCrossPrincipalAndCanceledReads(t *testing.T) {
	h := newResultFixture(t)
	at := time.Now().UTC()
	ctx := h.ctx
	m := h.m
	_, err := m.verificationInboxWithLeads(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 12}, nil)
	require.ErrorIs(t, err, governance.ErrForbidden)
	_, err = m.verificationInboxWithLeads(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "HUMAN_VERIFICATION", Limit: 0}, nil)
	require.ErrorIs(t, err, governance.ErrInvalidArgument)
	_, err = m.planApprovalInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "PLAN_APPROVAL", Limit: 0})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.reviewInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "WORK_ITEM_REVIEW", Limit: 0})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.reviewSheetNotificationOn(ctx, nil, 1, 1)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, found, err := m.reviewSheetNotificationOn(ctx, tx, 1, 999999)
		require.NoError(t, err)
		require.False(t, found)
		_, _, err = m.leaseExpiredNotificationOn(ctx, nil, 1, 1)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, _, err = m.humanVerificationNotificationOn(ctx, h.db, 1, h.work, 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, _, err = m.humanVerificationNotificationOn(ctx, nil, 1, h.work, 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, found, err = m.humanVerificationNotificationOn(ctx, tx, 1, 999999, 11)
		require.NoError(t, err)
		require.False(t, found)
		_, _, found, err = m.humanVerificationNotificationOn(ctx, tx, 1, h.work, 11)
		require.NoError(t, err)
		require.False(t, found, "claimed work has no pending Human verification")
		_, _, err = m.constitutionNotificationEligibleOn(ctx, nil, 1, 1, 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, found, err = m.constitutionNotificationEligibleOn(ctx, tx, 1, 999999, 11)
		require.NoError(t, err)
		require.False(t, found)
		_, err = m.qualityReviewWindowOn(ctx, tx, 1, 99, at, at)
		require.ErrorIs(t, err, ErrInvalidArgument)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, _, err = m.reviewSheetNotificationOn(canceled, tx, 1, 1)
		require.ErrorIs(t, err, context.Canceled)
		_, _, err = m.leaseExpiredNotificationOn(canceled, tx, 1, 1)
		require.ErrorIs(t, err, context.Canceled)
		_, _, _, err = m.humanVerificationNotificationOn(canceled, tx, 1, h.work, 11)
		require.ErrorIs(t, err, context.Canceled)
		_, _, err = m.constitutionNotificationEligibleOn(canceled, tx, 1, 1, 11)
		require.ErrorIs(t, err, context.Canceled)
		return nil
	}))
}

func TestProjectProviderRejectsInvalidInputBeforeEffects(t *testing.T) {
	h := newResultFixture(t)
	for _, in := range []IntakeProjectInput{{Name: " ", Code: "AB"}, {Name: "Valid", Code: "invalid"}, {Name: string([]byte{255}), Code: "AB"}} {
		_, err := h.m.prepareIntakeProject(h.ctx, h.db, 1, in)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	in := IntakeProjectInput{Name: "New project", Code: "NP"}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.ErrorIs(t, err, ErrUnavailable)
		h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{}, false, errors.New("source offline")
		}
		h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
			return errors.New("proof revoked")
		}
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, IntakeProjectInput{}, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{12}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.EqualError(t, err, "proof revoked")
		h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.EqualError(t, err, "source offline")
		h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{}, false, nil
		}
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{Kind: "NEW", Status: "TRIAGED", ContactID: 4}, true, nil
		}
		_, err = h.m.createIntakeProjectOn(h.ctx, tx, 1, 50, in, intake.TriageDraft{}, []guid.ID{11}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

func TestKnowledgeProviderDoesNotTreatMissingResolverAsValid(t *testing.T) {
	h := newResultFixture(t)
	refs := []workContextRef{{Kind: "KNOWLEDGE", Ref: "42"}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, h.m.validateKnowledgeRefsOn(h.ctx, tx, 1, 99, refs), ErrUnavailable)
		require.ErrorIs(t, h.m.validateKnowledgeRefsOn(h.ctx, tx, 1, 99, []workContextRef{{Kind: "KNOWLEDGE", Ref: "not-an-id"}}), ErrInvalidArgument)
		return nil
	}))
	s := module.NewServices()
	require.NoError(t, module.Provide[knowledge.RuntimeContext](s, knowledge.RuntimeContext{ResolveOn: func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]knowledge.ContextItem, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, []guid.ID{42}, ids)
		return nil, knowledge.ErrUnavailable
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, h.m.injectKnowledgeContext(s))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, h.m.validateKnowledgeRefsOn(h.ctx, tx, 1, 99, refs), knowledge.ErrUnavailable)
		return nil
	}))
}

func TestReworkProviderRejectsMissingSourceAndInactiveProject(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.newReworkRequirementOn(h.ctx, tx, 1, 99, 50, intake.TriageDraft{})
		require.ErrorIs(t, err, ErrUnavailable)
		h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{}, false, gorm.ErrInvalidDB
		}
		_, err = h.m.newReworkRequirementOn(h.ctx, tx, 1, 99, 50, intake.TriageDraft{})
		require.ErrorIs(t, err, gorm.ErrInvalidDB)
		return nil
	}))
	require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=99").Update("stage", "INTAKE").Error)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.reopenRejectedWorkOn(h.ctx, tx, 1, 99, intake.ReworkItem{WorkItemID: h.work}, 1, "Reopen")
	}), ErrResultState)
}

func TestHistoryProviderRejectsUnavailableClaimsAndDecisions(t *testing.T) {
	h := newResultFixture(t)
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	for _, table := range []string{h.m.service.names.ResultVersions.String(), h.m.service.names.ResultActions.String()} {
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("regression:history_outage", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				_ = tx.AddError(gorm.ErrInvalidDB)
			}
		}))
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			facts, err := h.m.dispatchDecisionHistoryOn(h.ctx, tx, 1, h.stored().Type, time.Now().UTC().Add(time.Hour))
			require.Empty(t, facts, "incomplete decisions must not become measured dispatch history")
			require.ErrorIs(t, err, gorm.ErrInvalidDB)
			return nil
		}))
		require.NoError(t, h.db.Callback().Query().Remove("regression:history_outage"))
	}
}

func TestProjectAndReworkProvidersDoNotPromoteAgentAuthority(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 901, TenantID: 1, PrincipalID: 25, State: "ACTIVE"}, true, nil
	}
	project := guid.ID(99)
	h.m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return intake.IntakeRef{ID: 50, TenantID: 1, ProjectID: &project, Kind: "NEW", Status: "TRIAGED", ContactID: 4}, true, nil
	}
	h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	ctx := h.actor(25)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, h.m.reworkDispositionAuthorityOn(ctx, tx, 1, 99), ErrConstitutionHumanOnly)
		_, err := h.m.createIntakeProjectOn(ctx, tx, 1, 50, IntakeProjectInput{Name: "New project", Code: "NP"}, intake.TriageDraft{}, []guid.ID{25}, "", "")
		require.ErrorIs(t, err, ErrProjectNonDelegable, "Agent scope permission cannot create a new project")
		h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
		_, err = h.m.createIntakeProjectOn(ctx, tx, 1, 50, IntakeProjectInput{Name: "New project", Code: "NP"}, intake.TriageDraft{}, []guid.ID{25}, "", "")
		require.ErrorIs(t, err, ErrInvalidMember)
		return nil
	}))
}

func TestDeadlineProviderNeverInventsAnEpisodeOnMissingOrFailedRead(t *testing.T) {
	h := newResultFixture(t)
	_, found, err := h.m.deadlineNotificationOn(h.ctx, nil, 1, h.work, "review_age")
	require.False(t, found)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, found, err := h.m.deadlineNotificationOn(h.ctx, tx, 1, h.work, "unknown")
		require.False(t, found)
		require.ErrorIs(t, err, ErrInvalidArgument)
		for _, kind := range []string{"review_age", "project_blocked"} {
			fact, found, err := h.m.deadlineNotificationOn(h.ctx, tx, 1, 999999, kind)
			require.NoError(t, err)
			require.False(t, found)
			require.Zero(t, fact.ProjectID)
			require.True(t, fact.ChangedAt.IsZero())
			canceled, cancel := context.WithCancel(h.ctx)
			cancel()
			fact, found, err = h.m.deadlineNotificationOn(canceled, tx, 1, h.work, kind)
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, found)
			require.Zero(t, fact.ProjectID)
			require.True(t, fact.ChangedAt.IsZero())
		}
		return nil
	}))
}

func TestSignerProviderRejectsOverLimitBeforeResolvingContacts(t *testing.T) {
	h := newResultFixture(t)
	h.m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		t.Fatal("an incomplete capped contact set must not be used to grant signature authority")
		return nil, nil
	}
	at := time.Now().UTC()
	rows := make([]map[string]any, 0, 21)
	for i := range 21 {
		rows = append(rows, map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 100 + i, "role": "CC", "can_accept": true, "receives_deliveries": true, "created_at": at, "updated_at": at})
	}
	require.NoError(t, h.db.Table(h.m.service.names.Contacts.String()).Create(rows).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		id, err := h.m.clientSignerContactOn(h.ctx, tx, 1, 99, 11)
		require.Zero(t, id)
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
}

func TestStageDiscoveryPreservesTenantCursorAndTransactionBoundary(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	_, err := m.stageNotificationProjectsOn(ctx, nil, 1, 0, 100)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.stageNotificationSuggestionOn(ctx, nil, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.stageNotificationSuggestionOn(ctx, db, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.stageNotificationProjectsOn(ctx, tx, 1, 0, 100)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{99}, ids)
		ids, err = m.stageNotificationProjectsOn(ctx, tx, 2, 0, 100)
		require.NoError(t, err)
		require.Empty(t, ids)
		ids, err = m.stageNotificationProjectsOn(ctx, tx, 1, 99, 100)
		require.NoError(t, err)
		require.Empty(t, ids)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		ids, err = m.stageNotificationProjectsOn(canceled, tx, 1, 0, 100)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, ids)
		_, err = m.stageNotificationSuggestionOn(canceled, tx, 1, 99)
		require.ErrorIs(t, err, context.Canceled)
		return nil
	}))
}

func TestProviderRegistrationCannotReplaceExistingAuthoritySources(t *testing.T) {
	h := newResultFixture(t)
	for _, capability := range []string{"rework", "draft", "signer", "project", "schedule"} {
		s := module.NewServices()
		switch capability {
		case "rework":
			require.NoError(t, module.Provide[RejectedWorkDisposition](s, RejectedWorkDisposition{}))
		case "draft":
			require.NoError(t, module.Provide[ClarificationDraftAccess](s, ClarificationDraftAccess{}))
		case "signer":
			require.NoError(t, module.Provide[ClientSignerFacts](s, ClientSignerFacts{}))
		case "project":
			require.NoError(t, module.Provide[IntakeProjectCreation](s, IntakeProjectCreation{}))
		case "schedule":
			require.NoError(t, module.Provide[DispatchScheduleFacts](s, DispatchScheduleFacts{}))
		}
		require.ErrorIs(t, h.m.provideDispatchHistory(s), module.ErrServiceDuplicate, capability)
	}
}
