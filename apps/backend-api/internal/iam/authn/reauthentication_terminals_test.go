package authn

import (
	"github.com/stretchr/testify/require"
	"reflect"
	"strings"
	"testing"
)

func TestTerminalStepUpPurposesAreRegistered(t *testing.T) {
	require.True(t, validReauthenticationPurpose(ReauthenticationTerminalInteractive))
	require.True(t, validReauthenticationPurpose(ReauthenticationMachineTerminalEnable))
	require.False(t, validReauthenticationPurpose("terminal_unbound"))
}

func TestTerminalStepUpPurposesAreAcceptedByHTTPContract(t *testing.T) {
	typ := reflect.TypeOf(reauthenticationBeginInput{})
	body, _ := typ.FieldByName("Body")
	purpose, _ := body.Type.FieldByName("Purpose")
	values := strings.Split(purpose.Tag.Get("enum"), ",")
	require.Contains(t, values, ReauthenticationTerminalInteractive)
	require.Contains(t, values, ReauthenticationMachineTerminalEnable)
}
