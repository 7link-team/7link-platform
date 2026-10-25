package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestVideoAndProviderBindingPropagatesMissingOrRejectedCapabilities(t *testing.T) {
	for _, fault := range []string{"video_error", "provider_missing", "provider_error", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("video binding failed")
			videoBound, providerBound := false, false
			require.NoError(t, module.Provide(s, templates.VideoSourceConnector{Bind: func(source templates.VideoSource) error {
				require.NotNil(t, source)
				videoBound = true
				if fault == "video_error" {
					return failure
				}
				return nil
			}}))
			if fault != "provider_missing" {
				require.NoError(t, module.Provide(s, templates.ProviderExecutionConnector{Bind: func(authorize templates.ProviderExecutionAuthority) error {
					require.NotNil(t, authorize)
					providerBound = true
					if fault == "provider_error" {
						return failure
					}
					return nil
				}}))
			}
			require.NoError(t, s.Seal())
			e := f.m.injectVideo(s)
			if fault == "none" {
				require.NoError(t, e)
				require.True(t, providerBound)
			} else if fault == "provider_missing" {
				require.ErrorIs(t, e, module.ErrServiceMissing)
			} else {
				require.ErrorIs(t, e, failure)
			}
			require.True(t, videoBound)
		})
	}
}

func TestArchivedExecutionHistoryRetainsReadWithoutResponseAuthority(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).UpdateSimple(q.State.Value("SUCCEEDED"), q.LeaseExpiresAt.Null())
	require.NoError(t, err)
	base := f.m.policy
	deniedRead := false
	optionalFailure := delivery.ErrWorkProjectReadOnly
	f.m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if deniedRead && permission == "execution_read" {
			return authz.DataConstraint{}, ErrForbidden
		}
		if permission == "interaction_respond" {
			return authz.DataConstraint{}, optionalFailure
		}
		return base(ctx, tx, tenant, project, permission, actor)
	}
	path := "/executions/" + v.ID.String()
	detail := executionRouteData[ExecutionDetail](t, executionRouteRequest(t, handler, http.MethodGet, path, nil, 0, false), http.StatusOK)
	require.Equal(t, v.ID, detail.Execution.ID)
	require.Equal(t, "SUCCEEDED", detail.Execution.State)
	require.Empty(t, detail.Approvals)
	deniedRead = true
	require.Equal(t, http.StatusForbidden, executionRouteRequest(t, handler, http.MethodGet, path, nil, 0, false).Code)
	deniedRead = false
	optionalFailure = errors.New("current response authority unavailable")
	require.NotEqual(t, http.StatusOK, executionRouteRequest(t, handler, http.MethodGet, path, nil, 0, false).Code)
}

func TestCalibrationProjectionPropagatesCurrentToolAndModelSourceFailures(t *testing.T) {
	for _, fault := range []string{"tool", "model", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			failure := errors.New("calibration source failed")
			pin := &TemplateVerifierPin{TemplateVersionID: 91, Verifier: templates.VerifierDefinition{Key: "model-review", Kind: "DETERMINISTIC"}}
			f.m.templateVerifiers.catalog.ToolCalibration = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (string, error) {
				if fault == "tool" {
					return "", failure
				}
				return "UNCALIBRATED", nil
			}
			f.m.templateVerifiers.catalog.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (templates.VerifierDefinition, string, error) {
				if fault == "model" {
					return templates.VerifierDefinition{}, "", failure
				}
				return templates.VerifierDefinition{}, "CALIBRATED", nil
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				v := VerifierResult{TemplatePin: pin, VerifierKey: "model-review", Kind: "MODEL_SCORED", Verdict: "PASSED"}
				e := f.m.verifierCalibrationOn(f.human, tx, 7, 1, &v)
				if fault == "none" {
					require.NoError(t, e)
					require.Equal(t, "CALIBRATED", v.CalibrationState)
				} else {
					require.ErrorIs(t, e, failure)
				}
				if fault == "tool" {
					require.ErrorIs(t, f.m.receiptCalibrationOn(f.human, tx, 7, 1, VerificationPlan{TemplateVerifier: pin}), failure)
				}
				return nil
			}))
		})
	}
}

