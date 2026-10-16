package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestProjectReferencesUseActualTenantOwnedIntakeRecords(t *testing.T) {
	_, db, _ := projectHarness(t)
	sourceModule := intake.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("d_")), intake.Config{})
	source := sourceModule.MigrationSource()
	source.Prefix = "d_"
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	for tenant := 1; tenant <= 2; tenant++ {
		require.NoError(t, db.Table("d_intake_contacts").Create(map[string]any{"id": tenant, "tenant_id": tenant, "display_name": "Contact", "created_at": time.Now(), "updated_at": time.Now()}).Error)
		require.NoError(t, db.Table("d_intake_records").Create(map[string]any{"id": tenant + 9, "tenant_id": tenant, "ref_no": 1, "channel_id": 7, "channel_kind": "SHARED_CHANNEL", "contact_id": tenant, "title": "Source", "raw_content_hash": strings.Repeat("a", 64), "reply_token": strings.Repeat("x", tenant), "received_at": time.Now(), "created_at": time.Now(), "updated_at": time.Now()}).Error)
	}
	m := &Module{sources: intake.IntakeReader{GetOn: sourceModule.GetOn, ContactsOn: sourceModule.ContactsOn}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id := guid.ID(10)
		input := projectCreateBody{OriginIntakeID: &id, Contacts: []projectContactInput{{IntakeContactID: 1}}}
		require.NoError(t, m.projectReferencesOn(t.Context(), tx, 1, input))
		require.NoError(t, m.projectReferencesOn(t.Context(), tx, 1, projectCreateBody{}))
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 2, input), ErrIntakeNotFound)
		input.OriginIntakeID = nil
		input.Contacts[0].IntakeContactID = 2
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrContactNotFound)
		input.Contacts = []projectContactInput{{IntakeContactID: 1}, {IntakeContactID: 1}}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrInvalidArgument)
		input.Contacts = []projectContactInput{{IntakeContactID: 0}}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrInvalidArgument)
		zero := guid.ID(0)
		input = projectCreateBody{OriginIntakeID: &zero}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrInvalidArgument)
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), nil, 1, input), ErrInvalidArgument)
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), db, 1, input), ErrInvalidArgument)
		require.ErrorIs(t, (*Module)(nil).projectReferencesOn(t.Context(), tx, 1, input), ErrUnavailable)
		injected := errors.New("provider unavailable")
		m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{}, false, injected
		}
		input = projectCreateBody{OriginIntakeID: &id}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), injected)
		m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
			return intake.IntakeRef{ID: 10, TenantID: 2}, true, nil
		}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrUnavailable)
		input = projectCreateBody{Contacts: []projectContactInput{{IntakeContactID: 1}}}
		m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
			return nil, injected
		}
		require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), injected)
		for _, refs := range [][]intake.ContactReference{{{ID: 1, TenantID: 2}}, {{ID: 999, TenantID: 1}}, {{ID: 1, TenantID: 1}, {ID: 1, TenantID: 1}}} {
			m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
				return refs, nil
			}
			require.ErrorIs(t, m.projectReferencesOn(t.Context(), tx, 1, input), ErrUnavailable)
		}
		return nil
	}))
}
