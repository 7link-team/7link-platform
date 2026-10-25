package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func registrationPreservesExisting[T any](t *testing.T, existing T) {
	t.Helper()
	t.Run(fmt.Sprintf("%T", existing), func(t *testing.T) {
		services := module.NewServices()
		require.NoError(t, module.Provide[T](services, existing))
		m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
		require.ErrorIs(t, m.ProvideServices(services), module.ErrServiceDuplicate)
		require.NoError(t, services.Seal())
		retained, err := module.Resolve[T](services)
		require.NoError(t, err)
		if reflect.TypeOf(existing).Kind() == reflect.Func {
			require.Equal(t, reflect.ValueOf(existing).Pointer(), reflect.ValueOf(retained).Pointer(), "wiring must retain the existing callable owner")
		} else {
			require.Equal(t, existing, retained, "wiring must not silently replace the existing capability owner")
		}
	})
}

func TestCompositionRejectsConflictingCapabilityOwners(t *testing.T) {
	registrationPreservesExisting(t, DeliveryConfirmationFacts{})
	registrationPreservesExisting(t, ProjectControlNotifications{})
	registrationPreservesExisting(t, ProjectModeSwitcher{})
	registrationPreservesExisting(t, ArtifactCommentsConnector{})
	registrationPreservesExisting(t, WorkItemActivityRecorder{})
	registrationPreservesExisting(t, WorkItemActivityReader{})
	registrationPreservesExisting(t, DispatchStarvationFacts{})
	registrationPreservesExisting(t, HiddenAcceptanceConnector{})
	registrationPreservesExisting(t, WorkKnowledgeContext{})
	registrationPreservesExisting(t, RequirementSuiteConnector{})
	registrationPreservesExisting(t, DecisionNotifications{})
	registrationPreservesExisting(t, WorkflowTransitionConnector{})
	registrationPreservesExisting(t, LoopAssignments{})
	registrationPreservesExisting(t, ScheduledPlanningFacts{})
	registrationPreservesExisting(t, SelectedAgentWork{})
	registrationPreservesExisting(t, IntakeAppender{})
	registrationPreservesExisting(t, DispatchExecutionConnector{})
	registrationPreservesExisting(t, ProjectCancellationConnector{})
	registrationPreservesExisting(t, DispatchAccess{})
	registrationPreservesExisting(t, ForecastWorkFacts{})
	registrationPreservesExisting(t, PackageSourceFacts{})
	registrationPreservesExisting(t, WorkNotifications{})
	registrationPreservesExisting(t, RealtimeTopics{})
	registrationPreservesExisting(t, ExecutionWork{})
	registrationPreservesExisting(t, ProjectScope{})
	registrationPreservesExisting(t, PlanningFacts{})
	registrationPreservesExisting(t, TemplateInstantiation{})
	registrationPreservesExisting(t, ProjectProvisioningConnector{})
	registrationPreservesExisting(t, LocalCodeLinks{})
	registrationPreservesExisting(t, ExecutionHoldConnector{})
	registrationPreservesExisting(t, VerificationAutomation{})
	registrationPreservesExisting(t, VerificationExecutorConnector{})
	registrationPreservesExisting(t, ProjectBudgets{})
	registrationPreservesExisting[ClientWorkAccess](t, clientWork{})
	registrationPreservesExisting(t, RejectedWorkDisposition{})
	registrationPreservesExisting(t, ClarificationDraftAccess{})
	registrationPreservesExisting(t, ClientSignerFacts{})
	registrationPreservesExisting(t, IntakeProjectCreation{})
	registrationPreservesExisting(t, IntakeDelegatedConversion{})
	registrationPreservesExisting(t, DispatchScheduleFacts{})
	registrationPreservesExisting(t, DispatchDecisionHistory{})
	registrationPreservesExisting(t, AcceptanceCostConnector{})
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	registrationPreservesExisting(t, IntakeWorkFence(m.intakeWorkFenceOn))
	registrationPreservesExisting(t, IntakeWorkOwner(m.intakeWorkOwnerOn))
	registrationPreservesExisting(t, ReportSuperior(m.reportSuperiorOn))
	registrationPreservesExisting(t, HomeFacts(m.homeFactsOn))
	registrationPreservesExisting(t, HeavyPlanningReferences(m.heavyPlanningReferenceOn))
	registrationPreservesExisting(t, ProjectNotificationAuthority(m.projectNotificationAuthorityOn))
	registrationPreservesExisting(t, ExecutionArtifactCriteria(m.executionArtifactCriteriaOn))
	registrationPreservesExisting(t, AutomationWorkOwnerOn(m.automationWorkOwnerOn))
	registrationPreservesExisting(t, PlanningWorkIdentityOn(m.iterationWorkOn))
	registrationPreservesExisting(t, ReportQualityReader(m.reportQualityOn))
	registrationPreservesExisting(t, ClarificationHandled(m.clarificationHandledOn))
}

func TestCompositionIdentifiesEachMissingRequiredProvider(t *testing.T) {
	expected := []string{"DelegatedNewSources", "InboxSources", "MessageWorkConnector", "PinnedResources", "DeliveryAuthority", "ReauthenticationConsumer", "TransactionGuard", "ProjectStageAuthority", "EntityConstraintOn", "AccessTableNames", "TenantConstraint", "EntityOwnerResolver", "idempotency.Store", "ProjectCreationAuthority", "IntakeReader", "AgentDirectory", "SpawnGrantDirectory", "ActiveMemberChecker", "TenantConstraintOn", "PrincipalStateStore", "guid.Source", "SeededRoles", "Registrar", "AuthorizationEvaluator", "audit.Appender", "events.Publisher", "events.Catalog"}
	expected = append(expected, "SubscriberCatalog", "jobs.Queue")
	expected = append([]string{"EvidenceReaderConnector"}, expected...)
	require.Len(t, moduleProviders(t), len(expected))
	for missing := range moduleProviders(t) {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
			services := module.NewServices()
			require.NoError(t, m.ProvideServices(services))
			for i, provide := range moduleProviders(t) {
				if i != missing {
					require.NoError(t, provide(services))
				}
			}
			require.NoError(t, services.Seal())
			err := m.InjectServices(services)
			require.ErrorIs(t, err, module.ErrServiceMissing, "a missing provider must fail as that missing dependency, not an unrelated wiring error")
			require.ErrorContains(t, err, expected[missing])
		})
	}
}

func TestCompositionRejectsConflictingEventContracts(t *testing.T) {
	for _, eventType := range []string{"delivery.confirmation_requested", "work_item.requirement_conflict_resolved", "project.mode_changed", "project.autonomy_lost", "project.taken_over", "project.andon_pulled", "clarification.handled", "andon.pulled", "project.constitution_proposed", "result.feedback_added", "work_item.escalated", "code_link.created", "requirement.scope_changed", "work_item.schedule_changed"} {
		t.Run(eventType, func(t *testing.T) {
			m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
			services := module.NewServices()
			require.NoError(t, m.ProvideServices(services))
			for _, provide := range moduleProviders(t) {
				require.NoError(t, provide(services))
			}
			require.NoError(t, services.Seal())
			catalog, err := module.Resolve[events.Catalog](services)
			require.NoError(t, err)
			require.NoError(t, catalog.AddTypes(Name, events.TypeSpec{Name: eventType, SchemaVersion: 1, SubjectKind: "wrong_contract", Realtime: events.RealtimeSignal}))
			err = m.InjectServices(services)
			require.Error(t, err, "composition cannot silently publish an incompatible event contract")
			require.ErrorContains(t, err, eventType)
		})
	}
}

func TestModePreconditionsAndWorkTakeoverHTTPPreserveCurrentAuthority(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	read, err := m.projectModePreconditionsHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	require.NoError(t, err)
	require.Len(t, read.Body.Data, 5)
	for i, criterion := range read.Body.Data {
		require.Equal(t, fmt.Sprintf("P%d", i+1), criterion.Key)
		require.True(t, criterion.Passed)
	}
	q := query.BindProjects(db, m.service.names)
	project, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, 1, project.Version, "reading prerequisites never changes project state")
	w := query.BindWorkItems(db, m.service.names)
	work, err := w.WithContext(ctx).First()
	require.NoError(t, err)
	in := &workTakeoverInput{EntityID: "99", WorkItemID: work.ID.String(), Key: "human-takeover", Cookie: "cookie", Body: workTakeoverBody{Version: work.Version, Reason: "Human takes over the current work decisions", ReauthProof: "proof"}}
	first, err := m.takeoverWorkHTTP(ctx, in)
	require.NoError(t, err)
	taken, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.True(t, taken.RequireHumanDecision)
	require.Equal(t, work.State, first.Body.Data.State, "takeover does not invent an execution or state change")
	again, err := m.takeoverWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	var proofs int64
	require.NoError(t, db.Table("mode_probe").Where("kind=?", "proof").Count(&proofs).Error)
	require.EqualValues(t, 1, proofs, "the committed proof is not consumed again on replay")
	m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) {
		return "", governance.ErrForbidden
	}
	_, err = m.takeoverWorkHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
	current, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, first.Body.Data.Version, current.Version)
	for _, raw := range []string{"", "0", "invalid"} {
		_, err = m.projectModePreconditionsHTTP(ctx, &stageReadinessInput{EntityID: raw})
		assertWorkStatus(t, err, 422)
		bad := *in
		bad.WorkItemID = raw
		_, err = m.takeoverWorkHTTP(ctx, &bad)
		assertWorkStatus(t, err, 422)
	}
	_, err = (&Module{}).projectModePreconditionsHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
	_, err = (&Module{}).takeoverWorkHTTP(ctx, in)
	assertWorkStatus(t, err, 503)
}

func TestModeHTTPReadsRefuseEveryObservedStorageOutage(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			if history {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "AUTONOMOUS", Reason: "Human verifies all current prerequisites", ReauthProof: "proof"}, "cookie", false, []guid.ID{11})
					return err
				}))
			}
			read := func() error {
				if history {
					out, err := m.projectModeHistoryHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
					if err != nil {
						require.Nil(t, out, "a failed history source cannot publish a partial history")
					} else {
						require.Len(t, out.Body.Data, 1)
					}
					return err
				}
				out, err := m.projectModePreconditionsHTTP(ctx, &stageReadinessInput{EntityID: "99"})
				if err != nil {
					require.Nil(t, out, "a failed prerequisite source cannot publish readiness")
				} else {
					require.Len(t, out.Body.Data, 5)
				}
				return err
			}
			seen, position := 0, 0
			const hook = "regression:mode_http_read"
			router := m.runtime.Database().Write()
			require.NoError(t, router.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
				seen++
				if position > 0 && seen == position {
					_ = tx.AddError(errors.New("mode read storage unavailable"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, router.Callback().Query().Remove(hook)) })
			require.NoError(t, read())
			observed := seen
			require.Positive(t, observed)
			for at := 1; at <= observed; at++ {
				t.Run(fmt.Sprint(at), func(t *testing.T) {
					position, seen = at, 0
					require.Error(t, read())
				})
			}
			position = 0
			require.NoError(t, read(), "restoring storage restores the actual read")
		})
	}
}

func TestModeHistoryRejectsMalformedFrozenFacts(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	var receipt ProjectModeReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "AUTONOMOUS", Reason: "Human verifies all current prerequisites", ReauthProof: "proof"}, "cookie", false, []guid.ID{11})
		return err
	}))
	for _, column := range []string{"preconditions_snapshot", "revoked_delegation_ids", "revoked_spawn_grant_ids"} {
		t.Run(column, func(t *testing.T) {
			var original string
			table := func() *gorm.DB {
				return db.Table(m.service.names.ModeChanges.String()).Where("id = ?", receipt.ModeChange.ID)
			}
			require.NoError(t, table().Select(column).Scan(&original).Error)
			require.NoError(t, table().Update(column, "{").Error)
			out, err := m.projectModeHistoryHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
			assertWorkStatus(t, err, 503)
			require.Nil(t, out)
			require.NoError(t, table().Update(column, original).Error)
		})
	}
	out, err := m.projectModeHistoryHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
	require.NoError(t, err)
	require.Len(t, out.Body.Data, 1)
	require.Equal(t, receipt.ModeChange.ID, out.Body.Data[0].ID)
}

