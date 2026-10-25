package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func commentDefectHarness(t *testing.T) (*Module, *gorm.DB, context.Context, guid.ID, *bool) {
	t.Helper()
	m, db, ctx, body := workHTTPHarness(t)
	var source workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		source, err = m.createWorkOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	require.NoError(t, db.Exec("CREATE TABLE artifact_source_probe (target_id BIGINT NOT NULL, state TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO artifact_source_probe VALUES (0,'OPEN')").Error)
	failed := false
	m.artifactComments = ArtifactComments{ScopeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentScope, error) {
		return ArtifactCommentScope{ProjectID: 99, WorkItemID: source.ID}, nil
	}, GetOn: func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) (ArtifactCommentSource, error) {
		var target guid.ID
		require.NoError(t, tx.Table("artifact_source_probe").Select("target_id").Scan(&target).Error)
		return ArtifactCommentSource{ID: id, TenantID: tenant, ProjectID: 99, WorkItemID: source.ID, ExecutionID: 71, Visibility: "INTERNAL", State: "OPEN", FirstBody: "Wrong return value", Anchor: ArtifactAnchorSnapshot{Kind: "CODE_LINES", Path: "source.go", VersionRef: "file-version:3 sha256:" + strings.Repeat("a", 64), FileID: 2, VersionID: 3, SHA256: strings.Repeat("a", 64), StartLine: 4, EndLine: 4, Excerpt: "return false"}, DefectID: target}, nil
	}, MarkConvertedOn: func(_ context.Context, tx *gorm.DB, tenant, thread guid.ID, kind string, target, actor guid.ID) error {
		if failed {
			return errors.New("source conversion failed")
		}
		return tx.Table("artifact_source_probe").Where("target_id = ?", 0).Updates(map[string]any{"target_id": target, "state": "RESOLVED"}).Error
	}}
	return m, db, ctx, source.ID, &failed
}

func TestCommentDefectCanonicalCreateFrozenProvenanceAndDuplicate(t *testing.T) {
	m, db, ctx, source, _ := commentDefectHarness(t)
	var auditDetails map[string]any
	m.service.audit = func(_ context.Context, _ *gorm.DB, e audit.DomainEvent) error {
		if e.EventType == "delivery.defect_created_from_comment" {
			auditDetails = e.Detail
		}
		return nil
	}
	input := CommentDefectCreate{Severity: "CRITICAL", Expected: "Return true for a valid input"}
	var got CommentDefectView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		got, err = m.createCommentDefectOn(ctx, tx, 1, 80, input)
		return err
	}))
	require.Positive(t, got.ID)
	require.NotEqual(t, source, got.ID)
	require.Contains(t, got.Steps, "source.go:4-4")
	require.Contains(t, got.Steps, "file-version:3")
	require.Contains(t, got.Steps, "return false")
	require.Equal(t, "Wrong return value", got.Actual)
	require.Equal(t, "CODE_LINES", auditDetails["source_kind"])
	require.NotContains(t, auditDetails, "body")
	require.NotContains(t, auditDetails, "excerpt")
	w := query.BindWorkItems(db, m.service.names)
	bug, err := w.WithContext(ctx).Where(w.ID.Eq(got.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "BUG", bug.Type)
	require.Equal(t, "CRITICAL", *bug.Severity)
	require.Equal(t, guid.ID(11), bug.AccountablePrincipalID)
	c := query.BindWorkContracts(db, m.service.names)
	contracts, err := c.WithContext(ctx).Where(c.WorkItemID.Eq(got.ID)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, contracts)
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.createCommentDefectOn(ctx, tx, 1, 80, input); return e })
	var duplicate *commentDefectError
	require.ErrorAs(t, err, &duplicate)
	require.Equal(t, "ALREADY_CONVERTED", duplicate.Reason)
	require.Equal(t, got.ID, duplicate.TargetID)
	var count int64
	require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCommentDefectSourceFailureRollsBackCanonicalAggregateAndActivities(t *testing.T) {
	m, db, ctx, source, fail := commentDefectHarness(t)
	*fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createCommentDefectOn(ctx, tx, 1, 80, CommentDefectCreate{Severity: "NORMAL", Expected: "Expected value"})
		return e
	}))
	var works, provenance, activities int64
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Count(&works).Error)
	require.EqualValues(t, 1, works)
	require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Count(&provenance).Error)
	require.Zero(t, provenance)
	require.NoError(t, db.Table(m.service.names.WorkActivities.String()).Where("work_item_id = ?", source).Count(&activities).Error)
	require.Zero(t, activities)
}

