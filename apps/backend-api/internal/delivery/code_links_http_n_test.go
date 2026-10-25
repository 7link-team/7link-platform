package delivery

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCodeLinkHTTPPreservesManualProvenanceEvidenceAndCurrentReplayAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	repo := model.ProjectRepository{TenantID: 1, ID: 558, ProjectID: 99, Name: "Source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	path := ResultWorkInput{EntityID: "99", WorkItemID: work.ID.String()}
	in := &codeLinkCreateInput{ResultWorkInput: path, Key: "n-manual-reference", Body: CodeLinkInput{Kind: "COMMIT", RepositoryID: repo.ID, Ref: strings.Repeat("a", 40), Title: "Reviewed source reference"}}
	created, err := m.createCodeLinkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 201, created.Status)
	require.Equal(t, "MANUAL_REFERENCE", created.Body.Data.Provenance)
	first := created.Body.Data
	replayed, err := m.createCodeLinkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.Body.Data.ID)
	list, err := m.listCodeLinksHTTP(ctx, &path)
	require.NoError(t, err)
	require.Len(t, list.Body.Data, 1)
	require.Equal(t, first.ID, list.Body.Data[0].ID)
	evidenceIn := &codeLinkEvidenceInput{ResultWorkInput: path, CodeLinkID: first.ID.String()}
	evidence, err := m.codeLinkEvidenceHTTP(ctx, evidenceIn)
	require.NoError(t, err)
	require.False(t, evidence.Body.Data.Observed, "a manual reference is never observed Git, merged, deployed or verified")
	require.Equal(t, first.ContentHash, evidence.Body.Data.SHA256)
	require.Contains(t, evidence.Body.Data.Snapshot, in.Body.Title)
	evidenceIn.CodeLinkID = "bad"
	_, err = m.codeLinkEvidenceHTTP(ctx, evidenceIn)
	require.Error(t, err)
	evidenceIn.CodeLinkID = first.ID.String()
	require.NoError(t, query.BindCodeLinks(db, m.service.names).Where("id=?", first.ID).Update("snapshot", "changed bytes").Error)
	_, err = m.codeLinkEvidenceHTTP(ctx, evidenceIn)
	require.Error(t, err, "tampered stored evidence fails closed")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.createCodeLinkHTTP(ctx, in)
	require.Error(t, err, "receipt replay cannot retain revoked permission")
	_, err = m.listCodeLinksHTTP(ctx, &path)
	require.Error(t, err)
}

func TestRequirementCodeLinkHTTPKeepsTargetScopeAndRejectsUnreadableEvidence(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var target RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		target, err = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	repo := model.ProjectRepository{TenantID: 1, ID: 559, ProjectID: 99, Name: "Source", RemoteURL: "https://example.test/source.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(&repo))
	path := RequirementGetInput{EntityID: "99", RequirementID: target.ID.String()}
	in := &requirementCodeLinkInput{RequirementGetInput: path, Key: "n-requirement-reference", Body: CodeLinkInput{Kind: "BRANCH", RepositoryID: repo.ID, Ref: "feature/requirement", Title: "Reference only"}}
	created, err := m.createRequirementCodeLinkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "requirement", created.Body.Data.TargetKind)
	replayed, err := m.createRequirementCodeLinkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, created.Body.Data.ID, replayed.Body.Data.ID)
	list, err := m.listRequirementCodeLinksHTTP(ctx, &path)
	require.NoError(t, err)
	require.Len(t, list.Body.Data, 1)
	evidenceIn := &requirementCodeEvidenceInput{RequirementGetInput: path, CodeLinkID: created.Body.Data.ID.String()}
	evidence, err := m.requirementCodeLinkEvidenceHTTP(ctx, evidenceIn)
	require.NoError(t, err)
	require.False(t, evidence.Body.Data.Observed)
	require.Equal(t, created.Body.Data.ContentHash, evidence.Body.Data.SHA256)
	evidenceIn.CodeLinkID = "bad"
	_, err = m.requirementCodeLinkEvidenceHTTP(ctx, evidenceIn)
	require.Error(t, err)
	evidenceIn.CodeLinkID = created.Body.Data.ID.String()
	require.NoError(t, query.BindCodeLinks(db, m.service.names).Where("id=?", created.Body.Data.ID).Update("snapshot", "tampered").Error)
	_, err = m.requirementCodeLinkEvidenceHTTP(ctx, evidenceIn)
	require.Error(t, err)
	for _, invalid := range []RequirementGetInput{{EntityID: "bad", RequirementID: target.ID.String()}, {EntityID: "99", RequirementID: "bad"}} {
		_, err := m.listRequirementCodeLinksHTTP(ctx, &invalid)
		require.Error(t, err)
		_, err = m.createRequirementCodeLinkHTTP(ctx, &requirementCodeLinkInput{RequirementGetInput: invalid})
		require.Error(t, err)
		_, err = m.requirementCodeLinkEvidenceHTTP(ctx, &requirementCodeEvidenceInput{RequirementGetInput: invalid})
		require.Error(t, err)
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.createRequirementCodeLinkHTTP(ctx, in)
	require.Error(t, err)
	_, err = m.listRequirementCodeLinksHTTP(ctx, &path)
	require.Error(t, err)
	m.runtime = nil
	_, err = m.listRequirementCodeLinksHTTP(ctx, &path)
	require.Error(t, err)
}
