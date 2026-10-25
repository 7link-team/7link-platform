package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestCodeReferencesProjectLocalBoundaries(t *testing.T) {
	refs := ParseCodeReferences("fix #WI-139 #WI-999 #R-12 #WI-139 x#WI-3 #WI-8x #WI-1234567890 #WI-0")
	require.Equal(t, []CodeReference{{"#WI-139", "WI", 139}, {"#WI-999", "WI", 999}, {"#R-12", "R", 12}}, refs)
}
func TestCodeLinkImmutableEvidenceReplayAndRollback(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	repo := model.ProjectRepository{TenantID: 1, ID: 555, ProjectID: 99, Name: "Mapped source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	input := CodeLinkInput{Kind: "COMMIT", RepositoryID: repo.ID, Ref: strings.Repeat("a", 40), Title: "manual reference"}
	create := func() (model.CodeLink, error) {
		var row model.CodeLink
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			row, e = m.createCodeLinkOn(ctx, tx, 1, 99, work.ID, 11, 11, input, "MANUAL_REFERENCE")
			return e
		})
		return row, err
	}
	row, err := create()
	require.NoError(t, err)
	require.Equal(t, "MANUAL_REFERENCE", row.Provenance)
	input.Title = "cannot rewrite snapshot"
	replay, err := create()
	require.NoError(t, err)
	require.Equal(t, row, replay)
	evidence, err := m.CodeLinkEvidenceOn(ctx, db, 1, 99, work.ID, row.ID, 11)
	require.NoError(t, err)
	require.False(t, evidence.Observed)
	require.Contains(t, evidence.Snapshot, "manual reference")
	input.Ref = strings.Repeat("b", 40)
	saved := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit offline") }
	_, err = create()
	require.Error(t, err)
	m.service.audit = saved
	var count int64
	require.NoError(t, query.BindCodeLinks(db, m.service.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
	input.RepositoryID = 999
	_, err = create()
	require.Error(t, err)
	require.NoError(t, query.BindCodeLinks(db, m.service.names).Where("id = ?", row.ID).Update("snapshot", "tampered").Error)
	_, err = m.CodeLinkEvidenceOn(ctx, db, 1, 99, work.ID, row.ID, 11)
	require.ErrorIs(t, err, ErrUnavailable)
}
func TestLocalCodeImportLinksKnownWIAndLogsUnknownWithoutForeignLookup(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	repo := model.ProjectRepository{TenantID: 1, ID: 556, ProjectID: 99, Name: "Mapped local source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", work.ID).Update("number", 139).Error)
	var result CodeLinkImport
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.importCodeObjectsOn(ctx, tx, 1, 99, 11, &repo, []LocalGitObject{{Kind: "COMMIT", Ref: strings.Repeat("c", 40), Title: "fix #WI-139 #WI-999"}})
		return err
	}))
	require.Len(t, result.Links, 1)
	require.Equal(t, work.ID, result.Links[0].TargetID)
	require.Equal(t, "LOCAL_GIT_OBSERVED", result.Links[0].Provenance)
	require.Len(t, result.Ignored, 1)
	require.Equal(t, "#WI-999", result.Ignored[0].Token)
	require.Equal(t, "UNKNOWN_REFERENCE", result.Ignored[0].Reason)
	evidence, err := m.CodeLinkEvidenceOn(ctx, db, 1, 99, work.ID, result.Links[0].ID, 11)
	require.NoError(t, err)
	require.True(t, evidence.Observed)
}
func TestLocalCodeImportRequirementReferencesAreDistinctScopedAndReadable(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var first, second RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		if err != nil {
			return err
		}
		second, err = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	repo := model.ProjectRepository{TenantID: 1, ID: 557, ProjectID: 99, Name: "Requirement source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	q := query.BindRequirements(db, m.service.names)
	require.NoError(t, q.WithContext(ctx).Where(q.ID.Eq(first.ID)).UnderlyingDB().Update("number", 139).Error)
	require.NoError(t, q.WithContext(ctx).Where(q.ID.Eq(second.ID)).UnderlyingDB().Update("number", 140).Error)
	var result CodeLinkImport
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.importCodeObjectsOn(ctx, tx, 1, 99, 11, &repo, []LocalGitObject{{Kind: "COMMIT", Ref: strings.Repeat("d", 40), Title: "fix #R-139 #R-140 #R-999"}})
		return err
	}))
	require.Len(t, result.Links, 2)
	require.Len(t, result.Ignored, 1)
	require.Equal(t, "#R-999", result.Ignored[0].Token)
	require.Equal(t, "requirement", result.Links[0].TargetKind)
	require.Equal(t, first.ID, result.Links[0].TargetID)
	require.Equal(t, second.ID, result.Links[1].TargetID)
	evidence, err := m.RequirementCodeLinkEvidenceOn(ctx, db, 1, 99, first.ID, result.Links[0].ID, 11)
	require.NoError(t, err)
	require.True(t, evidence.Observed)
	_, err = m.CodeLinkEvidenceOn(ctx, db, 1, 99, first.ID, result.Links[0].ID, 11)
	require.Error(t, err)
}

func TestLocalCodeImportFailuresCannotLeavePartialLinksOrUnknownReferenceScans(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	repo := model.ProjectRepository{TenantID: 1, ID: 556, ProjectID: 99, Name: "Local source", RemoteURL: "https://example.invalid/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("number", 139).Error)
	objects := []LocalGitObject{{Kind: "COMMIT", Ref: strings.Repeat("a", 40), Title: "fix #WI-139 #WI-999 #R-999"}}
	failure := errors.New("local import storage unavailable")
	rollback := errors.New("rollback successful import probe")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("local-import-source", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("local-import-source", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.importCodeObjectsOn(ctx, tx, 1, 99, 11, &repo, objects)
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 4)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "import storage stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("local-import-source"))
	require.NoError(t, db.Callback().Create().Remove("local-import-source"))
	var count int64
	for _, table := range []string{m.service.names.CodeLinks.String(), m.service.names.CodeLinkScans.String()} {
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
	m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
	require.ErrorIs(t, run(), failure)
}
