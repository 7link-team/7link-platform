package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchEveryStorageBoundaryIsAtomic(t *testing.T) {
	for _, operation := range []string{"query", "create", "update"} {
		for boundary := 1; boundary <= 24; boundary++ {
			t.Run(fmt.Sprintf("%s/%d", operation, boundary), func(t *testing.T) {
				m, db, ctx, work := assignmentHarness(t, true)
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: id}, true, nil
				}
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
				}
				w := query.BindWorkItems(db, m.service.names)
				before, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
				require.NoError(t, err)
				failure := errors.New("dispatch storage boundary failed")
				calls, fired := 0, false
				hook := func(tx *gorm.DB) {
					calls++
					if calls == boundary {
						fired = true
						_ = tx.AddError(failure)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("dispatch_boundary", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("dispatch_boundary", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dispatch_boundary", hook))
				}
				err = db.Transaction(func(tx *gorm.DB) error {
					_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 9000, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version, Reason: "Reviewed candidate"})
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("dispatch_boundary"))
				require.NoError(t, db.Callback().Create().Remove("dispatch_boundary"))
				require.NoError(t, db.Callback().Update().Remove("dispatch_boundary"))
				after, readErr := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
				require.NoError(t, readErr)
				var assignments int64
				require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&assignments).Error)
				if fired {
					require.Error(t, err)
					require.Equal(t, before, after, "every failed storage boundary must roll back the canonical work")
					require.Zero(t, assignments, "no assignment may survive a rejected dispatch")
				} else {
					require.NoError(t, err)
					require.EqualValues(t, 1, assignments)
					require.Equal(t, "CLAIMED", after.State)
					require.Nil(t, after.CurrentExecutionID, "assignment alone cannot invent execution")
				}
			})
		}
	}
}

func TestPublishedCapabilitiesRejectDuplicateServices(t *testing.T) {
	providers := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide(s, SelectedAgentWork{}) },
		func(s *module.Services) error { return module.Provide(s, IntakeAppender{}) },
		func(s *module.Services) error { return module.Provide(s, governance.DispatchAutoConsumer{}) },
		func(s *module.Services) error { return module.Provide(s, DispatchExecutionConnector{}) },
		func(s *module.Services) error { return module.Provide(s, ProjectCancellationConnector{}) },
		func(s *module.Services) error { return module.Provide(s, DispatchAccess{}) },
		func(s *module.Services) error { return module.Provide(s, ForecastWorkFacts{}) },
		func(s *module.Services) error { return module.Provide(s, PackageSourceFacts{}) },
	}
	for i, provide := range providers {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := module.NewServices()
			require.NoError(t, provide(s))
			require.ErrorIs(t, (&Module{}).provideClientWork(s), module.ErrServiceDuplicate)
		})
	}
}

func TestForecastCountsOnlyCurrentBaselineCriteria(t *testing.T) {
	m, db, ctx, requirement, _ := scopeHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, e := (clientWork{m: m}).forecastWorkOn(ctx, tx, 1, 99, &requirement.ID, time.Now().UTC())
		require.NoError(t, e)
		require.EqualValues(t, 2, facts.ScopeCriteria)
		require.Positive(t, facts.ScopeCriteria)
		require.Nil(t, facts.SpentMicros, "project spending cannot be invented for one requirement")
		return nil
	}))
}

func TestDispatchAuthorityAndStopReadFailuresReturnNoReceipt(t *testing.T) {
	for _, operation := range []string{"authority", "stop"} {
		for boundary := 1; boundary <= 8; boundary++ {
			t.Run(fmt.Sprintf("%s/%d", operation, boundary), func(t *testing.T) {
				m, db, ctx, work := assignmentHarness(t, true)
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
				}
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 88).Error)
				a := clientWork{m: m, dispatchExecution: &DispatchExecutionAccess{StopOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (DispatchExecutionState, error) {
					return DispatchExecutionState{State: "STOPPED"}, nil
				}}}
				calls, fired := 0, false
				failure := errors.New("dispatch current authority read failed")
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("dispatch_receipt_boundary", func(tx *gorm.DB) {
					calls++
					if calls == boundary {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
				err := db.Transaction(func(tx *gorm.DB) error {
					if operation == "authority" {
						source, e := a.dispatchAuthorityOn(ctx, tx, 1, 99, work.ID, 12)
						if fired {
							require.Empty(t, source)
						} else {
							require.Equal(t, "ROLE:delivery_lead", source)
						}
						return e
					}
					state, e := a.dispatchStopOn(ctx, tx, 1, 99, work.ID, 88, 1)
					if fired {
						require.Empty(t, state)
					} else {
						require.Equal(t, "STOPPED", state.State)
					}
					return e
				})
				if fired {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestProjectReadRejectsUnparseableOwnerGrant(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error { return (clientWork{m: m}).LockProjectOn(ctx, tx, 1, 99, "project_read") })
	require.Error(t, err)
}

func TestWorkbenchPlanReadAndDecisionFailuresNeverReturnPartialData(t *testing.T) {
	for _, operation := range []string{"list", "decision"} {
		for boundary := 1; boundary <= 12; boundary++ {
			t.Run(fmt.Sprintf("%s/%d", operation, boundary), func(t *testing.T) {
				m, db, ctx, work, _ := readinessHarness(t)
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: id}, true, nil
				}
				in := &reviewPlanProposeInput{EntityID: "99", Key: "workbench-read-source"}
				in.Body.WorkItemID, in.Body.WorkItemVersion = work.ID, work.Version
				in.Body.Content = ReviewPlanContent{OutOfScopeConfirmed: true}
				for _, c := range work.Criteria {
					in.Body.Content.CriteriaCoverage = append(in.Body.Content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement exact criterion", Verification: "Verify independent evidence"})
				}
				proposal, err := m.proposeReviewPlanHTTP(ctx, in)
				require.NoError(t, err)
				calls, fired := 0, false
				failure := errors.New("workbench plan read unavailable")
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workbench_plan_read", func(tx *gorm.DB) {
					calls++
					if calls == boundary {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
				if operation == "list" {
					out, e := m.listReviewPlansHTTP(ctx, &reviewPlansListInput{EntityID: "99", WorkItemID: work.ID.String(), Limit: 50})
					err = e
					if fired {
						require.Nil(t, out)
					} else {
						require.NoError(t, e)
						require.Len(t, out.Body.Data, 1)
					}
				} else {
					reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
					decision := &reviewPlanDecisionInput{EntityID: "99", ID: proposal.Body.Data.ID.String(), Key: "workbench-independent-decision"}
					decision.Body.Version, decision.Body.ContentHash, decision.Body.Action = 1, proposal.Body.Data.ContentHash, "APPROVE"
					out, e := m.decideReviewPlanHTTP(reviewer, decision)
					err = e
					if fired {
						require.Nil(t, out)
					} else {
						require.NoError(t, e)
						require.Equal(t, "APPROVED", out.Body.Data.State)
					}
				}
				if fired {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
	for _, scenario := range []string{"invalid_project", "invalid_work", "unavailable", "policy", "invalid_scope", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			m, _, ctx, _, _ := readinessHarness(t)
			in := &reviewPlansListInput{EntityID: "99", WorkItemID: "999999", Limit: 50}
			switch scenario {
			case "invalid_project":
				in.EntityID = "invalid"
			case "invalid_work":
				in.WorkItemID = "invalid"
			case "unavailable":
				m.runtime = nil
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "invalid_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			}
			out, err := m.listReviewPlansHTTP(ctx, in)
			if scenario == "empty" {
				require.NoError(t, err)
				require.Empty(t, out.Body.Data)
			} else {
				require.Error(t, err)
				require.Nil(t, out)
			}
		})
	}
}

type inspectionProbe struct {
	files.PinnedResources
	inspect func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error)
}

func (p inspectionProbe) InspectOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	return p.inspect(ctx, tx, id)
}

func TestProgressRechecksExactFileAndBaselineBeforeReturningBytes(t *testing.T) {
	for _, scenario := range []string{"changed_file", "second_inspection", "stale_baseline"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			failure := errors.New("file permission revoked before second inspection")
			calls := 0
			h.m.resultFiles = inspectionProbe{PinnedResources: h.files, inspect: func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
				calls++
				if scenario == "second_inspection" && calls == 2 {
					return files.PinnedCandidate{}, failure
				}
				candidate, e := h.files.InspectOn(ctx, tx, id)
				if scenario == "changed_file" {
					candidate.VersionID++
				}
				return candidate, e
			}}
			if scenario == "stale_baseline" {
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_version_no", 2).Error)
			}
			err = h.db.Transaction(func(tx *gorm.DB) error {
				item, file, e := (clientWork{m: h.m}).ProgressArtifactOn(h.ctx, tx, 1, 99, ClientArtifactRef{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID})
				require.Empty(t, item)
				require.Empty(t, file)
				return e
			})
			if scenario == "changed_file" {
				require.ErrorIs(t, err, ErrResultCoverage)
			} else if scenario == "second_inspection" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, ErrResultState)
			}
		})
	}
}

func TestDispatchReplacementRequiresExistingEligibleOwnership(t *testing.T) {
	for _, reassign := range []bool{false, true} {
		t.Run(fmt.Sprint(reassign), func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, true, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}
			if !reassign {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "DRAFT").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 9001, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version, Reason: "Reviewed replacement", Reassign: reassign})
				return e
			})
			require.ErrorIs(t, err, ErrAssignmentState)
			var count int64
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestNonDelegableClientDecisionsRejectRealAgentIdentity(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
	}
	a := clientWork{m: m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, a.LockProjectOn(ctx, tx, 1, 99, "scope_change"), ErrInvalidMember)
		require.ErrorIs(t, a.ApprovePreviewShareOn(ctx, tx, 1, 99), ErrWorkForbidden)
		require.ErrorIs(t, a.ExceptionProjectOn(ctx, tx, 1, 99, "HOLD", "Investigate forecast risk", "", ""), ErrWorkForbidden)
		return nil
	}))
}

func TestAutoAuthorityReadFailureNeverReturnsPolicyAuthority(t *testing.T) {
	for boundary := 1; boundary <= 10; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
			}
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"dispatch_policy": "AUTO", "team_mode": "HYBRID", "lead_agent_id": 30}).Error)
			calls, fired := 0, false
			failure := errors.New("AUTO source unavailable")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("auto_authority_boundary", func(tx *gorm.DB) {
				calls++
				if calls == boundary {
					fired = true
					_ = tx.AddError(failure)
				}
			}))
			a := clientWork{m: m, autoAuthority: func(context.Context, *gorm.DB, governance.DispatchAutoFacts) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "POLICY:dispatch_auto"}, nil
			}}
			err := db.Transaction(func(tx *gorm.DB) error {
				source, e := a.dispatchAutoAuthorityOn(ctx, tx, 1, 99, work.ID, 21)
				if fired {
					require.Empty(t, source)
				} else {
					require.Equal(t, "POLICY:dispatch_auto", source)
				}
				return e
			})
			if fired {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAutonomyHoldBlocksOnlyAutomaticDispatchAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
	}
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"dispatch_policy": "AUTO", "team_mode": "HYBRID", "lead_agent_id": 30, "budget_cap_micros": nil}).Error)
	a := clientWork{m: m, autoAuthority: func(context.Context, *gorm.DB, governance.DispatchAutoFacts) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "POLICY:dispatch_auto"}, nil
	}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := a.dispatchAutoAuthorityOn(ctx, tx, 1, 99, work.ID, 21)
		return err
	}))
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("holds", `[{"kind":"AUTONOMY_PRECONDITION"}]`).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := a.dispatchAutoAuthorityOn(ctx, tx, 1, 99, work.ID, 21)
		return err
	}), ErrWorkForbidden)
}