func TestCommentDefectExplicitFoundInWorkItem(t *testing.T) {
	m, db, ctx, source, _ := commentDefectHarness(t)
	w := query.BindWorkItems(db, m.service.names)
	other, err := w.WithContext(ctx).Where(w.ID.Eq(source)).First()
	require.NoError(t, err)
	other.ID = source + 100
	other.Number += 100
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Create(other).Error)
	var got CommentDefectView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		got, e = m.createCommentDefectOn(ctx, tx, 1, 80, CommentDefectCreate{Severity: "NORMAL", Expected: "Expected result", FoundInWorkItemID: &other.ID})
		return e
	}))
	require.Equal(t, other.ID, got.FoundInWorkItemID)
	var receipt model.CommentDefect
	require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Where("work_item_id = ?", got.ID).First(&receipt).Error)
	require.Equal(t, other.ID, receipt.FoundInWorkItemID)
}

func TestCommentDefectAgentSeenSeqRefreshSameKeyAndCurrentAuthorization(t *testing.T) {
	m, db, ctx, source, _ := commentDefectHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 220, TenantID: 1, PrincipalID: 22, State: "ACTIVE"}, true, nil
	}}
	agent := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 22, SubjectType: authn.SubjectTypePrincipal, EntityID: 99})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordWorkActivityOn(ctx, tx, 1, source, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 11, RefKind: "artifact_comment", RefID: 80, Title: "New comment"})
		return e
	}))
	in := &commentDefectInput{ThreadID: "80", IdempotencyKey: "agent-convert", Body: CommentDefectCreate{Severity: "NORMAL", Expected: "Expected value"}}
	_, err := m.createCommentDefectHTTP(agent, in)
	assertWorkStatus(t, err, 422)
	stale := int64(0)
	in.Body.SeenSeq = &stale
	_, err = m.createCommentDefectHTTP(agent, in)
	assertWorkStatus(t, err, 409)
	fresh := int64(1)
	in.Body.SeenSeq = &fresh
	out, err := m.createCommentDefectHTTP(agent, in)
	require.NoError(t, err)
	require.Positive(t, out.Body.Data.ID)
	replay, err := m.createCommentDefectHTTP(agent, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, out.Body.Data, replay.Body.Data)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.createCommentDefectHTTP(agent, in)
	assertWorkStatus(t, err, 403)
	// General creation remains Human-only even for this otherwise valid Agent.
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createWorkOn(agent, tx, 1, 99, 22, workCreateBody{Title: "General", Type: "TASK", Objective: "Objective", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Expected", VerificationMethod: "Verify"}}})
		return e
	}), ErrInvalidMember)
}

func TestCommentDefectRejectsClientAndRequiredFields(t *testing.T) {
	for _, which := range []string{"CLIENT", "EXPECTED", "SEVERITY"} {
		t.Run(which, func(t *testing.T) {
			m, db, ctx, _, _ := commentDefectHarness(t)
			get := m.artifactComments.GetOn
			m.artifactComments.GetOn = func(ctx context.Context, tx *gorm.DB, t, id guid.ID) (ArtifactCommentSource, error) {
				v, e := get(ctx, tx, t, id)
				if which == "CLIENT" {
					v.Visibility = "CLIENT"
				}
				return v, e
			}
			input := CommentDefectCreate{Severity: "NORMAL", Expected: "Expected"}
			if which == "EXPECTED" {
				input.Expected = ""
			}
			if which == "SEVERITY" {
				input.Severity = ""
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.createCommentDefectOn(ctx, tx, 1, 80, input); return e })
			var denied *commentDefectError
			require.ErrorAs(t, err, &denied)
			var records []model.CommentDefect
			require.NoError(t, db.Table(m.service.names.CommentDefects.String()).Find(&records).Error)
			require.Empty(t, records)
		})
	}
}
