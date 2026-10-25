package executor

import (
	"errors"
	"testing"
)

func TestIntakeModelRejectsUnavailableRuntimeBeforeLaunch(t *testing.T) {
	for _, runtime := range []string{"UNKNOWN", ""} {
		_, err := SuggestIntake(t.Context(), runtime, "configured", "Actual source")
		if !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
	}
}