func TestProgressFileInspectionFailureNeverPublishesAClaim(t *testing.T) {
	for _, source := range []string{"source", "submitted"} {
		t.Run(source, func(t *testing.T) {
			h := newResultFixture(t)
			var result ResultView
			if source == "submitted" {
				var err error
				result, err = h.submit()
				require.NoError(t, err)
			}
			h.files.fail = errors.New("file inspection unavailable")
			a := clientWork{m: h.m}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var item ClientArtifact
				var file files.PinnedCandidate
				var e error
				if source == "source" {
					item, file, e = a.SourceArtifactOn(h.ctx, tx, 1, 99, ClientSourceRef{WorkItemID: h.work, FileID: 501, VersionID: 502, SHA256: strings.Repeat("a", 64)})
				} else {
					item, file, e = a.ProgressArtifactOn(h.ctx, tx, 1, 99, ClientArtifactRef{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID})
				}
				require.Empty(t, item)
				require.Empty(t, file)
				return e
			})
			require.ErrorIs(t, err, h.files.fail)
		})
	}
}

func TestExceptionHoldSourceFailureIsAtomicAndProceedDoesNotEditProject(t *testing.T) {
	for _, scenario := range []string{"hold", "proceed", "missing_authority", "authority_error", "corrupt_holds", "update_failure", "project_query"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			failure := errors.New("current exception source failed")
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				if scenario == "authority_error" {
					return governance.AuthorityDecision{}, failure
				}
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			if scenario == "missing_authority" {
				m.deliveryAuthority = nil
			}
			if scenario == "corrupt_holds" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", `{"invalid":"hold"}`).Error)
			}
			q := query.BindProjects(db, m.service.names)
			before, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			if scenario == "project_query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("exception_boundary", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						_ = tx.AddError(failure)
					}
				}))
			}
			if scenario == "update_failure" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("exception_boundary", func(tx *gorm.DB) { _ = tx.AddError(failure) }))
			}
			decision := "HOLD"
			if scenario == "proceed" {
				decision = "PROCEED"
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return (clientWork{m: m}).ExceptionProjectOn(ctx, tx, 1, 99, decision, "Investigated forecast risk", "", "")
			})
			require.NoError(t, db.Callback().Query().Remove("exception_boundary"))
			require.NoError(t, db.Callback().Update().Remove("exception_boundary"))
			after, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			if scenario == "hold" {
				require.NoError(t, err)
				require.Contains(t, after.Holds, "TOLERANCE_EXCEPTION")
				require.Equal(t, before.Version+1, after.Version)
			} else {
				require.Equal(t, before, after)
				if scenario == "proceed" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestAdaptersRejectUnavailableSourcesWithoutInventedReceipts(t *testing.T) {
	m, db, ctx, requirement, body := scopeHarness(t)
	m.sources.GetOn = nil
	_, err := m.appendFromIntakeOn(ctx, db, 1, 99, requirement.ID, 50, requirement.Version, "cookie", body)
	require.ErrorIs(t, err, ErrUnavailable)
	failure := errors.New("Intake source query failed")
	m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return intake.IntakeRef{}, false, failure
	}
	_, err = m.appendFromIntakeOn(ctx, db, 1, 99, requirement.ID, 50, requirement.Version, "cookie", body)
	require.ErrorIs(t, err, failure)
	_, err = (&Module{}).resultHTTP(ctx, &resultInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: "1"}, ResultID: "1"})
	require.Error(t, err)
	_, err = m.resultHTTP(ctx, &resultInput{ResultWorkInput: ResultWorkInput{EntityID: "bad", WorkItemID: "1"}, ResultID: "1"})
	require.Error(t, err)
	a := clientWork{m: m}
	_, err = a.dispatchStopOn(ctx, db, 1, 99, 1, 1, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = a.packageSourcesOn(ctx, nil, 1, 99, []ClientArtifactRef{{WorkItemID: 1}})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = a.CancelProjectOn(ctx, nil, 1, 99, 1, "Human canceled project", "", "")
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestWorkbenchPlanDecisionUsesExactIndependentReceiptAndReplay(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	allowed := true
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		if !allowed {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	in := &reviewPlanProposeInput{EntityID: "99", Key: "workbench-plan-source"}
	in.Body.WorkItemID = work.ID
	in.Body.WorkItemVersion = work.Version
	in.Body.Content = ReviewPlanContent{Files: []string{}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		in.Body.Content.CriteriaCoverage = append(in.Body.Content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement exact criterion", Verification: "Verify independent evidence"})
	}
	proposal, err := m.proposeReviewPlanHTTP(ctx, in)
	require.NoError(t, err)
	decision := &reviewPlanDecisionInput{EntityID: "99", ID: proposal.Body.Data.ID.String(), Key: "workbench-plan-approval"}
	decision.Body.Version = 1
	decision.Body.ContentHash = proposal.Body.Data.ContentHash
	decision.Body.Action = "APPROVE"
	_, err = m.decideReviewPlanHTTP(ctx, decision)
	require.Error(t, err, "proposal author cannot independently approve their own plan")
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	decision.Body.ContentHash = strings.Repeat("f", 64)
	_, err = m.decideReviewPlanHTTP(reviewer, decision)
	require.Error(t, err, "approval must bind the actual immutable content hash")
	decision.Body.ContentHash = proposal.Body.Data.ContentHash
	decision.Key = "workbench-exact-plan-approval"
	approved, err := m.decideReviewPlanHTTP(reviewer, decision)
	require.NoError(t, err)
	require.Equal(t, "APPROVED", approved.Body.Data.State)
	require.Equal(t, 2, approved.Body.Data.Version)
	replay, err := m.decideReviewPlanHTTP(reviewer, decision)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, approved.Body.Data, replay.Body.Data)
	allowed = false
	_, err = m.decideReviewPlanHTTP(reviewer, decision)
	require.Error(t, err, "a cached receipt must not bypass current authority")
	allowed = true
	page, err := m.listReviewPlansHTTP(reviewer, &reviewPlansListInput{EntityID: "99", WorkItemID: work.ID.String(), Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, "APPROVED", page.Body.Data[0].State)
	var stored model.ReviewPlan
	require.NoError(t, db.Table(m.service.names.ReviewPlans.String()).Where("id=?", proposal.Body.Data.ID).Take(&stored).Error)
	require.Equal(t, proposal.Body.Data.ContentHash, stored.ContentHash)
	require.EqualValues(t, 12, *stored.DecidedBy)
	_, err = (&Module{}).decideReviewPlanHTTP(ctx, decision)
	require.Error(t, err)
	decision.ID = "invalid"
	_, err = m.decideReviewPlanHTTP(ctx, decision)
	require.Error(t, err)
	decision.ID = "999999"
	_, err = m.decideReviewPlanHTTP(ctx, decision)
	require.Error(t, err)
}

func TestWorkbenchPlanProposalFailureRollsBackFrozenSource(t *testing.T) {
	for _, operation := range []string{"query", "create", "update"} {
		for boundary := 1; boundary <= 12; boundary++ {
			t.Run(fmt.Sprintf("%s/%d", operation, boundary), func(t *testing.T) {
				m, db, ctx, work, _ := readinessHarness(t)
				content := ReviewPlanContent{Files: []string{}, Risks: []string{}, OutOfScopeConfirmed: true}
				for _, c := range work.Criteria {
					content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement exact criterion", Verification: "Verify independent evidence"})
				}
				calls, fired := 0, false
				failure := errors.New("plan source write failed")
				hook := func(tx *gorm.DB) {
					calls++
					if calls == boundary {
						fired = true
						_ = tx.AddError(failure)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("plan_source_boundary", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("plan_source_boundary", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("plan_source_boundary", hook))
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					_, e := m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("plan_source_boundary"))
				require.NoError(t, db.Callback().Create().Remove("plan_source_boundary"))
				require.NoError(t, db.Callback().Update().Remove("plan_source_boundary"))
				var count int64
				require.NoError(t, db.Table(m.service.names.ReviewPlans.String()).Count(&count).Error)
				if fired {
					require.Error(t, err)
					require.Zero(t, count, "failed proposal cannot publish any frozen plan")
				} else {
					require.NoError(t, err)
					require.EqualValues(t, 1, count)
				}
			})
		}
	}
}

func TestDispatchFactsBudgetAndCorruptSourcesFailClosed(t *testing.T) {
	for _, scenario := range []string{"cap", "overspent", "required_tags", "preferred_tags", "holds", "missing_work", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			if scenario == "cap" || scenario == "overspent" {
				spent := 40
				if scenario == "overspent" {
					spent = 140
				}
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"budget_cap_micros": 100, "budget_spent_micros": spent}).Error)
			}
			if scenario == "required_tags" || scenario == "preferred_tags" {
				column := "skill_tags"
				if scenario == "preferred_tags" {
					column = "preferred_skill_tags"
				}
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update(column, `[42]`).Error)
			}
			if scenario == "holds" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", `{"wrong":"type"}`).Error)
			}
			id := work.ID
			if scenario == "missing_work" {
				id = 999999
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if scenario == "unavailable" {
					tx = nil
				}
				facts, e := (clientWork{m: m}).dispatchWorkOn(ctx, tx, 1, 99, id)
				if scenario == "cap" || scenario == "overspent" {
					require.NoError(t, e)
					require.NotNil(t, facts.BudgetRemaining)
					remaining := int64(60)
					if scenario == "overspent" {
						remaining = 0
					}
					require.Equal(t, remaining, *facts.BudgetRemaining)
				}
				return e
			})
			if scenario != "cap" && scenario != "overspent" {
				require.Error(t, err)
			}
		})
	}
}

