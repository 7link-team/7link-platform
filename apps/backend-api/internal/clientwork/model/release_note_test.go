package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReleaseNoteDraftTableName(t *testing.T) {
	require.Equal(t, "clientwork_release_note_drafts", (ReleaseNoteDraft{}).TableName())
}
