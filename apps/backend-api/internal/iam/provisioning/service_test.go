package provisioning

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var provisioningTestNow = time.Date(2026, 9, 3, 12, 0, 0, 123_000_000, time.UTC)

func TestBoundSCIMActorRevalidationAndAudit(t *testing.T) {
	env := newProvisioningEnvironment(t)
	s := env.service
	actor := testID("automation-actor")
	active := true
	var validationError error
	s.actors = func(ctx context.Context, db *gorm.DB, tenantID, principalID guid.ID, now time.Time) (bool, error) {
		assert.Equal(t, env.auth.TenantID, tenantID)
		assert.Equal(t, actor, principalID)
		assert.Equal(t, provisioningTestNow, now)
		require.NotNil(t, db)
		return active, validationError
	}
	var events []audit.DomainEvent
	appender := s.appender
	s.appender = func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		events = append(events, event)
		return appender(ctx, db, event)
	}
	directory, err := s.ReplaceDirectory(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "Primary IdP", DirectoryActive, 1, &actor)
	require.NoError(t, err)
	assert.Equal(t, int64(2), directory.Version)
	auth, err := s.Authenticate(t.Context(), "Bearer "+env.token)
	require.NoError(t, err)
	assert.Equal(t, actor, auth.PrincipalID)
	user, err := s.CreateUser(t.Context(), auth, activeUserInput("bound-user", "bound-user", "bound@example.test"))
	require.NoError(t, err)
	assert.Equal(t, actor, events[len(events)-1].PrincipalID)
	provider, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-bound-user"}`)
	target, err := s.CreateTarget(t.Context(), auth.TenantID, "Bound target", server.URL, "token", &actor)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	_, err = s.PushResource(t.Context(), auth.TenantID, targetID, ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	assert.Equal(t, actor, events[len(events)-1].PrincipalID)
	assert.Equal(t, actor, events[len(events)-1].Detail["service_account_id"])
	require.NoError(t, s.DeprovisionResource(t.Context(), auth.TenantID, targetID, ResourceUser, parseGUID(user.ID)))
	assert.Equal(t, actor, events[len(events)-1].PrincipalID)
	assert.Equal(t, actor, events[len(events)-1].Detail["service_account_id"])
	active = false
	before := provider.count()
	_, err = s.Authenticate(t.Context(), "Bearer "+env.token)
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = s.GetUser(t.Context(), auth, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = s.PushResource(t.Context(), auth.TenantID, targetID, ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.ErrorIs(t, s.DeprovisionResource(t.Context(), auth.TenantID, targetID, ResourceUser, parseGUID(user.ID)), ErrUnauthorized)
	_, err = s.SyncTarget(t.Context(), auth.TenantID, targetID)
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.Equal(t, before, provider.count())
	_, err = s.CreateDirectory(t.Context(), auth.TenantID, "Inactive actor", &actor)
	assert.ErrorIs(t, err, ErrInvalidDirectory)
	_, err = s.CreateTarget(t.Context(), auth.TenantID, "Inactive actor", server.URL, "token", &actor)
	assert.ErrorIs(t, err, ErrInvalidTarget)
	validationError = errors.New("actor lookup unavailable")
	assert.ErrorIs(t, s.requireServiceAccount(t.Context(), s.repo, auth.TenantID, &actor), validationError)
	s.actors = nil
	assert.ErrorIs(t, s.requireServiceAccount(t.Context(), s.repo, auth.TenantID, &actor), errRepositoryUnavailable)
	invalid := guid.ID(0)
	assert.ErrorIs(t, s.requireServiceAccount(t.Context(), s.repo, auth.TenantID, &invalid), ErrUnauthorized)
}

func TestSCIMCredentialRejectsInactiveTenant(t *testing.T) {
	for _, prefix := range []string{"", "scim_gate_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			env := newProvisioningEnvironmentWithPrefix(t, prefix)
			for _, status := range []string{"suspended", "deleted"} {
				require.NoError(t, env.db.Table(prefix+"iam_tenants").Where("id = ?", env.auth.TenantID).Update("status", status).Error)
				auth, err := env.service.Authenticate(t.Context(), "Bearer "+env.token)
				assert.ErrorIs(t, err, ErrUnauthorized, status)
				assert.Equal(t, AuthContext{}, auth)
			}
			require.NoError(t, env.db.Table(prefix+"iam_tenants").Where("id = ?", env.auth.TenantID).Update("status", "active").Error)
			auth, err := env.service.Authenticate(t.Context(), "Bearer "+env.token)
			require.NoError(t, err)
			assert.Equal(t, env.auth, auth)
		})
	}
}