func TestPreviewAndSignatureStorageFailuresDoNotPublishPartialFacts(t *testing.T) {
	for _, operation := range []string{"signature", "progress", "source"} {
		for boundary := 1; boundary <= 20; boundary++ {
			t.Run(fmt.Sprintf("%s/%d", operation, boundary), func(t *testing.T) {
				failure := errors.New("collaboration source read failed")
				calls, fired := 0, false
				hook := func(tx *gorm.DB) {
					calls++
					if calls == boundary {
						fired = true
						_ = tx.AddError(failure)
					}
				}
				if operation == "signature" {
					m, db, ctx, body := requirementHarness(t)
					var id guid.ID
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { r, e := m.createRequirementOn(ctx, tx, 1, 99, 11, body); id = r.ID; return e }))
					a := clientWork{m: m}
					items, err := a.RequirementsOn(ctx, db, 1, 99, []guid.ID{id})
					require.NoError(t, err)
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("signature_read_boundary", hook))
					err = db.Transaction(func(tx *gorm.DB) error { return a.SignRequirementsOn(ctx, tx, 1, 99, items) })
					require.NoError(t, db.Callback().Query().Remove("signature_read_boundary"))
					var versions int64
					require.NoError(t, db.Table(m.service.names.RequirementVersions.String()).Count(&versions).Error)
					if fired {
						require.Error(t, err)
						require.Zero(t, versions)
					} else {
						require.NoError(t, err)
						require.EqualValues(t, 1, versions)
					}
					return
				}
				h := newResultFixture(t)
				var result ResultView
				if operation == "progress" {
					var err error
					result, err = h.submit()
					require.NoError(t, err)
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("preview_read_boundary", hook))
				err := h.db.Transaction(func(tx *gorm.DB) error {
					a := clientWork{m: h.m}
					var item ClientArtifact
					var file files.PinnedCandidate
					var e error
					if operation == "progress" {
						item, file, e = a.ProgressArtifactOn(h.ctx, tx, 1, 99, ClientArtifactRef{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID})
					} else {
						item, file, e = a.SourceArtifactOn(h.ctx, tx, 1, 99, ClientSourceRef{WorkItemID: h.work, FileID: 501, VersionID: 502, SHA256: strings.Repeat("a", 64)})
					}
					if fired {
						require.Empty(t, item)
						require.Empty(t, file)
					} else {
						require.EqualValues(t, 502, item.VersionID)
					}
					return e
				})
				if fired {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestAcceptedPackageArtifactEveryReadFailureReturnsNoProjection(t *testing.T) {
	for boundary := 1; boundary <= 30; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(result.ID, 12, "PASSED")
			require.NoError(t, err)
			_, err = h.accept(result.ID, 12)
			require.NoError(t, err)
			calls, fired := 0, false
			failure := errors.New("accepted artifact read unavailable")
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("artifact_boundary", func(tx *gorm.DB) {
				calls++
				if calls == boundary {
					fired = true
					_ = tx.AddError(failure)
				}
			}))
			err = h.db.Transaction(func(tx *gorm.DB) error {
				items, e := (clientWork{m: h.m}).AcceptedArtifactsOn(h.ctx, tx, 1, 99, []ClientArtifactRef{{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID}})
				if fired {
					require.Nil(t, items, "never return partial internal or accepted evidence on a failed read")
				} else {
					require.Len(t, items, 1)
					require.Equal(t, result.Evidence[0].SHA256, items[0].SHA256)
				}
				return e
			})
			if fired {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestForecastReadsRealRemainingAcceptedWindowAndBaseline(t *testing.T) {
	h := newResultFixture(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	w := query.BindWorkItems(h.db, h.m.service.names)
	source, e := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).First()
	require.NoError(t, e)
	for i := 1; i <= 6; i++ {
		row := *source
		row.ID = guid.ID(90000 + i)
		row.Number = 100 + i
		row.State = "ACCEPTED"
		accepted := at.Add(-time.Duration(i) * time.Hour)
		started := accepted.Add(-time.Duration(i) * time.Minute)
		row.AcceptedAt = &accepted
		row.FirstClaimedAt = &started
		if i == 5 {
			row.FirstClaimedAt = nil
		}
		if i == 6 {
			old := at.AddDate(0, 0, -31)
			row.AcceptedAt = &old
			start := old.Add(-time.Hour)
			row.FirstClaimedAt = &start
		}
		require.NoError(t, w.WithContext(h.ctx).Create(&row))
	}
	a := clientWork{m: h.m}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, e := a.forecastWorkOn(t.Context(), tx, 1, 99, nil, at)
		require.NoError(t, e)
		require.EqualValues(t, 1, facts.Remaining)
		require.Len(t, facts.Samples, 4)
		require.ElementsMatch(t, []guid.ID{90001, 90002, 90003, 90004, 90005}, facts.AcceptedWorkIDs)
		require.NotNil(t, facts.SpentMicros)
		require.Len(t, facts.WorkIDs, 7)
		return nil
	}))
}

func TestPackageSourcesReadOnlyRealTenantWorkRequirementLinkage(t *testing.T) {
	h := newResultFixture(t)
	sourceID := guid.ID(50000)
	at := time.Now().UTC()
	requirement := &model.Requirement{TenantID: 1, ID: 8000, ProjectID: 99, Number: 88, Title: "Source", Description: "Original source", Priority: 1, Status: "BASELINED", CurrentVersionNo: 1, SourceIntakeID: &sourceID, Version: 1, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(requirement))
	w := query.BindWorkItems(h.db, h.m.service.names)
	_, e := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).UpdateSimple(w.RequirementID.Value(requirement.ID))
	require.NoError(t, e)
	a := clientWork{m: h.m}
	refs := []ClientArtifactRef{{WorkItemID: h.work, ResultID: 20, EvidenceID: 30}, {WorkItemID: 999999, ResultID: 21, EvidenceID: 31}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, e := a.packageSourcesOn(h.ctx, tx, 1, 99, refs)
		require.NoError(t, e)
		require.Equal(t, []PackageSource{{WorkItemID: h.work, RequirementID: 8000, IntakeID: sourceID}, {WorkItemID: 999999}}, facts)
		foreign, e := a.packageSourcesOn(h.ctx, tx, 2, 99, refs)
		require.NoError(t, e)
		require.Zero(t, foreign[0].IntakeID)
		require.Zero(t, foreign[0].RequirementID)
		return nil
	}))
	_, e = a.packageSourcesOn(h.ctx, h.db, 1, 99, refs)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

// Partial package rejection must retain only genuine source linkage. A stale
// requirement is unknown, while a failed source query aborts the whole receipt.
func TestPackageReworkSourcesDoNotInventOrPartiallyReturnLineage(t *testing.T) {
	for _, scenario := range []string{"no_requirement", "requirement_without_intake", "work_failure", "requirement_failure"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			id := guid.ID(8000)
			if scenario != "no_requirement" {
				at := time.Now().UTC()
				require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.Requirement{TenantID: 1, ID: id, ProjectID: 99, Number: 88, Title: "Original requirement", Description: "Source", Priority: 1, Status: "BASELINED", CurrentVersionNo: 1, Version: 1, CreatedAt: at, UpdatedAt: at}))
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Update("requirement_id", id).Error)
			}
			failure := errors.New("lineage source unavailable")
			target := ""
			if scenario == "work_failure" {
				target = h.m.service.names.WorkItems.String()
			} else if scenario == "requirement_failure" {
				target = h.m.service.names.Requirements.String()
			}
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("lineage_failure", func(tx *gorm.DB) {
				if target != "" && tx.Statement.Table == target {
					_ = tx.AddError(failure)
				}
			}))
			a := clientWork{m: h.m}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				facts, e := a.packageSourcesOn(h.ctx, tx, 1, 99, []ClientArtifactRef{{WorkItemID: 999999}, {WorkItemID: h.work}})
				if target != "" {
					require.Nil(t, facts, "a failed source must not leak an earlier partial result")
					return e
				}
				require.NoError(t, e)
				want := PackageSource{WorkItemID: h.work}
				if scenario == "requirement_without_intake" {
					want.RequirementID = id
				}
				require.Equal(t, []PackageSource{{WorkItemID: 999999}, want}, facts)
				return nil
			})
			if target != "" {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRequirementReadScopeUsesRequirementCreator(t *testing.T) {
	for _, projectOwner := range []guid.ID{11, 12} {
		for _, requirementOwner := range []guid.ID{11, 12} {
			t.Run(projectOwner.String()+"/"+requirementOwner.String(), func(t *testing.T) {
				m, db, ctx, body := requirementHarness(t)
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				scoped := *claims
				scoped.EntityID = 99
				ctx = authn.WithClaims(ctx, &scoped)
				var id guid.ID
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					view, e := m.createRequirementOn(ctx, tx, 1, 99, 11, body)
					id = view.ID
					return e
				}))
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("created_by", projectOwner).Error)
				require.NoError(t, db.Table(m.service.names.Requirements.String()).Where("id=?", id).Update("created_by", requirementOwner).Error)
				m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
					require.Equal(t, "requirement_read", permission)
					return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					return (clientWork{m: m}).LockRequirementReadOn(ctx, tx, 1, 99, id)
				})
				_, canonical := m.readRequirement(ctx, db, 1, 99, id, 11)
				if requirementOwner == 11 {
					require.NoError(t, err)
					require.NoError(t, canonical)
				} else {
					require.Error(t, err)
					require.Error(t, canonical)
				}
			})
		}
	}
}

func TestRequirementReadRejectsInvalidScopesAndFailedDependencies(t *testing.T) {
	for _, scenario := range []string{"nil_tx", "no_transaction", "tenant", "project", "id", "missing_policy", "policy_error", "denied", "malformed_owner", "missing_project", "archived_project", "missing_requirement", "query_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := requirementHarness(t)
			claims, ok := authn.FromContext(ctx)
			require.True(t, ok)
			scoped := *claims
			scoped.EntityID = 99
			ctx = authn.WithClaims(ctx, &scoped)
			failure := errors.New("requirement authority unavailable")
			tenant, project, id := guid.ID(1), guid.ID(99), guid.ID(999)
			switch scenario {
			case "tenant":
				tenant = 2
			case "project":
				project = 98
			case "id":
				id = 0
			case "missing_policy":
				m.workPolicy = nil
			case "policy_error":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "denied":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{DeniedIDs: []string{"99"}}, nil
				}
			case "malformed_owner":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			case "missing_project":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", project).Delete(&model.Project{}).Error)
			case "archived_project":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", project).Updates(map[string]any{"archived_at": time.Now().UTC(), "stage": "CLOSED"}).Error)
			case "query_error":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("requirement_scope_failure", func(tx *gorm.DB) { _ = tx.AddError(failure) }))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if scenario == "nil_tx" {
					tx = nil
				}
				if scenario == "no_transaction" {
					tx = db
				}
				return (clientWork{m: m}).LockRequirementReadOn(ctx, tx, tenant, project, id)
			})
			require.Error(t, err)
			if scenario == "policy_error" || scenario == "query_error" {
				require.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestPersonalWorkFiltersBeforeCursorAndUsesAssignmentNotClaim(t *testing.T) {
	m, db, ctx, input := workHTTPHarness(t)
	var works []workDetailView
	for i := 0; i < 5; i++ {
		input.AccountablePrincipalID = 11
		if i > 1 {
			input.AccountablePrincipalID = 12
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			row, err := m.createWorkOn(ctx, tx, 1, 99, 11, input)
			works = append(works, row)
			return err
		}))
	}
	now, future, past := time.Now().UTC(), time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(-time.Hour)
	assignments := []*model.Assignment{
		{TenantID: 1, ID: 501, ProjectID: 99, WorkItemID: works[1].ID, ActiveSlot: &works[1].ID, Role: "EXECUTOR", AssigneeKind: "HUMAN", AssigneePrincipalID: 11, Status: "ACTIVE", LeaseTTLMS: 900000, LeaseExpiresAt: &future, Version: 1, CreatedAt: now, UpdatedAt: now},
		{TenantID: 1, ID: 502, ProjectID: 99, WorkItemID: works[2].ID, ActiveSlot: &works[2].ID, Role: "EXECUTOR", AssigneeKind: "HUMAN", AssigneePrincipalID: 11, Status: "ACTIVE", LeaseTTLMS: 900000, LeaseExpiresAt: &future, Version: 1, CreatedAt: now, UpdatedAt: now},
		{TenantID: 1, ID: 503, ProjectID: 99, WorkItemID: works[3].ID, ActiveSlot: &works[3].ID, Role: "EXECUTOR", AssigneeKind: "HUMAN", AssigneePrincipalID: 11, Status: "ACTIVE", LeaseTTLMS: 900000, LeaseExpiresAt: &past, Version: 1, CreatedAt: now, UpdatedAt: now},
	}
	require.NoError(t, query.BindAssignments(db, m.service.names).WithContext(ctx).Create(assignments...))
	first, err := m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 2})
	require.NoError(t, err)
	require.True(t, first.Body.Data.HasMore)
	require.Len(t, first.Body.Data.Items, 2)
	require.Equal(t, works[2].ID, first.Body.Data.Items[0].ID)
	require.Equal(t, "executor", first.Body.Data.Items[0].Responsibility)
	require.Nil(t, first.Body.Data.Items[0].CurrentClaimID, "Human assignment is independent of an execution Claim")
	require.Equal(t, "both", first.Body.Data.Items[1].Responsibility)
	second, err := m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 2, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.False(t, second.Body.Data.HasMore)
	require.Empty(t, second.Body.Data.NextCursor)
	require.Len(t, second.Body.Data.Items, 1)
	require.Equal(t, works[0].ID, second.Body.Data.Items[0].ID)
	require.Equal(t, "accountable", second.Body.Data.Items[0].Responsibility)
	// Current authority is re-evaluated for each page; a previously issued
	// cursor cannot retain access or leak a count after grant revocation.
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied, err := m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 2, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.Empty(t, denied.Body.Data.Items)
	require.False(t, denied.Body.Data.HasMore)
}

