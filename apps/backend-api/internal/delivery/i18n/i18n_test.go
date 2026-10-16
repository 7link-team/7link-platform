package deliveryi18n

import (
	"context"
	"testing"

	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
)

func TestProjectValidationMessagesInAllLocales(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())
	for _, key := range []string{ClientChoice, PrimaryContact, PrimaryAccept, RepositoryURL, RepositoryName, PrimaryRepository, ProjectCodeTaken, ProjectNameTaken, ParentNotClient, ProvisioningUnavailable, ContactNotFound, IntakeNotFound, MemberRoleMismatch, ProjectLimitReached, HumanOnly} {
		translations := make(map[string]bool)
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			message := platformi18n.TContext(platformi18n.WithLocale(context.Background(), locale), key)
			require.NotEmpty(t, message)
			require.NotEqual(t, key, message)
			require.False(t, translations[message], "missing translation for %s in %s", key, locale)
			translations[message] = true
		}
	}
}
