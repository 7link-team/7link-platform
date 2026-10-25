package clientwork

import (
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

// Every read endpoint must reject malformed identifiers and fail closed when
// current project authority or the datasource disappears.
func TestDomainReadEndpointsFailClosed(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	lists := []struct {
		name string
		read func(*ListInput) error
	}{
		{"change", func(in *ListInput) error { _, e := m.list_changeHTTP(ctx, in); return e }},
		{"issue", func(in *ListInput) error { _, e := m.list_issueHTTP(ctx, in); return e }},
		{"package", func(in *ListInput) error { _, e := m.list_packageHTTP(ctx, in); return e }},
		{"preview", func(in *ListInput) error { _, e := m.list_previewHTTP(ctx, in); return e }},
		{"annotation", func(in *ListInput) error { _, e := m.list_annotationHTTP(ctx, in); return e }},
		{"test_case", func(in *ListInput) error { _, e := m.list_test_caseHTTP(ctx, in); return e }},
		{"test_plan", func(in *ListInput) error { _, e := m.list_test_planHTTP(ctx, in); return e }},
		{"test_run", func(in *ListInput) error { _, e := m.list_test_runHTTP(ctx, in); return e }},
	}
	for _, endpoint := range lists {
		t.Run(endpoint.name+"_list", func(t *testing.T) {
			for _, in := range []*ListInput{{EntityID: "bad", Limit: 1}, {EntityID: "3", Limit: 0}, {EntityID: "3", Limit: 101}, {EntityID: "3", Limit: 1, Cursor: "bad!"}} {
				require.Error(t, endpoint.read(in))
			}
			projects.deny = true
			require.Error(t, endpoint.read(&ListInput{EntityID: "3", Limit: 1}))
			projects.deny = false
			runtime := m.runtime
			m.runtime = nil
			require.Error(t, endpoint.read(&ListInput{EntityID: "3", Limit: 1}))
			m.runtime = runtime
			failure := errors.New("private list database diagnostic")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_list_read", func(tx *gorm.DB) { tx.AddError(failure) }))
			err := endpoint.read(&ListInput{EntityID: "3", Limit: 1})
			require.NoError(t, db.Callback().Query().Remove("regression_list_read"))
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, 500, status.GetStatus())
			require.NotContains(t, err.Error(), "private list")
		})
	}
	gets := []struct {
		name string
		read func(*ObjectInput) error
	}{
		{"change", func(in *ObjectInput) error { _, e := m.get_changeHTTP(ctx, in); return e }},
		{"issue", func(in *ObjectInput) error { _, e := m.get_issueHTTP(ctx, in); return e }},
		{"package", func(in *ObjectInput) error { _, e := m.get_packageHTTP(ctx, in); return e }},
		{"preview", func(in *ObjectInput) error { _, e := m.get_previewHTTP(ctx, in); return e }},
		{"annotation", func(in *ObjectInput) error { _, e := m.get_annotationHTTP(ctx, in); return e }},
		{"test_case", func(in *ObjectInput) error { _, e := m.get_test_caseHTTP(ctx, in); return e }},
		{"test_plan", func(in *ObjectInput) error { _, e := m.get_test_planHTTP(ctx, in); return e }},
		{"test_run", func(in *ObjectInput) error { _, e := m.get_test_runHTTP(ctx, in); return e }},
	}
	for _, endpoint := range gets {
		t.Run(endpoint.name+"_get", func(t *testing.T) {
			for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
				require.Error(t, endpoint.read(in))
			}
			projects.deny = true
			require.Error(t, endpoint.read(&ObjectInput{EntityID: "3", ID: "999"}))
			projects.deny = false
			runtime := m.runtime
			m.runtime = nil
			require.Error(t, endpoint.read(&ObjectInput{EntityID: "3", ID: "999"}))
			m.runtime = runtime
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_get_read", func(tx *gorm.DB) { tx.AddError(errors.New("private object database diagnostic")) }))
			err := endpoint.read(&ObjectInput{EntityID: "3", ID: "999"})
			require.NoError(t, db.Callback().Query().Remove("regression_get_read"))
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, 500, status.GetStatus())
			require.NotContains(t, err.Error(), "private object")
		})
	}
}
func TestDomainCommandEndpointsRequireValidRouteAndConcurrency(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	_, echange := m.create_changeHTTP(ctx, &CreateInput[ChangeBody]{EntityID: "bad"})
	require.Error(t, echange)
	_, eissue := m.create_issueHTTP(ctx, &CreateInput[IssueBody]{EntityID: "bad"})
	require.Error(t, eissue)
	_, epackage := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "bad"})
	require.Error(t, epackage)
	_, epreview := m.create_previewHTTP(ctx, &CreateInput[PreviewBody]{EntityID: "bad"})
	require.Error(t, epreview)
	_, eannotation := m.create_annotationHTTP(ctx, &CreateInput[AnnotationBody]{EntityID: "bad"})
	require.Error(t, eannotation)
	_, etest_case := m.create_test_caseHTTP(ctx, &CreateInput[TestCaseBody]{EntityID: "bad"})
	require.Error(t, etest_case)
	_, etest_plan := m.create_test_planHTTP(ctx, &CreateInput[TestPlanBody]{EntityID: "bad"})
	require.Error(t, etest_plan)
	for _, in := range []*EditInput[ChangeAction]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.changeHTTP(ctx, in)
		require.Error(t, e)
	}
	for _, in := range []*EditInput[IssueAction]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.issueHTTP(ctx, in)
		require.Error(t, e)
	}
	for _, in := range []*EditInput[PackagePublishBody]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.package_publishHTTP(ctx, in)
		require.Error(t, e)
	}
	for _, in := range []*EditInput[PackageDecision]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.package_acceptanceHTTP(ctx, in)
		require.Error(t, e)
	}
	for _, in := range []*EditInput[TestCaseBody]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.test_case_editHTTP(ctx, in)
		require.Error(t, e)
	}
	for _, in := range []*EditInput[TestRunBody]{{EntityID: "bad", ID: "1", IfMatch: "\"1\""}, {EntityID: "3", ID: "bad", IfMatch: "\"1\""}, {EntityID: "3", ID: "1", IfMatch: "bad"}} {
		_, e := m.test_runHTTP(ctx, in)
		require.Error(t, e)
	}
}