func TestNotificationFactsNeverTurnReadOutagesIntoSourceAbsence(t *testing.T) {
	for _, kind := range []string{"stage_projects", "canceled_dependents", "deadline_work", "deadline_project", "lease_expired", "execution_attention", "accepted_candidates", "constitution_eligible"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			read := func(tx *gorm.DB) error {
				switch kind {
				case "stage_projects":
					_, err := m.stageNotificationProjectsOn(ctx, tx, 1, 0, 100)
					return err
				case "canceled_dependents":
					_, err := m.cancelledDependentsOn(ctx, tx, 1, work.ID)
					return err
				case "deadline_work", "deadline_project":
					id, source := work.ID, "review_age"
					if kind == "deadline_project" {
						id, source = 99, "project_blocked"
					}
					_, _, err := m.deadlineNotificationOn(ctx, tx, 1, id, source)
					return err
				case "lease_expired":
					_, _, err := m.leaseExpiredNotificationOn(ctx, tx, 1, 999)
					return err
				case "execution_attention":
					_, _, err := m.executionAttentionWorkOn(ctx, tx, 1, work.ID)
					return err
				case "accepted_candidates":
					_, err := m.acceptedUndeliveredOn(ctx, tx, 1, 99, time.Now().UTC())
					return err
				default:
					_, _, err := m.constitutionNotificationEligibleOn(ctx, tx, 1, 999, 11)
					return err
				}
			}
			seen, position := 0, 0
			outage := errors.New("notification fact read unavailable")
			const hook = "regression:notification_fact_outage"
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
				seen++
				if position > 0 && seen == position {
					_ = tx.AddError(outage)
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(hook)) })
			run := func() error { return db.Transaction(read) }
			require.NoError(t, run())
			observed := seen
			require.Positive(t, observed)
			for at := 1; at <= observed; at++ {
				t.Run(fmt.Sprint(at), func(t *testing.T) {
					position, seen = at, 0
					require.ErrorIs(t, run(), outage)
				})
			}
			position = 0
			require.NoError(t, run())
		})
	}
}

func TestCurrentNotificationAudiencesNeverUseStaleAuthority(t *testing.T) {
	for _, kind := range []string{"andon", "plan", "sheet", "confirmation"} {
		t.Run(kind, func(t *testing.T) {
			var m *Module
			var db *gorm.DB
			var ctx context.Context
			var eligible func(*gorm.DB) (bool, error)
			switch kind {
			case "andon":
				h := newResultFixture(t)
				m, db, ctx = h.m, h.db, h.ctx
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
				}
				version := h.stored().Version
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.changeAndonOn(ctx, tx, 1, 99, h.work, 11, 11, andonBody{Version: version, Reason: "Investigate actual source anomaly"}, false)
					return err
				}))
				eligible = func(tx *gorm.DB) (bool, error) { return m.andonNotificationEligibleOn(ctx, tx, 1, h.work, 12) }
			case "plan":
				var work workDetailView
				m, db, ctx, work = assignmentHarness(t, true)
				content := ReviewPlanContent{OutOfScopeConfirmed: true}
				for _, criterion := range work.Criteria {
					content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: criterion.ID, Approach: "Implement criterion", Verification: "Verify criterion"})
				}
				var plan ReviewPlanView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					plan, err = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
					return err
				}))
				eligible = func(tx *gorm.DB) (bool, error) { return m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 12) }
			case "sheet":
				var req RequirementView
				m, db, ctx, req = sheetHarness(t)
				var sheet RequirementReviewSheetView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{12}, Minimum: 1})
					return err
				}))
				eligible = func(tx *gorm.DB) (bool, error) {
					return m.reviewSheetNotificationEligibleOn(ctx, tx, 1, sheet.ID, 12, "review_sheet.opened")
				}
			default:
				m, db, ctx, _ = deliveryConfirmationHarness(t)
				var fact DeliveryConfirmationFact
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					fact, err = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
					return err
				}))
				eligible = func(tx *gorm.DB) (bool, error) { return m.deliveryConfirmationEligibleOn(ctx, tx, 1, fact.ID, 12) }
			}
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, true, nil
			}
			m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			read := func() (bool, error) {
				var allowed bool
				err := db.Transaction(func(tx *gorm.DB) error { var err error; allowed, err = eligible(tx); return err })
				return allowed, err
			}
			allowed, err := read()
			require.NoError(t, err)
			require.True(t, allowed, "the actual persisted source must be eligible before authority changes")
			originalHuman, originalMembers, originalPolicy := m.principals.ActiveHumanByID, m.members, m.workPolicy
			failures := []string{"human_outage", "human_inactive", "member_outage", "member_removed", "policy_outage", "policy_revoked", "missing_human", "missing_member"}
			// Andon reads use the module's mandatory injected work policy. Its
			// supported failure is a policy error/revocation, both exercised above.
			if kind != "andon" {
				failures = append(failures, "missing_policy")
			}
			for _, failure := range failures {
				t.Run(failure, func(t *testing.T) {
					defer func() {
						m.principals.ActiveHumanByID, m.members, m.workPolicy = originalHuman, originalMembers, originalPolicy
					}()
					outage := errors.New("current notification authority unavailable")
					switch failure {
					case "human_outage", "human_inactive":
						m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
							if failure == "human_outage" {
								return identity.Principal{}, false, outage
							}
							return identity.Principal{}, false, nil
						}
					case "member_outage", "member_removed":
						m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
							if failure == "member_outage" {
								return false, outage
							}
							return false, nil
						})
					case "policy_outage", "policy_revoked":
						m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
							if failure == "policy_outage" {
								return authz.DataConstraint{}, outage
							}
							return authz.DataConstraint{}, nil
						}
					case "missing_human":
						m.principals.ActiveHumanByID = nil
					case "missing_member":
						m.members = nil
					case "missing_policy":
						m.workPolicy = nil
					}
					allowed, err := read()
					require.False(t, allowed, "a persisted invitation never grants current authority")
					if strings.HasSuffix(failure, "_outage") {
						require.ErrorIs(t, err, outage)
					} else if strings.HasPrefix(failure, "missing_") {
						require.ErrorIs(t, err, ErrUnavailable)
					} else {
						require.NoError(t, err)
					}
				})
			}
		})
	}
}

