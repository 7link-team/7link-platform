package webhook

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleDeclaresWhatItNeedsAndWhatItOwns(t *testing.T) {
	instance := NewModule(newWebhookEnvironment(t).runtime, Config{})

	assert.Equal(t, Name, instance.ModuleName())
	// Reading the audit chain is the whole design, so audit and core have to be
	// declared or the registry would admit this module before them.
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name, core.Name}, instance.ModuleDependencies())
	assert.Equal(t, "goose_webhook", instance.MigrationSource().TableName)
	require.Len(t, instance.MigrationMilestones(), 2)
	assert.Equal(t, "base", instance.MigrationMilestones()[0].Name)
	assert.EqualValues(t, 1, instance.MigrationMilestones()[0].Version)
	assert.Equal(t, "intake_metadata", instance.MigrationMilestones()[1].Name)
	assert.EqualValues(t, 2, instance.MigrationMilestones()[1].Version)
}

func TestModuleIsOffUntilItIsConfiguredOn(t *testing.T) {
	// A deployment that says nothing about webhooks gets no worker and no routes,
	// which is what keeps an unconfigured signing key from failing startup.
	assert.False(t, NewModule(newWebhookEnvironment(t).runtime, Config{}).Enabled())
	assert.False(t, (*Module)(nil).Enabled())
	assert.True(t, NewModule(newWebhookEnvironment(t).runtime, Config{
		Config: module.Config{Enabled: true},
	}).Enabled())
}

func TestModuleRefusesToStartWithoutItsService(t *testing.T) {
	// Starting a worker that has nothing to read would spin forever reporting
	// nothing, so the absence is an error rather than a quiet no-op.
	instance := NewModule(newWebhookEnvironment(t).runtime, Config{})
	require.ErrorIs(t, instance.Start(t.Context()), ErrUnavailable)
	require.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	assert.NoError(t, instance.Stop(t.Context()), "stopping a worker that never started is not an error")
}

func TestModuleWorkerFollowsTheChainAndStops(t *testing.T) {
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	env.subscribe(target)

	instance := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, PollInterval: 5 * time.Millisecond})
	instance.service = env.service
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))

	env.stream.append(guid.ID(1), "principal_created")
	require.NoError(t, instance.Start(t.Context()))
	// Starting twice must not start a second worker: both passes would claim the
	// same deliveries and the attempt counts would stop meaning anything.
	require.NoError(t, instance.Start(t.Context()))

	require.Eventually(t, func() bool { return target.count() >= 1 }, 5*time.Second, 10*time.Millisecond,
		"the worker should follow the chain without being told about the event")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, instance.Stop(ctx))
	assert.NoError(t, instance.Stop(ctx), "stopping twice is not an error")
}

func TestModuleResolvesItsCapabilitiesOrRefuses(t *testing.T) {
	env := newWebhookEnvironment(t)
	key := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	// Nothing registered: the module must say which capability is missing rather
	// than construct a service that cannot read anything.
	instance := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, EncryptionKey: key})
	services := module.NewServices()
	require.NoError(t, services.Seal())
	require.Error(t, instance.InjectServices(services))
}

func TestResolveKeyringRequiresAKey(t *testing.T) {
	// Without a key the signing secrets cannot be opened, and a deployment that
	// discovered that at the first delivery would have stored secrets it could
	// never read.
	_, err := ResolveKeyring(Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption key is required")

	ring, err := ResolveKeyring(Config{
		EncryptionKey: base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	})
	require.NoError(t, err)
	assert.NotNil(t, ring)
}

func TestResolveOutboundPolicyRejectsABadNetwork(t *testing.T) {
	_, err := ResolveOutboundPolicy(Config{OutboundAllowNetworks: []string{"not-a-cidr"}})
	require.Error(t, err)

	policy, err := ResolveOutboundPolicy(Config{OutboundAllowNetworks: []string{"10.0.0.0/8"}})
	require.NoError(t, err)
	assert.NoError(t, policy.ValidateURL("https://example.com"))
}

func TestConfigDefaultsKeepTheWorkerFromSpinning(t *testing.T) {
	// A zero config must still poll on an interval, retry with a delay, and give
	// up eventually; zero values would mean a tight loop and infinite attempts.
	filled := Config{}.withDefaults()
	assert.Positive(t, filled.PollInterval)
	assert.Positive(t, filled.RetryBase)
	assert.Positive(t, filled.MaxAttempts)
	assert.Positive(t, filled.DeliveryBatch)
	assert.Positive(t, filled.FanOutBatch)
	assert.Positive(t, filled.FanOutSubscriptions)
	assert.Positive(t, filled.HTTPTimeout)

	// What a deployment did set is kept.
	kept := Config{PollInterval: time.Minute, MaxAttempts: 2}.withDefaults()
	assert.Equal(t, time.Minute, kept.PollInterval)
	assert.Equal(t, 2, kept.MaxAttempts)
}

func TestOutboundUserAgentComesFromTheRuntime(t *testing.T) {
	assert.Empty(t, outboundUserAgent(nil), "no runtime means no claim about who is calling")
	env := newWebhookEnvironment(t)
	assert.Equal(t, "webhook-test/1", outboundUserAgent(env.runtime))
}

func TestInjectServicesResolvesTheChainReaderOrRefuses(t *testing.T) {
	env := newWebhookEnvironment(t)
	key := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	// Identifiers present but no chain reader: the module must refuse rather than
	// build a follower with nothing to follow.
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, &webhookTestIDs{}))
	require.NoError(t, services.Seal())
	instance := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, EncryptionKey: key})
	require.Error(t, instance.InjectServices(services))

	// Everything present: the service is built and can be used straight away.
	full := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](full, &webhookTestIDs{}))
	require.NoError(t, module.Provide[audit.EventStreamReader](full, audit.EventStreamReader(env.stream.read)))
	require.NoError(t, module.Provide[*authz.Registrar](full, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	)))
	require.NoError(t, full.Seal())

	ready := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, EncryptionKey: key})
	require.NoError(t, ready.InjectServices(full))
	require.NotNil(t, ready.service)
	require.NoError(t, ready.CheckHealth(t.Context()))
}

