package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQualityEscapeHTTPDecisionReplayRechecksCurrentHumanAuthority(t *testing.T) {
	m, db, ctx, pub, id := escapeHarness(t)
	store := idempotency.NewModule(m.runtime)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.seed.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	source := store.MigrationSource()
	source.Prefix = "g_"
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	m.delegations.replay = store
	in := escapeDecisionInput{TenantID: "1", ID: id.String(), IfMatch: `"1"`, Key: "escape-decision"}
	in.Body.State, in.Body.Reason = "DISMISSED", "Current source criterion was superseded"
	invalid := in
	invalid.Key = ""
	_, err = m.decideQualityEscapeHTTP(ctx, &invalid)
	require.Error(t, err)
	invalid = in
	invalid.ID = "999"
	_, err = m.decideQualityEscapeHTTP(ctx, &invalid)
	require.Error(t, err)
	pub.Fail = true
	_, err = m.decideQualityEscapeHTTP(ctx, &in)
	require.Error(t, err)
	q := query.BindQualityEscapes(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", row.LearningState)
	require.Equal(t, 1, row.Version)
	pub.Fail = false
	first, err := m.decideQualityEscapeHTTP(ctx, &in)
	require.NoError(t, err)
	require.Equal(t, 200, first.Status)
	require.Equal(t, "DISMISSED", first.Body.Data.LearningState)
	replay, err := m.decideQualityEscapeHTTP(ctx, &in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	in.Body.Reason = "A changed payload cannot reuse the same receipt"
	_, err = m.decideQualityEscapeHTTP(ctx, &in)
	require.Error(t, err)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	in.Body.Reason = "Current source criterion was superseded"
	_, err = m.decideQualityEscapeHTTP(ctx, &in)
	require.Error(t, err, "cached decision must require current Human authority")
	_, err = m.getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "1", ID: id.String()})
	require.NoError(t, err, "read permission remains independent of Human decision eligibility")
	m.explain = nil
	_, err = m.getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "1", ID: id.String()})
	require.Error(t, err)
	_, err = m.listQualityEscapesHTTP(ctx, &escapeListInput{TenantID: "1", Limit: 50})
	require.Error(t, err)
}

func TestEscapeHTTPRejectsInvalidSelectorsAndCorruptStoredFacts(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	for _, in := range []escapeListInput{{TenantID: "1", Limit: 0}, {TenantID: "0", Limit: 50}, {TenantID: "1", ProjectID: "bad", Limit: 50}} {
		_, err := m.listQualityEscapesHTTP(ctx, &in)
		require.Error(t, err)
	}
	for _, in := range []escapeRefInput{{TenantID: "0", ID: id.String()}, {TenantID: "1", ID: "0"}, {TenantID: "1", ID: "999"}} {
		_, err := m.getQualityEscapeHTTP(ctx, &in)
		require.Error(t, err)
	}
	for _, in := range []escapeDecisionInput{{TenantID: "0", ID: id.String()}, {TenantID: "1", ID: "0"}, {TenantID: "1", ID: id.String(), IfMatch: "invalid"}} {
		_, err := m.decideQualityEscapeHTTP(ctx, &in)
		require.Error(t, err)
	}
	_, err := (&Module{}).listQualityEscapesHTTP(ctx, &escapeListInput{TenantID: "1", Limit: 50})
	require.Error(t, err)
	_, err = (&Module{}).getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "1", ID: id.String()})
	require.Error(t, err)
	_, err = (&Module{}).decideQualityEscapeHTTP(ctx, &escapeDecisionInput{TenantID: "1", ID: id.String(), IfMatch: `"1"`})
	require.Error(t, err)
	require.NoError(t, db.Table(m.seed.names.QualityEscapes.String()).Where("id = ?", id).Update("passed_by", "invalid").Error)
	_, err = m.getQualityEscapeHTTP(ctx, &escapeRefInput{TenantID: "1", ID: id.String()})
	require.Error(t, err)
	_, err = m.listQualityEscapesHTTP(ctx, &escapeListInput{TenantID: "1", Limit: 50, Stage: "HUMAN_REVIEW", Category: "FUNCTIONAL_DEFECT"})
	require.Error(t, err)
}