// Fail each observed storage operation in a real command. The successful probe
// is rolled back too, so every attempt starts with the same canonical state.
func TestCanonicalCommandsRollbackEveryObservedStorageFailure(t *testing.T) {
	for _, kind := range []string{"delegated_new", "automation_transition", "automation_preparation", "dependency_add", "dependency_remove", "work_pull", "work_clear", "project_pull", "project_clear", "execution_authorize", "execution_start", "execution_finish", "mode_change", "project_takeover", "work_takeover", "confirmation_request", "confirmation_withdraw", "sheet_create_configured", "sheet_cancel", "sheet_close_pass", "result_submit", "result_check", "result_accept", "result_handoff", "result_receive", "verification_queue", "verification_collect", "requirement_create", "requirement_patch", "requirement_baseline", "criterion_create", "work_create", "work_prepare", "plan_propose", "plan_approve", "upstream_consume"} {
		t.Run(kind, func(t *testing.T) {
			var db *gorm.DB
			var command func(*gorm.DB) error
			var verificationResult ResultView
			var verificationGateTables []string
			var artifactReadTables []string
			released := false
			projectTable := ""
			if kind == "delegated_new" {
				m, store, ctx, card := delegatedNewHarness(t)
				db = store
				require.NoError(t, db.Exec("CREATE TABLE delegated_sink_probe(kind TEXT)").Error)
				m.service.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
					return tx.Exec("INSERT INTO delegated_sink_probe VALUES (?)", "audit:"+e.EventType).Error
				}
				m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
					return 1, tx.Exec("INSERT INTO delegated_sink_probe VALUES (?)", "outbox:"+e.Type).Error
				})
				command = func(tx *gorm.DB) error {
					_, err := m.createDelegatedNewOn(ctx, tx, 1, 99, 50, card.Version, card.Revision, card.Hash)
					return err
				}
			} else if strings.HasPrefix(kind, "automation_") {
				m, store, creator, work := assignmentHarness(t, true)
				db = store
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "DRAFT").Error)
				require.NoError(t, db.Exec("CREATE TABLE automation_sink_probe(kind TEXT)").Error)
				m.service.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					return tx.Exec("INSERT INTO automation_sink_probe VALUES (?)", "audit:"+event.EventType).Error
				}
				m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
					return 1, tx.Exec("INSERT INTO automation_sink_probe VALUES (?)", "outbox:"+event.Type).Error
				})
				ctx := authn.WithClaims(creator, &authn.Claims{TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
				in := AutomationTransition{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, CreatorID: 11, OwnerID: 11, RuleID: 700, RunID: 701, ToState: "READY", RunAs: "SYSTEM:automation:700", LockedPrincipals: []guid.ID{11}}
				command = func(tx *gorm.DB) error {
					if kind == "automation_preparation" {
						edit := in
						edit.ToState = ""
						return m.automationPreparationFieldOn(ctx, tx, AutomationPreparationField{AutomationTransition: edit, Field: "risk", Value: "HIGH"})
					}
					return m.automationTransitionOn(ctx, tx, in)
				}
			} else if kind == "upstream_consume" {
				h := newResultFixture(t)
				db = h.db
				artifactReadTables = []string{h.m.service.names.ResultVersions.String(), h.m.service.names.WorkItems.String(), h.m.service.names.WorkTransitions.String(), h.m.service.names.ReadinessChecks.String(), h.m.service.names.WorkCriteria.String(), h.m.service.names.WorkContracts.String()}
				result, err := h.submit()
				require.NoError(t, err)
				_, err = h.check(result.ID, 12, "PASSED")
				require.NoError(t, err)
				_, err = h.accept(result.ID, 13)
				require.NoError(t, err)
				_, _, _, body := workHTTPHarness(t)
				var consumer workDetailView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					consumer, err = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
					return err
				}))
				require.NoError(t, query.BindDependencies(db, h.m.service.names).WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: consumer.ID, DependsOnID: h.work}))
				e := query.BindResultEvidence(db, h.m.service.names)
				evidence, err := e.WithContext(h.ctx).Where(e.ResultID.Eq(result.ID)).First()
				require.NoError(t, err)
				digest := sha256.Sum256([]byte("exact"))
				evidence.SHA256 = hex.EncodeToString(digest[:])
				_, err = e.WithContext(h.ctx).Where(e.ID.Eq(evidence.ID)).Update(e.SHA256, evidence.SHA256)
				require.NoError(t, err)
				h.m.resultFiles = upstreamFiles{h.files, evidence.SHA256, false}
				command = func(tx *gorm.DB) error {
					_, err := h.m.consumeArtifactOn(h.ctx, tx, 1, 99, consumer.ID, 11, 11, consumptionBody{Version: consumer.Version, AcceptedArtifactID: evidence.ID})
					return err
				}
			} else if kind == "work_create" {
				m, store, ctx, body := workHTTPHarness(t)
				db = store
				command = func(tx *gorm.DB) error { _, err := m.createWorkOn(ctx, tx, 1, 99, 11, body); return err }
			} else if kind == "work_prepare" || strings.HasPrefix(kind, "plan_") {
				m, store, ctx, work, preparation := readinessHarness(t)
				db = store
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				content := ReviewPlanContent{Files: []string{"src/feature.go"}, CriteriaCoverage: []PlanCoverage{}, Risks: []string{}, OutOfScopeConfirmed: true}
				for _, criterion := range work.Criteria {
					content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: criterion.ID, Approach: "Implement the exact criterion", Verification: "Run independent negative-path regression"})
				}
				var plan ReviewPlanView
				if kind == "plan_approve" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						plan, err = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
						return err
					}))
					m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
						return identity.Principal{ID: id}, true, nil
					}
				}
				command = func(tx *gorm.DB) error {
					switch kind {
					case "work_prepare":
						_, err := m.prepareWorkOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, &preparation)
						return err
					case "plan_propose":
						_, err := m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
						return err
					default:
						reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
						_, err := m.decideReviewPlanOn(reviewer, tx, 1, 99, work.ID, plan.ID, 12, work.AccountablePrincipalID, plan.Version, plan.ContentHash, "APPROVE", "")
						return err
					}
				}
			} else if strings.HasPrefix(kind, "requirement_") || kind == "criterion_create" {
				m, store, ctx, input := requirementHarness(t)
				db = store
				var requirement RequirementView
				if kind != "requirement_create" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						requirement, err = m.createRequirementOn(ctx, tx, 1, 99, 11, input)
						return err
					}))
				}
				command = func(tx *gorm.DB) error {
					switch kind {
					case "requirement_create":
						_, err := m.createRequirementOn(ctx, tx, 1, 99, 11, input)
						return err
					case "requirement_baseline":
						_, err := m.baselineRequirementOn(ctx, tx, 1, 99, requirement.ID, 11, requirement.Version)
						return err
					case "criterion_create":
						_, err := m.createCriterionOn(ctx, tx, 1, 99, requirement.ID, 11, requirement.Version, criterionCreateBody{Text: "Independent negative-path requirement", Required: true, Position: 2})
						return err
					default:
						title := "Updated actual business requirement"
						_, err := m.patchRequirementOn(ctx, tx, 1, 99, requirement.ID, 11, requirement.Version, requirementPatchBody{Title: &title})
						return err
					}
				}
			} else if strings.HasPrefix(kind, "result_") || strings.HasPrefix(kind, "verification_") {
				var h *resultFixture
				var result ResultView
				var input verificationStartBody
				if strings.HasPrefix(kind, "verification_") {
					h, result, input = verificationFixture(t)
					if kind == "verification_collect" {
						_, err := h.command(12, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
							return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, result.ID, 12, 11, version, input)
						})
						require.NoError(t, err)
					}
				} else {
					h = newResultFixture(t)
					if kind != "result_submit" {
						var err error
						result, err = h.submit()
						require.NoError(t, err)
					}
					if kind == "result_accept" || kind == "result_handoff" || kind == "result_receive" {
						_, err := h.check(result.ID, 12, "PASSED")
						require.NoError(t, err)
					}
					if kind == "result_handoff" || kind == "result_receive" {
						_, err := h.accept(result.ID, 13)
						require.NoError(t, err)
					}
					if kind == "result_receive" {
						var err error
						result, err = h.handoff(result.ID)
						require.NoError(t, err)
					}
				}
				db = h.db
				if strings.HasPrefix(kind, "verification_") {
					verificationGateTables = []string{h.m.service.names.VerificationRuns.String(), h.m.service.names.VerificationOverrides.String(), h.m.service.names.WorkCriteria.String(), h.m.service.names.WorkContracts.String(), h.m.service.names.ResultEvidence.String()}
				}
				version := h.stored().Version
				command = func(tx *gorm.DB) error {
					h.tx = tx
					defer func() { h.tx = nil }()
					var err error
					switch kind {
					case "result_submit":
						_, err = h.m.submitResultOn(h.actor(11), tx, 1, 99, h.work, 11, 11, version, h.input)
					case "result_check":
						_, err = h.m.checkResultOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, version, resultCheckBody{Verdict: "PASSED", Reason: "Independent examination"})
					case "result_accept":
						_, err = h.m.acceptResultOn(h.actor(13), tx, 1, 99, h.work, result.ID, 13, 11, version, resultAcceptBody{Reason: "Criteria independently verified"})
					case "result_handoff":
						_, err = h.m.handoffResultOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, version, resultHandoffBody{Summary: "Exact frozen package"})
					case "result_receive":
						_, err = h.m.receiveResultOn(h.actor(13), tx, 1, 99, h.work, result.ID, result.Handoffs[0].ID, 13, 11, version, resultReceiptBody{Accepted: true, Reason: "Independent receipt decision"})
					case "verification_queue":
						verificationResult, err = h.m.startVerificationOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, version, input)
					default:
						verificationResult, err = h.m.collectVerificationOn(h.actor(12), tx, 1, 99, h.work, result.ID, 12, 11, version)
					}
					return err
				}
			} else if strings.HasPrefix(kind, "sheet_") {
				m, store, ctx, req := sheetHarness(t)
				db = store
				m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				var typ RequirementReviewTypeView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var err error
					typ, err = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture())
					return err
				}))
				input := RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewTypeID: &typ.ID}
				var sheet RequirementReviewSheetView
				if kind != "sheet_create_configured" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; sheet, err = m.createSheetOn(ctx, tx, 99, input); return err }))
				}
				if kind == "sheet_close_pass" {
					opinion := &sheetActionInput{}
					opinion.Body.Version, opinion.Body.Action, opinion.Body.Opinion.Decision = sheet.Version, "OPINION", "PASS"
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, opinion)
						return err
					}))
				}
				command = func(tx *gorm.DB) error {
					if kind == "sheet_create_configured" {
						_, err := m.createSheetOn(ctx, tx, 99, input)
						return err
					}
					action := &sheetActionInput{}
					action.Body.Version, action.Body.Action, action.Body.Reason = sheet.Version, "CANCEL", "Human withdraws this review request"
					if kind == "sheet_close_pass" {
						action.Body.Action = "CLOSE"
					}
					_, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, action)
					return err
				}
			} else if kind == "mode_change" || kind == "project_takeover" || kind == "work_takeover" {
				m, store, ctx := projectModeHarness(t)
				db = store
				q := query.BindWorkItems(db, m.service.names)
				work, err := q.WithContext(ctx).First()
				require.NoError(t, err)
				command = func(tx *gorm.DB) error {
					if kind == "work_takeover" {
						_, err := m.takeoverWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, workTakeoverBody{Version: work.Version, Reason: "Human takes over exact work decisions", ReauthProof: "proof"}, "cookie")
						return err
					}
					mode := "AUTONOMOUS"
					if kind == "project_takeover" {
						mode = "HYBRID"
					}
					_, err := m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: mode, Reason: "Human changes current project authority", ReauthProof: "proof"}, "cookie", kind == "project_takeover", []guid.ID{11})
					return err
				}
			} else if strings.HasPrefix(kind, "confirmation_") {
				m, store, ctx, _ := deliveryConfirmationHarness(t)
				db = store
				var fact DeliveryConfirmationFact
				if kind == "confirmation_withdraw" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						fact, err = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
						return err
					}))
				}
				command = func(tx *gorm.DB) error {
					if kind == "confirmation_withdraw" {
						_, err := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, fact.ID, 11)
						return err
					}
					_, err := m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
					return err
				}
			} else if strings.HasPrefix(kind, "execution_") {
				h, link := executionWorkFixture(t)
				db = h.db
				if kind != "execution_authorize" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
				}
				if kind == "execution_finish" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return h.m.startExecutionWorkOn(h.ctx, tx, link) }))
				}
				command = func(tx *gorm.DB) error {
					switch kind {
					case "execution_authorize":
						return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE")
					case "execution_start":
						return h.m.startExecutionWorkOn(h.ctx, tx, link)
					default:
						return h.m.finishExecutionWorkOn(h.ctx, tx, link, ExecutionCompletion{State: "SUCCEEDED", HeadCommit: strings.Repeat("a", 40), Evidence: []ExecutionEvidence{{ID: 8001, Title: "Observed execution head", ContentHash: strings.Repeat("b", 64)}}})
					}
				}
			} else {
				m, store, ctx, work, predecessor := dependencyHarness(t)
				db = store
				projectTable = m.service.names.Projects.String()
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
				}
				m.andonAuthority = governance.AndonAuthority(m.deliveryAuthority)
				m.executionHolds = ExecutionHolds{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }, ChangeOn: func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, release bool) ([]ExecutionHoldFact, error) {
					released = released || release
					return nil, nil
				}}
				version := work.Version
				if kind == "dependency_remove" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						next, err := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, version, false)
						version = next.Version
						return err
					}))
				}
				if kind == "work_clear" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						next, err := m.changeAndonOn(ctx, tx, 1, 99, work.ID, 11, 11, andonBody{Version: version, Reason: "Investigate actual source anomaly"}, false)
						version = next.Work.Version
						return err
					}))
				}
				projectVersion := 1
				if kind == "project_clear" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						next, err := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: projectVersion, Reason: "Investigate actual project anomaly"}, false)
						projectVersion = next.Version
						return err
					}))
				}
				command = func(tx *gorm.DB) error {
					switch kind {
					case "dependency_add", "dependency_remove":
						_, err := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, version, kind == "dependency_remove")
						return err
					case "work_pull", "work_clear":
						_, err := m.changeAndonOn(ctx, tx, 1, 99, work.ID, 11, 11, andonBody{Version: version, Reason: "Investigate actual source anomaly"}, kind == "work_clear")
						return err
					default:
						_, err := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: projectVersion, Reason: "Investigate actual project anomaly"}, kind == "project_clear")
						return err
					}
				}
			}
			var tables []string
			require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tables).Error)
			snapshot := func() map[string][]string {
				t.Helper()
				out := map[string][]string{}
				for _, table := range tables {
					var rows []map[string]any
					require.NoError(t, db.Table(table).Find(&rows).Error)
					for _, row := range rows {
						encoded, err := json.Marshal(row)
						require.NoError(t, err)
						out[table] = append(out[table], string(encoded))
					}
					slices.Sort(out[table])
				}
				return out
			}
			before := snapshot()
			rollback := errors.New("successful command probe rollback")
			run := func() error {
				released = false
				return db.Transaction(func(tx *gorm.DB) error {
					if err := command(tx); err != nil {
						return err
					}
					return rollback
				})
			}
			for _, operation := range []string{"query", "create", "update", "delete"} {
				t.Run(operation, func(t *testing.T) {
					position, seen := 0, 0
					injectedTable := ""
					outage := errors.New("canonical command storage outage")
					callback := func(tx *gorm.DB) {
						seen++
						if seen == position {
							injectedTable = tx.Statement.Table
							_ = tx.AddError(outage)
						}
					}
					const hook = "regression:canonical_failure"
					var remove func() error
					switch operation {
					case "query":
						require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, callback))
						remove = func() error { return db.Callback().Query().Remove(hook) }
					case "create":
						require.NoError(t, db.Callback().Create().Before("gorm:create").Register(hook, callback))
						remove = func() error { return db.Callback().Create().Remove(hook) }
					case "update":
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(hook, callback))
						remove = func() error { return db.Callback().Update().Remove(hook) }
					case "delete":
						require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(hook, callback))
						remove = func() error { return db.Callback().Delete().Remove(hook) }
					}
					require.ErrorIs(t, run(), rollback)
					observed := seen
					for at := 1; at <= observed; at++ {
						t.Run(fmt.Sprint(at), func(t *testing.T) {
							position, seen = at, 0
							err := run()
							if kind == "work_clear" && operation == "query" && errors.Is(err, rollback) {
								// A project-hold read failure keeps runtime paused.
								require.Equal(t, projectTable, injectedTable)
								require.False(t, released)
							} else if strings.HasPrefix(kind, "verification_") && operation == "query" && errors.Is(err, rollback) {
								// The completion projection deliberately treats a failed
								// gate read as L2=false; it cannot invent a QA pass.
								require.Contains(t, verificationGateTables, injectedTable)
								require.Positive(t, int64(verificationResult.ID))
								require.False(t, verificationResult.Completion.L2)
							} else if kind == "upstream_consume" && operation == "query" && errors.Is(err, ErrUpstreamStale) {
								// File opening refuses unavailable current result/work
								// authority, including its display-transition provenance.
								// Consumption reports it as inconclusive,
								// which must still reject the command and roll back.
								require.Contains(t, artifactReadTables, injectedTable)
							} else {
								require.ErrorIs(t, err, outage)
							}
							position = 0
							require.Equal(t, before, snapshot(), "every persisted table must survive a refused command unchanged")
						})
					}
					require.NoError(t, remove())
				})
			}
		})
	}
}