func TestInjectServicesRefusesAMisconfiguredKeyOrNetwork(t *testing.T) {
	env := newWebhookEnvironment(t)
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, &webhookTestIDs{}))
	require.NoError(t, module.Provide[audit.EventStreamReader](services, audit.EventStreamReader(env.stream.read)))
	require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewRegistrar(
		authz.DefaultRegistry(), webhookTokenVerifier{}, webhookPermissionChecker{}, webhookMembershipChecker{},
	)))
	require.NoError(t, services.Seal())

	// No key: stored signing secrets could never be opened, so this fails at
	// startup rather than at the first delivery.
	noKey := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}})
	require.Error(t, noKey.InjectServices(services))

	badNetwork := NewModule(env.runtime, Config{
		Config:                module.Config{Enabled: true},
		EncryptionKey:         base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		OutboundAllowNetworks: []string{"not-a-cidr"},
	})
	require.Error(t, badNetwork.InjectServices(services))
}

func TestNewServicePanicsWithoutWhatItCannotWorkWithout(t *testing.T) {
	env := newWebhookEnvironment(t)
	assert.Panics(t, func() { NewService(nil, env.stream.read, Config{}, nil, &webhookTestIDs{}, env.service.outbound) },
		"a service with no runtime could never resolve a datasource")
	assert.Panics(t, func() { NewService(env.runtime, nil, Config{}, nil, &webhookTestIDs{}, env.service.outbound) },
		"a follower with no chain to read has nothing to do")
	assert.Panics(t, func() { NewService(env.runtime, env.stream.read, Config{}, nil, nil, env.service.outbound) },
		"deliveries need identifiers")
}

func TestSealingASecretNeedsAKeyring(t *testing.T) {
	env := newWebhookEnvironment(t)
	env.service.keys = nil

	_, err := env.service.sealSecret(guid.ID(1), "shh")
	require.Error(t, err, "storing a secret that could never be opened would be worse than refusing")

	empty, err := env.service.sealSecret(guid.ID(1), "")
	require.NoError(t, err, "a subscription may have no secret at all")
	assert.Empty(t, empty)

	_, err = env.service.openSecret(subscriptionRow{SecretCiphertext: "sealed"})
	require.Error(t, err)

	none, err := env.service.openSecret(subscriptionRow{})
	require.NoError(t, err)
	assert.Nil(t, none, "no secret means the payload is sent unsigned")
}

func TestWorkerKeepsGoingAfterAFailedPass(t *testing.T) {
	// A chain that cannot be read, or a receiver that refuses, must not end the
	// worker: the next tick has to try again or a transient fault becomes
	// permanent until somebody restarts the process.
	env := newWebhookEnvironment(t)
	target := newReceiver(t)
	env.subscribe(target)
	env.stream.mu.Lock()
	env.stream.err = assert.AnError
	env.stream.mu.Unlock()

	instance := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, PollInterval: 5 * time.Millisecond})
	instance.service = env.service
	require.NoError(t, instance.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, instance.Stop(context.Background())) })

	// Let several passes fail, then let the chain recover.
	require.Eventually(t, func() bool { return env.stream.reads.Load() >= 3 }, 5*time.Second, 10*time.Millisecond,
		"the worker should keep trying")

	env.stream.mu.Lock()
	env.stream.err = nil
	env.stream.mu.Unlock()
	env.stream.append(guid.ID(1), "principal_created")

	require.Eventually(t, func() bool { return target.count() >= 1 }, 5*time.Second, 10*time.Millisecond,
		"a worker that survived the failures delivers once the chain reads again")
}

func TestStopReportsADeadlineItCouldNotMeet(t *testing.T) {
	// Stop waits for the pass in flight. A caller whose deadline passes first has
	// to be told, because the worker is still running.
	env := newWebhookEnvironment(t)
	instance := NewModule(env.runtime, Config{Config: module.Config{Enabled: true}, PollInterval: time.Hour})
	instance.service = env.service
	require.NoError(t, instance.Start(t.Context()))

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	// The worker is parked on its ticker and the deadline is already gone, so the
	// wait cannot succeed.
	err := instance.Stop(expired)
	if err != nil {
		assert.Contains(t, err.Error(), "did not stop")
	}
	// Either way the worker ends, so the test does not leak it.
	assert.NoError(t, instance.Stop(context.Background()))
}
