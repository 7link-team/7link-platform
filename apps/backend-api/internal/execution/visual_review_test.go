package execution

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func visualReviewFixture(t *testing.T) (*executionFixture, guid.ID, VisualReview) {
	t.Helper()
	f, task, p, o := visualObservationFixture(t)
	prepared, err := f.m.prepareVisualEvidence(f.runner, 7, task, f.fence, p.Nonce, o)
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, task, f.fence, p.Nonce, prepared)
	}))
	q := query.BindVerificationTasks(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.ID.Eq(task)).First()
	require.NoError(t, err)
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 12})
	f.m.work.AuthorizeVisualReviewOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, result guid.ID, version int, criterion guid.ID, head string) error {
		require.Equal(t, guid.ID(50), result)
		require.Equal(t, 2, version)
		require.Equal(t, guid.ID(51), criterion)
		require.Equal(t, p.HeadCommit, head)
		return f.m.work.AuthorizeOn(ctx, tx, l, "VISUAL_REVIEW")
	}
	return f, r.ExecutionID, VisualReview{TaskID: task, PlanHash: r.PlanHash, CaptureReceiptHash: r.ReceiptHash, Verdict: "PASSED", Reason: "Inspected captured desktop layout against acceptance criteria"}
}

func TestReviewerScreenshotReadsRejectForgedIdentityAndCapture(t *testing.T) {
	for _, fault := range []string{"none", "view", "agent", "policy", "scope", "file", "title", "task_id", "task", "plan", "capture", "digest"} {
		t.Run(fault, func(t *testing.T) {
			f, id, in := visualReviewFixture(t)
			q := query.BindExecutions(f.db, f.m.names)
			row, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
			require.NoError(t, e)
			v, e := view(row)
			require.NoError(t, e)
			v.Plan.ExecutorPrincipalID = 21
			v.Plan.ModelFamily = "source-family"
			raw, e := json.Marshal(v.Plan)
			require.NoError(t, e)
			row.PlanJSON = string(raw)
			row.PlanHash = hash(raw)
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{TenantID: 7, PrincipalID: 12, State: "ACTIVE", Roles: []string{"REVIEWER"}, ModelID: "review-model", ModelFamily: "review-family"}, true, nil
			}
			evidence := query.BindEvidence(f.db, f.m.names)
			file, e := evidence.WithContext(t.Context()).Where(evidence.Kind.Eq("FILE")).First()
			require.NoError(t, e)
			failure := errors.New("reviewer read dependency failed")
			expected := ErrForbidden
			switch fault {
			case "view":
				row.PlanJSON = "{"
				expected = ErrUnavailable
			case "agent":
				f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, failure
				}
				expected = failure
			case "policy":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
				expected = failure
			case "scope":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "file":
				file.ID++
			case "title":
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", file.ID).Update("title", "Ordinary execution file").Error)
			case "task_id":
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", file.ID).Update("title", "Visual verification invalid 320x240").Error)
			case "task":
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", in.TaskID).Update("state", "LEASED").Error)
			case "plan":
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", in.TaskID).Update("plan_json", "{").Error)
			case "capture":
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", in.TaskID).Update("observation_json", "{").Error)
			case "digest":
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", file.ID).Update("content_hash", "foreign screenshot").Error)
			}
			e = f.db.Transaction(func(tx *gorm.DB) error { return f.m.authorizeVisualReviewerReadOn(f.human, tx, row, file.ID) })
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, expected)
			}
		})
	}
}

func TestVisualReviewHumanDecisionReachesReceiptWithoutChangingCapture(t *testing.T) {
	for _, verdict := range []string{"PASSED", "FAILED"} {
		t.Run(verdict, func(t *testing.T) {
			f, execution, in := visualReviewFixture(t)
			in.Verdict = verdict
			decide := func() (VisualReviewDecision, error) {
				var out VisualReviewDecision
				err := f.db.Transaction(func(tx *gorm.DB) error {
					var e error
					out, e = f.m.RecordVisualReviewOn(f.human, tx, 7, execution, in)
					return e
				})
				return out, err
			}
			d, err := decide()
			require.NoError(t, err)
			require.Equal(t, guid.ID(12), d.ReviewerID)
			require.Equal(t, "HUMAN", d.ReviewerKind)
			replay, err := decide()
			require.NoError(t, err)
			require.Equal(t, d, replay)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				r, e := f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, 50, in.TaskID)
				require.NoError(t, e)
				require.Equal(t, verdict, r.Verdict)
				require.Equal(t, in.Reason, r.Reason)
				require.Equal(t, d.ReviewerID, r.ReviewerID)
				require.NotEqual(t, in.CaptureReceiptHash, r.ReceiptHash)
				require.Nil(t, r.ExitCode, "review must not fabricate a successful process exit")
				return e
			}))
			q := query.BindVerificationTasks(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.TaskID)).First()
			require.NoError(t, err)
			require.Equal(t, in.CaptureReceiptHash, r.ReceiptHash)
			f.authorized = false
			_, err = decide()
			require.ErrorIs(t, err, ErrForbidden, "replay rechecks revoked authority")
			f.authorized = true
			in.Reason = "changed judgement"
			_, err = decide()
			require.ErrorIs(t, err, ErrConflict)
		})
	}
}