func TestToolCalibrationFencesReceiptAndResultProjection(t *testing.T) {
	f, in := verificationFixture(t)
	pin := &TemplateVerifierPin{TemplateVersionID: 91, DefinitionHash: strings.Repeat("a", 64), Verifier: templates.VerifierDefinition{Key: "tool-probe", Kind: "DETERMINISTIC", Runner: "COMMAND", RequiredTools: []string{"node"}}}
	f.m.templateVerifiers.catalog.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (templates.VerifierBinding, bool, error) {
		return templates.VerifierBinding{TemplateVersionID: pin.TemplateVersionID, DefinitionHash: pin.DefinitionHash, Verifiers: []templates.VerifierDefinition{{Key: "tool-probe", Kind: "DETERMINISTIC", Runner: in.Method, Argv: in.Argv, TimeoutSec: in.TimeoutSec, RequiredTools: []string{"node"}}}}, true, nil
	}
	state := "UNCALIBRATED"
	f.m.templateVerifiers.catalog.ToolCalibration = func(_ context.Context, _ *gorm.DB, tenant, version guid.ID, key string, machine guid.ID) (string, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), version)
		require.Equal(t, "tool-probe", key)
		require.Equal(t, guid.ID(1), machine)
		return state, nil
	}
	plan := VerificationPlan{TemplateVerifier: pin}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.receiptCalibrationOn(f.human, tx, 7, 1, plan) }), ErrConflict)
	view := VerifierResult{TemplatePin: pin, VerifierKey: "tool-probe", Kind: "DETERMINISTIC", Verdict: "PASSED"}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.verifierCalibrationOn(f.human, tx, 7, 1, &view) }))
	require.Equal(t, "INCONCLUSIVE", view.Verdict)
	require.Equal(t, "TOOL_UNCALIBRATED", view.Reason)
	state = "CALIBRATED"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.receiptCalibrationOn(f.human, tx, 7, 1, plan) }))
	view.Kind = "MODEL_SCORED"
	view.CalibrationState = "UNCALIBRATED"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.verifierCalibrationOn(f.human, tx, 7, 1, &view) }))
	require.Equal(t, "UNCALIBRATED", view.CalibrationState)
	var task guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; task, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Len(t, plans, 1)
	raw, _ := json.Marshal(plans[0])
	exit := 0
	observation := VerificationObservation{PlanHash: hash(raw), HeadCommit: in.HeadCommit, ExitCode: &exit, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, task, f.fence, plans[0].Nonce, observation)
	}))
	readReceipt := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, 50, task); return e })
	}
	require.NoError(t, readReceipt())
	state = "DRIFTED"
	require.ErrorIs(t, readReceipt(), ErrConflict, "actual workflow-consumed receipt cannot bypass drift")
}

func TestProjectArchiveExecutionRequiresTerminalLeaseRelease(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	read := func() []delivery.ProjectArchiveResource {
		var out []delivery.ProjectArchiveResource
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.projectArchiveResourcesOn(f.human, tx, 7, 9)
			return e
		}))
		return out
	}
	rows := read()
	require.NotEmpty(t, rows)
	require.Equal(t, v.ID, rows[0].ID)
	require.False(t, rows[0].Ready)
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).UpdateSimple(q.State.Value("SUCCEEDED"), q.LeaseExpiresAt.Null())
	require.NoError(t, err)
	require.True(t, read()[0].Ready)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.projectArchiveResourcesOn(f.human, tx, 8, 9)
		require.Empty(t, out)
		return e
	}))
	_, err = f.m.projectArchiveResourcesOn(f.human, f.db, 7, 9)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestProjectArchiveVerificationRequiresDurableReceipt(t *testing.T) {
	f, request := verificationFixture(t)
	var task guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		task, err = f.m.queueVerificationOn(f.human, tx, request)
		return err
	}))
	read := func() bool {
		ready := false
		found := false
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			rows, err := f.m.projectArchiveResourcesOn(f.human, tx, 7, 9)
			for _, r := range rows {
				if r.Kind == "VERIFICATION_TASK" && r.ID == task {
					found = true
					ready = r.Ready
				}
			}
			return err
		}))
		require.True(t, found)
		return ready
	}
	require.False(t, read(), "queued verification is an outstanding obligation")
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		plans, err = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return err
	}))
	require.Len(t, plans, 1)
	require.False(t, read(), "leased verification must finish")
	planJSON, err := json.Marshal(plans[0])
	require.NoError(t, err)
	exit := 0
	observation := VerificationObservation{PlanHash: hash(planJSON), HeadCommit: request.HeadCommit, ExitCode: &exit, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, task, f.fence, plans[0].Nonce, observation)
	}))
	require.True(t, read(), "canonical DONE retains historical lease but releases machine slot")
	q := query.BindVerificationTasks(f.db, f.m.names)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(task)).UpdateSimple(q.ReceiptHash.Value("corrupt"))
	require.NoError(t, err)
	require.False(t, read(), "DONE alone cannot replace a valid receipt")
}

