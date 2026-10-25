package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHiddenRequirementCasesRejectPlaintextAndInvalidHashes(t *testing.T) {
	valid := RequirementCaseSnapshot{ID: 1, Version: 1, WorkItemID: 2, IsHiddenAcceptance: true, ContentHash: strings.Repeat("ab", 32)}
	require.NoError(t, validateHiddenRequirementCases([]RequirementCaseSnapshot{valid}))
	for _, alter := range []func(*RequirementCaseSnapshot){
		func(c *RequirementCaseSnapshot) { c.Title = "secret title" },
		func(c *RequirementCaseSnapshot) { c.Steps = "secret steps" },
		func(c *RequirementCaseSnapshot) { c.Expected = "secret result" },
		func(c *RequirementCaseSnapshot) { c.ContentHash = "" },
		func(c *RequirementCaseSnapshot) { c.ContentHash = strings.Repeat("g", 64) },
		func(c *RequirementCaseSnapshot) { c.ContentHash = strings.ToUpper(c.ContentHash) },
		func(c *RequirementCaseSnapshot) { c.Version = 0 },
	} {
		bad := valid
		alter(&bad)
		require.ErrorIs(t, validateHiddenRequirementCases([]RequirementCaseSnapshot{bad}), ErrInvalidArgument)
	}
	require.NoError(t, validateHiddenRequirementCases([]RequirementCaseSnapshot{{ID: 3, Title: "Public", Steps: "Public steps"}}))
}

func TestHiddenFrozenSuiteCannotBeSkippedByVerificationGate(t *testing.T) {
	m, db, ctx, req, change := scopeHarness(t)
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workCreateBody{Title: "Implement", Type: "TASK", Objective: "Implement requirement", AccountablePrincipalID: 11, RequirementID: &req.ID, BaselineVersionNo: &req.CurrentVersionNo, Criteria: []workCriterionInput{{Text: "Output", VerificationMethod: "Inspect output"}}}})
	require.NoError(t, err)
	m.requirementCases = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) ([]RequirementCaseSnapshot, error) {
		return []RequirementCaseSnapshot{{ID: 700, WorkItemID: ids[0], Version: 1, IsHiddenAcceptance: true, ContentHash: strings.Repeat("ab", 32)}}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.scopeChangeOn(ctx, tx, 1, 99, req.ID, 11, req.Version, "", change)
		return err
	}))
	work := &model.WorkItem{TenantID: 1, ProjectID: 99, ID: created.Body.Data.ID, RequirementID: &req.ID, BaselineVersionNo: 2}
	for _, internal := range []bool{false, true} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			return m.verificationGateOn(ctx, tx, work, &model.ResultVersion{TenantID: work.TenantID, ProjectID: work.ProjectID, WorkItemID: work.ID, BaselineVersionNo: work.BaselineVersionNo}, internal)
		}), ErrHiddenAcceptanceUnavailable)
	}
	q := query.BindRequirementVersions(db, m.service.names)
	version, err := q.WithContext(ctx).Where(q.RequirementID.Eq(req.ID), q.VersionNo.Eq(2)).First()
	require.NoError(t, err)
	require.Contains(t, version.AcceptanceSuite, strings.Repeat("ab", 32))
	require.NotContains(t, version.AcceptanceSuite, "SECRET")
	work.ID++
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.hiddenAcceptanceGateOn(ctx, tx, work) }))
	work.ID--
	_, err = q.WithContext(ctx).Where(q.ID.Eq(version.ID)).UpdateSimple(q.SuiteHash.Value(strings.Repeat("0", 64)))
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.hiddenAcceptanceGateOn(ctx, tx, work) }))
}

func TestHiddenLateReceiptReconcilesCompletedOrdinaryRuns(t *testing.T) {
	for _, verdict := range []string{"PASSED", "FAILED", "UNAVAILABLE"} {
		t.Run(verdict, func(t *testing.T) {
			h, r, in := verificationFixture(t)
			require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.Requirement{TenantID: 1, ID: 444, ProjectID: 99, Number: 444, Status: "BASELINED", Title: "Frozen", Description: "", Priority: 1, Version: 1, CurrentVersionNo: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}))
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"state": "INTERNAL_QA", "requirement_id": 444, "baseline_version_no": 1, "internal_qa_required": false}).Error)
			require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", r.ID).Update("baseline_version_no", 1).Error)
			suite, _ := json.Marshal(requirementFrozenSuite{RequirementID: 444, VersionNo: 1, Cases: []RequirementCaseSnapshot{{ID: 700, WorkItemID: h.work, Version: 1, IsHiddenAcceptance: true, ContentHash: strings.Repeat("a", 64)}}})
			hash := sha256.Sum256(suite)
			require.NoError(t, query.BindRequirementVersions(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.RequirementVersion{TenantID: 1, ID: 445, ProjectID: 99, RequirementID: 444, VersionNo: 1, Title: "Frozen", Description: "", Priority: 1, Criteria: "[]", ContentHash: strings.Repeat("b", 64), AcceptanceSuite: string(suite), SuiteHash: hex.EncodeToString(hash[:]), CreatedAt: time.Now().UTC()}))
			at := time.Now().UTC()
			require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.VerificationRun{TenantID: 1, ID: 889, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "PASSED", ReceiptHash: strings.Repeat("c", 64), ActorID: 12, CreatedAt: at, CompletedAt: &at}))
			h.m.hiddenVerifier = HiddenAcceptanceVerifier{PassedOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (bool, error) {
				return verdict == "PASSED", nil
			}, VerdictOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (string, error) { return verdict, nil }}
			apply := func() error {
				return h.db.Transaction(func(tx *gorm.DB) error {
					work := h.stored()
					row, e := h.m.resultOn(h.ctx, tx, 1, 99, h.work, r.ID)
					if e != nil {
						return e
					}
					_, e = h.m.collectLockedVerificationOn(h.ctx, tx, work, row, 0, true)
					return e
				})
			}
			require.NoError(t, apply())
			work := h.stored()
			if verdict == "UNAVAILABLE" {
				require.Equal(t, "INTERNAL_QA", work.State)
				require.Zero(t, work.InternalRoundCount)
			} else {
				require.Equal(t, "NEEDS_HUMAN_REVIEW", work.State)
			}
			if verdict == "FAILED" {
				require.Equal(t, 1, work.InternalRoundCount)
				require.NotNil(t, work.EscalationReason)
				require.Equal(t, "MANDATORY_HUMAN_GATE", *work.EscalationReason)
				require.True(t, work.RequireHumanDecision)
			}
			before := h.snapshot()
			require.NoError(t, apply())
			require.Equal(t, before, h.snapshot(), "duplicate hidden event cannot change state or emit another event")
		})
	}
}
