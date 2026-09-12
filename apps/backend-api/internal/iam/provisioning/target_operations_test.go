package provisioning

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func independentProvisioningService(t *testing.T, env *provisioningEnvironment) *Service {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": env.datasource})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("provisioning-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(env.prefix))
	s := env.service
	other := NewService(runtime, s.appender, newProvisioningTestIDs(90000), s.identity, s.groups, s.actors, s.fields, Config{}, s.keys, testOutboundPolicy())
	bindForeignTables(t, other.repo, runtime.TablePrefix())
	other.now = s.now
	return other
}

func TestTargetClaimExcludesIndependentInstanceAndProtectsFirstPush(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	other := independentProvisioningService(t, env)
	entered, resume := make(chan struct{}), make(chan struct{})
	var deletes atomic.Int64
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if puts.Add(1) == 1 {
				close(entered)
				<-resume
			}
		} else {
			deletes.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer close(resume)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("first", "first", "first@example.test"))
	require.NoError(t, err)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "First", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID, userID := parseGUID(target.Target.ID), parseGUID(user.ID)
	done := make(chan error, 1)
	go func() {
		_, err := env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("PUT did not start")
	}
	assert.ErrorIs(t, other.DeleteTarget(t.Context(), env.auth.TenantID, targetID), ErrTargetBusy)
	assert.ErrorIs(t, other.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID), ErrTargetBusy)
	_, err = other.RetireTarget(t.Context(), env.auth.TenantID, targetID)
	assert.ErrorIs(t, err, ErrTargetBusy)
	err = env.db.Transaction(func(tx *gorm.DB) error {
		return ErasePrincipalData(t.Context(), tx, env.prefix, userID, provisioningTestNow)
	})
	assert.ErrorIs(t, err, ErrPrincipalRemoteProvisioned)
	assert.Zero(t, deletes.Load())
	// Let the remote complete, then verify the other connection can own DELETE.
	resume <- struct{}{}
	require.NoError(t, <-done)
	require.NoError(t, other.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID))
	_, err = env.service.pushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID, true)
	assert.ErrorIs(t, err, ErrDeprovisionMissing, "stale automatic sync must recheck the tombstone inside the claim")
	assert.EqualValues(t, 1, deletes.Load())
	assert.EqualValues(t, 1, puts.Load())
	assert.Zero(t, env.service.operationReleaseFailures.Load(), "successful transactional completion must not release twice")
	assert.Zero(t, other.operationReleaseFailures.Load())
}

func TestTargetTimedOutPUTNeverExpiresOrAllowsRetirement(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	other := independentProvisioningService(t, env)
	entered, resume := make(chan struct{}), make(chan struct{})
	var deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			close(entered)
			<-resume
		} else {
			deletes.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer close(resume)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("timeout", "timeout", "timeout@example.test"))
	require.NoError(t, err)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Timeout", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	env.service.http.Timeout = 100 * time.Millisecond
	_, err = env.service.PushResource(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrRemoteUnavailable)
	select {
	case <-entered:
	default:
		t.Fatal("request must actually have reached the remote")
	}
	other.now = func() time.Time { return provisioningTestNow.Add(24 * time.Hour) }
	_, err = other.RetireTarget(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID))
	assert.ErrorIs(t, err, ErrTargetBusy)
	assert.ErrorIs(t, other.DeleteTarget(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID)), ErrTargetBusy)
	assert.Zero(t, deletes.Load())
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register((&Module{service: other}).Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) > 0 {
			values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
		}
	}
	assert.InDelta(t, 1, values["iam_scim_unfinished_operations"], 0.0001)
	assert.InDelta(t, 86400, values["iam_scim_oldest_operation_age_seconds"], 0.0001)
	assert.InDelta(t, 1, values["iam_scim_metrics_database_success"], 0.0001)
}

func TestTargetRetirementFailureBlocksPushAndResumes(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	other := independentProvisioningService(t, env)
	provider, server := newSCIMProviderEndpoint(t, 200, "")
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("retire", "retire", "retire@example.test"))
	require.NoError(t, err)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Retire", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	provider.configure(429, "rate limited", false)
	_, err = env.service.RetireTarget(t.Context(), env.auth.TenantID, targetID)
	assert.ErrorIs(t, err, ErrRemoteResponse)
	_, err = other.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrTargetRetiring)
	_, err = other.ReplaceTarget(t.Context(), env.auth.TenantID, targetID, "Retire", server.URL, TargetActive, "new token", 1, &env.auth.PrincipalID)
	assert.ErrorIs(t, err, ErrTargetRetiring)
	provider.configure(204, "", false)
	n, err := other.RetireTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	assert.ErrorIs(t, err, ErrTargetMissing)
}

