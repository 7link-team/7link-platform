package execution

import (
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTemplateVerifierQueueConsumesExactFrozenSettingsAndActualReceipt(t *testing.T) {
	f, in := verificationFixture(t)
	definition := templates.VerifierDefinition{Key: "frozen-command", Kind: "DETERMINISTIC", Runner: in.Method, Version: 1, Argv: in.Argv, TimeoutSec: in.TimeoutSec, Params: map[string]any{"threshold": float64(70)}, Limitations: "Checks the frozen repository command only."}
	f.m.templateVerifiers.catalog = templates.VerifierCatalog{ForWorkOn: func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) (templates.VerifierBinding, bool, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 9, project)
		require.EqualValues(t, 3, work)
		return templates.VerifierBinding{TemplateVersionID: 77, DefinitionHash: hash(nil), Verifiers: []templates.VerifierDefinition{definition}}, true, nil
	}}
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.EqualValues(t, 77, plans[0].TemplateVerifier.TemplateVersionID)
	require.Equal(t, definition, plans[0].TemplateVerifier.Verifier)
	read := func() VerifierResultPage {
		var page VerifierResultPage
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			page, e = f.m.verifierResultsOn(f.human, tx, 7, 3, "", "", 50)
			return e
		}))
		return page
	}
	pending := read()
	require.Equal(t, "INCONCLUSIVE", pending.Items[0].Verdict)
	require.Empty(t, pending.Items[0].EvidenceRefs)
	b, e := json.Marshal(plans[0])
	require.NoError(t, e)
	exit := 0
	now := time.Now().UTC()
	o := VerificationObservation{PlanHash: hash(b), HeadCommit: in.HeadCommit, ExitCode: &exit, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: now, EndedAt: now}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, plans[0].Nonce, o)
	}))
	page := read()
	require.Len(t, page.Items, 1)
	result := page.Items[0]
	require.Equal(t, "PASSED", result.Verdict)
	require.Equal(t, "frozen-command", result.VerifierKey)
	require.Equal(t, definition.Params, result.ParamsResolved)
	require.Contains(t, result.EvidenceRefs, "output-sha256:"+hash(nil))
	require.NotEmpty(t, result.Limitations)
	in.Argv = []string{"unapproved-command"}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.queueVerificationOn(f.human, tx, in); return e }), ErrCapabilityUnavailable)
	q := query.BindVerificationTasks(f.db, f.m.names)
	count, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
	_, e = q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.ObservationJSON, `{"passed":100}`)
	require.NoError(t, e)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.verifierResultsOn(f.human, tx, 7, 3, "", "", 50); return e }), ErrUnavailable)
}

func TestTemplateVerifierResultsEnforceCurrentWorkAuthorityAndPagination(t *testing.T) {
	f, in := verificationFixture(t)
	for i := 0; i < 3; i++ {
		in.Stage = "AUTOMATED"
		if i == 0 {
			in.Stage = "OTHER"
		}
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.queueVerificationOn(f.human, tx, in); return e }))
	}
	var first VerifierResultPage
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = f.m.verifierResultsOn(f.human, tx, 7, 3, "AUTOMATED", "", 1)
		return e
	}))
	require.Len(t, first.Items, 1)
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.verifierResultsOn(f.human, tx, 7, 3, "AUTOMATED", first.NextCursor, 1)
		require.Len(t, page.Items, 1)
		require.False(t, page.HasMore)
		require.NotEqual(t, first.Items[0].TaskID, page.Items[0].TaskID)
		return e
	}))
	f.m.templateVerifiers.read = delivery.RealtimeTopics{AuthorizeWork: func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error) { return false, nil }}
	_, e := f.m.verifierResultsHTTP(f.human, &verifierResultsInput{WorkItemID: "3", Limit: 50})
	require.Error(t, e)
	f.m.templateVerifiers.read.AuthorizeWork = func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error) { return true, nil }
	body, e := f.m.verifierResultsHTTP(f.human, &verifierResultsInput{WorkItemID: "3", Limit: 50})
	require.NoError(t, e)
	require.NotNil(t, body)
}

func TestInjectTemplateVerifiersRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectTemplateVerifiers(module.NewServices()), module.ErrServicesProviding)
}

func TestVerifierResultsKeepActualMetricsAndRequireAuthenticReceipt(t *testing.T) {
	for _, fault := range []string{"none", "plan", "observation", "binding", "cursor", "limit"} {
		t.Run(fault, func(t *testing.T) {
			f, in := verificationFixture(t)
			var id guid.ID
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
			var plans []VerificationPlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
				return e
			}))
			require.Len(t, plans, 1)
			raw, e := json.Marshal(plans[0])
			require.NoError(t, e)
			exit, passed, failed, skipped := 0, 3, 0, 1
			now := time.Now().UTC()
			o := VerificationObservation{PlanHash: hash(raw), HeadCommit: in.HeadCommit, ExitCode: &exit, Passed: &passed, Failed: &failed, Skipped: &skipped, Reason: "PROCESS_EXITED", OutputSHA256: hash([]byte("actual output")), ReportSHA256: hash([]byte("actual report")), StartedAt: now, EndedAt: now.Add(time.Millisecond)}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, plans[0].Nonce, o)
			}))
			cursor := ""
			limit := 50
			switch fault {
			case "plan":
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", id).Update("plan_json", "{").Error)
			case "observation":
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", id).Update("observation_json", "{").Error)
			case "binding":
				o.PlanHash = "foreign plan"
				raw, e := json.Marshal(o)
				require.NoError(t, e)
				require.NoError(t, f.db.Table(f.m.names.VerificationTasks.String()).Where("id=?", id).Updates(map[string]any{"observation_json": string(raw), "receipt_hash": hash(raw)}).Error)
			case "cursor":
				cursor = "bad"
			case "limit":
				limit = 0
			}
			e = f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.verifierResultsOn(f.human, tx, 7, 3, "", cursor, limit)
				if fault == "none" && e == nil {
					require.Len(t, out.Items, 1)
					r := out.Items[0]
					require.Equal(t, "PASSED", r.Verdict)
					require.Equal(t, map[string]int64{"exit_code": 0, "passed": 3, "failed": 0, "skipped": 1}, r.Metrics)
					require.NotNil(t, r.DurationMS)
					require.EqualValues(t, 1, *r.DurationMS)
					require.Contains(t, r.EvidenceRefs, "report-sha256:"+o.ReportSHA256)
					require.Contains(t, r.EvidenceRefs, "output-sha256:"+o.OutputSHA256)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, e)
			} else if fault == "cursor" || fault == "limit" {
				require.ErrorIs(t, e, ErrInvalidArgument)
			} else {
				require.ErrorIs(t, e, ErrUnavailable)
			}
		})
	}
	f, id, in := visualReviewFixture(t)
	read := func() VerifierResult {
		var out VerifierResultPage
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.verifierResultsOn(f.human, tx, 7, 3, "", "", 50)
			return e
		}))
		require.Len(t, out.Items, 1)
		return out.Items[0]
	}
	pending := read()
	require.Equal(t, "UNCALIBRATED", pending.CalibrationState)
	require.Equal(t, "VISUAL_REVIEW_REQUIRED", pending.Reason)
	require.Equal(t, "INCONCLUSIVE", pending.Verdict)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RecordVisualReviewOn(f.human, tx, 7, id, in); return e }))
	decided := read()
	require.Equal(t, "PASSED", decided.Verdict)
	require.Equal(t, in.Reason, decided.Reason)
}

func TestVerifierResultsStorageFailuresNeverYieldPartialOutcome(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, in := verificationFixture(t)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.queueVerificationOn(f.human, tx, in); return e }))
		return f, func(tx *gorm.DB) error {
			out, e := f.m.verifierResultsOn(f.human, tx, 7, 3, "", "", 50)
			if e == nil {
				require.Len(t, out.Items, 1)
			}
			return e
		}
	})
}