func TestVideoSourceUsesCurrentAuthorityAndImmutableArtifactHash(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	read := func(tenant, project guid.ID) (out templates.VideoExecution, err error) {
		err = f.db.Transaction(func(tx *gorm.DB) error {
			out, err = f.m.videoSourceOn(f.human, tx, tenant, project, v.ID)
			return err
		})
		return
	}
	out, e := read(7, 9)
	require.NoError(t, e)
	require.Equal(t, v.ID, out.ExecutionID)
	require.Empty(t, out.Artifacts)
	require.Empty(t, out.QC)
	_, e = read(7, 10)
	require.ErrorIs(t, e, ErrNotFound)
	a := ArtifactFact{BindingID: 501, FileID: 502, VersionID: 503, SHA256: strings.Repeat("a", 64)}
	raw, _ := json.Marshal(a)
	q := query.BindEvidence(f.db, f.m.names)
	row := &model.Evidence{ID: 900, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "ARTIFACT", Title: "actual fixture", Content: string(raw), ContentHash: hash(raw), CreatedAt: time.Now().UTC()}
	require.NoError(t, q.WithContext(f.human).Create(row))
	out, e = read(7, 9)
	require.NoError(t, e)
	require.Len(t, out.Artifacts, 1)
	require.Equal(t, a.SHA256, out.Artifacts[0].SHA256)
	_, e = q.WithContext(f.human).Where(q.ID.Eq(row.ID)).Update(q.ContentHash, strings.Repeat("b", 64))
	require.NoError(t, e)
	_, e = read(7, 9)
	require.ErrorIs(t, e, ErrUnavailable)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = read(7, 9)
	require.ErrorIs(t, e, ErrForbidden)
}
func TestProviderAuthorityRequiresCurrentInitiatorAndWorkDependencies(t *testing.T) {
	for _, fault := range []string{"project", "id", "initiator", "lock", "work_missing", "work_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			id, project := v.ID, guid.ID(9)
			failure := errors.New("provider work authority failed")
			expected := failure
			switch fault {
			case "project":
				project++
				expected = ErrNotFound
			case "id":
				id = 0
				expected = ErrInvalidArgument
			case "initiator":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("initiator_id", 0).Error)
				expected = ErrCapabilityUnavailable
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "work_missing":
				f.m.work.AuthorizeOn = nil
				expected = ErrCapabilityUnavailable
			case "work_error":
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return failure }
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.providerAuthorityOn(f.human, tx, 7, project, id, "EXECUTE") }), expected)
		})
	}
}

func TestProviderAuthorityRejectsUnstartedAndRevokedExecution(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	check := func(op string) error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.providerAuthorityOn(f.human, tx, 7, 9, v.ID, op) })
	}
	require.NoError(t, check("READ"))
	require.ErrorIs(t, check("GENERATE"), ErrConflict)
	require.ErrorIs(t, check("invented"), ErrInvalidArgument)
	q := query.BindExecutions(f.db, f.m.names)
	lease := time.Now().Add(time.Hour)
	_, e := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"state": "RUNNING", "lease_expires_at": lease})
	require.NoError(t, e)
	require.NoError(t, check("GENERATE"))
	require.NoError(t, check("EXECUTE"))
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, check("EXECUTE"), ErrForbidden)
}

