package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkdirPathRespectsMachineOSAndRejectsTraversal(t *testing.T) {
	for _, tc := range []struct{ os, path, want string }{
		{"WINDOWS", `D:\work\repo`, "D:/work/repo"},
		{"WINDOWS", "D:/work/./repo/", "D:/work/repo"},
		{"WINDOWS", "D:/", "D:/"},
		{"LINUX", "/work/./repo/", "/work/repo"},
		{"MACOS", "/Users/user/repo", "/Users/user/repo"},
	} {
		t.Run(tc.os+tc.path, func(t *testing.T) {
			path, err := workdirPath(tc.path, tc.os)
			require.NoError(t, err)
			require.Equal(t, tc.want, path)
		})
	}
	for _, tc := range []struct{ os, path string }{
		{"WINDOWS", "relative"}, {"WINDOWS", "D:relative"}, {"WINDOWS", "/unix/root"}, {"WINDOWS", `D:\work\..\private`},
		{"LINUX", "relative"}, {"LINUX", `/work\repo`}, {"LINUX", "/work/../private"}, {"MACOS", " /work"}, {"MACOS", "/work\nrepo"},
	} {
		t.Run("invalid"+tc.os+tc.path, func(t *testing.T) { _, err := workdirPath(tc.path, tc.os); require.ErrorIs(t, err, ErrInvalidArgument) })
	}
}

func TestWorkdirConnectorIsOneShotAndRequired(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	port := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	require.ErrorIs(t, (*Module)(nil).bindRepository(port), ErrInvalidArgument)
	require.ErrorIs(t, m.bindRepository(nil), ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "D:/work"); return err }), ErrUnavailable)
	require.NoError(t, m.bindRepository(port))
	require.ErrorIs(t, m.bindRepository(port), ErrInvalidArgument)
}

func TestWorkdirRegistrationReplaysCanonicalPathAndRejectsMovesOrCollisions(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	m.repository = func(_ context.Context, _ *gorm.DB, tenant, project, repository guid.ID) error {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(10), project)
		require.Contains(t, []guid.ID{11, 12}, repository)
		return nil
	}
	var first MachineWorkdirView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, `D:\work\repo`)
		return err
	}))
	require.Equal(t, "D:/work/repo", first.Path)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "d:/WORK/./repo")
		require.Equal(t, first, replay)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "D:/moved"); return err }), ErrVersionConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 12, "D:/work/repo")
		return err
	}), ErrVersionConflict)
	var count int64
	require.NoError(t, db.Table(m.names.Workdirs.String()).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestWorkdirRegistrationUsesCaseInsensitiveMacOSPathLock(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("os", "MACOS").Error)
	var first MachineWorkdirView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "/Users/me/Repo")
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "/users/ME/repo/")
		require.Equal(t, first, replay)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 12, "/USERS/me/REPO")
		return err
	}), ErrVersionConflict)
}

func TestWorkdirRegistrationFailureNeverLeavesUncommittedWorkspace(t *testing.T) {
	for _, fault := range []string{"human_inactive", "policy_denied", "repository_denied", "revoked", "id_error", "id_zero", "audit", "outbox", "insert"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, publisher := enrollmentHarness(t)
			m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			failure := errors.New("test workspace failure")
			switch fault {
			case "human_inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "policy_denied":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "repository_denied":
				m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrMachineForbidden }
			case "revoked":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			case "id_error":
				m.ids = failedFleetIDs{err: failure}
			case "id_zero":
				m.ids = failedFleetIDs{}
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				publisher.fail = true
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_workdir BEFORE INSERT ON dir_fleet_workdirs BEGIN SELECT RAISE(ABORT, 'test workspace insert failure'); END").Error)
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "D:/work"); return err }))
			var count int64
			require.NoError(t, db.Table(m.names.Workdirs.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestWorkdirHTTPUsesCurrentRepositoryPermissionAndReturnsStableRegistration(t *testing.T) {
	m, db, _, _, _ := enrollmentHarness(t)
	m.repository = func(_ context.Context, _ *gorm.DB, tenant, project, repository guid.ID) error {
		if tenant != 7 || project != 10 || repository != 11 {
			return ErrMachineForbidden
		}
		return nil
	}
	router := fleetBusinessRouter(t, m)
	body := `{"project_id":"10","repository_id":"11","path":"D:/work"}`
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/workdirs", body, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var registered struct {
		Data MachineWorkdirView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &registered))
	require.Positive(t, int64(registered.Data.ID))
	require.Equal(t, "D:/work", registered.Data.Path)
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/workdirs", body, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var replay struct {
		Data MachineWorkdirView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &replay))
	require.Equal(t, registered, replay)
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/workdirs", `{"project_id":"10","repository_id":"12","path":"D:/private"}`, nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/workdirs", `{"project_id":"10","repository_id":"11","path":"../private"}`, nil)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	var count int64
	require.NoError(t, db.Table(m.names.Workdirs.String()).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
