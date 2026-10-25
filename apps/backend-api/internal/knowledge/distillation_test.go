package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func distillationHarness(t *testing.T) (*Module, *gorm.DB, *int, *DistillationOutput) {
	t.Helper()
	m, db, _ := knowledgeHarness(t)
	launched := 0
	output := &DistillationOutput{ProjectID: 3, WorkItemID: 44, AgentPrincipalID: 7, State: "SUCCEEDED", Content: skillDocument("distilled-guidance", "Use independently verified source evidence.")}
	m.distillation.source = DistillationSource{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, work guid.ID) (AcceptedSkillSource, error) {
		require.EqualValues(t, 1, tenant)
		if work != 44 {
			return AcceptedSkillSource{}, ErrDistillationNotAccepted
		}
		return AcceptedSkillSource{ProjectID: 3, WorkItemID: work, ResultID: 45, ExecutionID: 46, Content: "Canonical accepted DELIVERY summary and passed verification", HeadCommit: strings.Repeat("a", 40)}, nil
	}}
	m.distillation.runtime = DistillationRuntime{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{6}, nil }, LaunchOn: func(_ context.Context, _ *gorm.DB, tenant guid.ID, in DistillationLaunch) (guid.ID, error) {
		launched++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 44, in.WorkItemID)
		require.EqualValues(t, 7, in.AgentPrincipalID)
		require.EqualValues(t, 46, in.SourceExecutionID)
		require.Contains(t, in.Objective, distillationInstruction)
		require.Contains(t, in.Objective, "Canonical accepted DELIVERY")
		return 47, nil
	}, ReadOn: func(_ context.Context, _ *gorm.DB, tenant, execution guid.ID) (DistillationOutput, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 47, execution)
		return *output, nil
	}}
	return m, db, &launched, output
}

func TestDistillationAcceptedGateAndDailyReplayCreateNoPrematureSkillVersion(t *testing.T) {
	m, db, launched, _ := distillationHarness(t)
	var request, replay model.Distillation
	start := func(actor, work guid.ID, out *model.Distillation) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			*out, e = m.startDistillationOn(knowledgeContext(t, actor), tx, 1, work, DistillationInput{AgentID: 7})
			return e
		})
	}
	require.ErrorIs(t, start(2, 99, &request), ErrDistillationNotAccepted)
	require.ErrorIs(t, start(7, 44, &request), ErrForbidden)
	require.Zero(t, *launched)
	require.NoError(t, start(2, 44, &request))
	require.NoError(t, start(2, 44, &replay))
	require.Equal(t, request.ID, replay.ID)
	require.Equal(t, 1, *launched)
	require.Equal(t, "RUNNING", request.State)
	require.Nil(t, request.SkillVersionID)
	require.NotEmpty(t, request.InputHash)
	var versions int64
	require.NoError(t, query.BindSkillVersions(db, m.names).Count(&versions).Error)
	require.Zero(t, versions)
	// Current authority is checked before returning the daily replay.
	m.distillation.source.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (AcceptedSkillSource, error) {
		return AcceptedSkillSource{}, ErrForbidden
	}
	require.ErrorIs(t, start(2, 44, &replay), ErrForbidden)
	require.Equal(t, 1, *launched)
}

func TestDistillationVerifiedOutputCreatesProvenanceDraftAndRequiresHumanReview(t *testing.T) {
	m, db, _, _ := distillationHarness(t)
	var request model.Distillation
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		request, e = m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.completeDistillationOn(knowledgeContext(t, 7), tx, 1, *request.ExecutionID)
	}))
	var version model.SkillVersion
	require.NoError(t, query.BindSkillVersions(db, m.names).Take(&version).Error)
	require.Equal(t, "DRAFT", version.State)
	require.Equal(t, "DISTILLED", version.Origin)
	require.Equal(t, guid.ID(44), *version.SourceWorkItemID)
	require.Equal(t, guid.ID(47), *version.SourceExecutionID)
	require.Equal(t, guid.ID(2), version.CreatedBy)
	require.Nil(t, version.ReviewedBy)
	var skill model.Skill
	require.NoError(t, query.BindSkills(db, m.names).Take(&skill).Error)
	require.Nil(t, skill.CurrentVersionID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reviewSkillVersionOn(knowledgeContext(t, 7), tx, 1, version.ID, SkillReviewInput{Decision: "APPROVED"})
		return e
	}), ErrForbidden)
	approveTestSkill(t, m, db, version.ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.completeDistillationOn(knowledgeContext(t, 7), tx, 1, *request.ExecutionID)
	}))
	var count int64
	require.NoError(t, query.BindSkillVersions(db, m.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDistillationFailedCancelledInvalidAndMismatchedOutputCannotCreateDraft(t *testing.T) {
	for _, state := range []string{"FAILED", "CANCELLED", "INVALID_DOCUMENT", "FOREIGN_AGENT", "RUNNING"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		t.Run(state, func(t *testing.T) {
			m, db, _, output := distillationHarness(t)
			var request model.Distillation
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				request, e = m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
				return e
			}))
			switch state {
			case "INVALID_DOCUMENT":
				output.Content = "No valid frontmatter"
			case "FOREIGN_AGENT":
				output.AgentPrincipalID = 8
			default:
				output.State = state
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.completeDistillationOn(t.Context(), tx, 1, *request.ExecutionID) })
			if state == "FOREIGN_AGENT" || state == "RUNNING" {
				require.ErrorIs(t, err, ErrConflict)
			} else {
				require.NoError(t, err)
			}
			var count int64
			require.NoError(t, query.BindSkillVersions(db, m.names).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestDistillationLaunchFailureRollsBackRequestAuditAndOutbox(t *testing.T) {
	m, db, _, _ := distillationHarness(t)
	m.distillation.runtime.LaunchOn = func(context.Context, *gorm.DB, guid.ID, DistillationLaunch) (guid.ID, error) {
		return 0, ErrUnavailable
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}), ErrUnavailable)
	var count int64
	require.NoError(t, query.BindDistillations(db, m.names).Count(&count).Error)
	require.Zero(t, count)
}

func TestDistillationDailyWindowAllowsNewRealRequestAfterExpiry(t *testing.T) {
	m, db, _, _ := distillationHarness(t)
	clock := m.now()
	m.now = func() time.Time { return clock }
	var first, second model.Distillation
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}))
	clock = clock.Add(25 * time.Hour)
	m.distillation.runtime.LaunchOn = func(context.Context, *gorm.DB, guid.ID, DistillationLaunch) (guid.ID, error) { return 48, nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		second, e = m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}))
	require.NotEqual(t, first.ID, second.ID)
	require.NotEqual(t, *first.ExecutionID, *second.ExecutionID)
}

