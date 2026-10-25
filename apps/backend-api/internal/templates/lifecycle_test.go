package templates

import (
	"7link.com/platform/internal/delivery"
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func lifecycleHarness(t *testing.T) (*Module, *gorm.DB, context.Context, ObservationSource) {
	t.Helper()
	m, db, ctx := templateHarness(t)
	v := draftTemplate(t, m, db, ctx, "lifecycle")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, v.ID, v.Hash); return e }))
	return m, db, ctx, ObservationSource{TemplateVersionID: v.ID, SourceKey: "actual-result:1", Reference: "verifier:stable", EvidenceIDs: []guid.ID{501}}
}

func TestLifecycleProviderReplayAuthorityAndAtomicity(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	var first LifecycleView
	publishCount := 0
	m.events = testPublisher(func(_ context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
		publishCount++
		require.Equal(t, "provider.state_changed", e.Type)
		return 77, nil
	})
	in := ProviderObservation{Source: s, State: "UNAVAILABLE", BlockedWorkIDs: []guid.ID{501, 502}}
	record := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			v, e := m.recordProviderOn(ctx, tx, in)
			if e == nil {
				if first.ID == 0 {
					first = v
				} else {
					require.Equal(t, first, v)
				}
			}
			return e
		})
	}
	require.NoError(t, record())
	require.NoError(t, record())
	require.Equal(t, 1, publishCount)
	require.Equal(t, "2", first.Params["n"])
	in.State = "ENABLED"
	require.ErrorIs(t, record(), ErrConflict)
	in.State = "UNAVAILABLE"
	in.Source.SourceKey = "another-result"
	require.ErrorIs(t, record(), ErrConflict)
	in.Source.PriorID = first.ID
	require.ErrorIs(t, record(), ErrConflict)
	in.Source.SourceKey = "recovered"
	in.State = "ENABLED"
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("outbox failed") })
	require.Error(t, record())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, found, e := m.lifecycleCurrentOn(ctx, tx, 1, first.ID)
		require.True(t, found)
		require.Equal(t, "UNAVAILABLE", v.State)
		return e
	}))
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
	require.Error(t, record())
	q := query.BindLifecycleReceipts(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, n)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	in = ProviderObservation{Source: s, State: "UNAVAILABLE", BlockedWorkIDs: []guid.ID{501, 502}}
	require.ErrorIs(t, record(), ErrForbidden)
}

func TestLifecycleCalibrationThresholdProbeAndCurrentState(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	in := CalibrationObservation{Source: s, Verifier: "visual", ProviderRef: "model:v1", Method: "AGREEMENT", State: "CALIBRATED", AgreementPPM: 900000, TargetPPM: 800000, Samples: 20, QualityOwnerID: 12}
	var calibrated, drift LifecycleView
	in.Samples = 19
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordCalibrationOn(ctx, tx, in); return e }), ErrInvalid)
	in.Samples = 20
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; calibrated, e = m.recordCalibrationOn(ctx, tx, in); return e }))
	in.Source.SourceKey = "drift-result"
	in.Source.PriorID = calibrated.ID
	in.State = "DRIFTED"
	in.AgreementPPM = 500000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; drift, e = m.recordCalibrationOn(ctx, tx, in); return e }))
	require.Equal(t, "verifier.calibration_drifted", drift.NoticeType)
	require.Equal(t, "visual", drift.Params["verifier"])
	require.Equal(t, "0.5000", drift.Params["agreement"])
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.lifecycleCurrentOn(ctx, tx, 1, calibrated.ID)
		require.False(t, found)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.lifecycleCurrentOn(ctx, tx, 2, drift.ID)
		require.False(t, found)
		return e
	}))
	in.Source.SourceKey = "recalibrated"
	in.Source.PriorID = drift.ID
	in.State = "CALIBRATED"
	in.AgreementPPM = 850000
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordCalibrationOn(ctx, tx, in); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.lifecycleCurrentOn(ctx, tx, 1, drift.ID)
		require.False(t, found)
		return e
	}))
	in.Source.SourceKey = "probe"
	in.Source.Reference = "machine:50:browser"
	in.Source.PriorID = 0
	in.Method = "PROBE"
	in.MachineID = 50
	in.Samples = 3
	in.FailedProbes = 1
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordCalibrationOn(ctx, tx, in); return e }), ErrInvalid)
	in.FailedProbes = 0
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordCalibrationOn(ctx, tx, in); return e }))
}

