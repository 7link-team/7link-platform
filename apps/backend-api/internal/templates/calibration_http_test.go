package templates

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCalibrationHTTPIdempotencyAuthorityPagingAndTruthfulUnavailable(t *testing.T) {
	m, _, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	request := &calibrationStartInput{IdempotencyKey: "calibration-start-once", Body: in}
	first, e := m.calibrationStartHTTP(ctx, request)
	require.NoError(t, e)
	replay, e := m.calibrationStartHTTP(ctx, request)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	require.Equal(t, "UNAVAILABLE", first.Body.Data.RunState)
	require.Nil(t, first.Body.Data.AgreementPPM)
	got, e := m.calibrationGetHTTP(ctx, &calibrationGetInput{ID: first.Body.Data.ID.String()})
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, got.Body.Data)
	page, e := m.calibrationListHTTP(ctx, &calibrationListInput{TemplateKey: "calibration", Sort: "id", Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	samples, e := m.calibrationSampleListHTTP(ctx, &calibrationSampleListInput{VersionID: in.TemplateVersionID.String(), VerifierKey: in.VerifierKey, Sort: "id", Limit: 3})
	require.NoError(t, e)
	require.Len(t, samples.Body.Data.Items, 3)
	require.True(t, samples.Body.Data.HasMore)
	next, e := m.calibrationSampleListHTTP(ctx, &calibrationSampleListInput{VersionID: in.TemplateVersionID.String(), VerifierKey: in.VerifierKey, Sort: "id", Limit: 3, Cursor: samples.Body.Data.NextCursor})
	require.NoError(t, e)
	require.NotEqual(t, samples.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	confirm := &calibrationConfirmInput{ID: first.Body.Data.ID.String(), IfMatch: "2", IdempotencyKey: "accept-unavailable"}
	confirm.Body.Accept = true
	_, e = m.calibrationConfirmHTTP(ctx, confirm)
	assertStatus(t, e, 409)
	request.Body.ProviderRef = "changed-provider"
	_, e = m.calibrationStartHTTP(ctx, request)
	assertStatus(t, e, 409)
	request.Body = in
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.calibrationStartHTTP(ctx, request)
	assertStatus(t, e, 403)
}
func TestCalibrationSampleHTTPBatchKeepsLabelsImmutableAndAtomic(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 0)
	request := &calibrationSampleInput{VersionID: in.TemplateVersionID.String(), IdempotencyKey: "sample-batch"}
	request.Body.VerifierKey = in.VerifierKey
	request.Body.Samples = []CalibrationLabel{{Input: json.RawMessage(`{"sample":0}`), Expected: "PASS"}, {Input: json.RawMessage(`{"sample":1}`), Expected: "FAIL"}}
	first, e := m.calibrationSampleCreateHTTP(ctx, request)
	require.NoError(t, e)
	require.Len(t, first.Body.Data.Items, 2)
	replay, e := m.calibrationSampleCreateHTTP(ctx, request)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	request.IdempotencyKey = "sample-batch-rollback"
	request.Body.Samples = []CalibrationLabel{{Input: json.RawMessage(`{"sample":2}`), Expected: "PASS"}, {Input: json.RawMessage(`{"sample":0}`), Expected: "FAIL"}}
	_, e = m.calibrationSampleCreateHTTP(ctx, request)
	assertStatus(t, e, 409)
	var n int64
	require.NoError(t, db.Table(m.names.CalibrationSamples.String()).Count(&n).Error)
	require.EqualValues(t, 2, n, "a duplicate label rolls back the whole batch")
}

func TestCalibrationHTTPFiltersIdentityCurrentReadsAndCorruption(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	first := calibrationStartForTest(t, m, db, ctx, in)
	in.ProviderRef = "second-source"
	second := calibrationStartForTest(t, m, db, ctx, in)
	page, e := m.calibrationListHTTP(ctx, &calibrationListInput{TemplateKey: "calibration", VerifierKey: in.VerifierKey, State: "UNCALIBRATED", Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.calibrationListHTTP(ctx, &calibrationListInput{Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.Equal(t, second.ID, next.Body.Data.Items[0].ID)
	for _, f := range []func() error{
		func() error {
			_, e := m.calibrationListHTTP(ctx, &calibrationListInput{Limit: 0, Sort: "id"})
			return e
		},
		func() error { _, e := m.calibrationGetHTTP(ctx, &calibrationGetInput{ID: "bad"}); return e },
		func() error {
			_, e := m.calibrationConfirmHTTP(ctx, &calibrationConfirmInput{ID: first.ID.String(), IfMatch: "bad"})
			return e
		},
		func() error {
			_, e := m.calibrationSampleCreateHTTP(ctx, &calibrationSampleInput{VersionID: in.TemplateVersionID.String()})
			return e
		},
		func() error {
			_, e := m.calibrationSampleListHTTP(ctx, &calibrationSampleListInput{VersionID: "bad", Limit: 1, Sort: "id"})
			return e
		},
	} {
		assertStatus(t, f(), 422)
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("calibration-read-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Calibrations.String() || tx.Statement.Table == m.names.CalibrationSamples.String() {
			tx.AddError(errors.New("storage unavailable"))
		}
	}))
	_, e = m.calibrationListHTTP(ctx, &calibrationListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	_, e = m.calibrationGetHTTP(ctx, &calibrationGetInput{ID: first.ID.String()})
	assertStatus(t, e, 500)
	_, e = m.calibrationSampleListHTTP(ctx, &calibrationSampleListInput{VersionID: in.TemplateVersionID.String(), Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	require.NoError(t, db.Callback().Query().Remove("calibration-read-failure"))
	require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Where("calibration_id=?", first.ID).Update("results", "invalid").Error)
	_, e = m.calibrationGetHTTP(ctx, &calibrationGetInput{ID: first.ID.String()})
	assertStatus(t, e, 500)
}
