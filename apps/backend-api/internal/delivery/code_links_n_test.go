package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRequirementCodeReferencesRejectUnknownTargetsAndCurrentPolicyFailures(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var target RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		target, err = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	valid := CodeLinkInput{Kind: "BRANCH", RepositoryID: 887, Ref: "feature/reviewed"}
	policy := m.workPolicy
	for _, scenario := range []string{"invalid_reference", "fabricated_provenance", "missing_target", "policy_outage", "malformed_scope", "invisible_owner", "repository_outage"} {
		t.Run(scenario, func(t *testing.T) {
			m.workPolicy = policy
			input, provenance, id := valid, "MANUAL_REFERENCE", target.ID
			failure := errors.New("requirement code source unavailable")
			switch scenario {
			case "invalid_reference":
				input.URL = "https://user:password@example.test/source"
			case "fabricated_provenance":
				provenance = "VERIFIED_DEPLOYED"
			case "missing_target":
				id = 999999
			case "policy_outage", "malformed_scope", "invisible_owner":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if scenario == "policy_outage" {
						return authz.DataConstraint{}, failure
					}
					if scenario == "malformed_scope" {
						return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
					}
					return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
				}
			case "repository_outage":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_repository", func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.Table, m.service.names.Repositories.String()) {
						_ = tx.AddError(failure)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createRequirementCodeLinkOn(ctx, tx, 1, 99, id, 11, input, provenance)
				return err
			})
			if scenario == "repository_outage" {
				require.NoError(t, db.Callback().Query().Remove("regression_repository"))
			}
			require.Error(t, err)
			if scenario == "policy_outage" || scenario == "repository_outage" {
				require.ErrorIs(t, err, failure)
			}
			var count int64
			require.NoError(t, query.BindCodeLinks(db, m.service.names).Count(&count).Error)
			require.Zero(t, count, "rejected source or authority cannot leave an evidence reference")
		})
	}
}

func TestLocalGitImportReadsActualObjectsAndRechecksCurrentAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	repo := model.ProjectRepository{TenantID: 1, ID: 560, ProjectID: 99, Name: "Local source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("number", 139).Error)
	path := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fix #WI-139 #WI-999"}} {
		out, err := localGitCommand(ctx, path, args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	out, err := m.ImportLocalGit(ctx, 1, 99, repo.ID, 11, path)
	require.NoError(t, err)
	require.Len(t, out.Links, 1)
	require.Len(t, out.Ignored, 1)
	require.Equal(t, "LOCAL_GIT_OBSERVED", out.Links[0].Provenance)
	require.Equal(t, work.ID, out.Links[0].TargetID)
	replayed, err := m.ImportLocalGit(ctx, 1, 99, repo.ID, 11, path)
	require.NoError(t, err)
	require.Equal(t, out.Links[0].ID, replayed.Links[0].ID)
	_, err = m.ImportLocalGit(ctx, 1, 99, repo.ID, 12, path)
	require.ErrorIs(t, err, ErrWorkForbidden)
	_, err = m.ImportLocalGit(ctx, 1, 100, repo.ID, 11, path)
	require.Error(t, err, "local filesystem access requires the actual mapped project")
	_, err = m.ImportLocalGit(ctx, 1, 99, repo.ID, 11, t.TempDir())
	require.Error(t, err, "a directory with no Git objects is not observed evidence")
	require.Len(t, []rune(truncateCodeTitle(strings.Repeat("界", 301))), 300)
	m.runtime = nil
	_, err = m.ImportLocalGit(ctx, 1, 99, repo.ID, 11, path)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestCodeLinkCreationFailuresNeverPersistAnEvidenceClaim(t *testing.T) {
	for _, fault := range []string{"lookup", "ids_missing", "ids_failed", "ids_zero", "audit_missing", "events_missing", "insert", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			repo := model.ProjectRepository{TenantID: 1, ID: 561, ProjectID: 99, Name: "Source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
			require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
			failure := errors.New("code evidence dependency unavailable")
			injected := true
			switch fault {
			case "lookup":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_code_lookup", func(tx *gorm.DB) {
					if injected && tx.Statement.Table == m.service.names.CodeLinks.String() {
						_ = tx.AddError(failure)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("regression_code_lookup")) })
			case "ids_missing":
				m.ids = nil
			case "ids_failed":
				m.ids = draftIDSource(func() (int64, error) { return 0, failure })
			case "ids_zero":
				m.ids = draftIDSource(func() (int64, error) { return 0, nil })
			case "audit_missing":
				m.service.audit = nil
			case "events_missing":
				m.service.events = nil
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_code_insert", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.CodeLinks.String() {
						_ = tx.AddError(failure)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("regression_code_insert")) })
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createCodeLinkOn(ctx, tx, 1, 99, work.ID, 11, 11, CodeLinkInput{Kind: "COMMIT", RepositoryID: repo.ID, Ref: strings.Repeat("e", 40)}, "MANUAL_REFERENCE")
				return err
			})
			injected = false
			switch fault {
			case "ids_missing", "ids_zero", "audit_missing", "events_missing":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.ErrorIs(t, err, failure)
			}
			var n int64
			require.NoError(t, db.Table(m.service.names.CodeLinks.String()).Count(&n).Error)
			require.Zero(t, n, "failed commands cannot leave claimed immutable evidence")
		})
	}
}
