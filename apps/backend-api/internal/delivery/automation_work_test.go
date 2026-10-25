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
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAutomationParticipantsPreserveCurrentOwnerProducerAndReviewerSources(t *testing.T) {
	for _, fault := range []string{"ok", "quality", "reviewers", "producer", "result", "work"} {
		t.Run(fault, func(t *testing.T) {
			h, r, _ := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("source_execution_id", 401).Error)
			injected := errors.New("automation participants unavailable")
			h.m.verificationExecutor.QualityPrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if fault == "quality" {
					return nil, injected
				}
				return []guid.ID{21, 22}, nil
			}
			h.m.verificationExecutor.ReviewersOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, stage string) ([]guid.ID, error) {
				if fault == "reviewers" {
					return nil, injected
				}
				if stage == "AGENT_TEST" {
					return []guid.ID{22}, nil
				}
				return []guid.ID{23}, nil
			}
			h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) {
				if fault == "producer" {
					return 0, injected
				}
				return 21, nil
			}
			if fault == "result" || fault == "work" {
				table := h.m.service.names.ResultVersions.String()
				if fault == "work" {
					table = h.m.service.names.WorkItems.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("automation-participant-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				ids, e := h.m.automationWorkParticipantsOn(h.ctx, tx, 1, 99, h.work)
				if fault == "ok" {
					require.Equal(t, []guid.ID{11, 21, 22, 23}, ids)
				}
				return e
			})
			if fault == "ok" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, injected)
			}
		})
	}
}