type provisioningTestIDs struct{ next atomic.Int64 }

func newProvisioningTestIDs(start int64) *provisioningTestIDs {
	ids := &provisioningTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *provisioningTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type fixedProvisioningID struct {
	id  int64
	err error
}

func (source fixedProvisioningID) Next() (int64, error) { return source.id, source.err }

func testID(value string) guid.ID {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	id, err := guid.Parse(strconv.FormatUint(hash.Sum64()>>1, 10))
	if err != nil {
		panic(err)
	}
	return id
}

func wireID(value string) string { return testID(value).String() }

func parseGUID(value string) guid.ID {
	id, _ := guid.Parse(value)
	return id
}

type testTenant struct {
	ID     guid.ID
	Status string
}

type testPrincipal struct {
	ID          guid.ID
	LoginName   string `gorm:"size:128"`
	Email       string
	DisplayName string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type testMember struct {
	TenantID    guid.ID
	PrincipalID guid.ID
	DisplayName string
	Email       string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type testGroup struct {
	TenantID  guid.ID
	ID        guid.ID
	Name      string
	NameKey   string `gorm:"size:128"`
	Type      string
	Status    string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type testGroupMember struct {
	TenantID    guid.ID
	GroupID     guid.ID
	PrincipalID guid.ID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type testAudit struct {
	ID         uint
	TenantID   guid.ID
	EventType  string
	TargetType string
	TargetID   guid.ID
}

type provisioningIdentity struct {
	db     *gorm.DB
	ids    guid.Source
	prefix string
}

func (p *provisioningIdentity) CreateProvisionedTo(ctx context.Context, db *gorm.DB, tenantID guid.ID, input identity.ProvisionedPrincipalInput) (identity.Principal, error) {
	id, err := p.ids.Next()
	if err != nil {
		return identity.Principal{}, err
	}
	now := provisioningTestNow
	row := testPrincipal{ID: guid.ID(id), LoginName: input.LoginName, Email: input.Email, DisplayName: input.DisplayName, Status: activeStatus(input.Active), CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(p.prefix + "iam_principals").Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return identity.Principal{}, identity.ErrLoginConflict
		}
		return identity.Principal{}, err
	}
	member := testMember{TenantID: tenantID, PrincipalID: row.ID, DisplayName: input.DisplayName, Email: input.Email, Status: activeStatus(input.Active), CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(p.prefix + "iam_tenant_members").Create(&member).Error; err != nil {
		return identity.Principal{}, err
	}
	return identityFromTest(row), nil
}

func (p *provisioningIdentity) ReplaceProvisionedTo(ctx context.Context, db *gorm.DB, tenantID, id guid.ID, input identity.ProvisionedPrincipalInput) (identity.Principal, error) {
	var count int64
	if err := db.WithContext(ctx).Table(p.prefix+"iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", tenantID, id).Count(&count).Error; err != nil {
		return identity.Principal{}, err
	}
	if count != 1 {
		return identity.Principal{}, identity.ErrNotFound
	}
	now := provisioningTestNow
	result := db.WithContext(ctx).Table(p.prefix+"iam_principals").Where("id = ?", id).Updates(map[string]any{
		"login_name": input.LoginName, "email": input.Email, "display_name": input.DisplayName, "status": activeStatus(input.Active), "updated_at": now,
	})
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return identity.Principal{}, identity.ErrLoginConflict
	}
	if result.Error != nil {
		return identity.Principal{}, result.Error
	}
	if err := db.WithContext(ctx).Table(p.prefix+"iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", tenantID, id).
		Updates(map[string]any{"display_name": input.DisplayName, "email": input.Email, "status": activeStatus(input.Active), "updated_at": now}).Error; err != nil {
		return identity.Principal{}, err
	}
	return p.get(ctx, db, tenantID, id)
}

func (p *provisioningIdentity) Get(ctx context.Context, tenantID, id guid.ID) (identity.Principal, error) {
	return p.get(ctx, p.db, tenantID, id)
}

func (p *provisioningIdentity) EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error) {
	return 0, false, errors.New("not used by provisioning")
}

func (p *provisioningIdentity) get(ctx context.Context, db *gorm.DB, tenantID, id guid.ID) (identity.Principal, error) {
	var row testPrincipal
	err := db.WithContext(ctx).Table(p.prefix+"iam_principals AS principal").
		Joins("JOIN "+p.prefix+"iam_tenant_members AS member ON member.principal_id = principal.id AND member.tenant_id = ?", tenantID).
		Where("principal.id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return identity.Principal{}, identity.ErrNotFound
	}
	return identityFromTest(row), err
}

func identityFromTest(row testPrincipal) identity.Principal {
	return identity.Principal{ID: row.ID, LoginName: row.LoginName, Email: row.Email, DisplayName: row.DisplayName, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

type provisioningGroups struct {
	db     *gorm.DB
	ids    guid.Source
	prefix string
}

func (p *provisioningGroups) CreateProvisionedTo(ctx context.Context, db *gorm.DB, tenantID guid.ID, input organization.ProvisionedGroupInput) (organization.Group, error) {
	id, err := p.ids.Next()
	if err != nil {
		return organization.Group{}, err
	}
	now := provisioningTestNow
	row := testGroup{TenantID: tenantID, ID: guid.ID(id), Name: input.DisplayName, NameKey: strings.ToLower(input.DisplayName), Type: "static", Status: activeStatus(input.Active), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(p.prefix + "iam_groups").Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return organization.Group{}, organization.ErrGroupNameConflict
		}
		return organization.Group{}, err
	}
	if err := p.replaceMembers(ctx, db, tenantID, row.ID, input.MemberIDs); err != nil {
		return organization.Group{}, err
	}
	return groupFromTest(row), nil
}

func (p *provisioningGroups) ReplaceProvisionedTo(ctx context.Context, db *gorm.DB, tenantID, id guid.ID, input organization.ProvisionedGroupInput) (organization.Group, error) {
	row, err := p.find(ctx, db, tenantID, id)
	if err != nil {
		return organization.Group{}, err
	}
	row.Name, row.NameKey, row.Status, row.Version, row.UpdatedAt = input.DisplayName, strings.ToLower(input.DisplayName), activeStatus(input.Active), row.Version+1, provisioningTestNow
	if err := db.WithContext(ctx).Table(p.prefix+"iam_groups").Where("tenant_id = ? AND id = ?", tenantID, id).Updates(&row).Error; err != nil {
		return organization.Group{}, err
	}
	if err := p.replaceMembers(ctx, db, tenantID, id, input.MemberIDs); err != nil {
		return organization.Group{}, err
	}
	return groupFromTest(row), nil
}

func (p *provisioningGroups) DisableProvisionedTo(ctx context.Context, db *gorm.DB, tenantID, id guid.ID) (organization.Group, error) {
	row, err := p.find(ctx, db, tenantID, id)
	if err != nil {
		return organization.Group{}, err
	}
	row.Status, row.Version, row.UpdatedAt = "disabled", row.Version+1, provisioningTestNow
	err = db.WithContext(ctx).Table(p.prefix+"iam_groups").Where("tenant_id = ? AND id = ?", tenantID, id).Updates(&row).Error
	return groupFromTest(row), err
}

func (p *provisioningGroups) Get(ctx context.Context, tenantID, id guid.ID) (organization.Group, error) {
	row, err := p.find(ctx, p.db, tenantID, id)
	return groupFromTest(row), err
}

func (p *provisioningGroups) ListMembers(ctx context.Context, tenantID, id guid.ID) ([]organization.GroupMember, error) {
	var rows []testGroupMember
	err := p.db.WithContext(ctx).Table(p.prefix+"iam_group_members").Where("tenant_id = ? AND group_id = ?", tenantID, id).Order("principal_id").Find(&rows).Error
	result := make([]organization.GroupMember, 0, len(rows))
	for _, row := range rows {
		result = append(result, organization.GroupMember{GroupID: row.GroupID, PrincipalID: row.PrincipalID})
	}
	return result, err
}

func (p *provisioningGroups) find(ctx context.Context, db *gorm.DB, tenantID, id guid.ID) (testGroup, error) {
	var row testGroup
	err := db.WithContext(ctx).Table(p.prefix+"iam_groups").Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, organization.ErrGroupNotFound
	}
	return row, err
}

func (p *provisioningGroups) replaceMembers(ctx context.Context, db *gorm.DB, tenantID, groupID guid.ID, ids []guid.ID) error {
	if err := db.WithContext(ctx).Table(p.prefix+"iam_group_members").Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Delete(&testGroupMember{}).Error; err != nil {
		return err
	}
	for _, id := range ids {
		row := testGroupMember{TenantID: tenantID, GroupID: groupID, PrincipalID: id, CreatedAt: provisioningTestNow, UpdatedAt: provisioningTestNow}
		if err := db.WithContext(ctx).Table(p.prefix + "iam_group_members").Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func groupFromTest(row testGroup) organization.Group {
	return organization.Group{ID: row.ID, TenantID: row.TenantID, Name: row.Name, Type: row.Type, Status: row.Status, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func activeStatus(active bool) string {
	if active {
		return "active"
	}
	return "disabled"
}

type provisioningEnvironment struct {
	db         *gorm.DB
	runtime    *appenv.Runtime
	service    *Service
	keys       *keyringx.Keyring
	auth       AuthContext
	token      string
	prefix     string
	auditMu    sync.Mutex
	auditErr   error
	datasource gormx.Datasource
}

func newProvisioningEnvironment(t *testing.T) *provisioningEnvironment {
	t.Helper()
	return newProvisioningEnvironmentWithPrefix(t, "")
}

func newProvisioningEnvironmentWithPrefix(t *testing.T, prefix string) *provisioningEnvironment {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on", filepath.ToSlash(filepath.Join(t.TempDir(), "provisioning.sqlite")))
	return newProvisioningEnvironmentWithDatasource(t, prefix, gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true, Readable: true})
}

func newProvisioningEnvironmentWithDatasource(t *testing.T, prefix string, datasource gormx.Datasource) *provisioningEnvironment {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": datasource,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("provisioning-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	env := &provisioningEnvironment{db: router.Write(), runtime: runtime, prefix: prefix, datasource: datasource}
	if datasource.Type != "sqlite" {
		t.Cleanup(func() {
			for _, table := range []string{"iam_group_members", "iam_groups", "iam_tenant_members", "iam_principals", "iam_audit_events", "iam_scim_target_resources", "iam_scim_targets", "iam_scim_resources", "iam_scim_credentials", "iam_scim_directories", "iam_tenants"} {
				require.NoError(t, env.db.Migrator().DropTable(prefix+table))
			}
		})
	}
	env.migrate(t)
	require.NoError(t, env.db.Table(prefix+"iam_tenants").Create([]testTenant{{ID: testID("tenant-a"), Status: "active"}, {ID: testID("tenant-b"), Status: "active"}}).Error)
	ids := newProvisioningTestIDs(1000)
	identities := &provisioningIdentity{db: env.db, ids: ids, prefix: prefix}
	groups := &provisioningGroups{db: env.db, ids: ids, prefix: prefix}
	appender := audit.Appender(func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		env.auditMu.Lock()
		defer env.auditMu.Unlock()
		if env.auditErr != nil {
			return env.auditErr
		}
		row := testAudit{TenantID: event.TenantID, EventType: event.EventType, TargetType: event.TargetType, TargetID: event.TargetID}
		return db.WithContext(ctx).Table(prefix + "iam_audit_events").Create(&row).Error
	})
	env.keys = newProvisioningTestKeyring(t, testProvisioningKey())
	actorID := testID("scim-actor")
	actors := identity.ServiceAccountValidator(func(_ context.Context, _ *gorm.DB, tenant, actor guid.ID, _ time.Time) (bool, error) {
		return tenant == testID("tenant-a") && actor == actorID, nil
	})
	env.service = NewService(runtime, appender, ids, identities, groups, actors, provisioningProfileChecker{}, Config{}, env.keys, testOutboundPolicy())
	bindForeignTables(t, env.service.repo, prefix)
	env.service.now = func() time.Time { return provisioningTestNow }
	directory, err := env.service.CreateDirectory(t.Context(), testID("tenant-a"), "Primary IdP", &actorID)
	require.NoError(t, err)
	credential, err := env.service.CreateCredential(t.Context(), testID("tenant-a"), directory.ID, "acceptance", nil)
	require.NoError(t, err)
	env.auth, err = env.service.Authenticate(t.Context(), "Bearer "+credential.Token)
	require.NoError(t, err)
	env.token = credential.Token
	return env
}

func (env *provisioningEnvironment) migrate(t *testing.T) {
	t.Helper()
	for _, item := range []struct {
		name string
		row  any
	}{
		{model.Directory{}.TableName(), &model.Directory{}}, {model.Credential{}.TableName(), &model.Credential{}},
		{model.Resource{}.TableName(), &model.Resource{}}, {model.Target{}.TableName(), &model.Target{}},
		{model.TargetResource{}.TableName(), &model.TargetResource{}}, {"iam_tenants", &testTenant{}},
		{"iam_principals", &testPrincipal{}}, {"iam_tenant_members", &testMember{}}, {"iam_groups", &testGroup{}},
		{"iam_group_members", &testGroupMember{}}, {"iam_audit_events", &testAudit{}},
	} {
		require.NoError(t, env.db.Table(env.prefix+item.name).AutoMigrate(item.row), item.name)
	}
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefixName(env.prefix, "test_principal_login")+" ON "+env.prefix+"iam_principals (login_name)").Error)
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefixName(env.prefix, "test_group_name")+" ON "+env.prefix+"iam_groups (tenant_id, name_key)").Error)
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefixName(env.prefix, "test_directory_name")+" ON "+env.prefix+"iam_scim_directories (tenant_id, name_key)").Error)
	require.NoError(t, env.db.Exec("CREATE UNIQUE INDEX "+prefixName(env.prefix, "test_target_name")+" ON "+env.prefix+"iam_scim_targets (tenant_id, name_key)").Error)
}