func TestLifecycleEvalRequiresRunAndFreezesInitiator(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	in := EvalObservation{Source: s, State: "COMPLETED", Total: 20, Passed: 12, Failed: 8, AgreementPPM: 600000, TargetPPM: 800000}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordEvalOn(ctx, tx, in); return e }), ErrConflict)
	in.State = "RUNNING"
	in.Total = 0
	in.Passed = 0
	in.Failed = 0
	var running LifecycleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; running, e = m.recordEvalOn(ctx, tx, in); return e }))
	in.Source.SourceKey = "completion:1"
	in.Source.PriorID = running.ID
	in.State = "COMPLETED"
	in.Total = 20
	in.Passed = 12
	in.Failed = 7
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordEvalOn(ctx, tx, in); return e }), ErrInvalid)
	in.Failed = 8
	in.TargetPPM = 500000
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordEvalOn(ctx, tx, in); return e }), ErrConflict, "completion cannot lower its frozen evaluation target")
	in.TargetPPM = 800000
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.recordEvalOn(other, tx, in)
		require.Equal(t, guid.ID(11), v.InitiatorID)
		require.Equal(t, "eval.below_target", v.NoticeType)
		require.Equal(t, "8", v.Params["failed"])
		return e
	}))
}

func TestLifecycleRubricBoundaryAndToolAbsenceAreNotPasses(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, s.TemplateVersionID, 99, 11); return e }))
	s.ProjectID = 99
	s.Reference = "WORK_ITEM:501"
	rubric := RubricObservation{Source: s, InternalScoreID: 801, ExternalScoreID: 802, Dimensions: []RubricDimensionObservation{{Key: "narrative", InternalMilli: 4000, ExternalMilli: 3000}, {Key: "pacing", InternalMilli: 4000, ExternalMilli: 3001}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.recordRubricOn(ctx, tx, rubric)
		require.Equal(t, "rubric.divergence_detected", v.NoticeType)
		require.Equal(t, "narrative", v.Params["dimensions"])
		return e
	}))
	rubric.InternalScoreID = rubric.ExternalScoreID
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordRubricOn(ctx, tx, rubric); return e }), ErrInvalid)
	s.Reference = "ffmpeg"
	s.SourceKey = "fleet-capability-snapshot:1"
	var missing LifecycleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		missing, e = m.recordToolOn(ctx, tx, ToolObservation{Source: s, EligibleMachines: 0})
		require.Equal(t, "UNAVAILABLE", missing.State)
		require.Equal(t, "verifier.tool_unavailable", missing.NoticeType)
		return e
	}))
	s.SourceKey = "fleet-capability-snapshot:2"
	s.PriorID = missing.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s, EligibleMachines: 0})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.lifecycleCurrentOn(ctx, tx, 1, missing.ID)
		require.True(t, found, "same daily condition must not hide its deduplicated notification")
		return e
	}))
}

func TestLifecycleCapabilityProvisionAndValidation(t *testing.T) {
	m, _, _, s := lifecycleHarness(t)
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	recorder, e := module.Resolve[LifecycleRecorder](services)
	require.NoError(t, e)
	require.NotNil(t, recorder.ToolOn)
	facts, e := module.Resolve[LifecycleFacts](services)
	require.NoError(t, e)
	require.NotNil(t, facts.CurrentOn)
	for _, change := range []func(*ObservationSource){func(s *ObservationSource) { s.TemplateVersionID = 0 }, func(s *ObservationSource) { s.SourceKey = " " }, func(s *ObservationSource) { s.Reference = "" }, func(s *ObservationSource) { s.EvidenceIDs = []guid.ID{0} }, func(s *ObservationSource) { s.PriorID = -1 }} {
		bad := s
		change(&bad)
		require.False(t, validSource(bad))
	}
	require.True(t, validSource(s))
	require.False(t, ppmValid(1000001, 800000))
}