func TestPersonalWorkScopesFailuresAndContracts(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	for _, in := range []*personalWorkInput{
		{EntityID: "0", Limit: 1}, {EntityID: "99", Limit: 0}, {EntityID: "99", Limit: 101},
		{EntityID: "99", Limit: 1, Cursor: "invalid!"},
		{EntityID: "99", Limit: 1, Cursor: personalWorkCursor(2, 99, 11, 1)},
		{EntityID: "99", Limit: 1, Cursor: personalWorkCursor(1, 98, 11, 1)},
		{EntityID: "99", Limit: 1, Cursor: personalWorkCursor(1, 99, 12, 1)},
		{EntityID: "99", Limit: 1, Cursor: personalWorkCursor(1, 99, 11, 0)},
	} {
		_, err := m.personalWorkHTTP(ctx, in)
		assertWorkStatus(t, err, 422)
	}
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "fixture-contract", Method: "GET", Path: "/entities/{entity_id}/my-work"}, func(_ context.Context, in *personalWorkInput) (*struct{ Body int }, error) {
		_, err := m.personalWorkHTTP(ctx, in)
		return &struct{ Body int }{Body: 1}, err
	})
	require.Equal(t, 422, api.Get("/entities/0/my-work").Code)
	require.Equal(t, 422, api.Get("/entities/99/my-work?limit=101").Code)
	require.Equal(t, 200, api.Get("/entities/99/my-work?limit=1").Code)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	_, err := m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 1})
	assertWorkStatus(t, err, 403)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("storage unavailable")
	}
	_, err = m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 1})
	require.Error(t, err)
	m.runtime = nil
	_, err = m.personalWorkHTTP(ctx, &personalWorkInput{EntityID: "99", Limit: 1})
	assertWorkStatus(t, err, 503)
}

func TestTenantPersonalWorkAppliesEveryDataScopeBeforePagination(t *testing.T) {
	m, db, ctx, input := workHTTPHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createWorkOn(ctx, tx, 1, 99, 11, input)
		return err
	}))
	for _, tc := range []struct {
		name       string
		constraint authz.DataConstraint
		count      int
	}{
		{"all", authz.DataConstraint{AllowAll: true}, 1},
		{"project", authz.DataConstraint{ResourceIDs: []string{"99"}}, 1},
		{"owner", authz.DataConstraint{OwnerIDs: []string{"11"}}, 1},
		{"other_owner", authz.DataConstraint{OwnerIDs: []string{"12"}}, 0},
		{"other_project", authz.DataConstraint{ResourceIDs: []string{"98"}}, 0},
		{"denial_overrides_all", authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, 0},
		{"denial_overrides_owner", authz.DataConstraint{OwnerIDs: []string{"11"}, DeniedIDs: []string{"99"}}, 0},
		{"denial_overrides_union", authz.DataConstraint{ResourceIDs: []string{"98"}, OwnerIDs: []string{"11"}, DeniedIDs: []string{"99"}}, 0},
		{"department_does_not_widen", authz.DataConstraint{DepartmentIDs: []string{"99"}}, 0},
		{"none", authz.DataConstraint{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.listConstraint = func(_ context.Context, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(11), actor)
				require.Equal(t, "work_item_read", permission)
				return tc.constraint, nil
			}
			page, err := m.personalTenantWorkHTTP(ctx, &personalTenantWorkInput{Limit: 1})
			require.NoError(t, err)
			require.Len(t, page.Body.Data.Items, tc.count)
			require.False(t, page.Body.Data.HasMore)
		})
	}
	for _, constraint := range []authz.DataConstraint{{DeniedIDs: []string{"invalid"}}, {ResourceIDs: []string{"0"}}, {OwnerIDs: []string{"invalid"}}} {
		m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) { return constraint, nil }
		_, err := m.personalTenantWorkHTTP(ctx, &personalTenantWorkInput{Limit: 1})
		assertWorkStatus(t, err, 503)
	}
	m.listConstraint = nil
	_, err := m.personalTenantWorkHTTP(ctx, &personalTenantWorkInput{Limit: 1})
	assertWorkStatus(t, err, 503)
}

func TestClientChangeAdapterUsesCanonicalReauthenticationAndBaseline(t *testing.T) {
	m, db, ctx, requirement, body := scopeHarness(t)
	adapter := clientWork{m: m}
	body.ReauthProof = "invalid"
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := adapter.ApplyClientChangeOn(ctx, tx, 1, 99, requirement.ID, requirement.Version, "cookie", body)
		return e
	}))
	q := query.BindRequirements(db, m.service.names)
	before, e := q.WithContext(ctx).Where(q.ID.Eq(requirement.ID)).First()
	require.NoError(t, e)
	require.Equal(t, 1, before.CurrentVersionNo)
	body.ReauthProof = "verified"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, e := adapter.ApplyClientChangeOn(ctx, tx, 1, 99, requirement.ID, requirement.Version, "cookie", body)
		if e == nil {
			require.Positive(t, int64(id))
		}
		return e
	}))
	after, e := q.WithContext(ctx).Where(q.ID.Eq(requirement.ID)).First()
	require.NoError(t, e)
	require.Equal(t, 2, after.CurrentVersionNo)
	require.Equal(t, *body.Title, after.Title)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := adapter.ApplyClientChangeOn(ctx, tx, 1, 99, requirement.ID, requirement.Version, "cookie", body)
		return e
	}))
}

func TestExactResultAndL3ArtifactProjection(t *testing.T) {
	h := newResultFixture(t)
	submitted, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(submitted.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(submitted.ID, 12)
	require.NoError(t, e)
	in := &resultInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, ResultID: submitted.ID.String()}
	view, e := h.m.resultHTTP(h.ctx, in)
	require.NoError(t, e)
	require.Equal(t, submitted.ID, view.Body.Data.ID)
	require.NotEmpty(t, view.Body.Data.Evidence)
	adapter := clientWork{m: h.m}
	refs := []ClientArtifactRef{{WorkItemID: h.work, ResultID: submitted.ID, EvidenceID: view.Body.Data.Evidence[0].ID}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		items, e := adapter.AcceptedArtifactsOn(h.ctx, tx, 1, 99, refs)
		require.NoError(t, e)
		require.Len(t, items, 1)
		require.Equal(t, "evidence.txt", items[0].Name)
		return e
	}))
	q := query.BindWorkItems(h.db, h.m.service.names)
	_, e = q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).UpdateSimple(q.BaselineStale.Value(true))
	require.NoError(t, e)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := adapter.AcceptedArtifactsOn(h.ctx, tx, 1, 99, refs); return e }))
	in.WorkItemID = "999"
	_, e = h.m.resultHTTP(h.ctx, in)
	require.Error(t, e)
}

func TestExactResultCannotInventAMissingDeclaration(t *testing.T) {
	h := newResultFixture(t)
	out, err := h.m.resultHTTP(h.ctx, &resultInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, ResultID: "999999"})
	require.Error(t, err)
	require.Nil(t, out)
}

func TestAcceptedArtifactFinalReadFailureDoesNotReturnFile(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 12)
	require.NoError(t, err)
	a := clientWork{m: h.m}
	ref := ClientArtifactRef{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID}
	reads := 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("final_artifact_read", func(*gorm.DB) { reads++ }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := a.ArtifactFileOn(h.ctx, tx, 1, 99, ref); return e }))
	require.Positive(t, reads)
	require.NoError(t, h.db.Callback().Query().Remove("final_artifact_read"))
	last := reads
	reads = 0
	failure := errors.New("final evidence read unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("final_artifact_failure", func(tx *gorm.DB) {
		reads++
		if reads == last {
			_ = tx.AddError(failure)
		}
	}))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		file, e := a.ArtifactFileOn(h.ctx, tx, 1, 99, ref)
		require.Empty(t, file)
		return e
	})
	require.ErrorIs(t, err, failure)
}

func TestSettingsPatchValidatesVersionAndPreservesUnrelatedFields(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return []core.EntityRoleBinding{}, nil
	}
	m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{Name: "Current project"}, nil
	}
	adapter := clientWork{m: m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, e := adapter.SettingsOn(ctx, tx, 1, 99, nil, 0)
		require.NoError(t, e)
		description, date := "Updated project brief", "2027-02-28"
		after, e := adapter.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Description: &description, TargetDate: &date}, before.Version)
		require.NoError(t, e)
		require.Equal(t, before.Version+1, after.Version)
		require.Equal(t, description, *after.Description)
		_, e = adapter.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Description: &description}, before.Version)
		require.ErrorIs(t, e, ErrWorkVersion)
		bad := "2027-02-30"
		_, e = adapter.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{TargetDate: &bad}, after.Version)
		require.ErrorIs(t, e, ErrInvalidArgument)
		empty := ""
		cleared, e := adapter.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{TargetDate: &empty}, after.Version)
		require.NoError(t, e)
		require.Nil(t, cleared.TargetDate)
		require.Equal(t, description, *cleared.Description)
		return nil
	}))
}

func TestSettingsRejectsUnavailableOrFailedOwnerWithoutChangingProject(t *testing.T) {
	for _, fault := range []string{"get_missing", "get_error", "empty", "description", "update_missing", "update_error", "project_update", "policy"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			failure := errors.New("settings owner failed")
			m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
				if fault == "get_error" {
					return core.Entity{}, failure
				}
				return core.Entity{Name: "Original"}, nil
			}
			name, description := "Updated", strings.Repeat("a", 20001)
			patch := &ClientSettingsPatch{Name: &name}
			switch fault {
			case "get_missing":
				m.service.entities.GetOn = nil
			case "empty":
				patch = &ClientSettingsPatch{}
			case "description":
				patch = &ClientSettingsPatch{Description: &description}
			case "update_error":
				m.service.entities.UpdateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, core.EntityPatch) (core.Entity, error) {
					return core.Entity{}, failure
				}
			case "project_update":
				patch = &ClientSettingsPatch{Description: &name}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("settings_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(failure)
					}
				}))
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			}
			p := query.BindProjects(db, m.service.names)
			before, e := p.WithContext(ctx).First()
			require.NoError(t, e)
			e = db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).SettingsOn(ctx, tx, 1, 99, patch, before.Version)
				return e
			})
			require.Error(t, e)
			after, e := p.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestDispatchLoadsRefusesFailedOrDeniedSources(t *testing.T) {
	for _, fault := range []string{"lock", "member", "forbidden", "policy", "query", "limit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := assignmentHarness(t, true)
			failure := errors.New("dispatch source failed")
			ids := []guid.ID{11}
			switch fault {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
			case "forbidden", "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if fault == "forbidden" {
						return authz.DataConstraint{}, ErrWorkForbidden
					}
					return authz.DataConstraint{}, failure
				}
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("dispatch_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Assignments.String() {
						tx.AddError(failure)
					}
				}))
			case "limit":
				ids = make([]guid.ID, 101)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := (clientWork{m: m}).dispatchLoadsOn(ctx, tx, 1, 99, ids)
				if fault == "forbidden" {
					require.NoError(t, e)
					require.Len(t, out, 1)
					require.False(t, out[0].Member)
				} else {
					require.Error(t, e)
					require.Empty(t, out)
				}
				return e
			})
			if fault == "forbidden" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDispatchApplyFailurePreservesWorkAndAssignments(t *testing.T) {
	for _, fault := range []string{"agent_missing", "agent_error", "agent_inactive", "human_error", "human_target_error", "human_inactive", "member", "held", "readiness", "assignment_create", "work_update", "transition", "event", "second_event", "assignment_id", "transition_id"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}
			failure := errors.New("dispatch assignment failure")
			expected := work.Version
			switch fault {
			case "agent_missing":
				m.agents.ByPrincipalOn = nil
			case "agent_error":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, failure
				}
			case "agent_inactive":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{State: "DISABLED"}, true, nil
				}
			case "human_error":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "human_target_error":
				original := m.principals.ActiveHumanByID
				m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 12 {
						return identity.Principal{}, false, failure
					}
					return original(ctx, tx, id)
				}
			case "assignment_id", "transition_id":
				calls := 0
				original := m.ids
				m.ids = draftIDSource(func() (int64, error) {
					calls++
					if calls == 1 && fault == "assignment_id" || calls == 2 && fault == "transition_id" {
						return 0, failure
					}
					return original.Next()
				})
			case "human_inactive":
				original := m.principals.ActiveHumanByID
				m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 12 {
						return identity.Principal{}, false, nil
					}
					return original(ctx, tx, id)
				}
			case "member":
				m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _, id guid.ID) (bool, error) { return id != 12, nil })
			case "held":
				assignmentClaim(t, m, db, ctx, work, false)
				expected++
			case "readiness":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("readiness_ok", false).Error)
			case "assignment_create", "transition":
				table := m.service.names.Assignments.String()
				if fault == "transition" {
					table = m.service.names.WorkTransitions.String()
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("dispatch_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(failure)
					}
				}))
			case "work_update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dispatch_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkItems.String() {
						tx.AddError(failure)
					}
				}))
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			case "second_event":
				calls := 0
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					calls++
					if calls == 2 {
						return 0, failure
					}
					return 1, nil
				})
			}
			w := query.BindWorkItems(db, m.service.names)
			before, e := w.WithContext(ctx).First()
			require.NoError(t, e)
			q := query.BindAssignments(db, m.service.names)
			assignments, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 999, AssigneePrincipalID: 12, ExpectedWorkVersion: expected, Reason: "Reviewed dispatch"})
				return e
			})
			require.Error(t, err)
			after, e := w.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
			remaining, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			require.Equal(t, assignments, remaining)
		})
	}
}