func prefixName(prefix, name string) string { return prefix + name }

func testProvisioningKey() []byte { return []byte("0123456789abcdef0123456789abcdef") }

// newProvisioningTestKeyring wraps raw key material the way the module does, so
// a test that seals through the ring and a test that reads a stored row are
// talking about the same key.
func newProvisioningTestKeyring(t *testing.T, active []byte, previous ...[]byte) *keyringx.Keyring {
	t.Helper()
	encoded := make([]string, 0, len(previous))
	for _, key := range previous {
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(key))
	}
	ring, err := keyringx.New(provisioningKeyDomain, base64.RawStdEncoding.EncodeToString(active), encoded...)
	require.NoError(t, err)
	return ring
}

func activeUserInput(externalID, userName, email string) UserInput {
	active := true
	return UserInput{Schemas: []string{UserSchema}, ExternalID: externalID, UserName: userName, DisplayName: "Display " + userName, Active: &active, Emails: []UserEmail{{Value: email, Type: "work", Primary: true}}}
}

func TestDirectoryCredentialAndResourceLifecycle(t *testing.T) {
	env := newProvisioningEnvironmentWithPrefix(t, "sl_")
	directories, err := env.service.ListDirectories(t.Context(), env.auth.TenantID)
	require.NoError(t, err)
	require.Len(t, directories, 1)
	_, err = env.service.ListDirectories(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidDirectory)
	_, err = env.service.ListCredentials(t.Context(), testID("tenant-b"), env.auth.DirectoryID)
	assert.ErrorIs(t, err, ErrDirectoryMissing)

	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("alice-external", "Alice", "alice@example.test"))
	require.NoError(t, err)
	assert.Equal(t, "alice", user.UserName)
	assert.Equal(t, weakETag(1), user.Meta.Version)
	group, err := env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, ExternalID: "team-external", DisplayName: "Team", Members: []SCIMGroupMember{{Value: user.ID}}})
	require.NoError(t, err)
	require.Len(t, group.Members, 1)

	request, err := normalizeListRequest("userName sw \"ali\" and active eq true", 1, 20)
	require.NoError(t, err)
	users, err := env.service.ListUsers(t.Context(), env.auth, request)
	require.NoError(t, err)
	require.Len(t, users.Resources, 1)
	groupRequest, err := normalizeListRequest("members[value eq \""+user.ID+"\"]", 1, 20)
	require.NoError(t, err)
	groups, err := env.service.ListGroups(t.Context(), env.auth, groupRequest)
	require.NoError(t, err)
	require.Len(t, groups.Resources, 1)

	user, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{{Op: "replace", Path: "displayName", Value: []byte("\"Alice Updated\"")}}}, 1)
	require.NoError(t, err)
	assert.Equal(t, weakETag(2), user.Meta.Version)
	_, err = env.service.ReplaceUser(t.Context(), env.auth, parseGUID(user.ID), activeUserInput("alice-external", "alice", "alice@example.test"), 1)
	assert.ErrorIs(t, err, ErrResourceVersion)
	require.NoError(t, env.service.DeleteGroup(t.Context(), env.auth, parseGUID(group.ID), 1))
	require.NoError(t, env.service.DeleteUser(t.Context(), env.auth, parseGUID(user.ID), 2))
	_, err = env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrResourceMissing)
}