func TestCommentConversionStorageFailuresRollbackSourceAndCanonicalDefect(t *testing.T) {
	for _, operation := range []string{"query", "create", "update"} {
		t.Run(operation, func(t *testing.T) {
			m, db, ctx, _, _ := commentDefectHarness(t)
			m.service.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				return tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "audit:"+event.EventType).Error
			}
			m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
				return 1, tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "outbox:"+event.Type).Error
			})
			var frozen ArtifactCommentSource
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				frozen, err = m.artifactComments.GetOn(ctx, tx, 1, 80)
				return err
			}))
			m.artifactComments.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentSource, error) { return frozen, nil }
			tables := []string{m.service.names.WorkItems.String(), m.service.names.WorkCriteria.String(), m.service.names.WorkContracts.String(), m.service.names.CommentDefects.String(), m.service.names.WorkActivities.String(), m.service.names.Counters.String(), "artifact_source_probe", "provision_probe"}
			snapshot := func() map[string][]map[string]any {
				t.Helper()
				out := map[string][]map[string]any{}
				for _, table := range tables {
					var rows []map[string]any
					require.NoError(t, db.Table(table).Find(&rows).Error)
					out[table] = rows
				}
				return out
			}
			before := snapshot()
			position, count := 0, 0
			outage := errors.New("canonical defect storage unavailable")
			callback := func(tx *gorm.DB) {
				count++
				if position == count {
					tx.AddError(outage)
				}
			}
			const name = "regression_comment_conversion_outage"
			var remove func() error
			switch operation {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register(name, callback))
				remove = func() error { return db.Callback().Query().Remove(name) }
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, callback))
				remove = func() error { return db.Callback().Create().Remove(name) }
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, callback))
				remove = func() error { return db.Callback().Update().Remove(name) }
			}
			t.Cleanup(func() { require.NoError(t, remove()) })
			rollback := errors.New("probe rollback")
			run := func() error {
				return db.Transaction(func(tx *gorm.DB) error {
					_, err := m.createCommentDefectOn(ctx, tx, 1, 80, CommentDefectCreate{Severity: "NORMAL", Expected: "Return true for valid input"})
					if err != nil {
						return err
					}
					return rollback
				})
			}
			require.ErrorIs(t, run(), rollback)
			observed := count
			require.Positive(t, observed)
			for at := 1; at <= observed; at++ {
				t.Run(fmt.Sprint(at), func(t *testing.T) {
					position, count = at, 0
					require.ErrorIs(t, run(), outage)
					position = 0
					require.Equal(t, before, snapshot(), "source disposition, canonical work, criteria, contract, provenance and activities must roll back together")
				})
			}
		})
	}
}

func TestDefectProvenanceReadsFrozenSourceUnderCurrentAuthority(t *testing.T) {
	m, db, ctx, source, _ := commentDefectHarness(t)
	var created CommentDefectView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = m.createCommentDefectOn(ctx, tx, 1, 80, CommentDefectCreate{Severity: "NORMAL", Expected: "Return true for valid source input"})
		return err
	}))
	owner, found, err := m.commentThreadOwner(ctx, 80)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, guid.ID(99), owner.EntityID)
	require.Equal(t, "entity", owner.ScopeType)
	in := &workGetInput{EntityID: "99", WorkItemID: created.ID.String()}
	read, err := m.commentDefectProvenanceHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, created, read.Body.Data)
	// The conversion receipt survives source-side changes; it never substitutes
	// the current text of an artifact or an unrelated work item.
	require.NoError(t, db.Exec("DELETE FROM artifact_source_probe").Error)
	read, err = m.commentDefectProvenanceHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "Wrong return value", read.Body.Data.SourceFirstBody)
	require.Equal(t, source, read.Body.Data.FoundInWorkItemID)
	missing := *in
	missing.WorkItemID = source.String()
	_, err = m.commentDefectProvenanceHTTP(ctx, &missing)
	require.Error(t, err, "an ordinary work item cannot invent conversion provenance")
	require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Where("work_item_id=?", created.ID).Update("anchor_snapshot", "malformed").Error)
	_, err = m.commentDefectProvenanceHTTP(ctx, in)
	require.Error(t, err, "a malformed frozen anchor is unavailable, not a partially valid receipt")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.commentDefectProvenanceHTTP(ctx, in)
	require.Error(t, err, "historical conversion never bypasses current work read policy")
	for _, invalid := range []workGetInput{{EntityID: "bad", WorkItemID: created.ID.String()}, {EntityID: "99", WorkItemID: "bad"}} {
		_, err = m.commentDefectProvenanceHTTP(ctx, &invalid)
		require.Error(t, err)
	}
	_, err = (&Module{}).commentDefectProvenanceHTTP(ctx, in)
	require.Error(t, err)
	_, found, err = (&Module{}).commentThreadOwner(ctx, 80)
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, found)
	outage := errors.New("source owner lookup unavailable")
	m.artifactComments.ScopeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentScope, error) {
		return ArtifactCommentScope{}, outage
	}
	_, found, err = m.commentThreadOwner(ctx, 80)
	require.ErrorIs(t, err, outage)
	require.False(t, found)
}

func TestCommentConversionRequiresCurrentAuthorityAndFrozenArtifact(t *testing.T) {
	for _, scenario := range []string{"scope_outage", "source_outage", "principal_lock", "human_outage", "membership_outage", "membership_revoked", "policy_outage", "project_closed", "source_mismatch", "missing_source", "invalid_anchor", "invalid_actual", "invalid_steps", "invalid_found_work"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, source, _ := commentDefectHarness(t)
			outage := errors.New("current comment conversion authority unavailable")
			input := CommentDefectCreate{Severity: "NORMAL", Expected: "Expected independently observed result"}
			want := outage
			switch scenario {
			case "scope_outage":
				m.artifactComments.ScopeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentScope, error) {
					return ArtifactCommentScope{}, outage
				}
			case "source_outage":
				m.artifactComments.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentSource, error) {
					return ArtifactCommentSource{}, outage
				}
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return outage }
			case "human_outage":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, outage
				}
			case "membership_outage", "membership_revoked":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "membership_outage" {
						return false, outage
					}
					return false, nil
				})
				if scenario == "membership_revoked" {
					want = ErrWorkForbidden
				}
			case "policy_outage":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, outage
				}
			case "project_closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = ?", 99).Update("stage", "CLOSED").Error)
				want = ErrWorkProjectReadOnly
			case "missing_source":
				m.artifactComments.GetOn = nil
				want = ErrUnavailable
			case "source_mismatch", "invalid_anchor":
				original := m.artifactComments.GetOn
				m.artifactComments.GetOn = func(ctx context.Context, tx *gorm.DB, tenant, thread guid.ID) (ArtifactCommentSource, error) {
					fact, err := original(ctx, tx, tenant, thread)
					if scenario == "source_mismatch" {
						fact.ProjectID++
					} else {
						fact.Anchor.SHA256 = "unverified"
					}
					return fact, err
				}
				want = ErrWorkForbidden
				if scenario == "invalid_anchor" {
					want = ErrUnavailable
				}
			case "invalid_actual":
				input.Actual, want = string([]byte{0xff}), ErrInvalidArgument
			case "invalid_steps":
				input.Steps, want = strings.Repeat("x", 20001), ErrInvalidArgument
			case "invalid_found_work":
				id := guid.ID(0)
				input.FoundInWorkItemID, want = &id, ErrInvalidArgument
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.createCommentDefectOn(ctx, tx, 1, 80, input); return err })
			require.ErrorIs(t, err, want)
			var works, receipts, converted int64
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Count(&works).Error)
			require.EqualValues(t, 1, works)
			require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Count(&receipts).Error)
			require.Zero(t, receipts)
			require.NoError(t, db.Table("artifact_source_probe").Where("target_id <> 0").Count(&converted).Error)
			require.Zero(t, converted)
			stored, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, source, stored.ID)
		})
	}
}

func TestReportQualityOutagesCannotPublishPartialMetrics(t *testing.T) {
	h := newResultFixture(t)
	at := time.Now().UTC()
	start, end := at.Add(-time.Hour), at.Add(time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", at.Add(-24*time.Hour)).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.recordReportQualityOn(h.ctx, tx, 1, 99, h.work, "INTERNAL_QA_PASSED", 0, 2, at)
	}))
	type outageKey struct{}
	type readFault struct{ position, count int }
	outage := errors.New("quality receipt storage unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("regression_quality_outage", func(tx *gorm.DB) {
		if f, ok := tx.Statement.Context.Value(outageKey{}).(*readFault); ok {
			f.count++
			if f.position == f.count {
				tx.AddError(outage)
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, h.db.Callback().Query().Remove("regression_quality_outage")) })
	read := func(f *readFault) (*ReportQualityMetrics, error) {
		ctx := context.WithValue(h.ctx, outageKey{}, f)
		var result *ReportQualityMetrics
		err := h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			result, err = h.m.reportQualityOn(ctx, tx, 1, 99, []guid.ID{h.work}, start, end)
			return err
		})
		return result, err
	}
	probe := &readFault{}
	result, err := read(probe)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.EqualValues(t, 1, result.InternalQACompleted)
	require.EqualValues(t, 2, result.InternalRoundsSum)
	require.InDelta(t, 2, *result.AvgInternalRounds, 1e-9)
	require.GreaterOrEqual(t, probe.count, 3, "observation marker, receipts and legacy-work guard are independent dependencies")
	for position := 1; position <= probe.count; position++ {
		t.Run(fmt.Sprint(position), func(t *testing.T) {
			result, err := read(&readFault{position: position})
			require.ErrorIs(t, err, outage)
			require.Nil(t, result, "an outage must never become a valid zero numerator or legacy-history absence")
		})
	}
	// Aggregation must recompute rates from numerators, never average per-project rates.
	combined := &ReportQualityMetrics{L3Entered: 9, EscapesHuman: 1, InternalQACompleted: 9, InternalFirstPass: 9, InternalRoundsSum: 9}
	combined.Add(result)
	require.EqualValues(t, 10, combined.InternalQACompleted)
	require.InDelta(t, .9, *combined.InternalFirstPassRate, 1e-9)
	require.InDelta(t, 1.1, *combined.AvgInternalRounds, 1e-9)
}

func TestReviewSheetListUsesCurrentVisibilityAndPagination(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return err
	}))
	input := &sheetList{EntityID: "99", Limit: 1}
	listed, err := m.listSheetsHTTP(ctx, input)
	require.NoError(t, err)
	require.Len(t, listed.Body.Data, 1)
	require.Equal(t, sheet.ID, listed.Body.Data[0].ID)
	input.Offset = 1
	listed, err = m.listSheetsHTTP(ctx, input)
	require.NoError(t, err)
	require.Empty(t, listed.Body.Data)
	input.Offset = 0
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	listed, err = m.listSheetsHTTP(ctx, input)
	require.NoError(t, err)
	require.Empty(t, listed.Body.Data, "historical sheet must not reveal a currently hidden target")
	outage := errors.New("current target policy unavailable")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, outage
	}
	_, err = m.listSheetsHTTP(ctx, input)
	require.Error(t, err, "policy outage must not become an empty list")
	input.EntityID = "invalid"
	_, err = m.listSheetsHTTP(ctx, input)
	require.Error(t, err)
	input.EntityID = "99"
	_, err = (&Module{}).listSheetsHTTP(ctx, input)
	require.Error(t, err)
}

func TestTriageAgentRechecksIdentityAfterPrincipalLock(t *testing.T) {
	for _, scenario := range []string{"source_error", "principal_lock", "second_source_error", "changed_identity", "membership_error", "membership_revoked", "missing_source"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			outage := errors.New("triage identity source unavailable")
			calls := 0
			m.triageAgents.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				calls++
				if scenario == "source_error" || (scenario == "second_source_error" && calls == 2) {
					return fleet.AgentIdentity{}, false, outage
				}
				id := guid.ID(77)
				if scenario == "changed_identity" && calls == 2 {
					id = 78
				}
				return fleet.AgentIdentity{ID: id, TenantID: 1, PrincipalID: 12, State: "ACTIVE"}, true, nil
			}
			if scenario == "principal_lock" {
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return outage }
			}
			if scenario == "membership_error" || scenario == "membership_revoked" {
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "membership_error" {
						return false, outage
					}
					return false, nil
				})
			}
			if scenario == "missing_source" {
				m.triageAgents.AgentOn = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.validateTriageAgentOn(ctx, tx, 1, 77) })
			switch scenario {
			case "changed_identity", "membership_revoked":
				require.ErrorIs(t, err, ErrWorkForbidden)
			case "missing_source":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.ErrorIs(t, err, outage)
			}
		})
	}
}