func TestVisualReviewRejectsInvalidStaleUnauthorizedAndRollsBack(t *testing.T) {
	for _, fault := range []string{"blank_reason", "verdict", "plan", "capture", "tenant", "self", "inactive", "policy", "audit", "outbox", "missing_screenshot", "corrupt_plan", "corrupt_capture"} {
		t.Run(fault, func(t *testing.T) {
			f, execution, in := visualReviewFixture(t)
			ctx := f.human
			switch fault {
			case "blank_reason":
				in.Reason = "   "
			case "verdict":
				in.Verdict = "INCONCLUSIVE"
			case "plan":
				in.PlanHash = hash(nil)
			case "capture":
				in.CaptureReceiptHash = hash(nil)
			case "tenant":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 12})
			case "self":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
			case "inactive":
				f.humanActive = false
			case "policy":
				f.authorized = false
			case "audit":
				f.m.audit = nil
			case "outbox":
				f.pub.fail = true
			case "missing_screenshot":
				q := query.BindEvidence(f.db, f.m.names)
				_, err := q.WithContext(t.Context()).Where(q.Kind.Eq("FILE")).Delete()
				require.NoError(t, err)
			case "corrupt_plan", "corrupt_capture":
				q := query.BindVerificationTasks(f.db, f.m.names)
				column := "plan_json"
				if fault == "corrupt_capture" {
					column = "observation_json"
				}
				_, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.TaskID)).Updates(map[string]any{column: "{}"})
				require.NoError(t, err)
			}
			var before int64
			require.NoError(t, f.db.Table("effects").Count(&before).Error)
			err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecordVisualReviewOn(ctx, tx, 7, execution, in); return e })
			require.Error(t, err)
			q := query.BindEvidence(f.db, f.m.names)
			count, err := q.WithContext(t.Context()).Where(q.Kind.Eq("VISUAL_REVIEW")).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			var after int64
			require.NoError(t, f.db.Table("effects").Count(&after).Error)
			require.Equal(t, before, after, "audit and review roll back if outbox publication fails")
		})
	}
}

func TestVisualReviewAgentUsesCurrentIdentityAndDifferentFamily(t *testing.T) {
	for _, fault := range []string{"", "same_family", "same_model", "inactive", "role", "identity_error", "source_missing"} {
		t.Run(fault, func(t *testing.T) {
			f, execution, in := visualReviewFixture(t)
			q := query.BindExecutions(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.ID.Eq(execution)).First()
			require.NoError(t, err)
			v, err := view(r)
			require.NoError(t, err)
			v.Plan.ExecutorPrincipalID, v.Plan.ModelFamily = 21, "source-family"
			if fault == "source_missing" {
				v.Plan.ModelFamily = ""
			}
			b, err := json.Marshal(v.Plan)
			require.NoError(t, err)
			_, err = q.WithContext(t.Context()).Where(q.ID.Eq(execution)).Updates(map[string]any{"plan_json": string(b), "plan_hash": hash(b)})
			require.NoError(t, err)
			f.humanActive = false
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				a := fleet.AgentIdentity{TenantID: 7, PrincipalID: 12, State: "ACTIVE", Roles: []string{"ACCEPTOR"}, ModelID: "review-model", ModelFamily: "review-family"}
				switch fault {
				case "same_family":
					a.ModelFamily = "source-family"
				case "same_model":
					a.ModelID = v.Plan.Model
				case "inactive":
					a.State = "SUSPENDED"
				case "role":
					a.Roles = []string{"EXECUTOR"}
				case "identity_error":
					return a, false, errors.New("identity unavailable")
				}
				return a, true, nil
			}
			err = f.db.Transaction(func(tx *gorm.DB) error {
				evidence := query.BindEvidence(tx, f.m.names)
				file, findErr := evidence.WithContext(t.Context()).Where(evidence.Kind.Eq("FILE")).First()
				require.NoError(t, findErr)
				readErr := f.m.authorizeArtifactOn(f.human, tx, files.ExecutionArtifactAuthority{}, "execution_evidence", file.ID, files.ResourceOwner{Scope: "entity", ID: 9})
				if fault == "" {
					require.NoError(t, readErr, "Agent reviewer must be able to read actual screenshot bytes")
				} else {
					require.Error(t, readErr)
				}
				d, e := f.m.RecordVisualReviewOn(f.human, tx, 7, execution, in)
				if fault == "" {
					require.Equal(t, "AGENT", d.ReviewerKind)
					require.Equal(t, "review-family", d.Family)
				}
				return e
			})
			if fault == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestVisualReviewEvidenceCannotBeForgedByExecutorRunner(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "VISUAL_REVIEW", EvidenceTitle: "Visual review 1", Content: `{"verdict":"PASSED","reason":"forged"}`})
	require.Error(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Where(q.Kind.Eq("VISUAL_REVIEW")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestVisualReviewEveryStorageFailurePreservesCaptureAndDecision(t *testing.T) {
	for _, command := range []string{"record", "list", "receipt"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, id, in := visualReviewFixture(t)
				if command != "record" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecordVisualReviewOn(f.human, tx, 7, id, in); return e }))
				}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "record":
						out, e := f.m.RecordVisualReviewOn(f.human, tx, 7, id, in)
						if e == nil {
							require.Equal(t, in.TaskID, out.TaskID)
							require.Equal(t, "PASSED", out.Verdict)
						}
						return e
					case "list":
						out, e := f.m.listVisualReviewsOn(f.human, tx, 7, id, 0, 10)
						if e == nil {
							require.Len(t, out.Items, 1)
							require.Equal(t, in.CaptureReceiptHash, out.Items[0].CaptureReceiptHash)
							require.NotNil(t, out.Items[0].Decision)
						}
						return e
					default:
						out, e := f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, 50, in.TaskID)
						if e == nil {
							require.Equal(t, "PASSED", out.Verdict)
							require.EqualValues(t, 12, out.ReviewerID)
						}
						return e
					}
				}
			})
		})
	}
}

