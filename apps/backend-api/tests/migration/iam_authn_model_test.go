package migration_test

import (
	"testing"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMAuthnModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{
		model.Credential{}, model.Session{}, model.PasswordHistory{}, model.MFAEnrollment{},
		model.RecoveryCode{}, model.MFAChallenge{}, model.PasskeyUser{}, model.Passkey{},
		model.PasskeyChallenge{}, model.PasswordRecoveryToken{}, model.NotificationOutbox{},
		model.EmailVerificationToken{}, model.StepUpChallenge{},
	}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}

	require.Len(t, models, 13)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			sources := []goosex.Source{
				(&identity.Module{}).MigrationSource(),
				(&iamauthn.Module{}).MigrationSource(),
			}
			for _, source := range sources {
				require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			}

			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) {
					assertModelMatchesTable(t, db, engine.dialect, persisted)
				})
			}
		})
	}
}
