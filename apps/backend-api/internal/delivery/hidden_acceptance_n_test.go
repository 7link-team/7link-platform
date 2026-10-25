package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHiddenAcceptanceReconstructsFrozenSourceAndRejectsStaleReceipts(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	work := h.stored()
	requirement, execution := guid.ID(444), guid.ID(446)
	head := strings.Repeat("b", 40)
	now := time.Now().UTC()
	require.NoError(t, query.BindRequirements(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.Requirement{TenantID: 1, ID: requirement, ProjectID: 99, Number: 444, Status: "BASELINED", Title: "Frozen source", Priority: 1, Version: 1, CurrentVersionNo: 1, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"requirement_id": requirement, "baseline_version_no": 1}).Error)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", result.ID).Updates(map[string]any{"baseline_version_no": 1, "source_execution_id": execution, "head_commit": head}).Error)
	suite, err := json.Marshal(requirementFrozenSuite{RequirementID: requirement, VersionNo: 1, Cases: []RequirementCaseSnapshot{{ID: 700, WorkItemID: work.ID, Version: 1, IsHiddenAcceptance: true, ContentHash: strings.Repeat("a", 64)}}})
	require.NoError(t, err)
	hash := sha256.Sum256(suite)
	suiteHash := hex.EncodeToString(hash[:])
	require.NoError(t, query.BindRequirementVersions(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.RequirementVersion{TenantID: 1, ID: 445, ProjectID: 99, RequirementID: requirement, VersionNo: 1, Title: "Frozen source", Priority: 1, Criteria: "[]", ContentHash: strings.Repeat("c", 64), AcceptanceSuite: string(suite), SuiteHash: suiteHash, CreatedAt: now}))
	in := HiddenAcceptanceRequest{TenantID: 1, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, ExecutionID: 999, SuiteHash: "caller cannot choose suite", HeadCommit: "caller cannot choose HEAD", Cases: []RequirementCaseSnapshot{{ID: 999}}}
	read := func(in HiddenAcceptanceRequest) (out HiddenAcceptanceRequest, err error) {
		err = h.db.Transaction(func(tx *gorm.DB) error {
			out, err = h.m.readHiddenAcceptanceRequestOn(h.ctx, tx, in)
			return err
		})
		return
	}
	out, err := read(in)
	require.NoError(t, err)
	require.Equal(t, execution, out.ExecutionID)
	require.Equal(t, head, out.HeadCommit)
	require.Equal(t, suiteHash, out.SuiteHash)
	require.Len(t, out.Cases, 1)
	require.Equal(t, guid.ID(700), out.Cases[0].ID)
	for _, fault := range []string{"tenant", "project", "work", "result"} {
		bad := in
		switch fault {
		case "tenant":
			bad.TenantID++
		case "project":
			bad.ProjectID++
		case "work":
			bad.WorkItemID++
		case "result":
			bad.ResultID++
		}
		empty, err := read(bad)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, fault)
		require.Empty(t, empty.Cases)
	}
	for _, changes := range []map[string]any{{"baseline_stale": true}, {"current_claim_id": nil}, {"state": "IN_PROGRESS"}} {
		require.EqualError(t, h.db.Transaction(func(tx *gorm.DB) error {
			require.NoError(t, tx.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(changes).Error)
			empty, err := h.m.readHiddenAcceptanceRequestOn(h.ctx, tx, in)
			require.ErrorIs(t, err, ErrResultState)
			require.Empty(t, empty.Cases)
			return errors.New("rollback test mutation")
		}), "rollback test mutation")
	}
}

func TestHiddenVerifierRequiresActualVerdictAndDispatchAcknowledgment(t *testing.T) {
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
	result := &model.ResultVersion{ID: 900, TenantID: 1, ProjectID: 99, WorkItemID: work.ID, BaselineVersionNo: 2}
	failure := errors.New("verifier unavailable")
	for _, outcome := range []string{"FAILED", "verdict error", "receipt error", "pending", "PASSED"} {
		m.hiddenVerifier = HiddenAcceptanceVerifier{PassedOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (bool, error) {
			if outcome == "receipt error" {
				return false, failure
			}
			return outcome == "PASSED", nil
		}, VerdictOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (string, error) {
			if outcome == "verdict error" {
				return "", failure
			}
			return outcome, nil
		}}
		err := m.hiddenAcceptanceGateOn(ctx, db, work, result)
		if outcome == "PASSED" {
			require.NoError(t, err)
		} else if outcome == "FAILED" {
			require.ErrorIs(t, err, ErrHiddenAcceptanceFailed)
		} else {
			require.ErrorIs(t, err, ErrHiddenAcceptanceUnavailable)
		}
	}
	m.hiddenVerifier = HiddenAcceptanceVerifier{}
	require.NoError(t, m.dispatchHiddenAcceptanceOn(ctx, db, work, result), "no dispatcher does not claim a receipt")
	require.ErrorIs(t, m.hiddenAcceptanceGateOn(ctx, db, work, result), ErrHiddenAcceptanceUnavailable)
	m.hiddenVerifier.DispatchOn = func(_ context.Context, _ *gorm.DB, in HiddenAcceptanceRequest) error {
		require.Equal(t, result.ID, in.ResultID)
		require.Len(t, in.Cases, 1)
		return failure
	}
	require.ErrorIs(t, m.dispatchHiddenAcceptanceOn(ctx, db, work, result), failure)
	m.hiddenVerifier.PrincipalID = func() guid.ID { return 12 }
	// Use the actual stored work baseline, then exercise a missing canonical work.
	_, err = m.hiddenAcceptancePrincipalsOn(ctx, db, 1, 99, guid.ID(999999))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	bad := *result
	bad.BaselineVersionNo++
	_, err = m.hiddenAcceptanceRequestOn(ctx, db, work, &bad)
	require.ErrorIs(t, err, ErrInvalidArgument)
	s := module.NewServices()
	require.NoError(t, m.provideHiddenAcceptance(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[HiddenAcceptanceConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(HiddenAcceptanceVerifier{}), ErrInvalidArgument)
	m.hiddenVerifier = HiddenAcceptanceVerifier{}
	v := HiddenAcceptanceVerifier{DispatchOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) error { return nil }, PassedOn: func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (bool, error) { return true, nil }}
	require.NoError(t, connector.Bind(v))
	require.ErrorIs(t, connector.Bind(v), ErrInvalidArgument)
}
