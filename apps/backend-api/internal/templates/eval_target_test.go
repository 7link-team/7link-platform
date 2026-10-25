package templates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type evalContentFixture struct {
	pins    map[guid.ID]files.PinnedResource
	deny    bool
	corrupt bool
}

func (f *evalContentFixture) InspectOn(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	data := []byte(`{"valid":true}`)
	if id == 43 {
		data = []byte(`{"valid":false}`)
	}
	return files.PinnedCandidate{PinnedResource: files.PinnedResource{FileID: id, VersionID: id + 1, SHA256: evalDigest(data)}, NodeVersion: 1}, nil
}
func (f *evalContentFixture) PinOn(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
	candidate, _ := f.InspectOn(ctx, tx, in.FileID)
	pin := candidate.PinnedResource
	pin.BindingID = in.ID
	f.pins[in.ID] = pin
	return pin, nil
}
func (f *evalContentFixture) OpenOn(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
	if f.deny {
		return files.PinnedResourceRead{}, files.ErrPinnedForbidden
	}
	pin, found := f.pins[id]
	if !found {
		return files.PinnedResourceRead{}, files.ErrPinnedInvalid
	}
	data := `{"valid":true}`
	if pin.FileID == 43 {
		data = `{"valid":false}`
	}
	if f.corrupt {
		data = "corrupt"
	}
	return files.PinnedResourceRead{PinnedResource: pin, Reader: io.NopCloser(strings.NewReader(data))}, nil
}
func TestEvalInstalledAcceptorModelUsesPinnedBytesAndCurrentSourceConfiguration(t *testing.T) {
	fault := ""
	m, db, ctx, calibration := installedVerifierFixture(t, &fault, "MODEL_SCORED")
	content := &evalContentFixture{pins: map[guid.ID]files.PinnedResource{}}
	m.evalFiles = content
	fingerprint := strings.Repeat("b", 64)
	m.evalAgents.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, role, model string) (fleet.VerifierAgentConfiguration, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(77), id)
		require.Equal(t, "ACCEPTOR", role)
		require.Equal(t, "configured-model", model)
		return fleet.VerifierAgentConfiguration{AgentID: id, Role: role, Model: model, Family: "configured-family", SHA256: fingerprint, Instructions: "Private evaluation instructions"}, nil
	}
	for _, id := range []guid.ID{41, 43} {
		pin, _ := content.InspectOn(ctx, db, id)
		expected := "PASS"
		if id == 43 {
			expected = "FAIL"
		}
		addEvalFixture(t, m, db, ctx, EvalCaseDraft{TemplateVersionID: calibration.TemplateVersionID, TemplateKey: "installed-plugin", ProjectID: 99, Title: "Known probe", Kind: "TOOL_PROBE", InputRef: []EvalFileRef{{FileID: id, VersionID: id + 1, SHA256: pin.SHA256}}, Expected: map[string]string{"visual": expected}})
	}
	start := func() EvalRunView {
		var out EvalRunView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.startEvalOn(ctx, tx, EvalStart{TemplateKey: "installed-plugin", TemplateVersionID: calibration.TemplateVersionID, Target: EvalTarget{VerifierKey: "visual", ProviderRef: calibration.ProviderRef, AgentID: 77, Role: "ACCEPTOR", Model: "configured-model"}})
			return e
		}))
		return out
	}
	read := func(id guid.ID) EvalRunView {
		var out EvalRunView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.evalRunOn(ctx, tx, id); return e }))
		return out
	}
	run := start()
	require.Equal(t, fingerprint, run.Snapshot.Target.ConfigurationSHA256)
	raw, _ := json.Marshal(run)
	require.NotContains(t, string(raw), "Private evaluation instructions")
	require.NoError(t, m.evaluateEvalRun(ctx, 1, run.ID))
	done := read(run.ID)
	require.Equal(t, "COMPLETED", done.State)
	require.Equal(t, 2, done.Passed)
	require.Zero(t, done.Inconclusive)
	require.Equal(t, 1000000, *done.AgreementPPM)
	fault = "invert"
	next := start()
	require.NoError(t, m.evaluateEvalRun(ctx, 1, next.ID))
	wrong := read(next.ID)
	require.Equal(t, 1, wrong.FalsePositives)
	require.Equal(t, 1, wrong.FalseNegatives)
	fault = ""
	stale := start()
	fingerprint = strings.Repeat("c", 64)
	require.NoError(t, m.evaluateEvalRun(ctx, 1, stale.ID))
	require.Equal(t, "UNAVAILABLE", read(stale.ID).State)
	for _, failure := range []string{"acl", "bytes"} {
		t.Run(failure, func(t *testing.T) {
			content.deny = failure == "acl"
			content.corrupt = failure == "bytes"
			r := start()
			require.NoError(t, m.evaluateEvalRun(ctx, 1, r.ID))
			v := read(r.ID)
			require.Equal(t, "UNAVAILABLE", v.State)
			require.Equal(t, 2, v.Inconclusive)
			require.Nil(t, v.AgreementPPM)
		})
	}
}

func TestEvalTargetsRejectUnboundAgentsAndChangedMedia(t *testing.T) {
	m, db, ctx, _, draft := evalFixture(t)
	created := addEvalFixture(t, m, db, ctx, draft)
	for _, target := range []EvalTarget{{AgentID: 77}, {Model: "unbound"}, {Role: "AUTHOR", AgentID: 77, Model: "model"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.pinEvalTargetOn(ctx, tx, 1, &target, VerifierDefinition{}) }), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.evalAgentOn(ctx, tx, 1, EvalTarget{AgentID: 77}); return e }), ErrUnavailable)
	injected := errors.New("agent configuration unavailable")
	m.evalAgents.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (fleet.VerifierAgentConfiguration, error) {
		return fleet.VerifierAgentConfiguration{}, injected
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.evalAgentOn(ctx, tx, 1, EvalTarget{AgentID: 77}); return e }), injected)
	m.evalFiles = nil
	_, e := m.evalSampleInput(ctx, 1, 11, created, true)
	require.ErrorIs(t, e, ErrUnavailable)
	f := &videoFileFixture{resource: files.PinnedResource{BindingID: created.InputRef[0].BindingID, FileID: 41, VersionID: 43, SHA256: created.InputRef[0].SHA256}}
	m.evalFiles = f
	_, e = m.evalSampleInput(ctx, 1, 11, created, true)
	require.ErrorIs(t, e, ErrConflict)
	require.True(t, f.closed)
	f.resource.VersionID = 42
	_, e = m.evalSampleInput(ctx, 1, 11, created, true)
	require.ErrorIs(t, e, ErrConflict)
	require.True(t, f.closed)
	m.scope.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	_, e = m.evalSampleInput(ctx, 1, 11, created, true)
	require.ErrorIs(t, e, ErrForbidden)
}
