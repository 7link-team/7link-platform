package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	transportaudit "7link.com/platform/internal/infra/audit"
	authnext "7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func bootstrapComposedApp(t *testing.T) *platformapp.App {
	return bootstrapComposedAppConfigured(t, nil)
}

func bootstrapComposedAppConfigured(t *testing.T, configure func(*platformapp.AppConfig)) *platformapp.App {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	config := platformapp.AppConfig{
		Name: "iam-openapi-test", Version: "1", Timezone: "UTC", Migrate: true,
		Database: map[string]platformapp.Database{"primary": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-openapi.sqlite"), Writable: true, Readable: true,
		}},
		Authn: platformapp.IAMAuthn(authnext.Config{
			Enabled: true, Issuer: "https://iam.example.test", SigningKey: key, AccessTokenTTL: time.Hour,
			Web: authnext.WebConfig{
				Enabled: true, PostLoginURL: "https://app.example.test/home",
				PostLogoutURL:     "https://app.example.test/signed-out",
				AllowedReturnURLs: []string{"https://app.example.test/home"},
				AllowedOrigins:    []string{"https://app.example.test"},
			},
			MFA: authnext.MFAConfig{EncryptionKey: key},
		}),
		Federation: platformapp.IAMFederation{Enabled: true, EncryptionKey: key},
		Webhook:    platformapp.IAMWebhook{Enabled: true, EncryptionKey: key},
		Secrets:    testBusinessSecretsConfig(t),
	}
	// Subscribers start before later modules seed their data. WAL lets readers
	// coexist with startup writes; competing writers need time to finish seeding.
	if configure != nil {
		configure(&config)
	}
	database := config.Database["primary"]
	database.Dsn += "?_journal_mode=WAL&_busy_timeout=30000"
	config.Database["primary"] = database
	app := platformapp.New(config)
	require.NoError(t, app.Bootstrap(t.Context()))
	t.Cleanup(func() { assert.NoError(t, app.Shutdown()) })
	return app
}

func testBusinessSecretsConfig(t *testing.T) platformapp.BusinessSecretsConfig {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "business.key")
	require.NoError(t, os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0600))
	return platformapp.BusinessSecretsConfig{Enabled: true, ActiveKeyFile: path}
}

type capturingAuditSink struct {
	mu     sync.Mutex
	events []transportaudit.Event
}

func (sink *capturingAuditSink) Write(_ context.Context, events []transportaudit.Event) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, events...)
	return nil
}

func (sink *capturingAuditSink) snapshot() []transportaudit.Event {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]transportaudit.Event(nil), sink.events...)
}

func moduleRouter(t *testing.T, app *platformapp.App) *chi.Mux {
	t.Helper()
	router := chi.NewRouter()
	require.NoError(t, app.RegisterHTTP(t.Context(), router))
	return router
}