func TestSettingsRenameUsesOwnerTransactionAndRejectsInvalidNames(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return []core.EntityRoleBinding{}, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE settings_entity_probe(name TEXT)").Error)
	require.NoError(t, db.Exec("INSERT INTO settings_entity_probe VALUES ('Original')").Error)
	m.service.entities.GetOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (core.Entity, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		var name string
		e := tx.Raw("SELECT name FROM settings_entity_probe").Scan(&name).Error
		return core.Entity{Name: name}, e
	}
	m.service.entities.UpdateOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, patch core.EntityPatch) (core.Entity, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Nil(t, patch.Type)
		require.Nil(t, patch.ParentID)
		e := tx.Exec("UPDATE settings_entity_probe SET name = ?", strings.TrimSpace(*patch.Name)).Error
		return core.Entity{Name: strings.TrimSpace(*patch.Name)}, e
	}
	a := clientWork{m: m}
	name := " Renamed project "
	abort := errors.New("later command audit failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		updated, e := a.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Name: &name}, 1)
		require.NoError(t, e)
		require.Equal(t, "Renamed project", updated.Name)
		require.Equal(t, 2, updated.Version)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		before, e := a.SettingsOn(ctx, tx, 1, 99, nil, 0)
		require.NoError(t, e)
		require.Equal(t, "Original", before.Name)
		require.Equal(t, 1, before.Version)
		for _, invalid := range []string{" ", strings.Repeat("a", 201)} {
			_, e = a.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Name: &invalid}, 1)
			require.ErrorIs(t, e, ErrInvalidArgument)
		}
		updated, e := a.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Name: &name}, 1)
		require.NoError(t, e)
		require.Equal(t, "Renamed project", updated.Name)
		require.Equal(t, before.Description, updated.Description)
		require.Equal(t, before.TargetDate, updated.TargetDate)
		return nil
	}))
}

func TestAcceptedArtifactRechecksVerificationAndDisclosesImmutableOverrides(t *testing.T) {
	for _, scenario := range []string{"disclosed_override", "later_failure", "changed_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(result.ID, 12, "PASSED")
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Millisecond)
			q := query.BindVerificationRuns(h.db, h.m.service.names)
			original := &model.VerificationRun{TenantID: 1, ID: 902, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, CriterionID: *result.Evidence[0].CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "FAILED", Reason: "NONZERO_EXIT", ReceiptHash: strings.Repeat("a", 64), ActorID: 12, CreatedAt: now, CompletedAt: &now}
			require.NoError(t, q.WithContext(h.ctx).Create(original))
			h.m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			_, err = h.command(13, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
				return h.m.overrideVerificationOn(ctx, tx, 1, 99, h.work, result.ID, 13, 11, version, "cookie", verificationOverrideBody{VerificationIDs: []guid.ID{original.ID}, EvidenceIDs: []guid.ID{result.Evidence[0].ID}, Reason: "Known environment failure", Risk: "Follow-up remains required", ReauthProof: "fresh"})
			})
			require.NoError(t, err)
			accepted, err := h.accept(result.ID, 13)
			require.NoError(t, err)
			require.True(t, accepted.Completion.L3)
			require.False(t, accepted.Completion.L2, "human override must not fabricate automatic success")
			switch scenario {
			case "later_failure":
				later := *original
				later.ID, later.Round, later.ReceiptHash = 903, 2, strings.Repeat("b", 64)
				require.NoError(t, q.WithContext(h.ctx).Create(&later))
			case "changed_receipt":
				_, err = q.WithContext(h.ctx).Where(q.ID.Eq(original.ID)).UpdateSimple(q.ReceiptHash.Value(strings.Repeat("c", 64)))
				require.NoError(t, err)
			}
			before := h.snapshot()
			adapter := clientWork{m: h.m}
			ref := ClientArtifactRef{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID}
			err = h.db.Transaction(func(tx *gorm.DB) error {
				artifacts, err := adapter.AcceptedArtifactsOn(h.ctx, tx, 1, 99, []ClientArtifactRef{ref})
				if scenario != "disclosed_override" {
					require.Empty(t, artifacts)
					return err
				}
				require.NoError(t, err)
				require.Len(t, artifacts, 1)
				var disclosed []ClientVerificationOverride
				require.NoError(t, json.Unmarshal(artifacts[0].Overrides, &disclosed))
				require.Len(t, disclosed, 1)
				require.Equal(t, "Known environment failure", disclosed[0].Reason)
				require.Equal(t, "Follow-up remains required", disclosed[0].Risk)
				require.Equal(t, []ClientOriginalVerdict{{Verdict: "FAILED", ReceiptHash: strings.Repeat("a", 64)}}, disclosed[0].Original)
				file, err := adapter.ArtifactFileOn(h.ctx, tx, 1, 99, ref)
				require.NoError(t, err)
				require.Equal(t, result.Evidence[0].VersionID, file.VersionID)
				require.Equal(t, result.Evidence[0].SHA256, file.SHA256)
				return nil
			})
			if scenario == "disclosed_override" {
				require.NoError(t, err)
				stored, err := q.WithContext(h.ctx).Where(q.ID.Eq(original.ID)).First()
				require.NoError(t, err)
				require.Equal(t, "FAILED", stored.Verdict)
				require.Equal(t, original.ReceiptHash, stored.ReceiptHash)
			} else {
				require.ErrorIs(t, err, ErrVerificationPending, "an old exception cannot authorize a later or changed receipt")
				_, err = adapter.ArtifactFileOn(h.ctx, h.db, 1, 99, ref)
				require.ErrorIs(t, err, ErrVerificationPending)
			}
			require.Equal(t, before, h.snapshot(), "client projection cannot change acceptance or delivery facts")
		})
	}
}

func TestClientSigningFreezesExactPublishedSheetAndIsIdempotent(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { r, e := m.createRequirementOn(ctx, tx, 1, 99, 11, body); id = r.ID; return e }))
	a := clientWork{m: m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		sheet, e := a.RequirementsOn(ctx, tx, 1, 99, []guid.ID{id})
		require.NoError(t, e)
		require.Len(t, sheet, 1)
		require.Len(t, sheet[0].Criteria, 2)
		changed := append([]ClientRequirement{}, sheet...)
		changed[0].Title = "Unapproved replacement"
		require.ErrorIs(t, a.SignRequirementsOn(ctx, tx, 1, 99, changed), ErrRequirementVersion)
		return a.SignRequirementsOn(ctx, tx, 1, 99, sheet)
	}))
	q := query.BindRequirements(db, m.service.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "BASELINED", r.Status)
	require.Equal(t, 1, r.CurrentVersionNo)
	versions, e := query.BindRequirementVersions(db, m.service.names).WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, versions, 1)
	require.Equal(t, body.Description, versions[0].Description)
	require.Len(t, versions[0].ContentHash, 64)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		sheet, e := a.RequirementsOn(ctx, tx, 1, 99, []guid.ID{id})
		if e != nil {
			return e
		}
		return a.SignRequirementsOn(ctx, tx, 1, 99, sheet)
	}))
	versions, e = query.BindRequirementVersions(db, m.service.names).WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, versions, 1, "a repeated signature must not duplicate a baseline")
	for _, ids := range [][]guid.ID{nil, {0}, {id, id}, {999}} {
		_, e := a.RequirementsOn(ctx, db, 1, 99, ids)
		require.Error(t, e)
	}
	_, e = a.RequirementsOn(ctx, db, 2, 99, []guid.ID{id})
	require.Error(t, e)
	c := query.BindCriteria(db, m.service.names)
	_, e = c.WithContext(ctx).Where(c.RequirementID.Eq(id)).Update(c.Required, false)
	require.NoError(t, e)
	_, e = a.RequirementsOn(ctx, db, 1, 99, []guid.ID{id})
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestClientSignatureFailureRollsBackBaselineAndCriterionVersion(t *testing.T) {
	for _, stage := range []string{"id", "version_create", "criterion_update", "requirement_update", "audit", "event", "criteria_query"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := requirementHarness(t)
			var id guid.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { r, e := m.createRequirementOn(ctx, tx, 1, 99, 11, body); id = r.ID; return e }))
			a := clientWork{m: m}
			sheet, e := a.RequirementsOn(ctx, db, 1, 99, []guid.ID{id})
			require.NoError(t, e)
			failure := errors.New("signature boundary failure")
			fired := false
			switch stage {
			case "id":
				m.ids = draftIDSource(func() (int64, error) { fired = true; return 0, failure })
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { fired = true; return failure }
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { fired = true; return 0, failure })
			default:
				tables := map[string]string{"version_create": m.service.names.RequirementVersions.String(), "criterion_update": m.service.names.Criteria.String(), "requirement_update": m.service.names.Requirements.String(), "criteria_query": m.service.names.Criteria.String()}
				hook := func(tx *gorm.DB) {
					if !fired && tx.Statement.Table == tables[stage] {
						fired = true
						_ = tx.AddError(failure)
					}
				}
				if stage == "version_create" {
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("signature_failure", hook))
				} else if stage == "criteria_query" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("signature_failure", hook))
				} else {
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("signature_failure", hook))
				}
			}
			e = db.Transaction(func(tx *gorm.DB) error { return a.SignRequirementsOn(ctx, tx, 1, 99, sheet) })
			require.True(t, fired)
			require.ErrorIs(t, e, failure)
			_ = db.Callback().Query().Remove("signature_failure")
			_ = db.Callback().Update().Remove("signature_failure")
			_ = db.Callback().Create().Remove("signature_failure")
			q := query.BindRequirements(db, m.service.names)
			r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			require.Equal(t, "DRAFT", r.Status)
			require.Zero(t, r.CurrentVersionNo)
			require.Equal(t, sheet[0].Version, r.Version)
			versions, e := query.BindRequirementVersions(db, m.service.names).WithContext(ctx).Find()
			require.NoError(t, e)
			require.Empty(t, versions)
			criteria, e := query.BindCriteria(db, m.service.names).WithContext(ctx).Find()
			require.NoError(t, e)
			for _, c := range criteria {
				require.Zero(t, c.IntroducedInVersion)
			}
		})
	}
}