func TestAutomationLinkedDraftCopiesContentWithoutHumanConfirmation(t *testing.T) {
	for _, mode := range []string{"success", "revoked", "own_read", "missing_lock", "wrong_owner", "bad_template", "bad_type", "audit", "event", "later_failure"} {
		t.Run(mode, func(t *testing.T) {
			m, db, creator, source := assignmentHarness(t, true)
			m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
				if mode == "revoked" {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
				if mode == "own_read" && permission == "work_item_read" {
					return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			in := AutomationLinkedItem{AutomationTransition: AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: source.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}, Type: "TASK", TitleTemplate: "Follow up {title} #{number}"}
			switch mode {
			case "missing_lock":
				in.LockedPrincipals = nil
			case "wrong_owner":
				in.OwnerID = 12
			case "bad_template":
				in.TitleTemplate = "{untrusted}"
			case "bad_type":
				in.Type = "MADE_UP"
			}
			m.service.audit = func(c context.Context, _ *gorm.DB, e audit.DomainEvent) error {
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Zero(t, e.PrincipalID)
				require.Equal(t, false, e.Detail["contract_confirmed"])
				require.Equal(t, source.ID, e.Detail["source_work_item_id"])
				if mode == "audit" {
					return errors.New("audit failed")
				}
				return nil
			}
			m.service.events = projectPublisher(func(c context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, "SYSTEM", e.Actor.Kind)
				require.Zero(t, e.Actor.ID)
				require.Equal(t, in.RunAs, e.Actor.AuthoritySource)
				if mode == "event" {
					return 0, errors.New("event failed")
				}
				return 1, nil
			})
			var before int64
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Count(&before).Error)
			err := db.Transaction(func(tx *gorm.DB) error {
				err := m.automationLinkedItemOn(ctx, tx, in)
				if err == nil && mode == "later_failure" {
					return errors.New("later action failed")
				}
				return err
			})
			var after int64
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Count(&after).Error)
			if mode != "success" {
				require.Error(t, err)
				require.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			require.Equal(t, before+1, after)
			var created model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id <> ?", source.ID).First(&created).Error)
			require.Equal(t, "DRAFT", created.State)
			require.False(t, created.ReadinessOK)
			require.Nil(t, created.CreatedBy)
			require.Nil(t, created.UpdatedBy)
			require.Equal(t, source.Objective, created.Objective)
			var contracts int64
			require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id = ?", created.ID).Count(&contracts).Error)
			require.Zero(t, contracts)
			var criteria []model.WorkItemCriterion
			require.NoError(t, db.Table(m.service.names.WorkCriteria.String()).Where("work_item_id = ?", created.ID).Find(&criteria).Error)
			require.NotEmpty(t, criteria)
			for _, criterion := range criteria {
				require.Zero(t, criterion.CreatedBy)
			}
			var activities []model.WorkItemActivity
			require.NoError(t, db.Table(m.service.names.WorkActivities.String()).Where("ref_kind = ?", "work_item").Find(&activities).Error)
			require.Len(t, activities, 2)
			for _, a := range activities {
				require.Equal(t, "SYSTEM", a.ActorKind)
				require.Zero(t, a.ActorID)
			}
			// A plain preparation save never confirms the automated draft. The
			// explicit Human choice freezes the current criteria, atomically.
			m.service.audit = func(c context.Context, _ *gorm.DB, e audit.DomainEvent) error {
				require.EqualValues(t, 11, authn.PrincipalIDFromContext(c))
				require.EqualValues(t, 11, e.PrincipalID)
				return nil
			}
			failEvent := false
			m.service.events = projectPublisher(func(c context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
				require.EqualValues(t, 11, authn.PrincipalIDFromContext(c))
				require.Equal(t, "HUMAN", e.Actor.Kind)
				if failEvent {
					return 0, errors.New("outbox failed")
				}
				return 1, nil
			})
			prep, err := preparationRecord(&created)
			require.NoError(t, err)
			var receipt workPreparationReceipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = m.prepareWorkOn(creator, tx, 1, 99, created.ID, 11, 11, created.Version, &prep.WorkPreparationBody)
				return err
			}))
			require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id=?", created.ID).Count(&contracts).Error)
			require.Zero(t, contracts)
			prep.ConfirmContract = true
			failEvent = true
			err = db.Transaction(func(tx *gorm.DB) error {
				_, err := m.prepareWorkOn(creator, tx, 1, 99, created.ID, 11, 11, receipt.Version, &prep.WorkPreparationBody)
				return err
			})
			require.Error(t, err)
			require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id=?", created.ID).Count(&contracts).Error)
			require.Zero(t, contracts)
			failEvent = false
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.prepareWorkOn(creator, tx, 1, 99, created.ID, 11, 11, receipt.Version, &prep.WorkPreparationBody)
				return err
			}))
			var contract model.WorkItemContract
			require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id=?", created.ID).First(&contract).Error)
			require.EqualValues(t, 11, contract.ConfirmedBy)
			require.Equal(t, 1, contract.VersionNo)
		})
	}
}