func TestLifecycleProjectPinsAffectedProjectsAndCurrentFailures(t *testing.T) {
	m, db, ctx, source := lifecycleHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, source.TemplateVersionID, 99, 11); return e }))
	source.ProjectID = 99
	in := ProviderObservation{Source: source, State: "UNAVAILABLE", AffectedProjectIDs: []guid.ID{99}, BlockedWorkIDs: []guid.ID{700}}
	var first LifecycleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; first, e = m.recordProviderOn(ctx, tx, in); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, found, e := m.lifecycleCurrentOn(ctx, tx, 1, first.ID)
		require.True(t, found)
		require.Equal(t, []guid.ID{99}, out.AffectedProjectIDs)
		return e
	}))
	scope := m.scope
	m.scope = delivery.ProjectScope{}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordProviderOn(ctx, tx, in); return e }), ErrUnavailable)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordProviderOn(ctx, tx, in); return e }), ErrForbidden)
	m.scope = scope
	for _, mutation := range []func(*ProviderObservation){func(in *ProviderObservation) { in.Source.SourceKey = "" }, func(in *ProviderObservation) { in.State = "OTHER" }, func(in *ProviderObservation) { in.Source.EvidenceIDs = nil }, func(in *ProviderObservation) { in.BlockedWorkIDs = []guid.ID{700, 700} }, func(in *ProviderObservation) { in.AffectedProjectIDs = []guid.ID{98} }} {
		bad := in
		mutation(&bad)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordProviderOn(ctx, tx, bad); return e }), ErrInvalid)
	}
	require.NoError(t, db.Table(m.names.Bindings.String()).Where("project_id=?", 99).Delete(nil).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordProviderOn(ctx, tx, in); return e }), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.lifecycleCurrentOn(ctx, tx, 1, first.ID)
		require.False(t, found)
		return e
	}))
	_, _, e := m.lifecycleCurrentOn(ctx, nil, 1, first.ID)
	require.ErrorIs(t, e, ErrInvalid)
}
func TestLifecycleStorageFailuresNeverCommitObservation(t *testing.T) {
	for _, stage := range []string{"lookup", "create"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, s := lifecycleHarness(t)
			injected := errors.New("receipt unavailable")
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.LifecycleReceipts.String() {
					tx.AddError(injected)
				}
			}
			if stage == "lookup" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("lifecycle-failure", callback))
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("lifecycle-failure", callback))
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s, EligibleMachines: 0})
				return e
			}), injected)
			if stage == "lookup" {
				require.NoError(t, db.Callback().Query().Remove("lifecycle-failure"))
			} else {
				require.NoError(t, db.Callback().Create().Remove("lifecycle-failure"))
			}
			var n int64
			require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestLifecycleRejectsUnsupportedQualityObservations(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	for _, in := range []CalibrationObservation{
		{Source: s}, {Source: s, Verifier: "v", ProviderRef: "p", State: "CALIBRATING", Method: "AGREEMENT", MachineID: 1},
		{Source: ObservationSource{TemplateVersionID: s.TemplateVersionID, SourceKey: s.SourceKey, Reference: s.Reference}, Verifier: "v", ProviderRef: "p", State: "CALIBRATED", Method: "AGREEMENT", Samples: 20, AgreementPPM: 1000000, TargetPPM: 900000},
		{Source: s, Verifier: "v", ProviderRef: "p", State: "DRIFTED", Method: "PROBE", MachineID: 101, Samples: 1},
		{Source: s, Verifier: "v", ProviderRef: "p", State: "DRIFTED", Method: "AGREEMENT", Samples: 20, AgreementPPM: 1000000, TargetPPM: 900000},
	} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordCalibrationOn(ctx, tx, in); return e }), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordCalibrationOn(ctx, tx, CalibrationObservation{Source: s, Verifier: "v", ProviderRef: "p", State: "DRIFTED", Method: "AGREEMENT", Samples: 20, AgreementPPM: 500000, TargetPPM: 900000})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s, EligibleMachines: -1})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordEvalOn(ctx, tx, EvalObservation{Source: ObservationSource{TemplateVersionID: s.TemplateVersionID, SourceKey: s.SourceKey, Reference: s.Reference}, State: "FAILED"})
		return e
	}), ErrInvalid)
	scope := m.scope
	s.ProjectID = 99
	m.scope = delivery.ProjectScope{}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s}); return e }), ErrUnavailable)
	m.scope = scope
	m.ids = nil
	s.ProjectID = 0
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s}); return e }), ErrUnavailable)
}

func TestLifecycleCurrentProviderFiltersUnboundAffectedProjects(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, s.TemplateVersionID, 99, 11); return e }))
	var row LifecycleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.recordProviderOn(ctx, tx, ProviderObservation{Source: s, State: "UNAVAILABLE", AffectedProjectIDs: []guid.ID{99, 100}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, found, e := m.lifecycleCurrentOn(ctx, tx, 1, row.ID)
		require.True(t, found)
		require.Equal(t, []guid.ID{99}, out.AffectedProjectIDs)
		return e
	}))
	injected := errors.New("current source unavailable")
	for _, table := range []string{m.names.LifecycleReceipts.String(), m.names.Bindings.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("current-lifecycle-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.lifecycleCurrentOn(ctx, tx, 1, row.ID); return e }), injected)
		require.NoError(t, db.Callback().Query().Remove("current-lifecycle-failure"))
	}
	require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Where("id=?", row.ID).Update("payload", "invalid").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.lifecycleCurrentOn(ctx, tx, 1, row.ID); return e }))
}

func TestLifecycleIDSourceFailureDoesNotWriteReceipt(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	injected := errors.New("identifier unavailable")
	m.ids = failingTemplateIDs{injected}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s}); return e }), injected)
	m.ids = failingTemplateIDs{}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordToolOn(ctx, tx, ToolObservation{Source: s}); return e }), ErrUnavailable)
	var n int64
	require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Count(&n).Error)
	require.Zero(t, n)
}
