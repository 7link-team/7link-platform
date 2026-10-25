package templates

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFirstPartyPacksSeedImmutableReplayAndAuthorizedCopies(t *testing.T) {
	m, db, ctx := templateHarness(t)
	var published int
	m.events = testPublisher(func(_ context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
		if e.Actor.Kind == "SYSTEM" {
			require.Zero(t, e.TenantID)
			require.Zero(t, e.Actor.ID)
			require.Equal(t, "SYSTEM:first_party_catalog", e.Actor.AuthoritySource)
			published++
		}
		return 1, nil
	})
	seed := func() error { return m.Start(ctx) }
	require.NoError(t, seed())
	require.NoError(t, seed())
	require.Equal(t, 4, published, "reinitialization must not republish seeded versions")
	q := query.BindVersions(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(0))).Find()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, row := range rows {
		v, err := versionView(row)
		require.NoError(t, err)
		require.Equal(t, "FIRST_PARTY", v.Origin)
		require.Equal(t, "PUBLISHED", v.State)
		require.NoError(t, v.Definition.Validate(nil))
		hash, err := v.Definition.Hash()
		require.NoError(t, err)
		require.Equal(t, v.Hash, hash)
		defs, err := verifierDefinitions(v.Definition)
		require.NoError(t, err)
		require.NotEmpty(t, defs)
		rubric, err := rubricDefinition(v.Definition)
		require.NoError(t, err)
		require.NotNil(t, rubric)
		var golden []FirstPartyGoldenCase
		raw, err := json.Marshal(v.Definition.Metadata["golden_set"])
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &golden))
		require.Len(t, golden, 20)
		seen := map[string]bool{}
		keys := map[string]bool{}
		for _, def := range defs {
			keys[def.Key] = true
		}
		for _, c := range golden {
			require.True(t, keys[c.VerifierKey])
			require.Contains(t, []string{"PASS", "FAIL"}, c.Expected)
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			require.False(t, seen[string(raw)], "golden cases must not duplicate")
			seen[string(raw)] = true
		}
		require.Equal(t, "UNCALIBRATED", v.Definition.Metadata["calibration_state"])
		_, err = m.getHTTP(ctx, &getInput{ID: v.ID.String()})
		require.NoError(t, err)
		var snapshot TemplateVersionView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			snapshot, e = m.copyOn(ctx, tx, row.ID, "copy-"+v.Key, "Team copy")
			return e
		}))
		require.Equal(t, &v.ID, snapshot.BasedOnVersionID)
		require.Equal(t, v.Hash, snapshot.BasedOnSHA256)
		require.Equal(t, "TEAM", snapshot.Origin)
		require.Equal(t, "DRAFT", snapshot.State)
		require.Equal(t, v.Definition.Metadata, snapshot.Definition.Metadata)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchOn(ctx, tx, row.ID, TemplateWorkflowPatch{}); return e }), ErrNotFound)
	}
	page, err := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{Origin: "FIRST_PARTY"}, Limit: 2, Sort: "id"})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 2)
	require.True(t, page.Body.Data.HasMore)
	next, err := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{Origin: "FIRST_PARTY"}, Limit: 2, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, err)
	require.Len(t, next.Body.Data.Items, 2)
	require.False(t, next.Body.Data.HasMore)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 22, SubjectType: authn.SubjectTypePrincipal})
	page, err = m.listHTTP(other, &listInput{TemplateFilters: TemplateFilters{Origin: "FIRST_PARTY"}, Limit: 50, Sort: "id"})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 4)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.getHTTP(other, &getInput{ID: rows[0].ID.String()})
	require.Error(t, err, "global catalog never bypasses current IAM read authority")
}

func TestFirstPartySeedRollsBackAuditOutboxAndRejectsHashDrift(t *testing.T) {
	for _, boundary := range []string{"audit", "outbox"} {
		t.Run(boundary, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			if boundary == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			} else {
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				})
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.seedFirstPartyOn(ctx, tx) }))
			count, err := query.BindVersions(db, m.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
	m, db, ctx := templateHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.seedFirstPartyOn(ctx, tx) }))
	q := query.BindVersions(db, m.names)
	_, err := q.WithContext(ctx).Where(q.Key.Eq("software-dev")).UpdateSimple(q.SHA256.Value("tampered"))
	require.NoError(t, err)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.seedFirstPartyOn(ctx, tx) }), ErrConflict)
}

func TestFirstPartyPackDomainContractsAndFreshCopies(t *testing.T) {
	packs := firstPartyDefinitions()
	require.Len(t, packs, 4)
	for key, domain := range map[string]string{"software-dev": "SOFTWARE", "video-production": "VIDEO", "novel-writing": "NOVEL", "team-tasks": "TEAM_TASKS"} {
		pack := packs[key]
		require.Equal(t, domain, pack.Domain)
		require.Equal(t, "1.0.0", pack.Semver)
		names := pack.Metadata["names"].(map[string]any)
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			require.NotEmpty(t, names[locale])
		}
		require.GreaterOrEqual(t, len(pack.Metadata["artifacts"].([]any)), 8)
		gates := map[string]bool{}
		for _, node := range pack.Workflow.Nodes {
			if node.Kind == "HUMAN_GATE" {
				require.True(t, node.Mandatory)
				gates[node.Gate] = true
			}
		}
		require.Equal(t, map[string]bool{"TEAM": true, "CLIENT": true}, gates)
	}
	require.Equal(t, []any{"agile", "waterfall"}, packs["software-dev"].Metadata["methods"])
	require.Equal(t, []any{"requirements", "design", "testing", "delivery"}, packs["software-dev"].Metadata["waterfall_required_reviews"])
	require.Equal(t, []any{"HUMAN_ONLY"}, packs["team-tasks"].Metadata["team_modes"])
	require.Equal(t, "ADVISORY_ONLY", packs["team-tasks"].Metadata["assistant_role"])
	require.Equal(t, true, packs["novel-writing"].Metadata["whole_book_required"])
	require.Equal(t, "VIDEO_GEN", packs["video-production"].Metadata["provider_kind"])
	packs["software-dev"].Metadata["calibration_state"] = "tampered"
	require.Equal(t, "UNCALIBRATED", firstPartyDefinitions()["software-dev"].Metadata["calibration_state"])
}