func TestAutomationDispatchWorkUsesCurrentCreatorAndPrelockedOwner(t *testing.T) {
	for _, mode := range []string{"success", "revoked", "missing_lock", "wrong_owner", "human_context", "wrong_project"} {
		t.Run(mode, func(t *testing.T) {
			m, db, creator, work := assignmentHarness(t, true)
			m.workPolicy = func(c context.Context, _ *gorm.DB, _, _ guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				require.EqualValues(t, 11, authn.PrincipalIDFromContext(c))
				require.EqualValues(t, 11, actor)
				require.Equal(t, "work_item_assign", permission)
				if mode == "revoked" {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			in := AutomationDispatch{AutomationTransition: AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}, Role: "EXECUTOR"}
			switch mode {
			case "missing_lock":
				in.LockedPrincipals = nil
			case "wrong_owner":
				in.OwnerID = 12
			case "human_context":
				ctx = creator
			case "wrong_project":
				in.ProjectID = 100
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				facts, err := m.automationDispatchWorkOn(ctx, tx, in)
				if err == nil {
					require.Equal(t, work.ID, facts.WorkItemID)
					require.Equal(t, work.Version, facts.WorkVersion)
				}
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

func TestAutomationTestRunConnectorRequiresOneRealOwnerAndPreservesSource(t *testing.T) {
	m, db, ctx, _ := assignmentHarness(t, true)
	services := module.NewServices()
	require.NoError(t, m.provideAutomationWorkOwner(services))
	require.NoError(t, services.Seal())
	resolve, err := module.Resolve[AutomationTriggerWorkOn](services)
	require.NoError(t, err)
	connector, err := module.Resolve[AutomationTestRunWorkConnector](services)
	require.NoError(t, err)
	source := events.RecordedEvent{ID: 701, TenantID: 1, ProjectID: 99, SubjectID: 702, Type: "clientwork.test_run_recorded", SubjectKind: "clientwork"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := resolve(ctx, tx, source)
		return err
	}), ErrUnavailable)
	require.Error(t, connector.Bind(nil))
	owner := AutomationTriggerWorkOn(func(_ context.Context, _ *gorm.DB, actual events.RecordedEvent) (guid.ID, error) {
		require.Equal(t, source, actual)
		return 703, nil
	})
	require.NoError(t, connector.Bind(owner))
	require.Error(t, connector.Bind(owner))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		work, err := resolve(ctx, tx, source)
		require.Equal(t, guid.ID(703), work)
		return err
	}))
}

func TestAutomationTransitionUsesReadinessAuthorityWorkflowAndSystemProvenance(t *testing.T) {
	for _, mode := range []string{"ready", "draft", "wrong_target", "revoked", "paused", "claimed", "missing_lock", "workflow", "audit", "later_failure", "protected"} {
		t.Run(mode, func(t *testing.T) {
			m, db, creator, work := assignmentHarness(t, mode != "draft")
			ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			in := AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, ToState: "READY", RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}
			if mode == "missing_lock" {
				in.LockedPrincipals = nil
			}
			if mode == "draft" {
				in.ToState = "DRAFT"
			}
			if mode == "wrong_target" {
				in.ToState = "BLOCKED"
			}
			if mode == "protected" {
				in.ToState = "ACCEPTED"
			}
			if mode == "revoked" {
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			}
			if mode == "paused" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("paused_at", time.Now().UTC()).Error)
			}
			if mode == "claimed" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("state", "CLAIMED").Error)
			}
			// Force a real state change so the installed workflow hook must run.
			if mode == "workflow" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("state", "DRAFT").Error)
				require.NoError(t, m.installWorkflow(db, func(_ context.Context, _ *gorm.DB, request WorkflowTransition) error {
					require.Equal(t, "SYSTEM", request.ActorKind)
					require.Zero(t, request.ActorID)
					return errors.New("workflow condition denied")
				}))
			}
			var before model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).First(&before).Error)
			var countBefore int64
			require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Count(&countBefore).Error)
			m.service.audit = func(c context.Context, _ *gorm.DB, event audit.DomainEvent) error {
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, in.RunAs, event.Detail["authority_source"])
				require.Equal(t, in.RunID, event.Detail["run_id"])
				if mode == "audit" {
					return errors.New("audit unavailable")
				}
				return nil
			}
			m.service.events = projectPublisher(func(c context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, events.Actor{Kind: "SYSTEM", AuthoritySource: in.RunAs}, event.Actor)
				return 800, nil
			})
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := m.automationTransitionOn(ctx, tx, in); err != nil {
					return err
				}
				if mode == "later_failure" {
					return errors.New("later action failed")
				}
				return nil
			})
			var after model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).First(&after).Error)
			var countAfter int64
			require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Count(&countAfter).Error)
			if mode != "ready" && mode != "draft" {
				require.Error(t, err)
				require.Equal(t, before, after)
				require.Equal(t, countBefore, countAfter)
				return
			}
			require.NoError(t, err)
			require.Equal(t, in.ToState, after.State)
			require.Equal(t, before.Version+1, after.Version)
			require.Nil(t, after.UpdatedBy)
			require.Equal(t, countBefore+1, countAfter)
			q := query.BindWorkTransitions(db, m.service.names)
			transition, err := q.WithContext(ctx).Order(q.ID.Desc()).First()
			require.NoError(t, err)
			require.Equal(t, "SYSTEM", transition.ActorKind)
			require.Nil(t, transition.ActorID)
			require.Equal(t, in.RunAs, transition.AuthoritySource)
			require.Equal(t, in.RunAs+":run:701", *transition.SourceRef)
		})
	}
}

