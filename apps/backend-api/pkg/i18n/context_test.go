package i18n

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocaleContextHelpers(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		ctx := WithLocale(context.Background(), "ja")
		assert.Equal(t, "ja", LocaleFromContext(ctx))
	})

	t.Run("empty context", func(t *testing.T) {
		assert.Equal(t, "", LocaleFromContext(context.Background()))
	})
}