func TestTargetSyncPagesAndFailureBackoff(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, 200, "")
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Pages", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	for i := 0; i < syncPageSize+3; i++ {
		user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput(fmt.Sprint("page-", i), fmt.Sprint("page-", i), fmt.Sprintf("page-%d@example.test", i)))
		require.NoError(t, err)
		row := targetResourceRow{TargetID: targetID, ResourceType: ResourceUser, ResourceID: parseGUID(user.ID), ExternalID: user.ID, Version: 1, CreatedAt: provisioningTestNow, UpdatedAt: provisioningTestNow}
		require.NoError(t, env.service.repo.upsertTargetResource(t.Context(), &row))
	}
	n, err := env.service.SyncTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Equal(t, syncPageSize, n)
	n, err = env.service.SyncTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, syncPageSize+3, provider.count())
	provider.configure(429, "rate limited", false)
	m := &Module{service: env.service}
	m.reconcileAll(t.Context())
	assert.EqualValues(t, 1, m.syncFailures.Load())
	before := provider.count()
	m.reconcileAll(t.Context())
	assert.Equal(t, before, provider.count(), "persistent target backoff must suppress retries")
	assert.NoError(t, m.CheckHealth(t.Context()), "remote failures must not remove login readiness")
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	m.reconcileAll(canceled)
	assert.Equal(t, before, provider.count())
	provider.configure(200, "", false)
	env.service.now = func() time.Time { return provisioningTestNow.Add(3 * time.Second) }
	m.reconcileAll(t.Context())
	assert.Greater(t, provider.count(), before, "a definite failure must allow recovery after backoff")
	row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Zero(t, row.SyncFailures)
}

func TestTargetAcceptedResponseRetainsClaim(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	_, server := newSCIMProviderEndpoint(t, http.StatusAccepted, "")
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("accepted", "accepted", "accepted@example.test"))
	require.NoError(t, err)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Accepted", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrRemoteUnavailable)
	row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID))
	require.NoError(t, err)
	assert.NotZero(t, row.OperationToken)
	assert.Equal(t, parseGUID(user.ID), row.OperationResourceID)
	assert.Equal(t, ResourceUser, row.OperationResourceType)
}

func TestWorkerAdvancesAcrossTargetPages(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	provider, server := newSCIMProviderEndpoint(t, 200, "")
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("target-pages", "target-pages", "pages@example.test"))
	require.NoError(t, err)
	for i := 0; i < syncTargetPageSize+1; i++ {
		target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, fmt.Sprintf("Target %d", i), server.URL, "token", &env.auth.PrincipalID)
		require.NoError(t, err)
		row := targetResourceRow{TargetID: parseGUID(target.Target.ID), ResourceType: ResourceUser, ResourceID: parseGUID(user.ID), ExternalID: user.ID, Version: 1, CreatedAt: provisioningTestNow, UpdatedAt: provisioningTestNow}
		require.NoError(t, env.service.repo.upsertTargetResource(t.Context(), &row))
	}
	m := &Module{service: env.service}
	m.reconcileAll(t.Context())
	assert.Equal(t, syncTargetPageSize, provider.count())
	m.reconcileAll(t.Context())
	assert.Equal(t, syncTargetPageSize+1, provider.count())
	assert.Zero(t, m.syncCursor)
}

func newTargetOperationEnvironment(t *testing.T) *provisioningEnvironment {
	t.Helper()
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		return newProvisioningEnvironment(t)
	}
	require.Contains(t, []string{"postgres", "mysql"}, dialect)
	require.NotEmpty(t, dsn)
	// Each test owns only these generated-prefix tables, including cleanup.
	prefix := fmt.Sprintf("sop_%x_", time.Now().UnixNano())
	return newProvisioningEnvironmentWithDatasource(t, prefix, gormx.Datasource{Type: dialect, DSN: dsn, Writable: true, Readable: true})
}

func TestTargetCoordinationExternalDatabase(t *testing.T) {
	if os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE") == "" && os.Getenv("IAM_LIFECYCLE_DATABASE_DSN") == "" {
		t.Skip("external database is supplied by the PostgreSQL/MySQL gates")
	}
	t.Run("first_push_exclusion", TestTargetClaimExcludesIndependentInstanceAndProtectsFirstPush)
	t.Run("timeout_no_takeover", TestTargetTimedOutPUTNeverExpiresOrAllowsRetirement)
	t.Run("retirement_resume", TestTargetRetirementFailureBlocksPushAndResumes)
	t.Run("mapping_pages_backoff", TestTargetSyncPagesAndFailureBackoff)
	t.Run("target_pages", TestWorkerAdvancesAcrossTargetPages)
	t.Run("asynchronous_outcome", TestTargetAcceptedResponseRetainsClaim)
	t.Run("stale_completion", TestTargetStaleClaimCannotReleaseNewOwner)
}