func TestAutomationPreparationFieldsRetainReadinessGuardsAndSystemIdentity(t *testing.T) {
	for _, field := range []string{"risk", "autonomy_level", "estimate_minutes"} {
		for _, mode := range []string{"success", "invalid", "clear", "revoked", "paused", "claimed", "later_failure"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				m, db, creator, work := assignmentHarness(t, true)
				ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
				in := AutomationPreparationField{AutomationTransition: AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}, Field: field, Value: "LOW"}
				if field == "estimate_minutes" {
					in.Value = "60"
				}
				if mode == "invalid" {
					in.Value = "INVALID"
				}
				if mode == "clear" {
					in.Value = ""
				}
				if mode == "revoked" {
					m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, ErrWorkForbidden
					}
				}
				if mode == "paused" {
					require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("paused_at", time.Now().UTC()).Error)
				}
				if mode == "claimed" {
					require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("state", "CLAIMED").Error)
				}
				var before model.WorkItem
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).First(&before).Error)
				m.service.audit = func(c context.Context, _ *gorm.DB, event audit.DomainEvent) error {
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, field, event.Detail["field"])
					require.Equal(t, in.RunAs, event.Detail["authority_source"])
					require.Equal(t, in.RunID, event.Detail["run_id"])
					return nil
				}
				m.service.events = projectPublisher(func(c context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, events.Actor{Kind: "SYSTEM", AuthoritySource: in.RunAs}, event.Actor)
					return 800, nil
				})
				err := db.Transaction(func(tx *gorm.DB) error {
					if err := m.automationPreparationFieldOn(ctx, tx, in); err != nil {
						return err
					}
					if mode == "later_failure" {
						return errors.New("later action failed")
					}
					return nil
				})
				var after model.WorkItem
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).First(&after).Error)
				if mode != "success" && !(mode == "clear" && field == "estimate_minutes") {
					require.Error(t, err)
					require.Equal(t, before, after)
					return
				}
				require.NoError(t, err)
				require.Equal(t, before.Version+1, after.Version)
				require.Nil(t, after.UpdatedBy)
				require.Equal(t, before.VerificationPlan, after.VerificationPlan)
				require.Equal(t, before.ContextRefs, after.ContextRefs)
				require.Equal(t, before.OutOfScopeConfirmedAt, after.OutOfScopeConfirmedAt)
				require.Equal(t, before.AccountablePrincipalID, after.AccountablePrincipalID)
				if field == "risk" {
					require.Equal(t, "LOW", after.Risk)
				} else {
					require.Equal(t, before.Risk, after.Risk)
				}
				if field == "autonomy_level" {
					require.Equal(t, "LOW", after.AutonomyLevel)
				} else {
					require.Equal(t, before.AutonomyLevel, after.AutonomyLevel)
				}
				if field == "estimate_minutes" {
					if mode == "clear" {
						require.Nil(t, after.EstimateMinutes)
						require.Equal(t, "BLOCKED", after.State)
						require.Contains(t, after.BlockReasons, "ESTIMATE_MISSING")
					} else {
						require.Equal(t, 60, *after.EstimateMinutes)
					}
				} else {
					require.Equal(t, before.EstimateMinutes, after.EstimateMinutes)
				}
			})
		}
	}
}

