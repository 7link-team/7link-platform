package idempotency

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency/query"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testIDs struct{ next atomic.Int64 }

func (s *testIDs) Next() (int64, error) { return s.next.Add(1), nil }
func storeHarness(t *testing.T) (*Module, *gorm.DB, Key) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "keys.sqlite"), Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	m := &Module{names: query.BindNames("ik_"), ids: &testIDs{}, now: func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }}
	source := m.MigrationSource()
	source.Prefix = "ik_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	require.NoError(t, db.Exec("CREATE TABLE business_effects (id INTEGER)").Error)
	hash, err := HashRequest("POST", "/tenants/{tenant_id}/projects", map[string]string{"tenant_id": "1"}, struct {
		Name string `json:"name"`
	}{"Project"})
	require.NoError(t, err)
	return m, db, Key{TenantID: 1, SubjectKind: "PRINCIPAL", SubjectID: 11, Operation: "project.create", Value: "request-1", RequestHash: hash}
}
func keyCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table("ik_platform_idempotency_keys").Count(&n).Error)
	return n
}
func effectCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table("business_effects").Count(&n).Error)
	return n
}

func TestDoReplaysScopedResponseAndRollsBackWithBusiness(t *testing.T) {
	m, db, key := storeHarness(t)
	calls := 0
	expected := Result{StatusCode: 201, Body: json.RawMessage(`{"code":0,"data":{"id":"9"}}`), ResourceKind: "project", ResourceID: 9}
	fn := func(tx *gorm.DB) (Result, error) {
		calls++
		return expected, tx.Exec("INSERT INTO business_effects VALUES (9)").Error
	}
	run := func(k Key) (Result, bool, error) {
		var result Result
		var replay bool
		err := db.Transaction(func(tx *gorm.DB) error { var err error; result, replay, err = m.Do(t.Context(), tx, k, fn); return err })
		return result, replay, err
	}
	first, replayed, err := run(key)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, expected, first)
	second, replayed, err := run(key)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, first, second)
	require.Equal(t, 1, calls)
	require.EqualValues(t, 1, effectCount(t, db))
	different := key
	different.RequestHash[0]++
	_, _, err = run(different)
	require.ErrorIs(t, err, ErrKeyReused)
	require.Equal(t, 1, calls)
	for _, mutate := range []func(*Key){func(k *Key) { k.TenantID++ }, func(k *Key) { k.SubjectKind = "CONTACT" }, func(k *Key) { k.SubjectID++ }, func(k *Key) { k.Operation = "project.other" }, func(k *Key) { k.Value = "Request-1" }} {
		scoped := key
		mutate(&scoped)
		_, replay, err := run(scoped)
		require.NoError(t, err)
		require.False(t, replay)
	}
	require.Equal(t, 6, calls)
	aborted := errors.New("outer transaction failed")
	key.Value = "abort"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.Do(t.Context(), tx, key, fn)
		require.NoError(t, err)
		return aborted
	}), aborted)
	require.EqualValues(t, 6, keyCount(t, db))
	require.EqualValues(t, 6, effectCount(t, db))
	_, replayed, err = run(key)
	require.NoError(t, err)
	require.False(t, replayed)
	oldNow := m.now()
	m.now = func() time.Time { return oldNow.Add(24 * time.Hour) }
	key.RequestHash[0]++
	_, replayed, err = run(key)
	require.NoError(t, err)
	require.False(t, replayed, "expired keys can be reused with a new body")
	require.EqualValues(t, 7, keyCount(t, db))
}

