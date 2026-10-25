package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProvenanceFailedPartialOutputsPinnedComparisonAndScope(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Updates(map[string]any{"state": "FAILED"}).Error)
	var prior model.Execution
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Take(&prior).Error)
	prior.ID = v.ID + 1000
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&prior).Error)
	criterion := guid.ID(77)
	artifact := ArtifactFact{BindingID: 91, SpaceID: 92, FileID: 93, VersionID: 94, FileVersion: 3, CriterionID: &criterion, Coverage: "PARTIAL", Note: "Incomplete; review before reuse", SHA256: hash([]byte("actual bytes"))}
	content, err := json.Marshal(artifact)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	rows := []model.Evidence{{ID: 9007199254740993, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "FILE", Title: "output", Content: string(content), ContentHash: artifact.SHA256, CreatedAt: now}, {ID: 9007199254740994, TenantID: 7, ProjectID: 9, ExecutionID: prior.ID, Kind: "FILE", Title: "output", Content: string(content), ContentHash: artifact.SHA256, CreatedAt: now}, {ID: 9007199254740995, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "OBSERVATION", Title: "diagnostic", Content: "failed halfway", ContentHash: hash([]byte("failed halfway")), CreatedAt: now}}
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&rows).Error)
	read := func(compare, before guid.ID, limit int) (out EvidenceProvenancePage, err error) {
		err = f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.provenanceOn(f.human, tx, 7, v.ID, before, compare, limit)
			return e
		})
		return
	}
	page, err := read(prior.ID, 0, 1)
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Equal(t, "PARTIAL", page.Items[0].OutputState)
	require.Equal(t, "ONLY_CURRENT", page.Items[0].Comparison)
	page2, err := read(prior.ID, *page.NextBeforeID, 1)
	require.NoError(t, err)
	require.False(t, page2.HasMore)
	require.Equal(t, "SAME", page2.Items[0].Comparison)
	require.Equal(t, int64(3), page2.Items[0].Artifact.FileVersion)
	require.Equal(t, now, page2.Items[0].CreatedAt)
	require.Equal(t, v.PlanHash, page2.Items[0].PlanHash)
	require.Equal(t, rows[1].ID, *page2.Items[0].ComparedEvidenceID)
	encoded, err := json.Marshal(page2)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "failed halfway")
	require.Contains(t, string(encoded), "9007199254740993")
	require.Contains(t, page2.Limitations, "ASSOCIATION_IS_NOT_VERIFICATION")
	// An ambiguous kind/title/criterion must not silently select one version.
	extra := rows[1]
	extra.ID++
	extra.ID += 100
	artifact.SHA256 = hash([]byte("different output"))
	otherContent, err := json.Marshal(artifact)
	require.NoError(t, err)
	extra.Content = string(otherContent)
	extra.ContentHash = artifact.SHA256
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&extra).Error)
	ambiguous, err := read(prior.ID, rows[2].ID, 1)
	require.NoError(t, err)
	require.Equal(t, "UNKNOWN", ambiguous.Items[0].Comparison)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", prior.ID).Update("work_item_id", 999).Error)
	_, err = read(prior.ID, 0, 50)
	require.ErrorIs(t, err, ErrForbidden)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = read(0, 0, 50)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestProvenanceHTTPBoundsAuthorizationAndCorruptPin(t *testing.T) {
	f, router := executionRoutesFixture(t)
	v := f.create(t)
	base := "/executions/" + v.ID.String() + "/evidence-provenance"
	r := executionRouteRequest(t, router, http.MethodGet, base, nil, 0, false)
	page := executionRouteData[EvidenceProvenancePage](t, r, 200)
	require.Empty(t, page.Items)
	require.NotNil(t, page.Verifiers)
	for _, query := range []string{"?limit=101", "?cursor=9999999999999999999", "?cursor=1&before_id=2", "?before_id=9999999999999999999", "?compare_execution_id=" + v.ID.String(), "?sort=id"} {
		r = executionRouteRequest(t, router, http.MethodGet, base+query, nil, 0, false)
		require.Equal(t, 422, r.Code, r.Body.String())
	}
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&model.Evidence{ID: 9007199254740993, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "FILE", Title: "broken", Content: `{"sha256":"invented"}`, ContentHash: hash([]byte("bytes")), CreatedAt: time.Now().UTC()}).Error)
	r = executionRouteRequest(t, router, http.MethodGet, base, nil, 0, false)
	require.Equal(t, 503, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "invented")
}

func TestProvenanceComparisonRejectsUnavailableOrCorruptCurrentSource(t *testing.T) {
	for _, fault := range []string{"compare_missing", "current_plan", "compare_plan", "compare_policy", "current_evidence", "compare_evidence"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			var prior model.Execution
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).First(&prior).Error)
			prior.ID += 1000
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&prior).Error)
			expected := ErrUnavailable
			compare := prior.ID
			switch fault {
			case "compare_missing":
				compare++
				expected = ErrNotFound
			case "current_plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{").Error)
			case "compare_plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", prior.ID).Update("plan_json", "{").Error)
			case "compare_policy":
				calls := 0
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					calls++
					return authz.DataConstraint{AllowAll: calls == 1}, nil
				}
				expected = ErrForbidden
			case "current_evidence", "compare_evidence":
				execution := v.ID
				if fault == "compare_evidence" {
					execution = prior.ID
				}
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&model.Evidence{ID: 900, TenantID: 7, ProjectID: 9, ExecutionID: execution, Kind: "OBSERVATION", Title: "Recorded output", Content: "Actual", ContentHash: "tampered"}).Error)
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.provenanceOn(f.human, tx, 7, v.ID, 0, compare, 50); return e }), expected)
		})
	}
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f := executionHarness(t)
		v := f.create(t)
		var prior model.Execution
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).First(&prior).Error)
		prior.ID += 1000
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&prior).Error)
		return f, func(tx *gorm.DB) error { _, e := f.m.provenanceOn(f.human, tx, 7, v.ID, 0, prior.ID, 50); return e }
	})
}

func TestProvenanceReadStorageFailuresNeverReturnUnverifiedAssociations(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f := executionHarness(t)
		v := f.create(t)
		return f, func(tx *gorm.DB) error {
			out, e := f.m.provenanceOn(f.human, tx, 7, v.ID, 0, 0, 50)
			if e == nil {
				require.Empty(t, out.Items)
			}
			return e
		}
	})
}
