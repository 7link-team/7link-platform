package delivery

import (
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Use the actual catalog and publisher rather than accepting every event in a
// stub: undeclared types or mismatched subjects must abort the domain command.
func realDeliveryPublisher(t *testing.T, db *gorm.DB) events.Publisher {
	t.Helper()
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	for _, provide := range moduleProviders(t) {
		require.NoError(t, provide(services))
	}
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	publisher, err := module.Resolve[events.Publisher](services)
	require.NoError(t, err)
	eventModule, ok := publisher.(*events.Module)
	require.True(t, ok)
	require.NoError(t, eventModule.InjectServices(services))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, eventModule.MigrationSource(), db.Dialector.Name()))
	require.NoError(t, eventModule.Initialize(t.Context()))
	return publisher
}

func TestScopeAndFeedbackPublishRegisteredRealEvents(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		m, db, ctx, requirement, body := scopeHarness(t)
		m.service.events = realDeliveryPublisher(t, db)
		_, err := m.scopeChangeHTTP(ctx, &scopeChangeInput{EntityID: "99", RequirementID: requirement.ID.String(), IfMatch: fmtETag(requirement.Version), Body: body})
		require.NoError(t, err)
		var count int64
		require.NoError(t, db.Table("domain_events").Where("event_type = ?", "requirement.scope_changed").Count(&count).Error)
		require.EqualValues(t, 1, count)
	})
	t.Run("feedback", func(t *testing.T) {
		m, db, ctx, in := feedbackHarness(t)
		m.service.events = realDeliveryPublisher(t, db)
		_, err := m.appendResultFeedbackHTTP(ctx, in)
		require.NoError(t, err)
		var count int64
		require.NoError(t, db.Table("domain_events").Where("event_type = ?", "result.feedback_added").Count(&count).Error)
		require.EqualValues(t, 1, count)
	})
}