func TestPersonalReadFailureDoesNotReturnPartialPage(t *testing.T) {
	for _, stage := range []string{"policy", "work_query", "assignment_query"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, body := workHTTPHarness(t)
			body.AccountablePrincipalID = 11
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, body); return e }))
			failure := errors.New("personal list dependency failed")
			fired := false
			var seen []string
			m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			if stage == "policy" {
				m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					fired = true
					return authz.DataConstraint{}, failure
				}
			} else {
				target := m.service.names.WorkItems.String()
				if stage == "assignment_query" {
					target = m.service.names.Assignments.String()
				}
				require.NoError(t, m.runtime.Database().Write().Callback().Query().Before("gorm:query").Register("personal_failure", func(tx *gorm.DB) {
					seen = append(seen, tx.Statement.Table)
					if tx.Statement.Table == target {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
			}
			page, e := m.personalTenantWorkHTTP(ctx, &personalTenantWorkInput{Limit: 10})
			require.True(t, fired, "query tables=%v, error=%v", seen, e)
			require.Error(t, e)
			require.Nil(t, page)
		})
	}
}

func TestClientWorkValidationAndProjectLockEnforceCurrentScope(t *testing.T) {
	h := newResultFixture(t)
	a := clientWork{m: h.m}
	require.NoError(t, a.ValidateWorkOn(h.ctx, h.db, 1, 99, h.work, false))
	require.ErrorIs(t, a.ValidateWorkOn(h.ctx, h.db, 1, 99, h.work, true), ErrInvalidArgument)
	require.Error(t, a.ValidateWorkOn(h.ctx, h.db, 2, 99, h.work, false))
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("type", "BUG").Error)
	require.NoError(t, a.ValidateWorkOn(h.ctx, h.db, 1, 99, h.work, true))
	m, db, ctx := constitutionHarness(t)
	b := clientWork{m: m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return b.LockProjectOn(ctx, tx, 1, 99, "project_manage") }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return b.LockProjectOn(ctx, tx, 2, 99, "project_manage") }))
	for _, refs := range [][]ClientArtifactRef{nil, {{EvidenceID: 0}}, {{EvidenceID: 1, WorkItemID: 999, ResultID: 1}}} {
		_, e := a.AcceptedArtifactsOn(h.ctx, h.db, 1, 99, refs)
		require.Error(t, e)
	}
}

func TestAcceptedArtifactFailuresNeverExposeFile(t *testing.T) {
	for _, scenario := range []string{"missing_file_capability", "file_unavailable", "wrong_evidence", "not_applicable", "foreign_result"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			r, e := h.submit()
			require.NoError(t, e)
			_, e = h.check(r.ID, 12, "PASSED")
			require.NoError(t, e)
			_, e = h.accept(r.ID, 12)
			require.NoError(t, e)
			ref := ClientArtifactRef{WorkItemID: h.work, ResultID: r.ID, EvidenceID: r.Evidence[0].ID}
			switch scenario {
			case "missing_file_capability":
				h.m.resultFiles = nil
			case "file_unavailable":
				h.files.fail = errors.New("file source unavailable")
			case "wrong_evidence":
				ref.EvidenceID = 999
			case "not_applicable":
				require.NoError(t, h.db.Table(h.m.service.names.ResultEvidence.String()).Where("id=?", ref.EvidenceID).Updates(map[string]any{"coverage": "NOT_APPLICABLE", "note": "Excluded by the signed scope"}).Error)
			case "foreign_result":
				ref.ResultID = 999
			}
			a := clientWork{m: h.m}
			e = h.db.Transaction(func(tx *gorm.DB) error {
				items, e := a.AcceptedArtifactsOn(h.ctx, tx, 1, 99, []ClientArtifactRef{ref})
				require.Nil(t, items)
				return e
			})
			require.Error(t, e)
		})
	}
}

func TestExceptionHoldUsesCanonicalDispatchFenceAndPreservesOtherHolds(t *testing.T) {
	for _, op := range []string{"CREATE", "DISPATCH", "RECOVER", "START"} {
		t.Run(op, func(t *testing.T) {
			h, link := executionWorkFixture(t)
			policy := h.m.workPolicy
			h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				if permission == "exception_decide" {
					return authz.DataConstraint{AllowAll: true}, nil
				}
				return policy(ctx, tx, tenant, project, permission, actor)
			}
			h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			if op != "CREATE" {
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
			}
			q := query.BindProjects(h.db, h.m.service.names)
			_, e := q.WithContext(h.ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Holds, `[{"kind":"BUDGET","source":"original","amount":12}]`)
			require.NoError(t, e)
			adapter := clientWork{m: h.m}
			for range 2 {
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					return adapter.ExceptionProjectOn(h.ctx, tx, 1, 99, "HOLD", "Investigate forecast", "", "")
				}))
			}
			row, e := q.WithContext(h.ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, e)
			var holds []map[string]any
			require.NoError(t, json.Unmarshal([]byte(row.Holds), &holds))
			require.Len(t, holds, 2)
			require.Equal(t, "original", holds[0]["source"])
			require.EqualValues(t, 12, holds[0]["amount"])
			require.Equal(t, "MANUAL", holds[1]["kind"])
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				if op == "START" {
					return h.m.startExecutionWorkOn(h.ctx, tx, link)
				}
				return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, op)
			}), ErrWorkProjectReadOnly)
		})
	}
}
func TestExceptionAuthorityCancellationAndAuditRollback(t *testing.T) {
	for _, scenario := range []string{"denied", "delegated", "policy", "kill", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := workHTTPHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				r := governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}
				switch scenario {
				case "denied":
					r.Allowed = false
				case "delegated":
					r.DelegationID = 7
				case "policy":
					r.AuthoritySource = "POLICY:automatic"
				}
				return r, nil
			}
			q := query.BindProjects(db, m.service.names)
			before, e := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, e)
			failure := errors.New("audit rejected")
			if scenario == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			decision := "HOLD"
			if scenario == "kill" {
				decision = "KILL"
			}
			e = db.Transaction(func(tx *gorm.DB) error {
				return (clientWork{m: m}).ExceptionProjectOn(ctx, tx, 1, 99, decision, "Forecast investigation", "", "")
			})
			require.Error(t, e)
			if scenario == "kill" {
				require.ErrorIs(t, e, ErrUnavailable)
			}
			if scenario == "audit" {
				require.ErrorIs(t, e, failure)
			}
			after, e := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestProgressArtifactDoesNotRequireOrAssertAcceptance(t *testing.T) {
	h := newResultFixture(t)
	r, e := h.submit()
	require.NoError(t, e)
	ref := ClientArtifactRef{WorkItemID: h.work, ResultID: r.ID, EvidenceID: r.Evidence[0].ID}
	a := clientWork{m: h.m}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		item, file, e := a.ProgressArtifactOn(h.ctx, tx, 1, 99, ref)
		require.NoError(t, e)
		require.Equal(t, r.Evidence[0].VersionID, item.VersionID)
		require.Equal(t, item.SHA256, file.SHA256)
		_, e = a.AcceptedArtifactsOn(h.ctx, tx, 1, 99, []ClientArtifactRef{ref})
		require.ErrorIs(t, e, ErrResultState)
		return nil
	}))
	q := query.BindWorkItems(h.db, h.m.service.names)
	work, e := q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).First()
	require.NoError(t, e)
	require.Nil(t, work.AcceptedAt)
	require.Nil(t, work.AcceptedDecisionID)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
	_, _, e = a.ProgressArtifactOn(h.ctx, h.db, 1, 99, ref)
	require.Error(t, e)
}

func TestSourceArtifactBeforeAnyResult(t *testing.T) {
	h := newResultFixture(t)
	a := clientWork{m: h.m}
	ref := ClientSourceRef{WorkItemID: h.work, FileID: 501, VersionID: 502, SHA256: strings.Repeat("a", 64)}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		item, candidate, e := a.SourceArtifactOn(h.ctx, tx, 1, 99, ref)
		require.NoError(t, e)
		require.Equal(t, "UNVERIFIED", item.Coverage)
		require.JSONEq(t, "[]", string(item.Overrides))
		require.Equal(t, ref.VersionID, candidate.VersionID)
		require.Equal(t, ref.SHA256, item.SHA256)
		q := query.BindResultVersions(tx, h.m.service.names)
		n, e := q.WithContext(h.ctx).Count()
		require.NoError(t, e)
		require.Zero(t, n, "progress must not create or require a WorkResult")
		return nil
	}))
}

func TestSourceArtifactRejectsStaleForeignAndChangedSources(t *testing.T) {
	for _, scenario := range []string{"version", "hash", "invalid_hash", "foreign_work", "foreign_project", "foreign_tenant", "stale", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			a := clientWork{m: h.m}
			ref := ClientSourceRef{WorkItemID: h.work, FileID: 501, VersionID: 502, SHA256: strings.Repeat("a", 64)}
			tenant, project := guid.ID(1), guid.ID(99)
			switch scenario {
			case "version":
				ref.VersionID++
			case "hash":
				ref.SHA256 = strings.Repeat("b", 64)
			case "invalid_hash":
				ref.SHA256 = strings.Repeat("z", 64)
			case "foreign_work":
				ref.WorkItemID = 99999
			case "foreign_project":
				project++
			case "foreign_tenant":
				tenant++
			case "stale":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "unavailable":
				h.m.resultFiles = nil
			}
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, _, e := a.SourceArtifactOn(h.ctx, tx, tenant, project, ref)
				return e
			}))
		})
	}
}

func TestDispatchAutoUsesCurrentLeadAndPolicyForAgentAssignment(t *testing.T) {
	for _, scenario := range []string{"apply", "human_actor", "other_lead", "revoked", "human_target", "audit", "budget_cap", "authority_error", "missing_authority", "lead_source_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			actor := guid.ID(25)
			if scenario == "human_actor" {
				actor = 11
			}
			ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11 || id == 12, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				if id == 25 {
					return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
				}
				if id == 21 {
					return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			lead := guid.ID(30)
			if scenario == "other_lead" {
				lead = 31
			}
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"dispatch_policy": "AUTO", "team_mode": "HYBRID", "lead_agent_id": lead}).Error)
			adapter := clientWork{m: m, autoAuthority: func(_ context.Context, _ *gorm.DB, facts governance.DispatchAutoFacts) (governance.AuthorityDecision, error) {
				require.EqualValues(t, 25, facts.PrincipalID)
				require.EqualValues(t, 30, facts.LeadAgentID)
				require.EqualValues(t, 21, facts.AssigneePrincipalID)
				if scenario == "authority_error" {
					return governance.AuthorityDecision{}, errors.New("current AUTO authority unavailable")
				}
				return governance.AuthorityDecision{Allowed: scenario != "revoked", AuthoritySource: "POLICY:dispatch_auto"}, nil
			}}
			if scenario == "budget_cap" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("budget_cap_micros", 100).Error)
			}
			if scenario == "missing_authority" {
				adapter.autoAuthority = nil
			}
			if scenario == "lead_source_error" {
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, errors.New("lead source unavailable")
				}
			}
			if scenario == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			target := guid.ID(21)
			if scenario == "human_target" {
				target = 12
			}
			var assignment guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				assignment, e = adapter.dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{Auto: true, DecisionID: 1001, ProposalID: 1000, WorkItemID: work.ID, AssigneePrincipalID: target, ExpectedWorkVersion: work.Version, Reason: "Cold-start policy"})
				return e
			})
			if scenario != "apply" {
				require.Error(t, err)
				var count int64
				require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
				require.Zero(t, count)
				return
			}
			require.NoError(t, err)
			assignments := query.BindAssignments(db, m.service.names)
			row, err := assignments.WithContext(ctx).Where(assignments.ID.Eq(assignment)).First()
			require.NoError(t, err)
			require.Equal(t, "AGENT", row.AssigneeKind)
			require.EqualValues(t, 20, *row.AgentID)
			transitions := query.BindWorkTransitions(db, m.service.names)
			transition, err := transitions.WithContext(ctx).Where(transitions.DecisionID.Eq(guid.ID(1001))).First()
			require.NoError(t, err)
			require.Equal(t, "AGENT", transition.ActorKind)
			require.EqualValues(t, 25, *transition.ActorID)
			require.Equal(t, "POLICY:dispatch_auto", transition.AuthoritySource)
			require.Equal(t, "DECISION", transition.Trigger)
		})
	}
}