func TestReviewTypeWritesRollbackAtEveryStorageBoundary(t *testing.T) {
	for _, kind := range []string{"create", "replace", "project_override"} {
		for _, operation := range []string{"query", "create", "update"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				setup := func(t *testing.T) (*gorm.DB, func() error, string) {
					t.Helper()
					m, db, ctx, _ := sheetHarness(t)
					m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{AllowAll: true}, nil
					}
					var typ RequirementReviewTypeView
					if kind != "create" {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							var err error
							typ, err = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture())
							return err
						}))
					}
					settings := reviewTypeFixture()
					settings.Name = "Updated review configuration"
					settings.Propagate = false
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error {
							if kind == "project_override" {
								_, err := m.saveProjectReviewTypeOn(ctx, tx, 1, 99, typ.ID, typ.Version, settings)
								return err
							}
							_, err := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, typ.Version, settings)
							return err
						})
					}, m.service.names.ReviewTypes.String()
				}
				register := func(db *gorm.DB, callback func(*gorm.DB)) error {
					switch operation {
					case "create":
						return db.Callback().Create().Before("gorm:create").Register("regression:review_type", callback)
					case "update":
						return db.Callback().Update().Before("gorm:update").Register("regression:review_type", callback)
					default:
						return db.Callback().Query().Before("gorm:query").Register("regression:review_type", callback)
					}
				}
				remove := func(db *gorm.DB) error {
					switch operation {
					case "create":
						return db.Callback().Create().Remove("regression:review_type")
					case "update":
						return db.Callback().Update().Remove("regression:review_type")
					default:
						return db.Callback().Query().Remove("regression:review_type")
					}
				}
				db, run, _ := setup(t)
				count := 0
				require.NoError(t, register(db, func(*gorm.DB) { count++ }))
				require.NoError(t, run())
				require.NoError(t, remove(db))
				for boundary := 1; boundary <= count; boundary++ {
					t.Run(fmt.Sprint(boundary), func(t *testing.T) {
						db, run, table := setup(t)
						var before []map[string]any
						require.NoError(t, db.Table(table).Order("id").Find(&before).Error)
						outage := errors.New("review configuration storage unavailable")
						seen := 0
						require.NoError(t, register(db, func(tx *gorm.DB) {
							seen++
							if seen == boundary {
								_ = tx.AddError(outage)
							}
						}))
						err := run()
						require.NoError(t, remove(db))
						require.ErrorIs(t, err, outage)
						var after []map[string]any
						require.NoError(t, db.Table(table).Order("id").Find(&after).Error)
						require.Equal(t, before, after, "team configuration and project snapshots must roll back together")
					})
				}
			})
		}
	}
}

// A storage outage must never become an empty inbox or a zero-valued metric.
// Each query boundary is failed in turn after the real source is established.
func TestReadProvidersPropagateEveryStorageFailure(t *testing.T) {
	for _, kind := range []string{"home", "review_inbox", "claim", "review_sheet_inbox", "knowledge", "pending_plan", "plan_eligible", "verification_inbox", "realtime_work", "realtime_project", "decision_work", "notification_work", "notification_review", "notification_mode", "quality_window", "stage_ready", "andon_inbox", "sheet_eligible", "confirmation_pending", "confirmation_inbox"} {
		t.Run(kind, func(t *testing.T) {
			setup := func(t *testing.T) (*gorm.DB, func() error) {
				t.Helper()
				if kind == "confirmation_pending" || kind == "confirmation_inbox" {
					m, db, ctx, _ := deliveryConfirmationHarness(t)
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
						return err
					}))
					if kind == "confirmation_inbox" {
						return m.runtime.Database().Write(), func() error {
							items, err := m.deliveryConfirmationInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, ProjectID: 99, Kind: "DELIVERY_CONFIRM", Limit: 10})
							if err == nil {
								require.Len(t, items, 1)
							}
							return err
						}
					}
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error {
							items, err := m.pendingDeliveryConfirmationsOn(ctx, tx, 1, 99)
							if err == nil {
								require.Len(t, items, 1)
							}
							return err
						})
					}
				}
				if kind == "stage_ready" {
					m, db, ctx, _ := stageHarness(t)
					require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "INTAKE").Error)
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error {
							target, err := m.stageNotificationSuggestionOn(ctx, tx, 1, 99)
							if err == nil {
								require.Equal(t, "DISCOVERY", target)
							}
							return err
						})
					}
				}
				if kind == "andon_inbox" {
					h := newResultFixture(t)
					h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{AllowAll: true}, nil
					}
					h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
						return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
					}
					version := h.stored().Version
					require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
						_, err := h.m.changeAndonOn(h.ctx, tx, 1, 99, h.work, 11, 11, andonBody{Version: version, Reason: "Investigate actual source anomaly"}, false)
						return err
					}))
					return h.m.runtime.Database().Write(), func() error {
						items, err := h.m.andonInbox(h.actor(12), governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "ANDON", Limit: 10}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil })
						if err == nil {
							require.Len(t, items, 1)
						}
						return err
					}
				}
				if kind == "sheet_eligible" {
					m, db, ctx, req := sheetHarness(t)
					var sheet RequirementReviewSheetView
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
						return err
					}))
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error {
							yes, err := m.reviewSheetNotificationEligibleOn(ctx, tx, 1, sheet.ID, 11, "review_sheet.opened")
							if err == nil {
								require.True(t, yes)
							}
							return err
						})
					}
				}
				if kind == "pending_plan" || kind == "plan_eligible" {
					m, db, ctx, work := assignmentHarness(t, true)
					m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{AllowAll: true}, nil
					}
					m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
						return identity.Principal{ID: id}, true, nil
					}
					content := ReviewPlanContent{OutOfScopeConfirmed: true}
					for _, c := range work.Criteria {
						content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement criterion", Verification: "Verify criterion"})
					}
					var plan ReviewPlanView
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						plan, err = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
						return err
					}))
					reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12})
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error {
							if kind == "pending_plan" {
								_, err := m.pendingHumanPlansOn(reviewer, tx, 1, 99)
								return err
							}
							_, err := m.planNotificationEligibleOn(reviewer, tx, 1, plan.ID, 12)
							return err
						})
					}
				}
				if kind == "review_sheet_inbox" {
					m, db, ctx, req := sheetHarness(t)
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
						return err
					}))
					return m.runtime.Database().Write(), func() error {
						_, err := m.reviewSheetInbox(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, ProjectID: 99, Kind: "REVIEW_SHEET", Limit: 10})
						return err
					}
				}
				h := newResultFixture(t)
				result, err := h.submit()
				require.NoError(t, err)
				h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: id}, true, nil
				}
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				callbackDB := h.db
				if kind == "review_inbox" || kind == "verification_inbox" || kind == "realtime_work" || kind == "realtime_project" {
					callbackDB = h.m.runtime.Database().Write()
				}
				h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				return callbackDB, func() error {
					if kind == "notification_work" || kind == "notification_review" || kind == "notification_mode" || kind == "quality_window" {
						return h.db.Transaction(func(tx *gorm.DB) error {
							switch kind {
							case "notification_work":
								_, _, err := h.m.workNotificationFactsOn(h.ctx, tx, 1, h.work)
								return err
							case "notification_review":
								_, err := h.m.workNotificationReviewEligibleOn(authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, PrincipalID: 13}), tx, 1, h.work, 13)
								return err
							case "notification_mode":
								_, err := h.m.notificationProjectModeOn(h.ctx, tx, 1, 99)
								return err
							default:
								_, err := h.m.qualityReviewWindowOn(h.ctx, tx, 1, 99, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
								return err
							}
						})
					}
					if kind == "realtime_work" {
						_, err := h.m.authorizeRealtimeWork(h.ctx, 1, 11, h.work, false)
						return err
					}
					if kind == "realtime_project" {
						_, err := h.m.authorizeRealtimeProject(h.ctx, 1, 11, 99, false)
						return err
					}
					if kind == "decision_work" {
						return h.db.Transaction(func(tx *gorm.DB) error {
							_, _, err := h.m.decisionNotificationFactsOn(h.ctx, tx, 1, h.work, "work_item")
							return err
						})
					}
					switch kind {
					case "verification_inbox":
						_, err := h.m.verificationInboxWithLeads(h.actor(12), governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "HUMAN_VERIFICATION", Limit: 10}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil })
						return err
					case "review_inbox":
						_, err := h.m.reviewInbox(h.actor(12), governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "WORK_ITEM_REVIEW", Limit: 10})
						return err
					case "knowledge":
						return h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.workKnowledgeContextOn(h.ctx, tx, 1, 99, h.work); return err })
					case "home":
						return h.db.Transaction(func(tx *gorm.DB) error {
							_, err := h.m.homeFactsOn(h.ctx, tx, 1, 99, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
							return err
						})
					default:
						return h.db.Transaction(func(tx *gorm.DB) error {
							_, _, err := h.m.claimEvaluationOn(h.ctx, tx, 1, 99, h.work, result.ID)
							return err
						})
					}
				}
			}
			db, run := setup(t)
			count := 0
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:count", func(tx *gorm.DB) { count++ }))
			require.NoError(t, run())
			require.NoError(t, db.Callback().Query().Remove("regression:count"))
			require.Positive(t, count)
			for boundary := 1; boundary <= count; boundary++ {
				t.Run(fmt.Sprint(boundary), func(t *testing.T) {
					db, run := setup(t)
					seen := 0
					outage := errors.New("source database unavailable")
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:fail", func(tx *gorm.DB) {
						seen++
						if seen == boundary {
							_ = tx.AddError(outage)
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("regression:fail")) })
					if kind == "stage_ready" {
						// A failed generated subquery may surface as a SQL
						// construction error. Readiness must still be refused.
						require.Error(t, run(), "an unavailable source must not appear ready")
					} else {
						require.ErrorIs(t, run(), outage, "an unavailable source must not appear complete")
					}
				})
			}
		})
	}
}

// Provider commands must leave no partial review sheet, opinion or rework when
// any database read/write fails. This tests the transaction, not just the error.
func TestReviewProviderStorageFailuresRollBack(t *testing.T) {
	for _, kind := range []string{"create", "opinion", "close_reject"} {
		for _, operation := range []string{"query", "create", "update"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				setup := func(t *testing.T) (*gorm.DB, func() error, []string) {
					m, db, ctx, req := sheetHarness(t)
					tables := []string{m.service.names.ReviewSheets.String(), m.service.names.ReviewOpinions.String(), m.service.names.WorkItems.String(), m.service.names.WorkCriteria.String(), m.service.names.WorkTransitions.String()}
					if kind == "create" {
						return db, func() error {
							return db.Transaction(func(tx *gorm.DB) error {
								_, err := m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
								return err
							})
						}, tables
					}
					var sheet RequirementReviewSheetView
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
						return err
					}))
					input := &sheetActionInput{}
					input.Body.Version = sheet.Version
					input.Body.Action = "OPINION"
					input.Body.Opinion = RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{sheet.Document.Criteria[0].ID.String(): "The criterion requires correction before approval."}}
					if kind == "close_reject" {
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							var err error
							sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, input)
							return err
						}))
						input = &sheetActionInput{}
						input.Body.Version = sheet.Version
						input.Body.Action = "CLOSE"
					}
					return db, func() error {
						return db.Transaction(func(tx *gorm.DB) error { _, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, input); return err })
					}, tables
				}
				register := func(db *gorm.DB, callback func(*gorm.DB)) error {
					switch operation {
					case "create":
						return db.Callback().Create().Before("gorm:create").Register("regression:mutation", callback)
					case "update":
						return db.Callback().Update().Before("gorm:update").Register("regression:mutation", callback)
					default:
						return db.Callback().Query().Before("gorm:query").Register("regression:mutation", callback)
					}
				}
				remove := func(db *gorm.DB) error {
					switch operation {
					case "create":
						return db.Callback().Create().Remove("regression:mutation")
					case "update":
						return db.Callback().Update().Remove("regression:mutation")
					default:
						return db.Callback().Query().Remove("regression:mutation")
					}
				}
				db, run, _ := setup(t)
				count := 0
				require.NoError(t, register(db, func(tx *gorm.DB) { count++ }))
				require.NoError(t, run())
				require.NoError(t, remove(db))
				for boundary := 1; boundary <= count; boundary++ {
					t.Run(fmt.Sprint(boundary), func(t *testing.T) {
						db, run, tables := setup(t)
						before := make([]int64, len(tables))
						for i, table := range tables {
							require.NoError(t, db.Table(table).Count(&before[i]).Error)
						}
						seen := 0
						outage := errors.New("review storage failure")
						require.NoError(t, register(db, func(tx *gorm.DB) {
							seen++
							if seen == boundary {
								_ = tx.AddError(outage)
							}
						}))
						err := run()
						require.NoError(t, remove(db))
						require.ErrorIs(t, err, outage)
						for i, table := range tables {
							var after int64
							require.NoError(t, db.Table(table).Count(&after).Error)
							require.Equal(t, before[i], after, table+" must roll back")
						}
					})
				}
			})
		}
	}
}