func TestAuditFailureRollsBackDirectoryAndResource(t *testing.T) {
	env := newProvisioningEnvironment(t)
	env.auditErr = errors.New("audit unavailable")
	_, err := env.service.CreateDirectory(t.Context(), testID("tenant-a"), "Rolled back", nil)
	assert.ErrorContains(t, err, "audit unavailable")
	var count int64
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_directories").Where("name = ?", "Rolled back").Count(&count).Error)
	assert.Zero(t, count)
	_, err = env.service.CreateUser(t.Context(), env.auth, activeUserInput("rollback", "rollback", "rollback@example.test"))
	assert.ErrorContains(t, err, "audit unavailable")
	require.NoError(t, env.db.Table(env.prefix+"iam_principals").Where("login_name = ?", "rollback").Count(&count).Error)
	assert.Zero(t, count)
}

func TestProvisioningIDFailuresAreRejected(t *testing.T) {
	env := newProvisioningEnvironment(t)
	original := env.service.ids
	env.service.ids = fixedProvisioningID{err: errors.New("id unavailable")}
	_, err := env.service.CreateDirectory(t.Context(), testID("tenant-a"), "ID failure", nil)
	assert.ErrorContains(t, err, "id unavailable")
	env.service.ids = fixedProvisioningID{id: 0}
	_, err = env.service.CreateDirectory(t.Context(), testID("tenant-a"), "Zero ID", nil)
	assert.ErrorContains(t, err, "must be positive")
	env.service.ids = original
	assert.Equal(t, assert.AnError, firstError(assert.AnError, ErrInvalidTarget))
	assert.Equal(t, ErrInvalidTarget, firstError(nil, ErrInvalidTarget))
}

// testOutboundPolicy permits the loopback address the fake upstream in these
// tests listens on. The shipped default refuses it, which is the point of the
// guard; allowing it here is the same thing a deployment does for a provider on
// its own network, and the guard itself is covered in pkg/extension/httpx.
func testOutboundPolicy() httpx.Policy {
	return httpx.Policy{
		AllowPlaintext: true,
		Allow:          []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")},
	}
}

// TestProvisioningOutboundPolicyRefusesMalformedNetworks covers the startup
// path: a typo in the allow list stops the process instead of silently
// changing what outbound SCIM can reach.
func TestProvisioningOutboundPolicyRefusesMalformedNetworks(t *testing.T) {
	_, err := ResolveOutboundPolicy(Config{OutboundAllowNetworks: []string{"192.168.0.0/16", "10.0.0.0/999"}})
	assert.ErrorContains(t, err, "provisioning outbound policy")

	strict, err := ResolveOutboundPolicy(Config{})
	require.NoError(t, err)
	assert.Equal(t, httpx.Policy{}, strict, "an unconfigured deployment gets the strict policy")
}
