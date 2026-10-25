package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestArtifactCriteriaInjectionPreservesTransactionScopeAndFailure(t *testing.T) {
	m := &Module{}
	require.Error(t, m.injectArtifactCriteria(nil))
	require.ErrorIs(t, m.injectArtifactCriteria(module.NewServices()), module.ErrServicesProviding)
	f := executionHarness(t)
	s := module.NewServices()
	failure := errors.New("criterion lookup unavailable")
	l := WorkLink{TenantID: 7, ProjectID: 9, WorkItemID: 3, AssignmentID: 6, ExecutionID: 88, ActorID: 11, InitiatorID: 11, ParentExecutionID: 55, SourceDecisionID: 66}
	require.NoError(t, module.Provide[delivery.ExecutionArtifactCriteria](s, func(ctx context.Context, tx *gorm.DB, got delivery.ExecutionWorkLink, ids []guid.ID) error {
		require.Same(t, f.human, ctx)
		require.NotNil(t, tx)
		require.Equal(t, delivery.ExecutionWorkLink{TenantID: 7, ProjectID: 9, WorkItemID: 3, AssignmentID: 6, ExecutionID: 88, ActorID: 11, InitiatorID: 11, ParentExecutionID: 55, SourceDecisionID: 66}, got)
		require.Equal(t, []guid.ID{77}, ids)
		return failure
	}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectArtifactCriteria(s))
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return m.work.ArtifactCriteriaOn(f.human, tx, l, []guid.ID{77}) }), failure)
	require.ErrorIs(t, m.validatePlannedArtifactsOn(f.human, nil, l, make([]PlannedArtifact, 51)), ErrInvalidArgument)
}

func TestRejectedArtifactPlanRollsBackCreationAndCannotUseReadOnlyMode(t *testing.T) {
	f := executionHarness(t)
	in := validCreate()
	in.Plan.Artifacts = []PlannedArtifact{{CriterionID: 77, Path: "hello.txt", Note: "Required output"}}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, in); return e }), ErrInvalidArgument)
	in.Plan.AuthorizationMode = "WORKSPACE_WRITE"
	failure := errors.New("criterion changed since preparation")
	f.m.work.ArtifactCriteriaOn = func(ctx context.Context, tx *gorm.DB, _ WorkLink, _ []guid.ID) error {
		require.NoError(t, tx.Exec("INSERT INTO effects(kind) VALUES ('criterion.validation')").Error)
		return failure
	}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, in); return e }), failure)
	for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count, "failed plan must leave no transactional effects: %s", table)
	}
	f.m.work.ArtifactCriteriaOn = func(context.Context, *gorm.DB, WorkLink, []guid.ID) error { return nil }
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 0, failure }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, in); return e }), failure)
}

func TestFrozenAgentBindingFailsClosedOnUnavailableIdentityLookup(t *testing.T) {
	f := executionHarness(t)
	p := validCreate().Plan
	p.ExecutorPrincipalID, p.ModelFamily = 21, "family"
	failure := errors.New("fleet identity unavailable")
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 0, failure }
	require.ErrorIs(t, f.m.validateAgentBindingOn(f.human, nil, WorkLink{}, 1, p), failure)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = nil
	require.ErrorIs(t, f.m.validateAgentBindingOn(f.human, nil, WorkLink{}, 1, p), ErrCapabilityUnavailable)
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, failure
	}
	require.ErrorIs(t, f.m.validateAgentBindingOn(f.human, nil, WorkLink{}, 1, p), failure)
}