func TestKnowledgeProviderRequiresCurrentSubjectAndAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	probe := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.workKnowledgeContextOn(ctx, tx, 1, 99, work.ID); return err })
	}
	require.NoError(t, probe())
	_, err := m.workKnowledgeContextOn(ctx, db, 1, 99, work.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.workKnowledgeContextOn(ctx, tx, 2, 99, work.ID)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = (&Module{}).workKnowledgeContextOn(ctx, tx, 1, 99, work.ID)
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	original := m.principals.LockActiveRow
	outage := errors.New("principal source failed")
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, outage
	}
	require.ErrorIs(t, probe(), outage)
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.ErrorIs(t, probe(), ErrWorkForbidden)
	m.principals.LockActiveRow = original
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, outage
	}
	require.ErrorIs(t, probe(), outage)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, probe(), gorm.ErrRecordNotFound)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	q := query.BindProjects(db, m.service.names)
	_, err = q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Stage, "CLOSED")
	require.NoError(t, err)
	require.ErrorIs(t, probe(), ErrWorkProjectReadOnly)
}

func TestReviewConfigurationRequiresCurrentHumanAndTenantMembership(t *testing.T) {
	for _, scenario := range []string{"principal_lock", "principal_source", "revoked_human", "member_source", "revoked_member", "policy_source", "policy_denied", "missing_provider"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			outage := errors.New("review configuration authority unavailable")
			m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "policy_source" {
					return authz.DataConstraint{}, outage
				}
				return authz.DataConstraint{AllowAll: scenario != "policy_denied"}, nil
			}
			switch scenario {
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return outage }
			case "principal_source", "revoked_human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					if scenario == "principal_source" {
						return identity.Principal{}, false, outage
					}
					return identity.Principal{}, false, nil
				}
			case "member_source", "revoked_member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "member_source" {
						return false, outage
					}
					return false, nil
				})
			case "missing_provider":
				m.reviewTypePolicy = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.reviewTypeAuthorityOn(ctx, tx, 1, "workflow_manage") })
			switch scenario {
			case "revoked_human", "revoked_member", "policy_denied":
				require.ErrorIs(t, err, ErrWorkForbidden)
			case "missing_provider":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.ErrorIs(t, err, outage)
			}
			q := query.BindReviewTypes(db, m.service.names)
			count, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestRealtimeSubscriptionsUseCurrentRowPolicy(t *testing.T) {
	h := newResultFixture(t)
	for _, kind := range []string{"work", "project"} {
		t.Run(kind, func(t *testing.T) {
			probe := func(tenant, actor, id guid.ID) (bool, error) {
				if kind == "work" {
					return h.m.authorizeRealtimeWork(h.ctx, tenant, actor, id, false)
				}
				return h.m.authorizeRealtimeProject(h.ctx, tenant, actor, id, false)
			}
			id := guid.ID(99)
			if kind == "work" {
				id = h.work
			}
			setPolicy := func(c authz.DataConstraint, err error) {
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return c, err
				}
				h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) { return c, err }
			}
			setPolicy(authz.DataConstraint{AllowAll: true}, nil)
			allowed, err := probe(1, 11, id)
			require.NoError(t, err)
			require.True(t, allowed)
			for _, subject := range [][2]guid.ID{{2, 11}, {1, 12}} {
				allowed, err = probe(subject[0], subject[1], id)
				require.NoError(t, err)
				require.False(t, allowed)
			}
			allowed, err = probe(1, 11, 987654321)
			require.NoError(t, err)
			require.False(t, allowed, "missing rows cannot be subscribed to")
			setPolicy(authz.DataConstraint{}, nil)
			allowed, err = probe(1, 11, id)
			require.NoError(t, err)
			require.False(t, allowed, "current revocation invalidates subscription authority")
			outage := errors.New("current subscription policy unavailable")
			setPolicy(authz.DataConstraint{}, outage)
			allowed, err = probe(1, 11, id)
			require.ErrorIs(t, err, outage)
			require.False(t, allowed)
		})
	}
}

func TestProjectReviewConfigurationHTTPKeepsLocalVersionAndCurrentAuthority(t *testing.T) {
	m, _, ctx, _ := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	created, err := m.createReviewTypeHTTP(ctx, &reviewTypeCreateInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, Key: "fixture-type", Body: reviewTypeFixture()})
	require.NoError(t, err)
	typ := created.Body.Data
	input := &reviewTypeUpdateInput{RequirementReviewTypePath: RequirementReviewTypePath{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, TypeID: typ.ID.String()}, ProjectID: "99", Key: "fixture-project-type"}
	input.Body.Version = typ.Version
	input.Body.Settings = reviewTypeFixture()
	input.Body.Settings.Name = "Local project review"
	updated, err := m.updateReviewTypeHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Body.Data.Version)
	list := &reviewTypeListInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, ProjectID: "99", Limit: 10}
	local, err := m.listReviewTypesHTTP(ctx, list)
	require.NoError(t, err)
	require.Len(t, local.Body.Data, 1)
	require.Equal(t, "Local project review", local.Body.Data[0].Settings.Name)
	require.Equal(t, 2, local.Body.Data[0].Version)
	list.ProjectID = ""
	global, err := m.listReviewTypesHTTP(ctx, list)
	require.NoError(t, err)
	require.Len(t, global.Body.Data, 1)
	require.Equal(t, reviewTypeFixture().Name, global.Body.Data[0].Settings.Name)
	replayed, err := m.updateReviewTypeHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, updated.Body.Data, replayed.Body.Data)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.updateReviewTypeHTTP(ctx, input)
	require.Error(t, err, "revoked authority cannot replay a previously authorized write")
	_, err = m.listReviewTypesHTTP(ctx, list)
	require.Error(t, err)
}

func TestBudgetStorageOutageAndMissingPublisherCannotPersistPartialSpend(t *testing.T) {
	for _, scenario := range []string{"read", "write", "publisher"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, draft := projectHarness(t)
			m := &Module{service: s}
			capMicros := int64(100)
			draft.project.ProjectID = 10
			draft.project.BudgetCapMicros = &capMicros
			require.NoError(t, db.Table(s.names.Projects.String()).Create(&draft.project).Error)
			var before map[string]any
			require.NoError(t, db.Table(s.names.Projects.String()).Where("project_id = ?", 10).Take(&before).Error)
			outage := errors.New("budget storage unavailable")
			switch scenario {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:budget", func(tx *gorm.DB) { _ = tx.AddError(outage) }))
			case "write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression:budget", func(tx *gorm.DB) { _ = tx.AddError(outage) }))
			case "publisher":
				s.events = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.spendProjectBudgetOn(t.Context(), tx, 1, 10, 100) })
			if scenario == "publisher" {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, outage)
			}
			if scenario == "read" {
				require.NoError(t, db.Callback().Query().Remove("regression:budget"))
			}
			if scenario == "write" {
				require.NoError(t, db.Callback().Update().Remove("regression:budget"))
			}
			var after map[string]any
			require.NoError(t, db.Table(s.names.Projects.String()).Where("project_id = ?", 10).Take(&after).Error)
			require.Equal(t, before, after, "spend, threshold state and budget hold must roll back together")
		})
	}
}

func TestAgentReviewParticipationNeedsCurrentIdentityAndMembership(t *testing.T) {
	for _, scenario := range []string{"allowed", "human_source", "agent_source", "wrong_tenant", "revoked_agent", "member_source", "revoked_member", "policy_source", "policy_denied"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			outage := errors.New("review participant authority unavailable")
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				if scenario == "human_source" {
					return identity.Principal{}, false, outage
				}
				return identity.Principal{}, false, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if scenario == "agent_source" {
					return fleet.AgentIdentity{}, false, outage
				}
				agent := fleet.AgentIdentity{ID: 77, PrincipalID: 11, TenantID: 1, State: "ACTIVE"}
				if scenario == "wrong_tenant" {
					agent.TenantID = 2
				}
				if scenario == "revoked_agent" {
					agent.State = "REVOKED"
				}
				return agent, true, nil
			}
			m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
				if scenario == "member_source" {
					return false, outage
				}
				return scenario != "revoked_member", nil
			})
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "policy_source" {
					return authz.DataConstraint{}, outage
				}
				return authz.DataConstraint{AllowAll: scenario != "policy_denied"}, nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.sheetAuthorityOn(ctx, tx, 99, "review_participate") })
			switch scenario {
			case "allowed":
				require.NoError(t, err)
			case "wrong_tenant", "revoked_agent", "revoked_member", "policy_denied":
				require.ErrorIs(t, err, ErrWorkForbidden)
			default:
				require.ErrorIs(t, err, outage)
			}
			want := ErrWorkForbidden
			if scenario == "human_source" {
				want = outage
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.sheetAuthorityOn(ctx, tx, 99, "review_initiate") }), want, "Agent participation must never authorize initiating a Human review")
		})
	}
}

func TestReviewDefaultsNeverAcceptUnavailableOrRevokedIdentities(t *testing.T) {
	for _, scenario := range []string{"human_source", "agent_source", "missing_agent_source", "foreign_agent", "revoked_agent", "membership_source", "membership_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			outage := errors.New("default reviewer identity unavailable")
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				if scenario == "human_source" {
					return identity.Principal{}, false, outage
				}
				return identity.Principal{}, false, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if scenario == "agent_source" {
					return fleet.AgentIdentity{}, false, outage
				}
				agent := fleet.AgentIdentity{ID: 77, PrincipalID: 11, TenantID: 1, State: "ACTIVE"}
				if scenario == "foreign_agent" {
					agent.TenantID = 2
				}
				if scenario == "revoked_agent" {
					agent.State = "REVOKED"
				}
				return agent, true, nil
			}
			if scenario == "missing_agent_source" {
				m.agents.ByPrincipalOn = nil
			}
			m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
				if scenario == "membership_source" {
					return false, outage
				}
				return false, nil
			})
			err := db.Transaction(func(tx *gorm.DB) error { return m.validateReviewTypeReviewersOn(ctx, tx, 1, reviewTypeFixture()) })
			switch scenario {
			case "missing_agent_source":
				require.ErrorIs(t, err, ErrUnavailable)
			case "foreign_agent", "revoked_agent", "membership_revoked":
				require.ErrorIs(t, err, ErrInvalidMember)
			default:
				require.ErrorIs(t, err, outage)
			}
		})
	}
}

func TestKnowledgeContextDoesNotReturnMalformedOrRevokedReferences(t *testing.T) {
	for _, scenario := range []string{"membership_source", "membership_revoked", "malformed_json", "invalid_id"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, true, nil
			}
			outage := errors.New("knowledge membership unavailable")
			if scenario == "membership_source" || scenario == "membership_revoked" {
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if scenario == "membership_source" {
						return false, outage
					}
					return false, nil
				})
			} else {
				raw := "[1]"
				if scenario == "invalid_id" {
					raw = `[{"kind":"KNOWLEDGE","ref":"invalid"}]`
				}
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("context_refs", raw).Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				ids, err := m.workKnowledgeContextOn(ctx, tx, 1, 99, work.ID)
				require.Empty(t, ids)
				return err
			})
			switch scenario {
			case "membership_source":
				require.ErrorIs(t, err, outage)
			case "membership_revoked":
				require.ErrorIs(t, err, ErrWorkForbidden)
			case "malformed_json":
				require.ErrorIs(t, err, ErrUnavailable)
			case "invalid_id":
				require.ErrorIs(t, err, ErrInvalidArgument)
			}
		})
	}
}