func TestTargetStaleClaimCannotReleaseNewOwner(t *testing.T) {
	env := newTargetOperationEnvironment(t)
	other := independentProvisioningService(t, env)
	_, server := newSCIMProviderEndpoint(t, 200, "")
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Claim ownership", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	old, err := env.service.claimTarget(t.Context(), env.auth.TenantID, targetID, "push", false, ResourceUser, 123)
	require.NoError(t, err)
	require.NoError(t, old.finish(t.Context(), env.service.repo))
	current, err := other.claimTarget(t.Context(), env.auth.TenantID, targetID, "push", false, ResourceUser, 124)
	require.NoError(t, err)
	assert.ErrorIs(t, old.finish(t.Context(), env.service.repo), ErrTargetBusy)
	old.release()
	row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Equal(t, current.token, row.OperationToken)
	assert.EqualValues(t, 124, row.OperationResourceID)
	assert.EqualValues(t, 1, env.service.operationReleaseFailures.Load())
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Error(t, current.finish(canceled, other.repo))
	row, err = env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Equal(t, current.token, row.OperationToken, "a failed database completion keeps the claim")
	current.release()
	row, err = env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Zero(t, row.OperationToken)
}

func TestRetirementPreconditionsDoNotFreezeTarget(t *testing.T) {
	for _, condition := range []string{"disabled", "unbound", "inactive-actor"} {
		t.Run(condition, func(t *testing.T) {
			env := newProvisioningEnvironment(t)
			_, server := newSCIMProviderEndpoint(t, 200, "")
			target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Precondition", server.URL, "token", &env.auth.PrincipalID)
			require.NoError(t, err)
			targetID := parseGUID(target.Target.ID)
			want := ErrUnauthorized
			switch condition {
			case "disabled":
				require.NoError(t, env.db.Table(env.prefix+"iam_scim_targets").Where("id = ?", targetID).Update("status", TargetDisabled).Error)
				want = ErrTargetDisabled
			case "unbound":
				require.NoError(t, env.db.Table(env.prefix+"iam_scim_targets").Where("id = ?", targetID).Update("service_account_id", nil).Error)
			case "inactive-actor":
				env.service.actors = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			}
			_, err = env.service.RetireTarget(t.Context(), env.auth.TenantID, targetID)
			assert.ErrorIs(t, err, want)
			row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
			require.NoError(t, err)
			assert.False(t, row.Retiring)
			assert.Zero(t, row.OperationToken, "configuration can still be corrected")
		})
	}
}

func TestTargetDatabaseFailureIsNotReportedAsSuccessfulSync(t *testing.T) {
	env := newProvisioningEnvironment(t)
	_, server := newSCIMProviderEndpoint(t, 200, "")
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Database failure", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	idFailure := errors.New("identifier store unavailable")
	ids := env.service.ids
	env.service.ids = fixedProvisioningID{err: idFailure}
	_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, 123)
	assert.ErrorIs(t, err, idFailure)
	env.service.ids = ids
	row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
	require.NoError(t, err)
	assert.Zero(t, row.OperationToken, "ID failure must not leave an unidentifiable claim")
	// Removing this test fixture's mapping table simulates an unavailable
	// mapping store. Both synchronization and monitoring must expose failure.
	require.NoError(t, env.db.Migrator().DropTable(env.prefix+"iam_scim_target_resources"))
	_, err = env.service.SyncTarget(t.Context(), env.auth.TenantID, targetID)
	assert.ErrorContains(t, err, "page SCIM mappings")
	_, err = env.service.RetireTarget(t.Context(), env.auth.TenantID, targetID)
	assert.ErrorContains(t, err, "page SCIM mappings")
	m := &Module{service: env.service, config: Config{SyncInterval: time.Second}}
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	families, err := registry.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) > 0 {
			values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
		}
	}
	assert.Contains(t, values, "iam_scim_metrics_database_success")
	assert.InDelta(t, 0, values["iam_scim_metrics_database_success"], 0.0001)
	assert.InDelta(t, 1, values["iam_scim_sync_enabled"], 0.0001)
	assert.InDelta(t, 1, values["iam_scim_retiring_targets"], 0.0001)
}