func TestDoCachesOnlyDeterministicResponsesWithoutPartialEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		cached bool
	}{
		{"success", 200, `{"code":0}`, true}, {"no content", 204, "", true}, {"bad request", 400, `{"code":120400}`, true}, {"forbidden", 403, `{"code":120403}`, true}, {"missing", 404, `{"code":120404}`, true}, {"invalid", 422, `{"code":127044}`, true}, {"business conflict", 409, `{"code":127001}`, true}, {"version", 409, `{"code":120409}`, false}, {"version reason", 409, `{"data":{"reason_code":"VERSION_CONFLICT"}}`, false}, {"interrupted", 409, `{"data":{"state":"interrupted"}}`, false}, {"server", 500, `{"code":120500}`, false}, {"rate", 429, `{}`, false}, {"redirect", 302, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db, key := storeHarness(t)
			calls := 0
			for attempt := 0; attempt < 2; attempt++ {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					result, replay, err := m.Do(t.Context(), tx, key, func(work *gorm.DB) (Result, error) {
						calls++
						return Result{StatusCode: tc.status, Body: json.RawMessage(tc.body)}, work.Exec("INSERT INTO business_effects VALUES (1)").Error
					})
					require.NoError(t, err)
					require.Equal(t, tc.status, result.StatusCode)
					require.Equal(t, attempt == 1 && tc.cached, replay)
					return err
				}))
			}
			if tc.cached {
				require.Equal(t, 1, calls)
				require.EqualValues(t, 1, keyCount(t, db))
			} else {
				require.Equal(t, 2, calls)
				require.Zero(t, keyCount(t, db))
			}
			if tc.status < 300 {
				require.EqualValues(t, 1, effectCount(t, db))
			} else {
				require.Zero(t, effectCount(t, db))
			}
		})
	}
}

func TestDoRejectsInvalidKeysAndCallbackFailures(t *testing.T) {
	m, db, key := storeHarness(t)
	fn := func(*gorm.DB) (Result, error) {
		t.Fatal("invalid request called business function")
		return Result{}, nil
	}
	for _, mutate := range []func(*Key){func(k *Key) { k.Value = "" }, func(k *Key) { k.Value = "a b" }, func(k *Key) { k.Value = "中文" }, func(k *Key) { k.Value = strings.Repeat("a", 65) }, func(k *Key) { k.Operation = "" }, func(k *Key) { k.Operation = strings.Repeat("a", 129) }, func(k *Key) { k.SubjectKind = "AGENT" }, func(k *Key) { k.SubjectID = 0 }, func(k *Key) { k.TenantID = -1 }} {
		invalid := key
		mutate(&invalid)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.Do(t.Context(), tx, invalid, fn)
			require.ErrorIs(t, err, ErrInvalidKey)
			return nil
		}))
	}
	for _, tx := range []*gorm.DB{nil, {}, db} {
		_, _, err := m.Do(t.Context(), tx, key, fn)
		require.ErrorIs(t, err, ErrInvalidKey)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := (*Module)(nil).Do(t.Context(), tx, key, fn)
		require.ErrorIs(t, err, ErrUnavailable)
		_, _, err = m.Do(t.Context(), tx, key, nil)
		require.ErrorIs(t, err, ErrInvalidKey)
		return nil
	}))
	for _, result := range []Result{{StatusCode: 199, Body: json.RawMessage(`{}`)}, {StatusCode: 201, Body: json.RawMessage(`broken`)}, {StatusCode: 201, Body: json.RawMessage(`{}`), ResourceID: 1}, {StatusCode: 201, Body: json.RawMessage(`{}`), ResourceKind: "project"}, {StatusCode: 201, Body: json.RawMessage(`"` + strings.Repeat("x", 65536) + `"`)}} {
		err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.Do(t.Context(), tx, key, func(work *gorm.DB) (Result, error) {
				require.NoError(t, work.Exec("INSERT INTO business_effects VALUES (1)").Error)
				return result, nil
			})
			return err
		})
		require.ErrorIs(t, err, ErrInvalidResult)
		require.Zero(t, keyCount(t, db))
		require.Zero(t, effectCount(t, db))
	}
	injected := errors.New("callback failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.Do(t.Context(), tx, key, func(work *gorm.DB) (Result, error) {
			require.NoError(t, work.Exec("INSERT INTO business_effects VALUES (1)").Error)
			return Result{}, injected
		})
		return err
	}), injected)
	require.Zero(t, keyCount(t, db))
	require.Zero(t, effectCount(t, db))
}