func TestAutomationWorkOwnerHintDoesNotCrossTenantProjectOrArchive(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	owner, err := m.automationWorkOwnerOn(ctx, db, 1, 99, work.ID)
	require.NoError(t, err)
	require.Equal(t, guid.ID(11), owner)
	for _, scope := range [][2]guid.ID{{2, 99}, {1, 100}} {
		_, err = m.automationWorkOwnerOn(ctx, db, scope[0], scope[1], work.ID)
		require.Error(t, err)
	}
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Updates(map[string]any{"state": "CANCELLED", "archived_at": time.Now().UTC()}).Error) //nolint:misspell // Frozen persisted protocol state.
	_, err = m.automationWorkOwnerOn(ctx, db, 1, 99, work.ID)
	require.Error(t, err)
}

func TestAutomationReviewTriggerResolvesStoredWorkRatherThanPayload(t *testing.T) {
	m, db, ctx, requirement := sheetHarness(t)
	var work workDetailView
	var workSheet, requirementSheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		work, err = m.createWorkOn(ctx, tx, 1, 99, 11, workCreateBody{Title: "Review target", Type: "TASK", Objective: "Observable work", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Actual result", VerificationMethod: "Check"}}})
		if err != nil {
			return err
		}
		workSheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{WorkItemID: &work.ID, WorkItemVersion: work.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		if err != nil {
			return err
		}
		requirementSheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: requirement.ID, RequirementVersion: requirement.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return err
	}))
	for _, mode := range []string{"work", "requirement", "foreign_tenant", "foreign_project", "missing", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			source := events.RecordedEvent{TenantID: 1, ProjectID: 99, SubjectID: workSheet.ID, SubjectKind: "review_sheet", Type: "review_sheet.closed", Data: []byte(`{"work_item_id":"999"}`)}
			if mode == "requirement" {
				source.SubjectID = requirementSheet.ID
			}
			if mode == "foreign_tenant" {
				source.TenantID++
			}
			if mode == "foreign_project" {
				source.ProjectID++
			}
			if mode == "missing" {
				source.SubjectID = 999
			}
			if mode == "unrelated" {
				source.Type = "code_link.created"
			}
			var target guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				target, err = m.automationTriggerWorkOn(ctx, tx, source)
				return err
			})
			if mode == "foreign_tenant" || mode == "foreign_project" || mode == "missing" {
				require.Error(t, err)
				require.Zero(t, target)
				return
			}
			require.NoError(t, err)
			if mode == "work" {
				require.Equal(t, work.ID, target)
			} else {
				require.Zero(t, target)
			}
		})
	}
}

