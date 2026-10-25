package clientwork

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAcceptanceRemindersHTTPInvalidPackageAndScope(t *testing.T) {
	m, _, _, _ := harness(t)
	for _, id := range []string{"0", "missing", "999999"} {
		_, err := m.acceptanceRemindersHTTP(testContext(t, 4), &PackageTimelineInput{ListInput: ListInput{EntityID: "3", Limit: 10}, ID: id})
		require.Error(t, err)
	}
}