func TestArtifactPlanFrozenAndValidatedBeforeExecution(t *testing.T) {
	f := executionHarness(t)
	input := validCreate()
	input.Plan.AuthorizationMode = "WORKSPACE_WRITE"
	input.Plan.Artifacts = []PlannedArtifact{{CriterionID: 77, Path: "hello.txt", Note: "Actual output"}}
	f.m.work.ArtifactCriteriaOn = func(_ context.Context, _ *gorm.DB, l WorkLink, ids []guid.ID) error {
		require.Equal(t, guid.ID(3), l.WorkItemID)
		require.Equal(t, []guid.ID{77}, ids)
		return nil
	}
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, input); return e }))
	require.Equal(t, input.Plan.Artifacts, v.Plan.Artifacts)
	input.Plan.Artifacts[0].Path = "changed.txt"
	reloaded := f.pull(t, v.ID)
	require.Equal(t, "hello.txt", reloaded.Plan.Artifacts[0].Path)
	require.Equal(t, v.PlanHash, reloaded.PlanHash)
	for _, p := range []string{"../escape", "/absolute", "C:/outside", "dir\\file", ".git/config", "."} {
		input.Plan.Artifacts[0].Path = p
		require.ErrorIs(t, f.m.validatePlannedArtifactsOn(f.human, nil, WorkLink{}, input.Plan.Artifacts), ErrInvalidArgument)
	}
	f.m.work.ArtifactCriteriaOn = nil
	input.Plan.Artifacts[0].Path = "hello.txt"
	require.ErrorIs(t, f.m.validatePlannedArtifactsOn(f.human, nil, WorkLink{}, input.Plan.Artifacts), ErrCapabilityUnavailable)
}

func TestPlannedArtifactsCannotBeSatisfiedByUnrelatedCoverage(t *testing.T) {
	a := PlannedArtifact{CriterionID: 77, Path: "hello.txt", Note: "Actual bytes"}
	p := ExecutionPlan{Artifacts: []PlannedArtifact{a}}
	u := ArtifactUpload{Name: "hello.txt", CriterionID: &a.CriterionID, Coverage: "COVERED", Note: plannedArtifactNote(a)}
	r := ReceiptInput{EvidenceTitle: plannedArtifactTitle(0), Upload: &u}
	require.NoError(t, validateArtifactReceipt(p, r))
	u.Note = "unfrozen assertion"
	require.ErrorIs(t, validateArtifactReceipt(p, r), ErrInvalidArgument)
	u.Note = plannedArtifactNote(a)
	other := guid.ID(88)
	u.CriterionID = &other
	require.ErrorIs(t, validateArtifactReceipt(p, r), ErrInvalidArgument)
	require.ErrorIs(t, validateArtifactCompletion(p, nil), ErrConflict)
	f := EvidenceFact{Kind: "FILE", Title: plannedArtifactTitle(0), Artifact: &ArtifactFact{CriterionID: &a.CriterionID, Coverage: "COVERED", Note: plannedArtifactNote(a)}}
	require.NoError(t, validateArtifactCompletion(p, []EvidenceFact{f}))
	f.Artifact.Coverage = "PARTIAL"
	require.ErrorIs(t, validateArtifactCompletion(p, []EvidenceFact{f}), ErrConflict)
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.NoError(t, (&Module{}).injectArtifactCriteria(s))
}

func TestPlannedArtifactReceiptsEnforceFrozenAssociationBeforeClaim(t *testing.T) {
	f := executionHarness(t)
	in := validCreate()
	in.Plan.AuthorizationMode = "WORKSPACE_WRITE"
	a := PlannedArtifact{CriterionID: 77, Path: "hello.txt", Note: "Actual output"}
	in.Plan.Artifacts = []PlannedArtifact{a}
	f.m.work.ArtifactCriteriaOn = func(context.Context, *gorm.DB, WorkLink, []guid.ID) error { return nil }
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, in); return e }))
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	head := strings.Repeat("a", 40)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Head", Content: head})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: head})
	require.ErrorIs(t, err, ErrConflict)
	u := ArtifactUpload{Name: "hello.txt", Data: []byte("hello\n"), CriterionID: &a.CriterionID, Coverage: "COVERED", Note: "not frozen"}
	r := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: plannedArtifactTitle(0), Upload: &u}
	_, err = f.receipt(v.ID, r)
	require.ErrorIs(t, err, ErrInvalidArgument)
	u.Note = plannedArtifactNote(a)
	_, err = f.receipt(v.ID, r)
	require.NoError(t, err)
	var received WorkResult
	f.m.work.FinishedOn = func(_ context.Context, _ *gorm.DB, _ WorkLink, r WorkResult) error { received = r; return nil }
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: head})
	require.NoError(t, err)
	require.Len(t, received.Evidence, 2)
	require.Equal(t, &a.CriterionID, received.Evidence[1].Artifact.CriterionID)
	require.Equal(t, hash([]byte("hello\n")), received.Evidence[1].ContentHash)
}

func TestInjectArtifactCriteriaRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectArtifactCriteria(module.NewServices()), module.ErrServicesProviding)
}
