package maintenance

import (
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/files"
	"github.com/stretchr/testify/require"
)

func TestFilesMigrationRejectsInvalidRequestBeforeOpeningDeployment(t *testing.T) {
	_, err := RunFileMigration(t.Context(), app.AppConfig{}, 0, "local")
	require.ErrorIs(t, err, files.ErrInvalidKey)
	_, err = RunFileMigration(t.Context(), app.AppConfig{}, 1, "")
	require.ErrorIs(t, err, files.ErrInvalidKey)
}