func TestGateway504CannotReleaseInFlightRemoteOperation(t *testing.T) {
	for _, delayedMethod := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(delayedMethod, func(t *testing.T) {
			env := newProvisioningEnvironment(t)
			other := independentProvisioningService(t, env)
			entered, resume, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var delayed, remoteExists atomic.Bool
			delayed.Store(delayedMethod == http.MethodPut)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if delayed.Load() && r.Method == delayedMethod {
					close(entered)
					<-resume
				}
				remoteExists.Store(r.Method == http.MethodPut)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer provider.Close()
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				forward := func() {
					request, err := http.NewRequestWithContext(context.Background(), r.Method, provider.URL+r.URL.Path, bytes.NewReader(body))
					if err != nil {
						return
					}
					response, err := http.DefaultClient.Do(request)
					if err == nil {
						_ = response.Body.Close()
					}
				}
				if delayed.Load() && r.Method == delayedMethod {
					go func() { forward(); close(completed) }()
					<-entered
					w.WriteHeader(http.StatusGatewayTimeout)
					return
				}
				forward()
				w.WriteHeader(http.StatusNoContent)
			}))
			defer gateway.Close()
			release := sync.OnceFunc(func() { close(resume) })
			defer release()
			user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("gateway", "gateway", "gateway@example.test"))
			require.NoError(t, err)
			target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Gateway", gateway.URL, "token", &env.auth.PrincipalID)
			require.NoError(t, err)
			targetID, userID := parseGUID(target.Target.ID), parseGUID(user.ID)
			_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
			if delayedMethod == http.MethodDelete {
				require.NoError(t, err)
				delayed.Store(true)
				err = env.service.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
			}
			assert.ErrorIs(t, err, ErrRemoteResponse)
			var competingErr error
			if delayedMethod == http.MethodPut {
				competingErr = other.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
			} else {
				_, competingErr = other.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
			}
			assert.ErrorIs(t, competingErr, ErrTargetBusy, "a gateway response does not finish the provider operation")
			release()
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("provider did not complete")
			}
			if competingErr == nil {
				assert.Equal(t, delayedMethod != http.MethodPut, remoteExists.Load(), "late provider operation reversed an acknowledged competing write")
			}
			row, err := other.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
			require.NoError(t, err)
			assert.NotZero(t, row.OperationToken, "only verified recovery may resolve the unknown outcome")
		})
	}
}

func TestUncertainHTTPResponsesKeepClaimsAndExposeOperationalState(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			env := newProvisioningEnvironment(t)
			provider, server := newSCIMProviderEndpoint(t, 204, "")
			user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("statuses", "statuses", "statuses@example.test"))
			require.NoError(t, err)
			for _, status := range []int{408, 500, 502, 503, 504, 499, 429} {
				t.Run(fmt.Sprint(status), func(t *testing.T) {
					target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, fmt.Sprint("Status ", status), server.URL, "token", &env.auth.PrincipalID)
					require.NoError(t, err)
					targetID, userID := parseGUID(target.Target.ID), parseGUID(user.ID)
					if method == http.MethodDelete {
						provider.configure(204, "", false)
						_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
						require.NoError(t, err)
					}
					provider.configure(status, "remote response", false)
					if method == http.MethodPut {
						_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
					} else {
						err = env.service.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
					}
					assert.ErrorIs(t, err, ErrRemoteResponse)
					row, err := env.service.repo.getTarget(t.Context(), env.auth.TenantID, targetID)
					require.NoError(t, err)
					if status == 429 {
						assert.Zero(t, row.OperationToken)
					} else {
						assert.NotZero(t, row.OperationToken)
					}
					before := provider.count()
					provider.configure(204, "", false)
					if method == http.MethodPut {
						_, err = env.service.PushResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
					} else {
						err = env.service.DeprovisionResource(t.Context(), env.auth.TenantID, targetID, ResourceUser, userID)
					}
					if status == 429 {
						require.NoError(t, err)
					} else {
						assert.ErrorIs(t, err, ErrTargetBusy)
						assert.Equal(t, before, provider.count(), "restored health does not prove the old request finished")
					}
				})
			}
			registry := prometheus.NewRegistry()
			require.NoError(t, registry.Register((&Module{service: env.service}).Collector()))
			families, err := registry.Gather()
			require.NoError(t, err)
			values := map[string]float64{}
			for _, family := range families {
				if len(family.Metric) > 0 {
					values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
				}
			}
			assert.InDelta(t, 6, values["iam_scim_unfinished_operations"], 0.0001)
			assert.InDelta(t, 1, values["iam_scim_metrics_database_success"], 0.0001)
		})
	}
}