func TestVideoQCReadsOnlyPinnedDeterministicImmutableReceipts(t *testing.T) {
	f, in := verificationFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
	q := query.BindVerificationTasks(f.db, f.m.names)
	row, e := q.WithContext(f.human).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	// Explicit trusted-source fixture pins the video verifier before Runner pull.
	var plan VerificationPlan
	require.NoError(t, json.Unmarshal([]byte(row.PlanJSON), &plan))
	plan.TemplateVerifier = &TemplateVerifierPin{TemplateVersionID: 90, DefinitionHash: strings.Repeat("d", 64), Verifier: templates.VerifierDefinition{Key: "av_qc", Kind: "DETERMINISTIC", Runner: "COMMAND"}}
	raw, e := json.Marshal(plan)
	require.NoError(t, e)
	_, e = q.WithContext(f.human).Where(q.ID.Eq(id)).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)})
	require.NoError(t, e)
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Len(t, plans, 1)
	zero := 0
	now := time.Now().UTC()
	o := VerificationObservation{PlanHash: hash(raw), HeadCommit: in.HeadCommit, ExitCode: &zero, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: now, EndedAt: now}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, plans[0].Nonce, o)
	}))
	read := func() (out templates.VideoExecution, e error) {
		e = f.db.Transaction(func(tx *gorm.DB) error { out, e = f.m.videoSourceOn(f.human, tx, 7, 9, in.ExecutionID); return e })
		return
	}
	out, e := read()
	require.NoError(t, e)
	require.Len(t, out.QC, 1)
	require.Equal(t, id, out.QC[0].TaskID)
	require.Equal(t, in.ResultID, out.QC[0].ResultID)
	require.Equal(t, 2, out.QC[0].ResultVersion)
	require.Equal(t, "av_qc", out.QC[0].Verifier)
	require.Equal(t, "PASSED", out.QC[0].Verdict)
	require.Equal(t, in.HeadCommit, out.QC[0].HeadCommit)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		fact, e := f.m.rubricProofOn(f.human, tx, templates.RubricProofRequest{ProjectID: 9, WorkItemID: 3, ResultID: in.ResultID, ResultVersion: 2, HeadCommit: in.HeadCommit, VerifierKeys: []string{"av_qc"}})
		require.NoError(t, e)
		require.True(t, fact.Passed)
		require.Equal(t, id, fact.TaskID)
		require.Equal(t, out.QC[0].ReceiptSHA256, fact.ReceiptHash)
		_, e = f.m.rubricProofOn(f.human, tx, templates.RubricProofRequest{ProjectID: 9, WorkItemID: 3, ResultID: in.ResultID, ResultVersion: 2, HeadCommit: in.HeadCommit, VerifierKeys: []string{"unrelated"}})
		require.Error(t, e)
		return nil
	}))

	_, e = q.WithContext(f.human).Where(q.ID.Eq(id)).Update(q.ReceiptHash, "corrupt")
	require.NoError(t, e)
	_, e = read()
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestInjectArchiveCalibrationRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectArchiveCalibration(module.NewServices()), module.ErrServicesProviding)
}

func TestInjectVideoRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectVideo(module.NewServices()), module.ErrServicesProviding)
}

func TestInjectRubricProofRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectRubricProof(module.NewServices()), module.ErrServicesProviding)
}

func TestArchiveAndVideoReadsPropagateEveryStorageFailure(t *testing.T) {
	for _, command := range []string{"archive", "video", "provider"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				v := f.create(t)
				return f, func(tx *gorm.DB) error {
					switch command {
					case "archive":
						out, e := f.m.projectArchiveResourcesOn(f.human, tx, 7, 9)
						if e == nil {
							require.NotEmpty(t, out)
						}
						return e
					case "video":
						out, e := f.m.videoSourceOn(f.human, tx, 7, 9, v.ID)
						if e == nil {
							require.NotNil(t, out)
						}
						return e
					default:
						return f.m.providerAuthorityOn(f.human, tx, 7, 9, v.ID, "READ")
					}
				}
			})
		})
	}
}