func TestAutomationVerificationRequestsRealFrozenTasksWithoutVerdicts(t *testing.T) {
	for _, mode := range []string{"success", "revoked", "paused", "stale", "no_execution", "no_applicable_stage", "roster_changed", "roster_changed_during_queue", "audit", "later_failure"} {
		t.Run(mode, func(t *testing.T) {
			h, result, frozen := verificationFixture(t)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id = ?", result.ID).Update("source_execution_id", result.ID).Error)
			if mode == "no_execution" {
				require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id = ?", result.ID).Update("source_execution_id", nil).Error)
			}
			if mode == "paused" {
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Update("paused_at", time.Now().UTC()).Error)
			}
			if mode == "stale" {
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Update("baseline_stale", true).Error)
			}
			if mode == "revoked" {
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			}
			if mode == "audit" {
				h.fail = "audit"
			}
			ctx := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			in := AutomationVerification{TenantID: 1, ProjectID: 99, WorkItemID: h.work, CreatorID: 12, OwnerID: 11, RuleID: 700, RunID: 701, Stage: "AUTOMATED", RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11, 12}}
			if mode == "roster_changed" {
				h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
			}

			if mode == "roster_changed_during_queue" {
				reads := 0
				h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) {
					reads++
					if reads > 1 {
						return 30, nil
					}
					return 11, nil
				}
				originalLock := h.m.principals.Lock
				h.m.principals.Lock = func(c context.Context, tx *gorm.DB, id guid.ID) error {
					require.NotEqual(t, guid.ID(30), id, "new participant must be rejected before acquiring a late principal lock")
					return originalLock(c, tx, id)
				}
			}
			if mode == "no_applicable_stage" {
				in.Stage = "AGENT_ACCEPTANCE"
			}
			queues := 0
			h.m.verificationExecutor.QueueOn = func(c context.Context, tx *gorm.DB, request VerificationRequest) (guid.ID, error) {
				queues++
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, result.ID, request.ResultID)
				require.Equal(t, result.ID, request.ExecutionID)
				require.Equal(t, frozen.Argv, request.Argv)
				require.Equal(t, "AUTOMATED", request.Stage)
				return 901, tx.Exec("INSERT INTO provision_probe(step) VALUES ('automation_verification_task')").Error
			}
			before := h.snapshot()
			err := h.db.Transaction(func(tx *gorm.DB) error {
				if err := h.m.automationVerificationOn(ctx, tx, in); err != nil {
					return err
				}
				if mode == "later_failure" {
					return errors.New("later action failed")
				}
				return nil
			})
			if mode != "success" {
				require.Error(t, err)
				require.Equal(t, before, h.snapshot())
				return
			}
			require.NoError(t, err)
			runs, err := query.BindVerificationRuns(h.db, h.m.service.names).WithContext(ctx).Find()
			require.NoError(t, err)
			require.Len(t, runs, 1)
			require.Equal(t, "PENDING", runs[0].State)
			require.Empty(t, runs[0].Verdict)
			require.Zero(t, runs[0].ActorID)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.automationVerificationOn(ctx, tx, in) }))
			require.Equal(t, 1, queues, "repeated request reuses the canonical pending task")
		})
	}
}

