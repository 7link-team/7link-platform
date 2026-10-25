package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLibraryFiltersBeforePaginationAndFamilyGrouping(t *testing.T) {
	m, db, ctx := templateHarness(t)
	put := func(key, semver, domain, origin, state, mode string, owner guid.ID) TemplateVersionView {
		d := testDefinition()
		d.Domain = domain
		d.Semver = semver
		d.Metadata = map[string]any{"team_modes": []string{mode}}
		var v TemplateVersionView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.draftOn(ctx, tx, key, d); return e }))
		q := query.BindVersions(db, m.names)
		_, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).UpdateSimple(q.Origin.Value(origin), q.State.Value(state), q.CreatedBy.Value(owner))
		require.NoError(t, e)
		return v
	}
	// Interleaving families and creation order must not change semver selection.
	a := put("b", "1.10.0", "SOFTWARE", "TEAM", "PUBLISHED", "HYBRID", 11)
	put("a", "2.0.0", "VIDEO", "FIRST_PARTY", "DRAFT", "HUMAN_ONLY", 11)
	put("b", "1.9.0", "SOFTWARE", "TEAM", "DEPRECATED", "HYBRID", 11)
	put("a", "1.0.0", "VIDEO", "FIRST_PARTY", "PUBLISHED", "HUMAN_ONLY", 11)
	put("secret", "1.0.0", "VIDEO", "FIRST_PARTY", "PUBLISHED", "HUMAN_ONLY", 22)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	first, e := m.familiesHTTP(ctx, &familyInput{Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, "a", first.Body.Data.Items[0].Key)
	require.Equal(t, 2, first.Body.Data.Items[0].VersionCount)
	require.Equal(t, "2.0.0", first.Body.Data.Items[0].Latest.Definition.Semver)
	require.Equal(t, "1.0.0", first.Body.Data.Items[0].LatestPublished.Definition.Semver)
	second, e := m.familiesHTTP(ctx, &familyInput{Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Equal(t, a.ID, second.Body.Data.Items[0].Latest.ID)
	filters := TemplateFilters{Domain: "VIDEO", Origin: "FIRST_PARTY", State: "PUBLISHED", TeamMode: "HUMAN_ONLY"}
	page, e := m.listHTTP(ctx, &listInput{TemplateFilters: filters, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.False(t, page.Body.Data.HasMore)
	require.Equal(t, "a", page.Body.Data.Items[0].Key)
	families, e := m.familiesHTTP(ctx, &familyInput{TemplateFilters: filters, Limit: 10})
	require.NoError(t, e)
	require.Len(t, families.Body.Data.Items, 1)
	require.Equal(t, 1, families.Body.Data.Items[0].VersionCount)
	retired := put("retired", "1.0.0", "SOFTWARE", "TEAM", "RETIRED", "HYBRID", 11)
	retiredPage, err := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{State: "RETIRED"}, Limit: 10, Sort: "id"})
	require.NoError(t, err)
	require.Len(t, retiredPage.Body.Data.Items, 1)
	require.Equal(t, retired.ID, retiredPage.Body.Data.Items[0].ID)
	filters.TeamMode = "AUTONOMOUS"
	page, e = m.listHTTP(ctx, &listInput{TemplateFilters: filters, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	history, e := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{Key: "b"}, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.True(t, history.Body.Data.HasMore)
	rest, e := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{Key: "b"}, Cursor: history.Body.Data.NextCursor, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, rest.Body.Data.Items, 1)
	require.NotEqual(t, history.Body.Data.Items[0].ID, rest.Body.Data.Items[0].ID)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	empty, e := m.familiesHTTP(other, &familyInput{Limit: 1})
	require.NoError(t, e)
	require.Empty(t, empty.Body.Data.Items)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.familiesHTTP(ctx, &familyInput{Limit: 1})
	assertStatus(t, e, 403)
}
func TestLibraryScansPastNonmatchingBatchesAndRetainsSmallestFamilies(t *testing.T) {
	m, db, ctx := templateHarness(t)
	// Bulk insert valid immutable records through the canonical version shape.
	base := draftTemplate(t, m, db, ctx, "z")
	q := query.BindVersions(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(base.ID)).First()
	require.NoError(t, e)
	for i := 0; i < 105; i++ {
		version := *row
		version.ID = guid.ID(1000 + i)
		version.Key = fmt.Sprintf("skip-%03d", i)
		require.NoError(t, q.WithContext(ctx).Create(&version))
	}
	v := draftTemplate(t, m, db, ctx, "a")
	_, e = q.WithContext(ctx).Where(q.ID.Eq(v.ID)).UpdateSimple(q.Origin.Value("FIRST_PARTY"))
	require.NoError(t, e)
	page, e := m.listHTTP(ctx, &listInput{TemplateFilters: TemplateFilters{Origin: "FIRST_PARTY"}, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Equal(t, v.ID, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	families, e := m.familiesHTTP(ctx, &familyInput{Limit: 1})
	require.NoError(t, e)
	require.Equal(t, "a", families.Body.Data.Items[0].Key)
	require.True(t, families.Body.Data.HasMore)
}
func TestLibraryRejectsInvalidFiltersAndDoesNotInferApplicability(t *testing.T) {
	m, _, ctx := templateHarness(t)
	for _, f := range []TemplateFilters{{Domain: "OTHER"}, {Origin: "UNKNOWN"}, {State: "READY"}, {TeamMode: "ALL"}, {Key: " space "}} {
		_, e := m.listHTTP(ctx, &listInput{TemplateFilters: f, Limit: 1, Sort: "id"})
		assertStatus(t, e, 422)
		_, e = m.familiesHTTP(ctx, &familyInput{TemplateFilters: f, Limit: 1})
		assertStatus(t, e, 422)
	}
	for _, in := range []*familyInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Cursor: " x"}} {
		_, e := m.familiesHTTP(ctx, in)
		assertStatus(t, e, 422)
	}
	f := TemplateFilters{TeamMode: "HYBRID"}
	v := TemplateVersionView{Definition: TemplateDefinition{Metadata: map[string]any{}}}
	require.False(t, f.matches(v))
	v.Definition.Metadata["team_modes"] = []string{"HYBRID"}
	require.True(t, f.matches(v))
	v.Definition.Metadata["team_modes"] = []any{"HUMAN_ONLY"}
	require.False(t, f.matches(v))
}

func TestLibraryFamilyCursorUsesOneOrdering(t *testing.T) {
	m, db, ctx := templateHarness(t)
	for _, key := range []string{"a", "Z", "z"} {
		draftTemplate(t, m, db, ctx, key)
	}
	cursor := ""
	for index, key := range []string{"Z", "a", "z"} {
		page, err := m.familiesHTTP(ctx, &familyInput{Limit: 1, Cursor: cursor})
		require.NoError(t, err)
		require.Len(t, page.Body.Data.Items, 1)
		require.Equal(t, key, page.Body.Data.Items[0].Key)
		require.Equal(t, index < 2, page.Body.Data.HasMore)
		cursor = page.Body.Data.NextCursor
	}
	require.False(t, (TemplateFilters{Key: "Z"}).matches(TemplateVersionView{Key: "z"}))
}
