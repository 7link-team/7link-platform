package fleet

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsembleHTTPReceiptsReplayOnlyUnderCurrentOwnerAuthority(t *testing.T) {
	m, db, ctx, _ := ensembleRegressionTestEnsembleHarness(t)
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	m.idempotency = store
	in := ensembleCreateInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: 99}, IdempotencyKey: "create-one", Body: EnsembleCreate{WorkItemID: 77, ExecutionIDs: []guid.ID{11, 12}, Reason: "Compare actual completed alternatives"}}
	first, err := m.createEnsembleHTTP(ctx, &in)
	require.NoError(t, err)
	id := first.Body.Data.ID
	replay, err := m.createEnsembleHTTP(ctx, &in)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	in.Body.Reason = "Different request cannot reuse receipt"
	_, err = m.createEnsembleHTTP(ctx, &in)
	require.Error(t, err)
	list, err := m.listEnsemblesHTTP(ctx, &ensembleListInput{EnsembleProjectInput: in.EnsembleProjectInput, WorkItemID: 77})
	require.NoError(t, err)
	require.Len(t, list.Body.Data, 1)
	require.Equal(t, id, list.Body.Data[0].ID)
	ref := EnsembleRefInput{EnsembleProjectInput: in.EnsembleProjectInput, PlanID: id}
	detail, err := m.getEnsembleHTTP(ctx, &ref)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, detail.Body.Data)
	change := ensembleChangeInput{EnsembleRefInput: ref, IdempotencyKey: "compare-one", Body: EnsembleDecision{Action: "COMPARE", Version: 1, Reason: "Compare actual completed alternatives"}}
	compared, err := m.changeEnsembleHTTP(ctx, &change)
	require.NoError(t, err)
	require.Equal(t, "COMPARING", compared.Body.Data.State)
	comparedAgain, err := m.changeEnsembleHTTP(ctx, &change)
	require.NoError(t, err)
	require.Equal(t, compared.Body.Data, comparedAgain.Body.Data)
	ref.PlanID = 999
	_, err = m.getEnsembleHTTP(ctx, &ref)
	require.Error(t, err)
	change.PlanID = 999
	_, err = m.changeEnsembleHTTP(ctx, &change)
	require.Error(t, err)
	m.ensemble.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrMachineForbidden }
	change.PlanID = id
	_, err = m.changeEnsembleHTTP(ctx, &change)
	require.Error(t, err, "receipt cannot replay after current authority is removed")
	ref.PlanID = id
	_, err = m.getEnsembleHTTP(ctx, &ref)
	require.Error(t, err)
	_, err = m.listEnsemblesHTTP(ctx, &ensembleListInput{EnsembleProjectInput: in.EnsembleProjectInput, WorkItemID: 77})
	require.Error(t, err)
}

func TestEnsembleHTTPUnavailableWriterFailsEveryOperation(t *testing.T) {
	m := &Module{}
	ctx := t.Context()
	_, err := m.createEnsembleHTTP(ctx, &ensembleCreateInput{})
	require.Error(t, err)
	_, err = m.changeEnsembleHTTP(ctx, &ensembleChangeInput{})
	require.Error(t, err)
	_, err = m.getEnsembleHTTP(ctx, &EnsembleRefInput{})
	require.Error(t, err)
	_, err = m.listEnsemblesHTTP(ctx, &ensembleListInput{})
	require.Error(t, err)
}

func TestEnsembleWireSelectorsRejectMalformedAndOverflowIDs(t *testing.T) {
	m := &Module{}
	ctx := t.Context()
	for _, bad := range []string{"", "0", "-1", "9223372036854775808"} {
		_, err := m.createEnsembleWireHTTP(ctx, &ensembleWireCreate{EnsembleWireProject: EnsembleWireProject{EntityID: bad}})
		require.Error(t, err)
		_, err = m.changeEnsembleWireHTTP(ctx, &ensembleWireChange{EnsembleWireRef: EnsembleWireRef{EnsembleWireProject: EnsembleWireProject{EntityID: bad}, PlanID: "1"}})
		require.Error(t, err)
		_, err = m.changeEnsembleWireHTTP(ctx, &ensembleWireChange{EnsembleWireRef: EnsembleWireRef{EnsembleWireProject: EnsembleWireProject{EntityID: "99"}, PlanID: bad}})
		require.Error(t, err)
		_, err = m.getEnsembleWireHTTP(ctx, &EnsembleWireRef{EnsembleWireProject: EnsembleWireProject{EntityID: bad}, PlanID: "1"})
		require.Error(t, err)
		_, err = m.getEnsembleWireHTTP(ctx, &EnsembleWireRef{EnsembleWireProject: EnsembleWireProject{EntityID: "99"}, PlanID: bad})
		require.Error(t, err)
		_, err = m.listEnsemblesWireHTTP(ctx, &ensembleWireList{EnsembleWireProject: EnsembleWireProject{EntityID: bad}, WorkItemID: "77"})
		require.Error(t, err)
		_, err = m.listEnsemblesWireHTTP(ctx, &ensembleWireList{EnsembleWireProject: EnsembleWireProject{EntityID: "99"}, WorkItemID: bad})
		require.Error(t, err)
	}
}