func TestDispatchAutoPrelocksCompletePrincipalSetBeforeProject(t *testing.T) {
	for _, scenario := range []string{"allow", "lock_failure", "owner_changed", "human_only", "missing_work", "missing_lock"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
			}
			mode := "HYBRID"
			if scenario == "human_only" {
				mode = "HUMAN_ONLY"
			}
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"dispatch_policy": "AUTO", "team_mode": mode, "lead_agent_id": 30}).Error)
			var locked []guid.ID
			failure := errors.New("principal fence unavailable")
			m.principals.Lock = func(_ context.Context, tx *gorm.DB, id guid.ID) error {
				locked = append(locked, id)
				if scenario == "lock_failure" && id == 21 {
					return failure
				}
				if scenario == "owner_changed" && len(locked) == 1 {
					return tx.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("accountable_principal_id", 12).Error
				}
				return nil
			}
			if scenario == "missing_lock" {
				m.principals.Lock = nil
			}
			m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
				require.GreaterOrEqual(t, len(locked), 3)
				require.Equal(t, []guid.ID{11, 21, 25}, locked[:3], "sorted unique principal locks must precede project authorization")
				return authz.DataConstraint{AllowAll: true}, nil
			}
			id := work.ID
			if scenario == "missing_work" {
				id = 999999
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return (clientWork{m: m}).dispatchAutoLockOn(ctx, tx, 1, 99, id, []guid.ID{25, 21, 21})
			})
			if scenario == "allow" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				if scenario == "lock_failure" {
					require.ErrorIs(t, err, failure)
				}
				if scenario == "owner_changed" {
					require.ErrorIs(t, err, ErrWorkVersion)
				}
			}
		})
	}
}

func TestDispatchHistoryCountsOnlyCurrentTenantTypeAndWindow(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	a := clientWork{m: m}
	since := time.Now().UTC().Add(-90 * 24 * time.Hour)
	count, err := a.dispatchHistorySamplesOn(ctx, db, 1, work.Type, since)
	require.NoError(t, err)
	require.Zero(t, count, "empty history must be distinguishable from a failed provider")
	for i, at := range []time.Time{since.Add(time.Hour), since.Add(-time.Hour)} {
		row := &model.ResultVersion{TenantID: 1, ID: guid.ID(8000 + i), ProjectID: 99, WorkItemID: work.ID, Sequence: i + 1, Summary: "Historical claim", VerificationPlan: "[]", ActorID: 11, CreatedAt: at}
		require.NoError(t, query.BindResultVersions(db, m.service.names).WithContext(ctx).Create(row))
	}
	count, err = a.dispatchHistorySamplesOn(ctx, db, 1, work.Type, since)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	count, err = a.dispatchHistorySamplesOn(ctx, db, 2, work.Type, since)
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = a.dispatchHistorySamplesOn(ctx, db, 1, "UNRELATED_TYPE", since)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestDispatchStopAdapterRechecksLockedExecutionAndForwardsPendingState(t *testing.T) {
	for _, scenario := range []string{"pending", "stopped", "new_principal", "discovery_error", "rediscovery_error", "lock_error", "work_denied", "wrong_execution", "stop_error", "missing_provider"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "work_denied" {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 88).Error)
			var locked []guid.ID
			failure := errors.New("execution provider unavailable")
			m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
				locked = append(locked, id)
				if scenario == "lock_error" && id == 21 {
					return failure
				}
				return nil
			}
			discoveries, stops := 0, 0
			requested := time.Now().UTC().Truncate(time.Millisecond)
			access := &DispatchExecutionAccess{PrincipalsOn: func(_ context.Context, tx *gorm.DB, tenant, project, workID, execution guid.ID) ([]guid.ID, error) {
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 99, project)
				require.Equal(t, work.ID, workID)
				require.EqualValues(t, 88, execution)
				discoveries++
				if scenario == "discovery_error" || (scenario == "rediscovery_error" && discoveries == 2) {
					return nil, failure
				}
				if discoveries == 2 {
					require.Equal(t, []guid.ID{11, 21}, locked[:2])
					if scenario == "new_principal" {
						return []guid.ID{21, 22}, nil
					}
				}
				return []guid.ID{21, 11, 21}, nil
			}, StopOn: func(_ context.Context, tx *gorm.DB, tenant, project, workID, execution guid.ID, version int) (DispatchExecutionState, error) {
				stops++
				require.Equal(t, 7, version)
				if scenario == "stop_error" {
					return DispatchExecutionState{}, failure
				}
				state := "RUNNING"
				if scenario == "stopped" {
					state = "CANCELLED" //nolint:misspell // Canonical execution state.
				}
				return DispatchExecutionState{ExecutionID: execution, State: state, Version: version + 1, CancelRequestedAt: &requested}, nil
			}}
			a := clientWork{m: m, dispatchExecution: access}
			if scenario == "missing_provider" {
				a.dispatchExecution = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := a.dispatchLockStopOn(ctx, tx, 1, 99, work.ID, 88); err != nil {
					return err
				}
				id := guid.ID(88)
				if scenario == "wrong_execution" {
					id = 89
				}
				state, err := a.dispatchStopOn(ctx, tx, 1, 99, work.ID, id, 7)
				if err == nil {
					require.EqualValues(t, 88, state.ExecutionID)
					require.Equal(t, &requested, state.CancelRequestedAt)
					require.Equal(t, scenario == "stopped", state.State == "CANCELLED") //nolint:misspell // Canonical execution state.
				}
				return err
			})
			if scenario == "pending" || scenario == "stopped" {
				require.NoError(t, err)
				require.Equal(t, 1, stops)
			} else {
				require.Error(t, err)
				if scenario != "stop_error" {
					require.Zero(t, stops, "failed fences must never request execution cancellation")
				}
			}
		})
	}
}

func TestDispatchQueueAndAuthorityAdaptersFailClosed(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	a := clientWork{m: m}
	_, err := a.dispatchQueuesOn(ctx, db, 1, []guid.ID{7})
	require.ErrorIs(t, err, ErrUnavailable)
	a.dispatchExecution = &DispatchExecutionAccess{}
	_, err = a.dispatchQueuesOn(ctx, db, 1, []guid.ID{7})
	require.ErrorIs(t, err, ErrUnavailable)
	observed := time.Now().UTC()
	a.dispatchExecution.QueuesOn = func(_ context.Context, tx *gorm.DB, tenant guid.ID, machines []guid.ID) ([]DispatchQueueFact, error) {
		require.Same(t, db, tx)
		require.EqualValues(t, 1, tenant)
		require.Equal(t, []guid.ID{7}, machines)
		return []DispatchQueueFact{{MachineID: 7, PendingCommands: 3, ObservedAt: observed}}, nil
	}
	queues, err := a.dispatchQueuesOn(ctx, db, 1, []guid.ID{7})
	require.NoError(t, err)
	require.Equal(t, []DispatchQueueFact{{MachineID: 7, PendingCommands: 3, ObservedAt: observed}}, queues)
	for _, scenario := range []string{"allowed", "denied", "empty_source", "provider_error", "missing_provider"} {
		t.Run(scenario, func(t *testing.T) {
			m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Equal(t, "HUMAN", in.ActorKind)
				require.Equal(t, "work_item_assign", in.Code)
				require.Equal(t, work.ID, in.Resource.ID)
				require.EqualValues(t, 21, in.Facts.AssigneePrincipalID)
				if scenario == "provider_error" {
					return governance.AuthorityDecision{}, errors.New("authority unavailable")
				}
				source := "ROLE:delivery_lead"
				if scenario == "empty_source" {
					source = ""
				}
				return governance.AuthorityDecision{Allowed: scenario != "denied", AuthoritySource: source}, nil
			}
			if scenario == "missing_provider" {
				m.deliveryAuthority = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				source, err := a.dispatchAuthorityOn(ctx, tx, 1, 99, work.ID, 21)
				if scenario == "allowed" {
					require.Equal(t, "ROLE:delivery_lead", source)
				}
				return err
			})
			if scenario == "allowed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDispatchConnectorsRejectIncompleteAndDuplicateBinding(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 30, TenantID: tenant, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, principal == 25, nil
	}
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"dispatch_policy": "AUTO", "team_mode": "HYBRID", "lead_agent_id": 30}).Error)
	s := module.NewServices()
	require.NoError(t, m.provideClientWork(s))
	require.NoError(t, s.Seal())
	dispatch, err := module.Resolve[DispatchAccess](s)
	require.NoError(t, err)
	auto, err := module.Resolve[governance.DispatchAutoConsumer](s)
	require.NoError(t, err)
	require.ErrorIs(t, auto.Bind(nil), ErrInvalidArgument)
	authorityCalls := 0
	check := governance.DispatchAutoAuthority(func(_ context.Context, _ *gorm.DB, facts governance.DispatchAutoFacts) (governance.AuthorityDecision, error) {
		authorityCalls++
		require.EqualValues(t, 25, facts.PrincipalID)
		require.EqualValues(t, 30, facts.LeadAgentID)
		require.Equal(t, work.ID, facts.WorkItemID)
		require.EqualValues(t, 21, facts.AssigneePrincipalID)
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "POLICY:dispatch_auto"}, nil
	})
	require.NoError(t, auto.Bind(check))
	require.ErrorIs(t, auto.Bind(check), ErrInvalidArgument, "an established authority must not be silently replaced")
	connector, err := module.Resolve[DispatchExecutionConnector](s)
	require.NoError(t, err)
	discoveryCalls, stopCalls := 0, 0
	principals := func(_ context.Context, _ *gorm.DB, tenant, project, workID, execution guid.ID) ([]guid.ID, error) {
		discoveryCalls++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, work.ID, workID)
		require.EqualValues(t, 88, execution)
		return []guid.ID{21, 11}, nil
	}
	requested := time.Now().UTC().Truncate(time.Millisecond)
	stop := func(_ context.Context, _ *gorm.DB, tenant, project, workID, execution guid.ID, version int) (DispatchExecutionState, error) {
		stopCalls++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, work.ID, workID)
		require.EqualValues(t, 88, execution)
		require.Equal(t, 7, version)
		return DispatchExecutionState{ExecutionID: execution, State: "RUNNING", Version: 8, CancelRequestedAt: &requested}, nil
	}
	queues := func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]DispatchQueueFact, error) {
		return []DispatchQueueFact{{MachineID: 7, PendingCommands: 2}}, nil
	}
	for _, incomplete := range []DispatchExecutionAccess{{}, {PrincipalsOn: principals}, {PrincipalsOn: principals, StopOn: stop}} {
		require.ErrorIs(t, connector.Bind(incomplete), ErrInvalidArgument)
	}
	complete := DispatchExecutionAccess{PrincipalsOn: principals, StopOn: stop, QueuesOn: queues}
	require.NoError(t, connector.Bind(complete))
	require.ErrorIs(t, connector.Bind(complete), ErrInvalidArgument)
	facts, err := dispatch.QueuesOn(ctx, db, 1, []guid.ID{7})
	require.NoError(t, err)
	require.Equal(t, []DispatchQueueFact{{MachineID: 7, PendingCommands: 2}}, facts, "published adapter must see the subsequently bound Execution capability")
	lead := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 25, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		source, err := dispatch.AutoAuthorityOn(lead, tx, 1, 99, work.ID, 21)
		require.Equal(t, "POLICY:dispatch_auto", source)
		return err
	}))
	require.Equal(t, 1, authorityCalls, "published AUTO callback must see the later Governance binding")
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 88).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := dispatch.LockStopOn(ctx, tx, 1, 99, work.ID, 88); err != nil {
			return err
		}
		state, err := dispatch.StopOn(ctx, tx, 1, 99, work.ID, 88, 7)
		require.Equal(t, DispatchExecutionState{ExecutionID: 88, State: "RUNNING", Version: 8, CancelRequestedAt: &requested}, state)
		return err
	}))
	require.Equal(t, 2, discoveryCalls, "published stop prelock must discover and recheck the bound participants")
	require.Equal(t, 1, stopCalls, "published stop must invoke the later Execution binding after real work validation")
}