func TestAutomationAssignmentUsesCurrentAuthorityAndSystemWriter(t *testing.T) {
	for _, mode := range []string{"success", "proposal", "agent", "revoked", "missing_lock", "invalid_member", "audit", "event", "later_failure", "human_context", "wrong_owner", "foreign_project", "paused", "workflow", "claimed", "execution"} {
		t.Run(mode, func(t *testing.T) {
			m, db, creator, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11 || (id == 12 && mode != "invalid_member"), nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if mode == "agent" {
					return fleet.AgentIdentity{ID: 20, TenantID: 1, PrincipalID: 12, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			authorityCalls := 0
			m.deliveryAuthority = func(c context.Context, _ *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				authorityCalls++
				require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(c))
				require.Equal(t, "HUMAN", req.ActorKind)
				require.Equal(t, guid.ID(11), req.PrincipalID)
				require.Equal(t, "work_item_assign", req.Code)
				require.Equal(t, guid.ID(12), req.Facts.AssigneePrincipalID)
				return governance.AuthorityDecision{Allowed: mode != "revoked", AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			if mode == "human_context" {
				ctx = creator
			}
			in := AutomationAssignment{AutomationTransition: AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11, 12}}, AssigneePrincipalID: 12}
			if mode == "missing_lock" {
				in.LockedPrincipals = []guid.ID{11}
			}
			switch mode {
			case "wrong_owner":
				in.OwnerID = 12
			case "foreign_project":
				in.ProjectID = 100
			case "paused":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("paused_at", time.Now().UTC()).Error)
			case "claimed":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "CLAIMED").Error)
			case "execution":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 800).Error)
			case "workflow":
				require.NoError(t, m.installWorkflow(db, func(_ context.Context, _ *gorm.DB, r WorkflowTransition) error {
					require.Equal(t, "SYSTEM", r.ActorKind)
					require.Zero(t, r.ActorID)
					return ErrWorkForbidden
				}))
			}
			audits, eventsCount := 0, 0
			m.service.audit = func(c context.Context, _ *gorm.DB, e audit.DomainEvent) error {
				audits++
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, in.RunAs, e.Detail["authority_source"])
				require.Equal(t, in.RunID, e.Detail["run_id"])
				require.Equal(t, in.RuleID, e.Detail["rule_id"])
				require.Equal(t, in.CreatorID, e.Detail["creator_id"])
				if mode == "proposal" {
					require.Equal(t, "800", e.Detail["dispatch_proposal_id"])
					require.Equal(t, "801", e.Detail["decision_id"])
				} else {
					require.NotContains(t, e.Detail, "dispatch_proposal_id")
				}
				if mode == "audit" {
					return errors.New("audit failed")
				}
				return nil
			}
			m.service.events = projectPublisher(func(c context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
				eventsCount++
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, "SYSTEM", e.Actor.Kind)
				require.Zero(t, e.Actor.ID)
				require.Equal(t, in.RunAs, e.Actor.AuthoritySource)
				require.Equal(t, in.RunID, e.Data.(map[string]any)["run_id"])
				if mode == "proposal" {
					require.Equal(t, "800", e.Data.(map[string]any)["dispatch_proposal_id"])
				} else {
					require.NotContains(t, e.Data, "dispatch_proposal_id")
				}
				if mode == "event" {
					return 0, errors.New("event failed")
				}
				return 900, nil
			})
			err := db.Transaction(func(tx *gorm.DB) error {
				if mode == "proposal" {
					id, err := m.automationAssignmentResultOn(ctx, tx, in, 800, 801)
					if err == nil {
						require.Positive(t, id)
					}
					return err
				}
				if e := m.automationAssignmentOn(ctx, tx, in); e != nil {
					return e
				}
				if mode == "later_failure" {
					return errors.New("later failed")
				}
				return nil
			})
			var rows []model.Assignment
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Find(&rows).Error)
			var updated model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).First(&updated).Error)
			if mode != "success" && mode != "agent" && mode != "proposal" {
				require.Error(t, err)
				require.Empty(t, rows)
				require.Equal(t, work.Version, updated.Version)
				if mode == "claimed" {
					require.Equal(t, "CLAIMED", updated.State)
				} else {
					require.Equal(t, work.State, updated.State)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, authorityCalls)
			require.Equal(t, 1, audits)
			require.Equal(t, 2, eventsCount)
			require.Len(t, rows, 1)
			if mode == "agent" {
				require.Equal(t, "AGENT", rows[0].AssigneeKind)
				require.Equal(t, guid.ID(20), *rows[0].AgentID)
			} else {
				require.Equal(t, "HUMAN", rows[0].AssigneeKind)
			}
			require.Nil(t, rows[0].CreatedBy)
			require.Nil(t, rows[0].UpdatedBy)
			require.Equal(t, guid.ID(12), rows[0].AssigneePrincipalID)
			require.Equal(t, "CLAIMED", updated.State)
			require.Nil(t, updated.UpdatedBy)
			require.Nil(t, updated.CurrentClaimID)
			require.Nil(t, updated.CurrentExecutionID)
			var transitions []model.WorkItemTransition
			require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Where("work_item_id=? AND actor_kind=?", work.ID, "SYSTEM").Find(&transitions).Error)
			require.Len(t, transitions, 1)
			require.Nil(t, transitions[0].ActorID)
			if mode == "proposal" {
				require.Equal(t, "DECISION", transitions[0].Trigger)
				require.EqualValues(t, 801, *transitions[0].DecisionID)
			} else {
				require.Equal(t, "SYSTEM", transitions[0].Trigger)
			}
			require.Equal(t, in.RunAs, transitions[0].AuthoritySource)
			require.Equal(t, in.RunAs+":run:701", *transitions[0].SourceRef)
		})
	}
}

