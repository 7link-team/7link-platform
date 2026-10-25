package templatesi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRegisterValidatesLocalesAndResolvesBindingErrors(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		ctx := platformi18n.WithLocale(t.Context(), locale)
		for _, key := range []string{"error_override_out_of_range", "error_breaking_unacknowledged", "error_diff_mismatch"} {
			qualified := Namespace + "." + key
			value := platformi18n.TContext(ctx, qualified)
			require.NotEmpty(t, value)
			require.NotEqual(t, qualified, value)
		}
	}
}
