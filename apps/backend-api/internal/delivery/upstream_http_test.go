package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpstreamHTTPInvalidIdentityAndUnavailable(t *testing.T) {
	m := &Module{}
	_, err := m.listConsumptionsHTTP(t.Context(), &ResultWorkInput{EntityID: "bad", WorkItemID: "1"})
	require.Error(t, err)
	var response *respx.Body[[]model.ArtifactConsumption]
	response, err = m.listConsumptionsHTTP(t.Context(), &ResultWorkInput{EntityID: "1", WorkItemID: "2"})
	require.Nil(t, response)
	require.Error(t, err)
}