func TestVisualReviewListStorageFailuresNeverPublishPartialDecisions(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, id, in := visualReviewFixture(t)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecordVisualReviewOn(f.human, tx, 7, id, in); return e }))
		return f, func(tx *gorm.DB) error {
			out, e := f.m.listVisualReviewsOn(f.human, tx, 7, id, 0, 50)
			if e == nil {
				require.Len(t, out.Items, 1)
			}
			return e
		}
	})
}

func TestVisualReviewReadAndDecisionFailClosedOnCurrentDependencyErrors(t *testing.T) {
	for _, command := range []string{"list", "record"} {
		faults := []string{"policy_missing", "principal_missing", "principal_error", "lock_error", "policy_error", "agent_missing", "agent_error", "agent_inactive", "review_authority", "missing_task", "corrupt_capture"}
		if command == "list" {
			faults = append(faults, "corrupt_artifact")
		}
		for _, fault := range faults {
			t.Run(command+"/"+fault, func(t *testing.T) {
				f, execution, in := visualReviewFixture(t)
				failure := errors.New("review source unavailable")
				switch fault {
				case "policy_missing":
					f.m.policy = nil
				case "principal_missing":
					f.m.principals.ActiveHumanByID = nil
				case "principal_error":
					f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
						return identity.Principal{}, false, failure
					}
				case "lock_error":
					f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
				case "policy_error":
					f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, failure
					}
				case "agent_missing", "agent_error", "agent_inactive":
					f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
						return identity.Principal{}, false, nil
					}
					f.m.agents.ByPrincipalOn = nil
					if fault != "agent_missing" {
						f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
							if fault == "agent_error" {
								return fleet.AgentIdentity{}, false, failure
							}
							return fleet.AgentIdentity{State: "RETIRED"}, true, nil
						}
					}
				case "review_authority":
					f.m.work.AuthorizeVisualReviewOn = func(context.Context, *gorm.DB, WorkLink, guid.ID, int, guid.ID, string) error { return failure }
				case "missing_task":
					in.TaskID = 999
				case "corrupt_capture":
					require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", in.TaskID).Update("observation_json", "{").Error)
				case "corrupt_artifact":
					require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("kind=?", "FILE").Update("content", "{").Error)
				}
				before := f.read(t, execution)
				e := f.db.Transaction(func(tx *gorm.DB) error {
					if command == "list" {
						_, e := f.m.listVisualReviewsOn(f.human, tx, 7, execution, 0, 50)
						return e
					}
					_, e := f.m.RecordVisualReviewOn(f.human, tx, 7, execution, in)
					return e
				})
				if command == "list" && (fault == "review_authority" || fault == "missing_task") {
					require.NoError(t, e)
				} else {
					require.Error(t, e)
				}
				require.Equal(t, before, f.read(t, execution))
			})
		}
	}
}
