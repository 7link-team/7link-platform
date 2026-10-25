package fleet

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnrollmentIAMRequiresEveryWorkflowCapability(t *testing.T) {
	port := (&enrollmentIAMFake{}).port()
	require.True(t, port.Bound())
	value := reflect.ValueOf(port)
	for index := 0; index < value.NumField(); index++ {
		t.Run(value.Type().Field(index).Name, func(t *testing.T) {
			partial := port
			field := reflect.ValueOf(&partial).Elem().Field(index)
			field.SetZero()
			require.False(t, partial.Bound())
		})
	}
}