func TestAutomationReadinessStorageFailuresPreserveHumanFieldsAndSystemHistory(t *testing.T) {
	for _, operation := range []string{"transition", "preparation"} {
		t.Run(operation, func(t *testing.T) {
			m, db, creator, work := assignmentHarness(t, true)
			ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			in := AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, ToState: "READY", RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}
			if operation == "preparation" {
				in.ToState = ""
			}
			var before model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).First(&before).Error)
			failure := errors.New("automation storage unavailable")
			rollback := errors.New("rollback successful automation readiness")
			calls, failAt := 0, 0
			probe := func(tx *gorm.DB) {
				calls++
				if calls == failAt {
					tx.AddError(failure)
				}
			}
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("automation-readiness-storage", probe))
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("automation-readiness-storage", probe))
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("automation-readiness-storage", probe))
			run := func() error {
				return db.Transaction(func(tx *gorm.DB) error {
					var e error
					if operation == "transition" {
						e = m.automationTransitionOn(ctx, tx, in)
					} else {
						e = m.automationPreparationFieldOn(ctx, tx, AutomationPreparationField{AutomationTransition: in, Field: "estimate_minutes", Value: "60"})
					}
					if e != nil {
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
				require.NotErrorIs(t, e, rollback, "automation %s stage %d", operation, i)
			}
			require.NoError(t, db.Callback().Query().Remove("automation-readiness-storage"))
			require.NoError(t, db.Callback().Create().Remove("automation-readiness-storage"))
			require.NoError(t, db.Callback().Update().Remove("automation-readiness-storage"))
			var after model.WorkItem
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).First(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestAutomationCapabilitiesRejectCompetingOwnersWithoutReplacingBindings(t *testing.T) {
	m := &Module{}
	providers := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[AutomationDispatchConnector](s, AutomationDispatchConnector{})
		},
		func(s *module.Services) error {
			return module.Provide[AutomationLinkedItemOn](s, m.automationLinkedItemOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationTestRunWorkConnector](s, AutomationTestRunWorkConnector{})
		},
		func(s *module.Services) error {
			return module.Provide[AutomationWorkParticipantsOn](s, m.automationWorkParticipantsOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationVerificationOn](s, m.automationVerificationOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationTriggerWorkOn](s, m.automationTriggerWorkOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationTransitionOn](s, m.automationTransitionOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationPreparationFieldOn](s, m.automationPreparationFieldOn)
		},
		func(s *module.Services) error {
			return module.Provide[AutomationAssignmentOn](s, m.automationAssignmentOn)
		},
	}
	for _, provide := range providers {
		s := module.NewServices()
		require.NoError(t, provide(s))
		require.ErrorIs(t, m.provideAutomationWorkOwner(s), module.ErrServiceDuplicate)
	}
	s := module.NewServices()
	require.NoError(t, m.provideAutomationWorkOwner(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[AutomationDispatchConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil, nil), ErrInvalidArgument)
	discover := func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{12}, nil }
	run := func(context.Context, *gorm.DB, AutomationDispatch) error { return nil }
	require.NoError(t, connector.Bind(discover, run))
	require.ErrorIs(t, connector.Bind(discover, run), ErrInvalidArgument)
}

func TestAutomationOwnerCapabilitiesRejectUnscopedOrMissingRuntimeReads(t *testing.T) {
	h := newResultFixture(t)
	_, e := h.m.automationWorkOwnerOn(h.ctx, h.db, 0, 99, h.work)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = (&Module{}).automationWorkOwnerOn(h.ctx, h.db, 1, 99, h.work)
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = h.m.automationTriggerWorkOn(h.ctx, h.db, events.RecordedEvent{})
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = h.m.automationWorkParticipantsOn(h.ctx, h.db, 1, 99, h.work)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := (&Module{}).automationTriggerWorkOn(h.ctx, tx, events.RecordedEvent{TenantID: 1, ProjectID: 99, SubjectID: h.work})
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}