func TestProviderFactsRequireCallerTransaction(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	for _, source := range []string{"budget", "heavy_reference", "decision"} {
		t.Run(source, func(t *testing.T) {
			read := func(module *Module, tx *gorm.DB) error {
				switch source {
				case "budget":
					_, err := module.lockProjectBudgetOn(ctx, tx, 1, 99)
					return err
				case "heavy_reference":
					return module.heavyPlanningReferenceOn(ctx, tx, 1, 99, work.ID, 123)
				default:
					_, _, err := module.decisionNotificationFactsOn(ctx, tx, 1, work.ID, "work_item")
					return err
				}
			}
			require.ErrorIs(t, read(m, nil), ErrInvalidArgument)
			require.ErrorIs(t, read(m, db), ErrInvalidArgument, "unlocked snapshots cannot replace the command transaction")
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				require.ErrorIs(t, read(&Module{}, tx), ErrUnavailable)
				return nil
			}))
		})
	}
}

func TestHistoricalAssignmentsRejectInvalidInputsAndSourceOutages(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claim := assignmentClaim(t, m, db, ctx, work, false)
	id := claim.Assignment.ID
	_, err := m.loopAssignmentAgentsOn(ctx, nil, 1, 99, []guid.ID{id})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, ids := range [][]guid.ID{nil, {0}, make([]guid.ID, 1001)} {
			facts, readErr := m.loopAssignmentAgentsOn(ctx, tx, 1, 99, ids)
			if len(ids) == 0 {
				require.NoError(t, readErr)
				require.Empty(t, facts)
			} else {
				require.ErrorIs(t, readErr, ErrInvalidArgument)
			}
		}
		for _, scope := range [][2]guid.ID{{0, 99}, {1, 0}} {
			_, readErr := m.loopAssignmentAgentsOn(ctx, tx, scope[0], scope[1], []guid.ID{id})
			require.ErrorIs(t, readErr, ErrInvalidArgument)
		}
		facts, readErr := m.loopAssignmentAgentsOn(ctx, tx, 1, 99, []guid.ID{id})
		require.NoError(t, readErr)
		require.Equal(t, map[guid.ID]guid.ID{id: 0}, facts, "Human assignment cannot become Agent history")
		return nil
	}))
	outage := errors.New("assignment history unavailable")
	router := db
	const hook = "regression_historical_assignment_outage"
	require.NoError(t, router.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == m.service.names.Assignments.String() {
			_ = tx.AddError(outage)
		}
	}))
	t.Cleanup(func() { require.NoError(t, router.Callback().Query().Remove(hook)) })
	err = router.Transaction(func(tx *gorm.DB) error {
		facts, readErr := m.loopAssignmentAgentsOn(ctx, tx, 1, 99, []guid.ID{id})
		require.Empty(t, facts, "source outage must not become zero Agent participation")
		return readErr
	})
	require.ErrorIs(t, err, outage)
}

func TestEnsembleParticipantFenceRefusesUnavailableOrChangedSources(t *testing.T) {
	for _, scenario := range []string{"participant_source", "machine_source", "invalid_principal", "principal_lock", "owner_changed", "seed_read", "recheck_read", "missing_service", "missing_lock"} {
		t.Run(scenario, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			outage := errors.New("ensemble current participant source unavailable")
			services := module.NewServices()
			require.NoError(t, module.Provide[fleet.MachineDirectory](services, fleet.MachineDirectory{ByIDOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
				if scenario == "machine_source" {
					return fleet.MachineFact{}, outage
				}
				principal := guid.ID(102)
				if scenario == "invalid_principal" {
					principal = 0
				}
				return fleet.MachineFact{ID: 2, PrincipalID: principal}, nil
			}}))
			require.NoError(t, services.Seal())
			h.m.executionHolds.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				if scenario == "participant_source" {
					return nil, outage
				}
				return []guid.ID{11}, nil
			}
			changed := false
			h.m.principals.Lock = func(_ context.Context, tx *gorm.DB, _ guid.ID) error {
				if scenario == "principal_lock" {
					return outage
				}
				if scenario == "owner_changed" && !changed {
					changed = true
					return tx.Table(h.m.service.names.WorkItems.String()).Where("id = ?", link.WorkItemID).Update("accountable_principal_id", 12).Error
				}
				return nil
			}
			var runtime fleet.EnsembleRuntime
			require.NoError(t, h.m.bindEnsembleRuntime(services, fleet.EnsembleConnector{
				CheckBranchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
					return nil
				},
				BindRuntime: func(bound fleet.EnsembleRuntime) error { runtime = bound; return nil },
			}))
			if scenario == "seed_read" || scenario == "recheck_read" {
				reads := 0
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("regression:ensemble_source", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.service.names.WorkItems.String() {
						reads++
						if (scenario == "seed_read" && reads == 1) || (scenario == "recheck_read" && reads == 2) {
							_ = tx.AddError(outage)
						}
					}
				}))
				t.Cleanup(func() { require.NoError(t, h.db.Callback().Query().Remove("regression:ensemble_source")) })
			}
			if scenario == "missing_service" {
				h.m.service = nil
			}
			if scenario == "missing_lock" {
				h.m.principals.Lock = nil
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return runtime.LockOn(h.ctx, tx, link.TenantID, link.ProjectID, link.WorkItemID, []fleet.EnsembleLaunchBranch{{PrincipalID: 91, MachineID: 2}})
			})
			switch scenario {
			case "owner_changed":
				require.ErrorIs(t, err, ErrWorkVersion)
			case "invalid_principal":
				require.ErrorIs(t, err, ErrInvalidMember)
			case "missing_service", "missing_lock":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.ErrorIs(t, err, outage)
			}
		})
	}
}

func TestReviewConfigurationSinkFailuresPreserveTeamAndProjectCopies(t *testing.T) {
	for _, target := range []string{"team_create", "team_replace", "project_override", "sheet_create", "opinion"} {
		for _, sink := range []string{"audit", "outbox", "identifier"} {
			if (target == "project_override" || target == "team_replace") && sink == "identifier" {
				continue // Existing configuration reuses the team's row identity.
			}
			t.Run(target+"/"+sink, func(t *testing.T) {
				m, db, ctx, req := sheetHarness(t)
				m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				var typ RequirementReviewTypeView
				var sheet RequirementReviewSheetView
				if target == "team_replace" || target == "project_override" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						typ, err = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture())
						return err
					}))
				}
				if target == "opinion" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						var err error
						sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
						return err
					}))
				}
				tables := []string{m.service.names.ReviewTypes.String(), m.service.names.ReviewSheets.String(), m.service.names.ReviewOpinions.String(), m.service.names.Requirements.String()}
				snapshot := func() []string {
					t.Helper()
					var values []string
					for _, table := range tables {
						var rows []map[string]any
						require.NoError(t, db.Table(table).Order("id").Find(&rows).Error)
						encoded, err := json.Marshal(rows)
						require.NoError(t, err)
						values = append(values, string(encoded))
					}
					return values
				}
				before := snapshot()
				outage := errors.New("review command sink unavailable")
				switch sink {
				case "audit":
					m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return outage }
				case "outbox":
					m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, outage })
				case "identifier":
					m.ids = draftIDSource(func() (int64, error) { return 0, outage })
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					switch target {
					case "team_create", "team_replace":
						_, err := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, typ.Version, reviewTypeFixture())
						return err
					case "project_override":
						_, err := m.saveProjectReviewTypeOn(ctx, tx, 1, 99, typ.ID, typ.Version, reviewTypeFixture())
						return err
					case "sheet_create":
						_, err := m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
						return err
					default:
						input := &sheetActionInput{}
						input.Body.Version, input.Body.Action = sheet.Version, "OPINION"
						input.Body.Opinion = RequirementReviewOpinion{Decision: "PASS"}
						_, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, input)
						return err
					}
				})
				require.ErrorIs(t, err, outage)
				require.Equal(t, before, snapshot(), "failed sinks must preserve all frozen configuration, target and opinion facts")
			})
		}
	}
}

func TestReviewOpinionHistoryRefusesEveryReadOutageAndCorruptReceipt(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return err
	}))
	input := &sheetActionInput{}
	input.Body.Version, input.Body.Action = sheet.Version, "OPINION"
	input.Body.Opinion = RequirementReviewOpinion{Decision: "PASS", Comment: "Independently checked criterion"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, input); return err }))
	request := &requirementReviewHistoryInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: sheet.ID.String()}, Limit: 10}
	good, err := m.listReviewOpinionsHTTP(ctx, request)
	require.NoError(t, err)
	require.Len(t, good.Body.Data, 1)
	require.Equal(t, input.Body.Opinion.Comment, good.Body.Data[0].Opinion.Comment)
	reads, failAt := 0, 0
	outage := errors.New("immutable opinion history storage unavailable")
	router := m.runtime.Database().Write()
	require.NoError(t, router.Callback().Query().Before("gorm:query").Register("regression:opinion_history", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			_ = tx.AddError(outage)
		}
	}))
	t.Cleanup(func() { require.NoError(t, router.Callback().Query().Remove("regression:opinion_history")) })
	_, err = m.listReviewOpinionsHTTP(ctx, request)
	require.NoError(t, err)
	observed := reads
	require.Positive(t, observed)
	for at := 1; at <= observed; at++ {
		failAt, reads = at, 0
		got, err := m.listReviewOpinionsHTTP(ctx, request)
		require.Error(t, err)
		require.Nil(t, got, "a read outage cannot become empty or partial immutable opinion history")
	}
	failAt = 0
	require.NoError(t, db.Table(m.service.names.ReviewOpinions.String()).Where("sheet_id = ?", sheet.ID).Update("document", "{corrupted").Error)
	got, err := m.listReviewOpinionsHTTP(ctx, request)
	require.Error(t, err)
	require.Nil(t, got, "malformed immutable receipt must be refused")
}

func TestReviewConfigurationReadsRefuseEveryStorageOutageAndCorruptCopy(t *testing.T) {
	for _, project := range []string{"", "99"} {
		t.Run("project="+project, func(t *testing.T) {
			m, db, ctx, _ := sheetHarness(t)
			m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			var typ RequirementReviewTypeView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				typ, err = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, reviewTypeFixture())
				return err
			}))
			request := &reviewTypeListInput{RequirementReviewTypeTenantPath: RequirementReviewTypeTenantPath{TenantID: "1"}, ProjectID: project, Limit: 10}
			reads, failAt := 0, 0
			outage := errors.New("review configuration snapshot unavailable")
			router := m.runtime.Database().Write()
			require.NoError(t, router.Callback().Query().Before("gorm:query").Register("regression:configuration_read", func(tx *gorm.DB) {
				reads++
				if reads == failAt {
					_ = tx.AddError(outage)
				}
			}))
			t.Cleanup(func() { require.NoError(t, router.Callback().Query().Remove("regression:configuration_read")) })
			good, err := m.listReviewTypesHTTP(ctx, request)
			require.NoError(t, err)
			require.Len(t, good.Body.Data, 1)
			require.Equal(t, typ.ID, good.Body.Data[0].ID)
			observed := reads
			require.Positive(t, observed)
			for at := 1; at <= observed; at++ {
				failAt, reads = at, 0
				got, err := m.listReviewTypesHTTP(ctx, request)
				require.Error(t, err)
				require.Nil(t, got, "partial or absent configuration cannot replace the actual snapshot")
			}
			failAt = 0
			require.NoError(t, db.Table(m.service.names.ReviewTypes.String()).Where("id = ?", typ.ID).Update("document", "{corrupted").Error)
			got, err := m.listReviewTypesHTTP(ctx, request)
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}

