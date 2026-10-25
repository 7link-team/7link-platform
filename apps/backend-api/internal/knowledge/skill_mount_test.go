package knowledge

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSkillMountFollowPinArchiveAndFrozenContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	first := createTestSkill(t, m, db, "review-code")
	approveTestSkill(t, m, db, first.Version.ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.MountOn(knowledgeContext(t, 2), tx, 1, 7, []guid.ID{first.Skill.ID, first.Skill.ID})
	}))
	frozen, err := m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, err)
	require.Len(t, frozen, 1)
	require.Equal(t, 1, frozen[0].VersionNo)
	require.Equal(t, []string{"review", "go"}, frozen[0].Tags)
	var second SkillResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		second, e = m.addSkillVersionOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillContentInput{Content: skillDocument("review-code", "New approved instructions")})
		return e
	}))
	approveTestSkill(t, m, db, second.Version.ID)
	current, err := m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, err)
	require.Equal(t, second.Version.ID, current[0].VersionID)
	require.Equal(t, first.Version.Content, frozen[0].Content)
	require.Equal(t, first.Version.ContentHash, frozen[0].ContentHash)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID, &first.Version.ID)
		require.Equal(t, first.Version.ID, *v.ResolvedVersionID)
		return e
	}))
	current, err = m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, err)
	require.Equal(t, first.Version.ID, current[0].VersionID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.skillStateOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillStateInput{Version: 3, State: "ARCHIVED"})
		return e
	}))
	current, err = m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, err)
	require.Empty(t, current)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.agentSkillsOn(knowledgeContext(t, 2), tx, 1, 7, 0, 50)
		require.Len(t, page.Items, 1)
		require.True(t, page.Items[0].Archived)
		require.Nil(t, page.Items[0].ResolvedVersionID)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID, nil)
		return e
	}), ErrSkillNotMountable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.unmountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.unmountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID) }))
}
func TestSkillMountBatchAndPinValidationRollback(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	first := createTestSkill(t, m, db, "review-code")
	second := createTestSkill(t, m, db, "build-code")
	approveTestSkill(t, m, db, first.Version.ID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID, &second.Version.ID)
		return e
	}), ErrSkillNotMountable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, second.Skill.ID, &second.Version.ID)
		return e
	}), ErrSkillNotMountable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.skillStateOn(knowledgeContext(t, 2), tx, 1, second.Skill.ID, SkillStateInput{Version: 1, State: "ARCHIVED"})
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.MountOn(knowledgeContext(t, 2), tx, 1, 7, []guid.ID{first.Skill.ID, second.Skill.ID})
	}), ErrSkillNotMountable)
	var count int64
	require.NoError(t, query.BindAgentSkills(db, m.names).Count(&count).Error)
	require.Zero(t, count)
	ids := make([]guid.ID, 31)
	for i := range ids {
		ids[i] = guid.ID(i + 1)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.MountOn(knowledgeContext(t, 2), tx, 1, 7, ids) }), ErrSkillNotMountable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 99, first.Skill.ID, nil)
		return e
	}), ErrNotFound)
	// The cap must include existing mounts, not merely the submitted batch.
	live := 1
	for i := 0; i < 30; i++ {
		id := guid.ID(1000 + i)
		require.NoError(t, query.BindSkills(db, m.names).Create(&model.Skill{ID: id, TenantID: 1, SkillKey: "existing-" + id.String(), Title: "Existing", Description: "Existing skill description", State: "ACTIVE", Version: 1, CreatedBy: 2, CreatedAt: m.now(), UpdatedAt: m.now()}).Error)
		require.NoError(t, query.BindAgentSkills(db, m.names).Create(&model.AgentSkill{ID: guid.ID(2000 + i), TenantID: 1, AgentID: 70, SkillID: id, AttachedBy: 2, AttachedAt: m.now(), LiveSlot: &live}).Error)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, first.Skill.ID, nil)
		return e
	}), ErrSkillNotMountable)
}

func TestSkillMountStorageFailuresPreserveAttachmentAndResolvedContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	skill := createTestSkill(t, m, db, "verified-guidance")
	approveTestSkill(t, m, db, skill.Version.ID)
	failure := errors.New("transactional storage unavailable")
	rollback := errors.New("rollback diagnostic transaction")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("transaction-storage-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("transaction-storage-failure"))
	}
	exercise := func(run func(*gorm.DB) error) {
		calls, failAt = 0, 0
		install()
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if e := run(tx); e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, apply(), rollback)
		total := calls
		require.Positive(t, total)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "storage stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID, nil)
		return e
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID, nil)
		return e
	}))
	exercise(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID, &skill.Version.ID)
		return e
	})
	exercise(func(tx *gorm.DB) error { return m.unmountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID) })
	resolved, e := m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, e)
	require.Len(t, resolved, 1)
	require.Equal(t, skill.Version.Content, resolved[0].Content)
}

func TestResolvedSkillCatalogStorageFailuresAndOffsetNeverLoseFrozenContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	skills := []SkillResult{createTestSkill(t, m, db, "alpha-guidance"), createTestSkill(t, m, db, "beta-guidance")}
	for _, skill := range skills {
		approveTestSkill(t, m, db, skill.Version.ID)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.MountOn(knowledgeContext(t, 2), tx, 1, 7, []guid.ID{skills[0].Skill.ID, skills[1].Skill.ID})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.agentSkillsOn(knowledgeContext(t, 2), tx, 1, 7, 0, 1)
		require.True(t, page.HasMore)
		require.Len(t, page.Items, 1)
		require.Equal(t, 1, page.NextOffset)
		return e
	}))
	failure := errors.New("resolved catalog unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("resolved-catalog-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	out, e := m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.NoError(t, e)
	require.Len(t, out, 2)
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		out, e = m.ForAgent(knowledgeContext(t, 2), 1, 7)
		require.ErrorIs(t, e, failure, "catalog query %d", i)
		require.Empty(t, out)
	}
	require.NoError(t, db.Callback().Query().Remove("resolved-catalog-query"))
	m.runtime = nil
	_, e = m.ForAgent(knowledgeContext(t, 2), 1, 7)
	require.Error(t, e)
}

func TestSkillMountReadsCurrentPrincipalAndDirectoryAndRejectsInvalidDependency(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	skill := createTestSkill(t, m, db, "current-directory")
	approveTestSkill(t, m, db, skill.Version.ID)
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID, nil)
			return err
		})
	}
	directory, principal := m.agents.ByPrincipalOn, m.principals.LockActiveRow
	failure := errors.New("current agent directory unavailable")
	m.agents.ByPrincipalOn = nil
	require.ErrorIs(t, run(), ErrUnavailable)
	m.agents.ByPrincipalOn = directory
	m.principals.LockActiveRow = nil
	require.ErrorIs(t, run(), ErrUnavailable)
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		if id == 7 {
			return identity.Principal{}, false, failure
		}
		return principal(ctx, tx, id)
	}
	require.ErrorIs(t, run(), failure)
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		if id == 7 {
			return identity.Principal{}, false, nil
		}
		return principal(ctx, tx, id)
	}
	require.ErrorIs(t, run(), ErrNotFound)
	m.principals.LockActiveRow = principal
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, failure
	}
	require.ErrorIs(t, run(), failure)
	m.agents.ByPrincipalOn = directory
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.skillAgentOn(knowledgeContext(t, 2), tx, 1, 0, "agent_manage")
		return err
	}), ErrInvalid)
	require.NoError(t, run())
}
