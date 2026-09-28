package filesi18n

import (
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPublishesEveryFilesLocale(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())

	for _, locale := range platformi18n.Supported() {
		ctx := platformi18n.WithLocale(t.Context(), locale.Code)
		for _, key := range []string{
			InvalidFileUpdate, InvalidFileContent, InvalidFileRoot, InvalidFileScope, DirectoryContentNotAllowed,
			InvalidFileKind, InvalidFileOperation, InvalidFileRequest, FileServiceUnavailable,
		} {
			assert.NotEqual(t, key, platformi18n.TContext(ctx, key), "%s in %s", key, locale.Code)
		}
	}
	assert.Error(t, Register(), "a module locale namespace is registered exactly once")
}

func TestTranslationsRenderAStoredLabelInEveryLocale(t *testing.T) {
	assert.Equal(t, map[string]string{"en-US": "File management", "zh-CN": "文件管理", "ms-MY": "Pengurusan fail"}, Translations(MenuSystemFiles))
}
