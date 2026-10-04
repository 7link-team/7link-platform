package core

import (
	"strings"
	"sync"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// readTables records the table every read touches, so a test can say which
// statements a decision actually sent rather than how long it took.
type readTables struct {
	mu     sync.Mutex
	tables []string
}

func (r *readTables) record(table string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tables = append(r.tables, table)
}

func (r *readTables) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tables = r.tables[:0]
}

func (r *readTables) touched(fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, table := range r.tables {
		if strings.Contains(table, fragment) {
			return true
		}
	}
	return false
}

func (r *readTables) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tables)
}

func watchReads(t *testing.T, db *gorm.DB) *readTables {
	t.Helper()
	watched := &readTables{}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:watch_reads", func(tx *gorm.DB) { watched.record(tx.Statement.Table) }))
	return watched
}

// A permission the tenant's own grants allow is allowed whoever asks, so the
// platform tables cannot change the answer and must not be read. This is the
// hottest path in the service -- it runs once per request -- and the platform
// answer is four statements of its own.
func TestAnAlreadyGrantedPermissionDoesNotAskThePlatform(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenant, principal := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenant, principal, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenant, "Reader", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenant, role.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenant, role.ID, principal, nil)
	require.NoError(t, err)

	watched := watchReads(t, harness.db)

	allowed, err := harness.authorizer.CheckBulk(t.Context(), tenant, []string{"role_view"}, principal)
	require.NoError(t, err)
	assert.True(t, allowed["role_view"])
	granted := watched.count()
	assert.False(t, watched.touched("platform_administrators"),
		"the tenant's own grant already decided this")
	assert.False(t, watched.touched("iam_principals"),
		"nor the principal read that only the platform check needs")

	// The same request for something the tenant does not grant still has to ask:
	// a platform administrator is allowed through where a member is not, and that
	// answer is the only thing left that could allow it.
	watched.reset()
	allowed, err = harness.authorizer.CheckBulk(t.Context(), tenant, []string{"menu_view"}, principal)
	require.NoError(t, err)
	assert.False(t, allowed["menu_view"])
	assert.True(t, watched.touched("platform_administrators"),
		"an ungranted permission is not decided until the platform answer is in")
	assert.Greater(t, watched.count(), granted,
		"the saved statements are the ones the granted case skipped")
}

// Reaching the same verdicts as before is the whole requirement: the check moved,
// nothing about who may do what did.
func TestThePlatformShortcutChangesNoVerdict(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenant, member, administrator := guid.ID(1), guid.ID(11), guid.ID(12)
	harness.seedMember(t, tenant, member, MemberActive)
	// seedMember creates the tenant as well, so the second person joins the tenant
	// that already exists rather than seeding it twice.
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at)"+
			" VALUES (?, 'platform-root', 'Platform root', 'active', ?, ?)",
		administrator, repositoryTestTime, repositoryTestTime).Error)
	_, err := harness.service.PutTenantMember(
		t.Context(), tenant, administrator, "Platform root",
		"platform-root@example.test", 0, MemberActive,
	)
	require.NoError(t, err)
	role, err := harness.service.CreateRole(t.Context(), tenant, "Reader", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenant, role.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenant, role.ID, member, nil)
	require.NoError(t, err)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES (?,?)",
		administrator, repositoryTestTime).Error)

	for name, expectation := range map[string]struct {
		subject    guid.ID
		permission string
		allowed    bool
	}{
		"granted member":                  {member, "role_view", true},
		"member without the grant":        {member, "menu_view", false},
		"platform administrator":          {administrator, "menu_view", true},
		"platform administrator, granted": {administrator, "role_view", true},
		"unknown permission":              {member, "not_a_permission", false},
	} {
		t.Run(name, func(t *testing.T) {
			allowed, err := harness.authorizer.CheckBulk(
				t.Context(), tenant, []string{expectation.permission}, expectation.subject,
			)
			require.NoError(t, err)
			assert.Equal(t, expectation.allowed, allowed[expectation.permission])
		})
	}
}