func TestDoLargeResponsesReplayResourceReference(t *testing.T) {
	m, db, key := storeHarness(t)
	body := json.RawMessage(`"` + strings.Repeat("x", 65536) + `"`)
	for attempt := 0; attempt < 2; attempt++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			result, replayed, err := m.Do(t.Context(), tx, key, func(*gorm.DB) (Result, error) {
				require.Zero(t, attempt)
				return Result{StatusCode: 201, Body: body, ResourceKind: "project", ResourceID: guid.ID(42)}, nil
			})
			require.NoError(t, err)
			require.Equal(t, attempt == 1, replayed)
			require.Equal(t, guid.ID(42), result.ResourceID)
			if replayed {
				require.Nil(t, result.Body)
			} else {
				require.Equal(t, body, result.Body)
			}
			return err
		}))
	}
}

func TestDoConcurrentDuplicateWaitsForCommittedResult(t *testing.T) {
	m, db, key := storeHarness(t)
	first := db.Begin()
	require.NoError(t, first.Error)
	t.Cleanup(func() { _ = first.Rollback().Error })
	_, _, err := m.Do(t.Context(), first, key, func(tx *gorm.DB) (Result, error) {
		return Result{StatusCode: 201, Body: json.RawMessage(`{"data":{"id":"9"}}`)}, tx.Exec("INSERT INTO business_effects VALUES (9)").Error
	})
	require.NoError(t, err)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(entered)
		done <- db.Transaction(func(tx *gorm.DB) error {
			result, replayed, err := m.Do(t.Context(), tx, key, func(*gorm.DB) (Result, error) { return Result{}, errors.New("duplicate executed business callback") })
			if err != nil {
				return err
			}
			if !replayed || result.StatusCode != http.StatusCreated {
				return errors.New("duplicate did not replay committed result")
			}
			return nil
		})
	}()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("duplicate completed before first transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, first.Commit().Error)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate failed to finish")
	}
	require.EqualValues(t, 1, keyCount(t, db))
	require.EqualValues(t, 1, effectCount(t, db))
}

func TestDoRejectsInProgressAndCorruptCompletedResults(t *testing.T) {
	m, db, key := storeHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.Do(t.Context(), tx, key, func(*gorm.DB) (Result, error) { return Result{StatusCode: 201, Body: json.RawMessage(`{}`)}, nil })
		return err
	}))
	require.NoError(t, db.Table(m.names.Keys.String()).Where("subject_id = ?", key.SubjectID).Update("state", "IN_PROGRESS").Error)
	for _, expected := range []error{ErrInProgress, ErrInvalidResult} {
		err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.Do(t.Context(), tx, key, func(*gorm.DB) (Result, error) { t.Fatal("must not execute"); return Result{}, nil })
			return err
		})
		require.ErrorIs(t, err, expected)
		require.NoError(t, db.Table(m.names.Keys.String()).Where("subject_id = ?", key.SubjectID).Updates(map[string]any{"state": "COMPLETED", "status_code": nil}).Error)
	}
}

type failedIDs struct {
	err error
	id  int64
}

func (s failedIDs) Next() (int64, error) { return s.id, s.err }
func TestDoPropagatesStorageFailuresWithoutCommittedEffects(t *testing.T) {
	for _, stage := range []string{"id", "zero_id", "create", "query", "update", "delete"} {
		t.Run(stage, func(t *testing.T) {
			m, db, key := storeHarness(t)
			failure := errors.New("storage failure")
			want := failure
			hook := func(db *gorm.DB) { db.AddError(failure) }
			switch stage {
			case "id":
				m.ids = failedIDs{err: failure}
			case "zero_id":
				m.ids = failedIDs{}
				want = ErrUnavailable
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:fail", hook))
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail", hook))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:fail", hook))
			case "delete":
				require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("test:fail", hook))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, _, err := m.Do(t.Context(), tx, key, func(work *gorm.DB) (Result, error) {
					status := 201
					if stage == "delete" {
						status = 500
					}
					return Result{StatusCode: status, Body: json.RawMessage(`{}`)}, work.Exec("INSERT INTO business_effects VALUES (1)").Error
				})
				return err
			})
			require.ErrorIs(t, err, want)
			if stage == "query" {
				require.NoError(t, db.Callback().Query().Remove("test:fail"))
			}
			require.Zero(t, keyCount(t, db))
			require.Zero(t, effectCount(t, db))
		})
	}
}
