package delivery

import (
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func TestLargeResultResponseIdempotentReplay(t *testing.T) {
	h := newResultFixture(t)
	criterion := h.input.Evidence[0].CriterionID
	h.input.Evidence = nil
	for i := range 80 {
		h.input.Evidence = append(h.input.Evidence, resultEvidenceBody{SpaceID: 500, FileID: guid.ID(1000 + i), FileVersion: 3, VersionID: guid.ID(2000 + i), CriterionID: criterion, Coverage: "COVERED", Note: strings.Repeat("e", 1000)})
	}
	in := &resultSubmitInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IfMatch: fmtETag(h.stored().Version), IdempotencyKey: "large-result-response"}, Body: h.input}
	created, err := h.m.submitResultHTTP(h.actor(11), in)
	require.NoError(t, err)
	page, err := h.m.listResultsHTTP(h.actor(11), &resultListInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Len(t, page.Body.Data[0].Evidence, 80)
	replayed, err := h.m.submitResultHTTP(h.actor(11), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, created.Body.Data, replayed.Body.Data)
}

func TestResultProducerCannotRequestChangesOnOwnHandedOffResult(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	handoff, err := h.handoff(result.ID)
	require.NoError(t, err)
	before := h.snapshot()
	_, err = h.receive(result.ID, handoff.Handoffs[0].ID, 11, false)
	require.ErrorIs(t, err, ErrResultIndependent)
	require.Equal(t, before, h.snapshot())
}
