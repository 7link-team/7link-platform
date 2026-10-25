package clientwork

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProgressSourceCandidateUsesCurrentProjectWorkAndFileAccess(t *testing.T) {
	m, _, projects, _ := harness(t)
	in := &ProgressSourceInput{EntityID: "3", WorkItemID: "11", FileID: "60"}
	got, err := m.progressSourceHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, guid.ID(11), got.Body.Data.WorkItemID)
	require.Equal(t, guid.ID(60), got.Body.Data.FileID)
	require.Equal(t, guid.ID(40), got.Body.Data.VersionID)
	require.Equal(t, strings.Repeat("a", 64), got.Body.Data.SHA256)
	_, err = m.progressSourceHTTP(testContext(t, 4), in)
	require.Error(t, err, "external client cannot inspect internal source candidates")
	projects.stale = true
	_, err = m.progressSourceHTTP(testContext(t, 2), in)
	require.Error(t, err)
	projects.stale = false
	m.files.InspectOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
		return files.PinnedCandidate{}, errors.New("current file access revoked")
	}
	_, err = m.progressSourceHTTP(testContext(t, 2), in)
	require.Error(t, err)
}

func TestProgressSourceCandidateRejectsInvalidForeignAndUnavailableInputs(t *testing.T) {
	for _, in := range []*ProgressSourceInput{{EntityID: "bad", WorkItemID: "11", FileID: "60"}, {EntityID: "3", WorkItemID: "bad", FileID: "60"}, {EntityID: "3", WorkItemID: "11", FileID: "bad"}, {EntityID: "3", WorkItemID: "12", FileID: "60"}, {EntityID: "3", WorkItemID: "11", FileID: "61"}} {
		m, _, _, _ := harness(t)
		_, err := m.progressSourceHTTP(testContext(t, 2), in)
		require.Error(t, err)
	}
	m, _, _, _ := harness(t)
	m.files.InspectOn = nil
	_, err := m.progressSourceHTTP(testContext(t, 2), &ProgressSourceInput{EntityID: "3", WorkItemID: "11", FileID: "60"})
	require.Error(t, err)
}