func TestReviewInboxUsesCurrentInvitationAndRefusesCorruptDocument(t *testing.T) {
	for _, scenario := range []string{"wrong_tenant", "wrong_actor", "invalid_limit", "invalid_project", "other_project", "not_invited", "hidden_project", "corrupted_document"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, req := sheetHarness(t)
			var sheet RequirementReviewSheetView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
				return err
			}))
			request := governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "REVIEW_SHEET", Limit: 10}
			switch scenario {
			case "wrong_tenant":
				request.TenantID = 2
			case "wrong_actor":
				request.PrincipalID = 12
			case "invalid_limit":
				request.Limit = 102
			case "invalid_project":
				request.ProjectID = -1
			case "other_project":
				request.ProjectID = 100
			case "not_invited":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12})
				request.PrincipalID = 12
			case "hidden_project":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "corrupted_document":
				require.NoError(t, db.Table(m.service.names.ReviewSheets.String()).Where("id = ?", sheet.ID).Update("document", "{corrupted").Error)
			}
			items, err := m.reviewSheetInbox(ctx, request)
			require.Empty(t, items)
			switch scenario {
			case "other_project", "not_invited", "hidden_project":
				require.NoError(t, err)
			case "wrong_tenant", "wrong_actor":
				require.ErrorIs(t, err, ErrWorkForbidden)
			case "invalid_limit", "invalid_project":
				require.ErrorIs(t, err, ErrInvalidArgument)
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestReviewCreationNeverSubstitutesUnavailableReviewerIdentity(t *testing.T) {
	for _, scenario := range []string{"human_source", "agent_source", "missing_agent_source", "wrong_agent", "membership_source", "membership_revoked", "identifier_zero"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, req := sheetHarness(t)
			outage := errors.New("current reviewer identity unavailable")
			originalHuman := m.principals.ActiveHumanByID
			m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if id != 12 || scenario == "identifier_zero" || strings.HasPrefix(scenario, "membership_") {
					return originalHuman(ctx, tx, id)
				}
				if scenario == "human_source" {
					return identity.Principal{}, false, outage
				}
				return identity.Principal{}, false, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if scenario == "agent_source" {
					return fleet.AgentIdentity{}, false, outage
				}
				return fleet.AgentIdentity{ID: 77, PrincipalID: 12, TenantID: 2, State: "ACTIVE"}, true, nil
			}
			if scenario == "missing_agent_source" {
				m.agents.ByPrincipalOn = nil
			}
			if strings.HasPrefix(scenario, "membership_") {
				m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _ guid.ID, principal guid.ID) (bool, error) {
					if principal != 12 {
						return true, nil
					}
					if scenario == "membership_source" {
						return false, outage
					}
					return false, nil
				})
			}
			if scenario == "identifier_zero" {
				m.ids = draftIDSource(func() (int64, error) { return 0, nil })
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{12}, Minimum: 1})
				return err
			})
			switch scenario {
			case "missing_agent_source", "wrong_agent", "membership_revoked":
				require.ErrorIs(t, err, ErrInvalidMember)
			case "identifier_zero":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.ErrorIs(t, err, outage)
			}
			var sheets int64
			require.NoError(t, db.Table(m.service.names.ReviewSheets.String()).Count(&sheets).Error)
			require.Zero(t, sheets)
			stored, err := query.BindRequirements(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, req.Status, stored.Status, "refused review must not transition the requirement into review")
			require.Equal(t, req.Version, stored.Version)
		})
	}
}

func TestProjectControlFactsUseActualEpisodesAndCurrentRecipientAuthority(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var receipt ProjectModeReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "AUTONOMOUS", Reason: "Human explicitly enables project automation", ReauthProof: "proof"}, "cookie", false, []guid.ID{11})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, found, err := m.projectModeFactOn(ctx, tx, 1, receipt.ModeChange.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, receipt.ModeChange.ID, fact.OccurrenceID)
		require.Equal(t, receipt.ModeChange.OccurredAt, fact.CreatedAt)
		require.Equal(t, "AUTONOMOUS", fact.ToMode)
		allowed, err := m.projectControlReadableOn(ctx, tx, 1, 99, 11)
		require.NoError(t, err)
		require.True(t, allowed)
		allowed, err = m.projectControlReadableOn(ctx, tx, 1, 99, 0)
		require.NoError(t, err)
		require.False(t, allowed)
		return nil
	}))
	m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		allowed, err := m.projectControlReadableOn(ctx, tx, 1, 99, 11)
		require.NoError(t, err)
		require.False(t, allowed, "revoked membership removes visibility of a historical control episode")
		return nil
	}))
	outage := errors.New("current control recipient source unavailable")
	m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, outage })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		allowed, err := m.projectControlReadableOn(ctx, tx, 1, 99, 11)
		require.False(t, allowed)
		return err
	}), outage)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:mode_fact", func(tx *gorm.DB) {
		if tx.Statement.Table == m.service.names.ModeChanges.String() {
			_ = tx.AddError(outage)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("regression:mode_fact")) })
	_, found, err := m.projectModeFactOn(ctx, db, 1, receipt.ModeChange.ID)
	require.False(t, found)
	require.ErrorIs(t, err, outage, "a missing source cannot erase an actual control episode")
}

func TestHumanOnlyReleaseRollsBackEveryFailedSource(t *testing.T) {
	for _, fault := range []string{"", "assignment_query", "assignment_update", "work_update", "audit"} {
		t.Run("fault="+fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			claimed := assignmentClaim(t, m, db, ctx, work, false)
			// Persisted Agent assignment fixture; no Execution or result is invented.
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id = ?", claimed.Assignment.ID).Updates(map[string]any{"assignee_kind": "AGENT", "agent_id": 77}).Error)
			projects := query.BindProjects(db, m.service.names)
			project, err := projects.WithContext(ctx).First()
			require.NoError(t, err)
			before, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			outage := errors.New("human-only release source unavailable")
			if fault == "assignment_query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:release", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Assignments.String() {
						_ = tx.AddError(outage)
					}
				}))
			}
			if fault == "assignment_update" || fault == "work_update" {
				table := m.service.names.Assignments.String()
				if fault == "work_update" {
					table = m.service.names.WorkItems.String()
				}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression:release", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						_ = tx.AddError(outage)
					}
				}))
			}
			if fault == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return outage }
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				return m.releaseUnstartedAgentAssignmentsOn(ctx, tx, project, 11, "ROLE:owner", time.Now().UTC())
			})
			if fault == "assignment_query" {
				require.NoError(t, db.Callback().Query().Remove("regression:release"))
			}
			if fault == "assignment_update" || fault == "work_update" {
				require.NoError(t, db.Callback().Update().Remove("regression:release"))
			}
			row, readErr := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, readErr)
			after, readErr := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, readErr)
			if fault != "" {
				require.ErrorIs(t, err, outage)
				require.Equal(t, before, after)
				require.Equal(t, "ACTIVE", row.Status)
				require.Nil(t, row.EndedAt)
			} else {
				require.NoError(t, err)
				require.Equal(t, "RELEASED", row.Status)
				require.Equal(t, "PROJECT_HUMAN_ONLY", *row.EndReason)
				require.Nil(t, row.ActiveSlot)
				require.Equal(t, "READY", after.State)
				require.Nil(t, after.CurrentExecutionID)
				require.Nil(t, after.CurrentClaimID)
			}
		})
	}
}

func TestAndonNeverConfusesRequestedAndAcknowledgedRuntimePauses(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(fmt.Sprint(acknowledged), func(t *testing.T) {
			m, db, ctx, work, _ := dependencyHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
			}
			m.executionHolds.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				return []guid.ID{11}, nil
			}
			m.executionHolds.ChangeOn = func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, release bool) ([]ExecutionHoldFact, error) {
				require.False(t, release)
				control := "PAUSE_REQUESTED"
				if acknowledged {
					control = "PAUSED"
				}
				return []ExecutionHoldFact{{ExecutionID: 1000 + id, State: "RUNNING", Control: control}}, nil
			}
			var receipt andonView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = m.changeAndonOn(ctx, tx, 1, 99, work.ID, 11, 11, andonBody{Version: work.Version, Reason: "Investigate actual source inconsistency"}, false)
				return err
			}))
			require.NotEmpty(t, receipt.Executions)
			want := "REQUESTED"
			if acknowledged {
				want = "ACKNOWLEDGED"
			}
			require.Equal(t, want, receipt.RunnerPause)
			require.True(t, receipt.Work.AndonActive)
		})
	}
}

func TestEnsembleCompositionRefusesIncompleteCapabilities(t *testing.T) {
	for _, scenario := range []string{"missing_directory", "missing_reader", "missing_checker", "binding_failure"} {
		t.Run(scenario, func(t *testing.T) {
			services := module.NewServices()
			directory := fleet.MachineDirectory{ByIDOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
				return fleet.MachineFact{}, nil
			}}
			if scenario == "missing_reader" {
				directory.ByIDOn = nil
			}
			if scenario != "missing_directory" {
				require.NoError(t, module.Provide[fleet.MachineDirectory](services, directory))
			}
			require.NoError(t, services.Seal())
			bound := false
			outage := errors.New("ensemble runtime binding unavailable")
			connector := fleet.EnsembleConnector{
				CheckBranchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
					return nil
				},
				BindRuntime: func(fleet.EnsembleRuntime) error { bound = true; return outage },
			}
			if scenario == "missing_checker" {
				connector.CheckBranchOn = nil
			}
			err := (&Module{}).bindEnsembleRuntime(services, connector)
			switch scenario {
			case "missing_directory":
				require.ErrorIs(t, err, module.ErrServiceMissing)
			case "binding_failure":
				require.ErrorIs(t, err, outage)
			default:
				require.ErrorIs(t, err, ErrUnavailable)
			}
			require.Equal(t, scenario == "binding_failure", bound, "an incomplete connector must never receive a runnable runtime")
		})
	}
}

func TestEnsembleAssignmentCannotBypassCurrentProjectBranchLimit(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = ?", 99).Update("ensemble_max_branches", 1).Error)
	branches := []fleet.EnsembleLaunchBranch{{PrincipalID: 12, AssignmentID: 81, ExecutionID: 91}, {PrincipalID: 13, AssignmentID: 82, ExecutionID: 92}}
	err := db.Transaction(func(tx *gorm.DB) error { return m.assignEnsembleOn(ctx, tx, 1, 99, work.ID, 80, branches) })
	require.ErrorIs(t, err, ErrAssignmentState)
	var count int64
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
	require.Zero(t, count, "oversized approved-plan input must not create even the first assignment")
	outage := errors.New("current project ensemble policy unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression:ensemble_limit", func(tx *gorm.DB) {
		if tx.Statement.Table == m.service.names.Projects.String() {
			_ = tx.AddError(outage)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("regression:ensemble_limit")) })
	err = db.Transaction(func(tx *gorm.DB) error { return m.assignEnsembleOn(ctx, tx, 1, 99, work.ID, 80, branches[:1]) })
	require.ErrorIs(t, err, outage)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestExecutionEvidenceRefusesUnavailablePinSourcesWithoutPartialEvidence(t *testing.T) {
	for _, scenario := range []string{"missing_files", "unknown_criterion", "evidence_identifier", "binding_identifier", "missing_claims", "pin_source"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			outage := errors.New("execution evidence pin source unavailable")
			ctx := h.ctx
			input := ExecutionEvidence{Artifact: &ExecutionArtifact{SpaceID: 500, FileID: 501, VersionID: 502, FileVersion: 3, Coverage: "COVERED", SHA256: strings.Repeat("a", 64)}}
			want := outage
			switch scenario {
			case "missing_files":
				h.m.resultFiles, want = nil, ErrUnavailable
			case "unknown_criterion":
				id := guid.ID(999999)
				input.Artifact.CriterionID, want = &id, gorm.ErrRecordNotFound
			case "evidence_identifier", "binding_identifier":
				calls := 0
				h.m.ids = draftIDSource(func() (int64, error) {
					calls++
					if scenario == "evidence_identifier" || calls == 2 {
						return 0, outage
					}
					return 9999999, nil
				})
			case "missing_claims":
				ctx, want = context.Background(), ErrWorkForbidden
			case "pin_source":
				h.files.fail = outage
			}
			var before int64
			require.NoError(t, h.db.Table(h.m.service.names.ResultEvidence.String()).Count(&before).Error)
			err = h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.pinExecutionEvidenceOn(ctx, tx, ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, ExecutionID: 90, ActorID: 11}, result.ID, input)
			})
			require.ErrorIs(t, err, want)
			var after int64
			require.NoError(t, h.db.Table(h.m.service.names.ResultEvidence.String()).Count(&after).Error)
			require.Equal(t, before, after, "failure cannot create evidence or discard the original submitted source")
		})
	}
}
