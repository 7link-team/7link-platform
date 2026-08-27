package module

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testService interface {
	Value() string
}

type testServiceImpl struct{ value string }

func (service *testServiceImpl) Value() string { return service.value }

func TestServicesProvideAndResolve(t *testing.T) {
	services := NewServices()
	expected := &testServiceImpl{value: "ready"}
	require.NoError(t, Provide[testService](services, expected))
	require.NoError(t, services.Seal())

	resolved, err := Resolve[testService](services)
	require.NoError(t, err)
	assert.Same(t, expected, resolved)
	assert.Equal(t, "ready", resolved.Value())
}

func TestServicesRejectMissingDuplicateAndNil(t *testing.T) {
	services := NewServices()
	_, err := Resolve[testService](services)
	assert.ErrorIs(t, err, ErrServicesProviding)

	require.NoError(t, Provide[testService](services, &testServiceImpl{}))
	err = Provide[testService](services, &testServiceImpl{})
	assert.ErrorIs(t, err, ErrServiceDuplicate)
	require.NoError(t, services.Seal())
	_, err = Resolve[fmt.Stringer](services)
	assert.ErrorIs(t, err, ErrServiceMissing)
	err = Provide[testService](services, &testServiceImpl{})
	assert.ErrorIs(t, err, ErrServicesSealed)

	var nilService *testServiceImpl
	err = Provide[testService](NewServices(), nilService)
	assert.ErrorContains(t, err, "service is nil")
}
