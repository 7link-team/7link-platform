package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEscalationInboxAcknowledgementClosesAlertWithoutResolvingIncident(t *testing.T) {
	m, db, ctx, _ := settingsHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.recordEscalationOn(ctx, tx, escalationFixture())
		return err
	}))
	req := InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: "ESCALATION_ALERT", Limit: 1}
	read := func() []InboxItem { rows, err := m.escalationInbox(ctx, req); require.NoError(t, err); return rows }
	rows := read()
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ObjectID)
	original := m.explain
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.EntityID == 0}, nil
	}
	require.Empty(t, read(), "current project denial removes the source")
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.Permission != "project_takeover"}, nil
	}
	require.Empty(t, read(), "policy readers outside the owner/lead audience get no alert")
	m.explain = original
	req.ProjectID = 8
	require.Empty(t, read())
	req.ProjectID = 3
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.incidentDecisionOn(ctx, tx, 1, id, 1, "ACKNOWLEDGED", "")
		return err
	}))
	require.Empty(t, read(), "any Human acknowledgement closes the alert")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.incidentDecisionOn(ctx, tx, 1, id, 2, "DISMISSED", "Reviewed and disposed by Human")
		return err
	}))
	require.Empty(t, read())
}

func TestEscalationInboxRecipientRequiresCurrentSeededLeadMatch(t *testing.T) {
	m, db, ctx, _ := settingsHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := model.RoleSeed{ID: 900, TenantID: 1, RoleKey: "delivery_lead", RoleID: 901, ManifestVersion: 1, AppliedCodes: "[]", SkippedCodes: "[]", Managed: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Table(m.seed.names.RoleSeeds.String()).Create(&row).Error)
	match := authz.DecisionMatch{RoleID: "901", Effect: "allow"}
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, guid.ID(1), r.TenantID)
		require.Equal(t, guid.ID(3), r.EntityID)
		require.Equal(t, guid.ID(2), r.PrincipalID)
		return authz.Explanation{Allowed: r.Permission == "project_read", Matches: []authz.DecisionMatch{match}}, nil
	}
	check := func() bool {
		var allowed bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			allowed, err = m.escalationInboxRecipientOn(ctx, tx, 1, 3, 2)
			return err
		}))
		return allowed
	}
	require.True(t, check())
	match.Effect = "deny"
	require.False(t, check(), "a denied role match is not an eligible lead")
	match.Effect, match.RoleID = "allow", "902"
	require.False(t, check(), "another permitted role is not the delivery lead")
}