func TestDistillationStorageFailuresCannotPartiallyPublishDraft(t *testing.T) {
	m, db, _, _ := distillationHarness(t)
	failure := errors.New("transactional storage unavailable")
	rollback := errors.New("rollback diagnostic transaction")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("transaction-storage-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("transaction-storage-failure"))
	}
	exercise := func(run func(*gorm.DB) error) {
		calls, failAt = 0, 0
		install()
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if e := run(tx); e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, apply(), rollback)
		total := calls
		require.Greater(t, total, 2)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "storage stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error {
		_, e := m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}))
	exercise(func(tx *gorm.DB) error { return m.completeDistillationOn(knowledgeContext(t, 7), tx, 1, 47) })
	var row model.Distillation
	require.NoError(t, query.BindDistillations(db, m.names).First(&row).Error)
	require.Equal(t, "RUNNING", row.State)
	require.Nil(t, row.SkillVersionID)
	var n int64
	require.NoError(t, query.BindSkills(db, m.names).Count(&n).Error)
	require.Zero(t, n)
}

func TestDistillationConnectorRequiresCompleteSingleSourceAndRuntime(t *testing.T) {
	m, db, _, _ := distillationHarness(t)
	source, runtime := m.distillation.source, m.distillation.runtime
	m.distillation.source = DistillationSource{}
	m.distillation.runtime = DistillationRuntime{}
	s := module.NewServices()
	require.NoError(t, m.provideDistillation(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[DistillationConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.BindSource(DistillationSource{}), ErrUnavailable)
	require.NoError(t, connector.BindSource(source))
	require.ErrorIs(t, connector.BindSource(source), ErrConflict)
	require.ErrorIs(t, connector.BindRuntime(DistillationRuntime{}), ErrUnavailable)
	require.NoError(t, connector.BindRuntime(runtime))
	require.ErrorIs(t, connector.BindRuntime(runtime), ErrConflict)
	require.ErrorIs(t, connector.CompleteOn(t.Context(), db, 1, 47), ErrInvalid)
	require.ErrorIs(t, connector.CompleteOn(t.Context(), nil, 1, 47), ErrInvalid)
	failure := errors.New("distillation principal discovery unavailable")
	m.distillation.runtime.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
		return e
	}), failure)
}

func TestDistillationRejectsUnavailableOrInvalidCurrentSourceBeforeLaunching(t *testing.T) {
	m, db, launched, _ := distillationHarness(t)
	_, err := m.startDistillationOn(knowledgeContext(t, 2), nil, 1, 44, DistillationInput{AgentID: 7})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.startDistillationOn(knowledgeContext(t, 2), db, 1, 44, DistillationInput{AgentID: 7})
	require.ErrorIs(t, err, ErrInvalid)
	runtime := m.distillation.runtime
	m.distillation.runtime = DistillationRuntime{}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.startDistillationOn(knowledgeContext(t, 2), tx, 1, 44, DistillationInput{AgentID: 7})
			return err
		})
	}
	require.ErrorIs(t, run(), ErrUnavailable)
	m.distillation.runtime = runtime
	m.distillation.source.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (AcceptedSkillSource, error) {
		return AcceptedSkillSource{}, nil
	}
	require.ErrorIs(t, run(), ErrInvalid)
	require.Zero(t, *launched)
}