func TestForecastMissingScopeAndSourceErrorsCannotReturnMeasurements(t *testing.T) {
	for _, scenario := range []string{"project", "work", "requirements", "criteria", "missing_requirement"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			at := time.Now().UTC()
			requirement := &model.Requirement{TenantID: 1, ID: 8000, ProjectID: 99, Number: 88, Title: "Scope", Description: "Current scope", Priority: 1, Status: "BASELINED", CurrentVersionNo: 1, Version: 1, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(requirement))
			target := map[string]string{"project": h.m.service.names.Projects.String(), "work": h.m.service.names.WorkItems.String(), "requirements": h.m.service.names.Requirements.String(), "criteria": h.m.service.names.Criteria.String()}[scenario]
			failure := errors.New("measurement source failed")
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("forecast_source_failure", func(tx *gorm.DB) {
				if target != "" && tx.Statement.Table == target {
					_ = tx.AddError(failure)
				}
			}))
			a := clientWork{m: h.m}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var scope *guid.ID
				if scenario == "missing_requirement" {
					missing := guid.ID(999999)
					scope = &missing
				}
				_, err := a.forecastWorkOn(h.ctx, tx, 1, 99, scope, at)
				return err
			})
			if scenario == "missing_requirement" {
				require.ErrorIs(t, err, ErrProjectNotFound, "unknown requirement must never become a zero-work forecast")
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, err := a.forecastWorkOn(h.ctx, tx, 1, 99, nil, time.Time{})
				return err
			}), ErrInvalidArgument)
		})
	}
}

func TestDispatchCanonicalAssignmentAndRollback(t *testing.T) {
	for _, scenario := range []string{"human", "agent", "audit", "authority", "execution", "version", "held", "owner_membership"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11 || id == 12, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				if id == 21 {
					return fleet.AgentIdentity{ID: 20, TenantID: tenant, PrincipalID: id, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, true, nil
				}
				return fleet.AgentIdentity{}, false, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: scenario != "authority", AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			if scenario == "owner_membership" {
				m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _, principal guid.ID) (bool, error) {
					return principal != work.AccountablePrincipalID, nil
				})
			}
			target := guid.ID(12)
			if scenario == "agent" {
				target = 21
			}
			in := DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 1000, AssigneePrincipalID: target, ExpectedWorkVersion: work.Version, Reason: "Candidate reviewed"}
			if scenario == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			if scenario == "execution" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 88).Error)
			}
			if scenario == "version" {
				in.ExpectedWorkVersion++
			}
			if scenario == "held" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", `[{"kind":"MANUAL","source":"TOLERANCE_EXCEPTION"}]`).Error)
			}
			var id guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				id, e = (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, in)
				return e
			})
			if scenario != "human" && scenario != "agent" {
				require.Error(t, err)
				var n int64
				require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&n).Error)
				require.Zero(t, n)
				return
			}
			require.NoError(t, err)
			row, e := query.BindAssignments(db, m.service.names).WithContext(ctx).Where(query.BindAssignments(db, m.service.names).ID.Eq(id)).First()
			require.NoError(t, e)
			require.Equal(t, target, row.AssigneePrincipalID)
			expected := "HUMAN"
			if scenario == "agent" {
				expected = "AGENT"
				require.EqualValues(t, 20, *row.AgentID)
			}
			require.Equal(t, expected, row.AssigneeKind)
			updated, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
			require.NoError(t, e)
			require.Equal(t, "CLAIMED", updated.State)
			require.Nil(t, updated.CurrentClaimID)
			require.Nil(t, updated.CurrentExecutionID)
		})
	}
}

// Owner scope belongs to the work row, independently of project authorship.
func TestDispatchWorkOwnerScopeAcrossProjectCreators(t *testing.T) {
	for _, projectOwner := range []guid.ID{11, 12} {
		for _, workOwner := range []guid.ID{11, 12} {
			t.Run(projectOwner.String()+"/"+workOwner.String(), func(t *testing.T) {
				m, db, ctx, work := assignmentHarness(t, true)
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("created_by", projectOwner).Error)
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("created_by", workOwner).Error)
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
				}
				var facts DispatchWorkFacts
				err := db.Transaction(func(tx *gorm.DB) error {
					if e := (clientWork{m: m}).LockProjectOn(ctx, tx, 1, 99, "work_item_assign"); e != nil {
						return e
					}
					var e error
					facts, e = (clientWork{m: m}).dispatchWorkOn(ctx, tx, 1, 99, work.ID)
					return e
				})
				if workOwner == 11 {
					require.NoError(t, err)
					require.Equal(t, work.ID, facts.WorkItemID)
				} else {
					require.Error(t, err)
					require.Zero(t, facts.WorkItemID)
				}
			})
		}
	}
}

func TestDispatchReassignmentReleasesOnlyPreviousOwnershipAtomically(t *testing.T) {
	for _, fault := range []string{"apply", "audit", "release_update"} {
		rollback := fault != "apply"
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11 || id == 12, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}
			adapter := clientWork{m: m}
			var previous guid.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				previous, e = adapter.dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 1000, AssigneePrincipalID: 11, ExpectedWorkVersion: work.Version, Reason: "Initial assignment"})
				return e
			}))
			if fault == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			if fault == "release_update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dispatch_release_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Assignments.String() {
						_ = tx.AddError(errors.New("assignment release failed"))
					}
				}))
			}
			var next guid.ID
			e := db.Transaction(func(tx *gorm.DB) error {
				var e error
				next, e = adapter.dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 1001, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version + 1, Reason: "Reviewed replacement", Reassign: true})
				return e
			})
			q := query.BindAssignments(db, m.service.names)
			old, readErr := q.WithContext(ctx).Where(q.ID.Eq(previous)).First()
			require.NoError(t, readErr)
			active, readErr := q.WithContext(ctx).Where(q.WorkItemID.Eq(work.ID), q.Status.Eq("ACTIVE")).Find()
			require.NoError(t, readErr)
			require.Len(t, active, 1)
			if rollback {
				require.Error(t, e)
				require.Equal(t, "ACTIVE", old.Status)
				require.Equal(t, previous, active[0].ID)
			} else {
				require.NoError(t, e)
				require.Equal(t, "RELEASED", old.Status)
				require.Nil(t, old.ActiveSlot)
				require.Nil(t, old.LeaseExpiresAt)
				require.Equal(t, "DISPATCH_REASSIGNED", *old.EndReason)
				require.Equal(t, next, active[0].ID)
				require.EqualValues(t, 12, active[0].AssigneePrincipalID)
			}
		})
	}
}

// Preview-share approval must remain an independent, current Human ROLE grant.
// Neither a delegated approval nor a permissive project read substitutes for it.
func TestFinalPreviewShareAuthorityCannotBeSubstituted(t *testing.T) {
	for _, scenario := range []string{"role", "missing", "error", "denied", "delegated", "policy"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			failure := errors.New("current approval authority unavailable")
			m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Equal(t, "preview_share_approve", in.Code)
				require.Equal(t, guid.ID(99), in.Resource.ID)
				require.Equal(t, "HUMAN", in.ActorKind)
				if scenario == "error" {
					return governance.AuthorityDecision{}, failure
				}
				v := governance.AuthorityDecision{Allowed: scenario != "denied", AuthoritySource: "ROLE:approver"}
				if scenario == "delegated" {
					v.DelegationID = 77
				}
				if scenario == "policy" {
					v.AuthoritySource = "POLICY:dispatch_auto"
				}
				return v, nil
			}
			if scenario == "missing" {
				m.deliveryAuthority = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return (clientWork{m: m}).ApprovePreviewShareOn(ctx, tx, 1, 99) })
			if scenario == "role" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "error" {
				require.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestFinalDispatchOwnerRecheckFailuresLeaveNoAssignment(t *testing.T) {
	for _, scenario := range []string{"owner_lock", "owner_member", "owner_human", "not_ready_state"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			// A distinct target lets the later accountable-owner recheck fail after
			// the assignee's identity and membership have already succeeded.
			m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, true, nil
			}
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_lead"}, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}
			failure := errors.New("accountable owner recheck unavailable")
			if scenario == "owner_human" {
				calls := 0
				m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == work.AccountablePrincipalID {
						calls++
						if calls >= 2 {
							return identity.Principal{}, false, failure
						}
					}
					return identity.Principal{ID: id}, true, nil
				}
			}
			if scenario == "owner_lock" {
				calls := 0
				m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
					if id == work.AccountablePrincipalID {
						calls++
						if calls >= 2 {
							return failure
						}
					}
					return nil
				}
			}
			if scenario == "owner_member" {
				calls := 0
				m.members = memberCheck(func(_ context.Context, _ *gorm.DB, _, id guid.ID) (bool, error) {
					if id == work.AccountablePrincipalID {
						calls++
						if calls >= 2 {
							return false, failure
						}
					}
					return true, nil
				})
			}
			if scenario == "not_ready_state" {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("state", "DRAFT").Error)
			}
			before, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, e := (clientWork{m: m}).dispatchApplyOn(ctx, tx, 1, 99, DispatchAssignmentInput{WorkItemID: work.ID, ProposalID: 901, AssigneePrincipalID: 12, ExpectedWorkVersion: work.Version, Reason: "Reviewed candidate"})
				return e
			})
			require.Error(t, err)
			if scenario != "not_ready_state" {
				require.ErrorIs(t, err, failure)
			}
			after, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
			var count int64
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestSelectedAgentWorkUsesViewerScopeBeforeBoundCursorAndRetainsSourceErrors(t *testing.T) {
	h := newResultFixture(t)
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	q := query.BindWorkItems(h.db, h.m.service.names)
	work, e := q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).First()
	require.NoError(t, e)
	other := *work
	other.ID = work.ID + 100
	other.Number = work.Number + 100
	require.NoError(t, q.WithContext(h.ctx).Create(&other))
	read := func(cursor string) (PersonalWorkPage, error) {
		var out PersonalWorkPage
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.selectedAgentWorkOn(h.actor(12), tx, 1, 11, cursor, 1)
			return e
		})
		return out, e
	}
	page, e := read("")
	require.NoError(t, e)
	require.True(t, page.HasMore)
	require.Len(t, page.Items, 1)
	require.Equal(t, other.ID, page.Items[0].ID)
	require.Equal(t, "accountable", page.Items[0].Responsibility)
	next, e := read(page.NextCursor)
	require.NoError(t, e)
	require.False(t, next.HasMore)
	require.Len(t, next.Items, 1)
	require.Equal(t, h.work, next.Items[0].ID)
	_, e = read(personalWorkCursor(1, 11, 13, other.ID))
	require.ErrorIs(t, e, ErrInvalidArgument)
	failure := errors.New("viewer work scope unavailable")
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	_, e = read("")
	require.ErrorIs(t, e, failure)
	h.m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("selected-work-unavailable", func(tx *gorm.DB) { tx.AddError(failure) }))
	_, e = read("")
	require.ErrorIs(t, e, failure)
	require.NoError(t, h.db.Callback().Query().Remove("selected-work-unavailable"))
}
