package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func skillDocument(name, body string) string {
	return "---\nname: " + name + "\ndescription: Review concrete evidence carefully\ntags: [review, GO]\n---\n" + body
}
func createTestSkill(t *testing.T, m *Module, db *gorm.DB, key string) SkillResult {
	t.Helper()
	var result SkillResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.createSkillOn(knowledgeContext(t, 2), tx, 1, SkillContentInput{Content: skillDocument(key, "Original instructions")})
		return err
	}))
	return result
}
func approveTestSkill(t *testing.T, m *Module, db *gorm.DB, id guid.ID) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewSkillVersionOn(knowledgeContext(t, 2), tx, 1, id, SkillReviewInput{Decision: "APPROVED", Reason: "Reviewed actual skill"})
		return err
	}))
}
func TestSkillVersionLifecycleRequiresHumanReviewAndImmutableName(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	first := createTestSkill(t, m, db, "review-code")
	require.Equal(t, "DRAFT", first.Version.State)
	require.Nil(t, first.Skill.CurrentVersionID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewSkillVersionOn(knowledgeContext(t, 7), tx, 1, first.Version.ID, SkillReviewInput{Decision: "APPROVED"})
		return err
	}), ErrForbidden)
	approveTestSkill(t, m, db, first.Version.ID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.addSkillVersionOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillContentInput{Content: skillDocument("other-key", "changed")})
		return err
	}), ErrSkillFrontmatter)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.addSkillVersionOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillContentInput{Content: first.Version.Content})
		return err
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createSkillOn(knowledgeContext(t, 2), tx, 1, SkillContentInput{Content: first.Version.Content})
		return err
	}), ErrConflict)
	var next SkillResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		next, err = m.addSkillVersionOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillContentInput{Content: skillDocument("review-code", "New instructions")})
		return err
	}))
	require.Equal(t, 2, next.Version.VersionNo)
	require.Equal(t, "DRAFT", next.Version.State)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewSkillVersionOn(knowledgeContext(t, 2), tx, 1, next.Version.ID, SkillReviewInput{Decision: "REJECTED", Reason: "Not suitable"})
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewSkillVersionOn(knowledgeContext(t, 2), tx, 1, next.Version.ID, SkillReviewInput{Decision: "APPROVED"})
		return err
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		s, _, err := m.skillOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, "knowledge_read", false)
		require.Equal(t, first.Version.ID, *s.CurrentVersionID)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.skillsOn(knowledgeContext(t, 2), tx, 1, skillListInput{Search: "review", Limit: 1})
		require.Len(t, page.Items, 1)
		require.False(t, page.HasMore)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.skillVersionsOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, 0, 1, "")
		require.Len(t, page.Items, 1)
		require.True(t, page.HasMore)
		require.Empty(t, page.Items[0].Content)
		require.Equal(t, 2, page.Items[0].VersionNo)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.skillVersionsOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, 1, 1, "")
		require.Len(t, page.Items, 1)
		require.Equal(t, 1, page.Items[0].VersionNo)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.skillOn(knowledgeContext(t, 2), tx, 99, first.Skill.ID, "knowledge_read", false)
		return err
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.skillStateOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillStateInput{Version: 2, State: "ARCHIVED"})
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.skillStateOn(knowledgeContext(t, 2), tx, 1, first.Skill.ID, SkillStateInput{Version: 2, State: "ACTIVE"})
		return err
	}), ErrConflict)
	var auditCount int64
	require.NoError(t, db.Table("audit_probe").Where("event_type LIKE ?", "%skill%").Count(&auditCount).Error)
	require.GreaterOrEqual(t, auditCount, int64(5))
	var eventCount int64
	require.NoError(t, db.Table("k_domain_events").Where("event_type = ?", "skill_version.approved").Count(&eventCount).Error)
	require.Equal(t, int64(1), eventCount)
}

func TestSkillVersionStorageFailuresCannotPartiallyPublishReviewOrArchive(t *testing.T) {
	for _, operation := range []string{"version", "review", "archive"} {
		t.Run(operation, func(t *testing.T) {
			m, db, _ := knowledgeHarness(t)
			first := createTestSkill(t, m, db, "review-storage")
			ctx := knowledgeContext(t, 2)
			q := query.BindSkills(db, m.names)
			var before model.Skill
			require.NoError(t, q.WithContext(ctx).Where("id=?", first.Skill.ID).First(&before).Error)
			v := query.BindSkillVersions(db, m.names)
			var versions []model.SkillVersion
			require.NoError(t, v.WithContext(ctx).Find(&versions).Error)
			failure := errors.New("skill lifecycle storage unavailable")
			rollback := errors.New("rollback skill lifecycle")
			calls, failAt := 0, 0
			probe := func(tx *gorm.DB) {
				calls++
				if calls == failAt {
					tx.AddError(failure)
				}
			}
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("skill-lifecycle-storage", probe))
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("skill-lifecycle-storage", probe))
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("skill-lifecycle-storage", probe))
			run := func() error {
				return db.Transaction(func(tx *gorm.DB) error {
					var e error
					switch operation {
					case "version":
						_, e = m.addSkillVersionOn(ctx, tx, 1, first.Skill.ID, SkillContentInput{Content: skillDocument("review-storage", "Revised current instructions")})
					case "review":
						_, e = m.reviewSkillVersionOn(ctx, tx, 1, first.Version.ID, SkillReviewInput{Decision: "APPROVED", Reason: "Reviewed immutable current instructions"})
					case "archive":
						_, e = m.skillStateOn(ctx, tx, 1, first.Skill.ID, SkillStateInput{Version: first.Skill.Version, State: "ARCHIVED"})
					}
					if e != nil {
						return e
					}
					return rollback
				})
			}
			require.ErrorIs(t, run(), rollback)
			total := calls
			require.Greater(t, total, 2)
			for i := 1; i <= total; i++ {
				calls, failAt = 0, i
				require.ErrorIs(t, run(), failure, "%s stage %d", operation, i)
			}
			require.NoError(t, db.Callback().Query().Remove("skill-lifecycle-storage"))
			require.NoError(t, db.Callback().Create().Remove("skill-lifecycle-storage"))
			require.NoError(t, db.Callback().Update().Remove("skill-lifecycle-storage"))
			var after model.Skill
			require.NoError(t, q.WithContext(ctx).Where("id=?", first.Skill.ID).First(&after).Error)
			require.Equal(t, before, after)
			var afterVersions []model.SkillVersion
			require.NoError(t, v.WithContext(ctx).Find(&afterVersions).Error)
			require.Equal(t, versions, afterVersions)
		})
	}
}

func TestSkillIDAllocationFailureRollsBackParentAndDraftVersion(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	owner := m.ids
	failure := errors.New("skill id allocation unavailable")
	input := SkillContentInput{Content: skillDocument("allocation-test", "Use reviewed source evidence.")}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.createSkillOn(knowledgeContext(t, 2), tx, 1, input); return err })
	}
	m.ids = nil
	require.ErrorIs(t, run(), ErrUnavailable)
	m.ids = knowledgeIDs(func() (int64, error) { return 0, nil })
	require.ErrorIs(t, run(), ErrUnavailable)
	calls := 0
	m.ids = knowledgeIDs(func() (int64, error) {
		calls++
		if calls == 2 {
			return 0, failure
		}
		return owner.Next()
	})
	require.ErrorIs(t, run(), failure)
	var count int64
	require.NoError(t, query.BindSkills(db, m.names).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, query.BindSkillVersions(db, m.names).Count(&count).Error)
	require.Zero(t, count)
}
