package templates

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type evalFileFixture struct {
	deny bool
	pins int
}

func (f *evalFileFixture) InspectOn(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
	if f.deny {
		return files.PinnedCandidate{}, files.ErrPinnedForbidden
	}
	return files.PinnedCandidate{PinnedResource: files.PinnedResource{SpaceID: 3, FileID: 41, VersionID: 42, SHA256: strings.Repeat("a", 64)}, NodeVersion: 1}, nil
}
func (f *evalFileFixture) PinOn(_ context.Context, _ *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
	f.pins++
	return files.PinnedResource{BindingID: in.ID, FileID: in.FileID, VersionID: *in.VersionID, SHA256: strings.Repeat("a", 64)}, nil
}
func (f *evalFileFixture) OpenOn(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
	return files.PinnedResourceRead{}, errors.New("not read by command fixture")
}
func evalFixture(t *testing.T) (*Module, *gorm.DB, context.Context, TemplateVersionView, EvalCaseDraft) {
	t.Helper()
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Metadata["verifiers"] = []VerifierDefinition{verifierDefinition()}
	v := publishRuntime(t, m, db, ctx, "evaluated", d)
	m.evalFiles = &evalFileFixture{}
	return m, db, ctx, v, EvalCaseDraft{TemplateVersionID: v.ID, TemplateKey: v.Key, ProjectID: 99, Title: "Known labelled sample", Kind: "JUDGEMENT", InputRef: []EvalFileRef{{FileID: 41, VersionID: 42, SHA256: strings.Repeat("a", 64)}}, Expected: map[string]string{"unit-tests": "PASS"}} //nolint:misspell // Preserve the published domain or serialized contract spelling.
}
func addEvalFixture(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, in EvalCaseDraft) EvalCaseView {
	t.Helper()
	var out EvalCaseView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.createEvalCaseOn(ctx, tx, in); return err }))
	return out
}
func TestEvalCasesPinActualFilesAndRollbackDeniedOrUnauditedCommands(t *testing.T) {
	m, db, ctx, _, draft := evalFixture(t)
	f := m.evalFiles.(*evalFileFixture)
	f.deny = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, draft); return e }))
	var n int64
	require.NoError(t, db.Table(m.names.EvalCases.String()).Count(&n).Error)
	require.Zero(t, n)
	require.Zero(t, f.pins)
	f.deny = false
	original := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, draft); return e }))
	require.NoError(t, db.Table(m.names.EvalCases.String()).Count(&n).Error)
	require.Zero(t, n)
	m.audit = original
	created := addEvalFixture(t, m, db, ctx, draft)
	require.Positive(t, created.InputRef[0].BindingID)
	require.Equal(t, guid.ID(42), created.InputRef[0].VersionID)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchEvalCaseOn(ctx, tx, created.ID, 2, EvalCasePatch{Expected: map[string]string{"unit-tests": "FAIL"}})
		return e
	}), ErrConflict)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.evalCaseOn(other, tx, created.ID, "template_read"); return e }), ErrNotFound)
}
func TestEvalUnavailableRunFreezesCaseLabelsAndHashes(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	created := addEvalFixture(t, m, db, ctx, draft)
	var first EvalRunView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured/provider"}})
		return e
	}))
	require.Equal(t, "UNAVAILABLE", first.State)
	require.Nil(t, first.AgreementPPM)
	require.Empty(t, first.Results)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchEvalCaseOn(ctx, tx, created.ID, 1, EvalCasePatch{Expected: map[string]string{"unit-tests": "FAIL"}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		read, e := m.evalRunOn(ctx, tx, first.ID)
		require.Equal(t, first, read)
		require.Equal(t, "PASS", read.Snapshot.Cases[0].Expected["unit-tests"])
		return e
	}))
	require.NoError(t, db.Table(m.names.EvalRuns.String()).Where("id=?", first.ID).Update("snapshot", "{}").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.evalRunOn(ctx, tx, first.ID); return e }), ErrConflict)
}
func TestEscapeEvalDedupRetainsHumanRejectionWithoutInventingVerifierLabels(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	addEvalFixture(t, m, db, ctx, draft)
	var original EvalRunView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		original, e = m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured"}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	fact := governance.EscapeEvalFact{TenantID: 1, ProjectID: 99, ID: 901, WorkItemID: 700, Title: "Source rejection", Snapshot: []byte(`{"human_acceptance":"REJECTED","result_id":"100"}`)}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var created bool
		var e error
		id, created, e = m.proposeEscapeEvalOn(ctx, tx, fact)
		require.True(t, created)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		again, created, e := m.proposeEscapeEvalOn(ctx, tx, fact)
		require.False(t, created)
		require.Equal(t, id, again)
		return e
	}))
	var rows []model.EvalCase
	require.NoError(t, db.Table(m.names.EvalCases.String()).Find(&rows).Error)
	require.Len(t, rows, 2)
	var escape model.EvalCase
	for _, row := range rows {
		if row.Origin == "ESCAPE" {
			escape = row
		}
	}
	require.Equal(t, "{}", escape.Expected)
	require.Equal(t, string(fact.Snapshot), escape.SourceSnapshot)
	var runs []model.EvalRun
	require.NoError(t, db.Table(m.names.EvalRuns.String()).Order("id").Find(&runs).Error)
	require.Len(t, runs, 2)
	require.Equal(t, original.SHA256, runs[0].SHA256)
	next, e := evalRunView(&runs[1])
	require.NoError(t, e)
	require.Len(t, next.Snapshot.Cases, 2)
	require.Equal(t, "UNAVAILABLE", next.State)
	require.Nil(t, next.AgreementPPM)
}

func TestEvalMigrationsRetainTenantForeignKeysAndEscapeUniqueness(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		raw, err := migrationsFS.ReadFile("sql/" + dialect + "/00007_evaluations.sql")
		require.NoError(t, err)
		for _, part := range []string{"{{.Prefix}}templates_eval_cases", "{{.Prefix}}templates_eval_runs", "UNIQUE(tenant_id,template_key,source_escape_id)", "FOREIGN KEY(tenant_id,template_version_id)", "source_snapshot", "agreement_ppm", "UNAVAILABLE", "eval_rollback_guard"} {
			require.Contains(t, string(raw), part)
		}
	}
}

func TestEvalCommandsValidateLabelsAndRollbackPersistence(t *testing.T) {
	for _, failure := range []string{"case-create", "pin-update", "run-query", "run-create", "case-patch"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, v, draft := evalFixture(t)
			created := addEvalFixture(t, m, db, ctx, draft)
			injected := errors.New("evaluation persistence unavailable")
			operation, table := "create", m.names.EvalCases.String()
			switch failure {
			case "pin-update", "case-patch":
				operation = "update"
			case "run-query":
				operation = "query"
			case "run-create":
				table = m.names.EvalRuns.String()
			}
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}
			switch operation {
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("eval-write-failure", callback))
				defer db.Callback().Create().Remove("eval-write-failure")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("eval-write-failure", callback))
				defer db.Callback().Update().Remove("eval-write-failure")
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("eval-write-failure", callback))
				defer db.Callback().Query().Remove("eval-write-failure")
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				switch failure {
				case "case-create", "pin-update":
					_, e := m.createEvalCaseOn(ctx, tx, draft)
					return e
				case "case-patch":
					title := "Revised"
					_, e := m.patchEvalCaseOn(ctx, tx, created.ID, 1, EvalCasePatch{Title: &title})
					return e
				default:
					_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "configured"}})
					return e
				}
			})
			require.ErrorIs(t, err, injected)
			if operation == "query" {
				require.NoError(t, db.Callback().Query().Remove("eval-write-failure"))
			}
			var rows []model.EvalCase
			require.NoError(t, db.Table(m.names.EvalCases.String()).Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Equal(t, 1, rows[0].Version)
			require.Equal(t, created.Title, rows[0].Title)
			var runs int64
			require.NoError(t, db.Table(m.names.EvalRuns.String()).Count(&runs).Error)
			require.Zero(t, runs)
		})
	}
	m, db, ctx, v, draft := evalFixture(t)
	for _, mutate := range []func(*EvalCaseDraft){func(d *EvalCaseDraft) { d.Title = "" }, func(d *EvalCaseDraft) { d.TemplateKey = "wrong" }, func(d *EvalCaseDraft) { d.Expected = map[string]string{} }, func(d *EvalCaseDraft) { d.Expected = map[string]string{"unit-tests": "OTHER"} }, func(d *EvalCaseDraft) { d.Expected = map[string]string{"unknown": "PASS"} }, func(d *EvalCaseDraft) { d.InputRef = append(d.InputRef, d.InputRef[0]) }, func(d *EvalCaseDraft) {
		d.InputRef = []EvalFileRef{{FileID: 41, VersionID: 43, SHA256: d.InputRef[0].SHA256}}
	}} {
		in := draft
		mutate(&in)
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, in); return e }))
	}
	created := addEvalFixture(t, m, db, ctx, draft)
	for _, patch := range []EvalCasePatch{{}, {Title: new(string)}, {Expected: map[string]string{"unit-tests": "OTHER"}}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchEvalCaseOn(ctx, tx, created.ID, 1, patch); return e }), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{ProviderRef: ""}})
		return e
	}), ErrInvalid)
	for _, in := range []struct{ source, errorIs error }{{files.ErrPinnedConflict, ErrConflict}, {files.ErrPinnedUnavailable, ErrUnavailable}, {files.ErrPinnedInvalid, ErrInvalid}, {files.ErrNotFound, ErrInvalid}} {
		require.ErrorIs(t, evalFileError(in.source), in.errorIs)
	}
}

func TestEvalCurrentSourcesAndEscapePersistenceFailClosed(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.proposeEscapeEvalOn(ctx, tx, governance.EscapeEvalFact{})
		return e
	}), ErrInvalid)
	fact := governance.EscapeEvalFact{TenantID: 1, ProjectID: 99, ID: 901, WorkItemID: 700, Title: "Human rejection", Snapshot: []byte(`{}`)}
	_, _, e := m.proposeEscapeEvalOn(ctx, db, fact)
	require.ErrorIs(t, e, ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, created, e := m.proposeEscapeEvalOn(ctx, tx, fact)
		require.Zero(t, id)
		require.False(t, created)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	injected := errors.New("escape persistence unavailable")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("escape-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.EvalCases.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.proposeEscapeEvalOn(ctx, tx, fact); return e }), injected)
	require.NoError(t, db.Callback().Create().Remove("escape-failure"))
	files := m.evalFiles
	m.evalFiles = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, draft); return e }), ErrUnavailable)
	m.evalFiles = files
	scope := m.scope
	m.scope.AuthorizeOn = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, draft); return e }), ErrUnavailable)
	m.scope = scope
	created := addEvalFixture(t, m, db, ctx, draft)
	audit := m.audit
	m.audit = nil
	title := "Changed"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchEvalCaseOn(ctx, tx, created.ID, 1, EvalCasePatch{Title: &title})
		return e
	}), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
		return e
	}), ErrUnavailable)
	m.audit = audit
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: "missing", Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
		return e
	}), ErrNotFound)
}

func TestEscapeLearningRetiresOnlyOldSeedCases(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	manual := addEvalFixture(t, m, db, ctx, draft)
	var source model.EvalCase
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("id=?", manual.ID).Take(&source).Error)
	seeds := make([]*model.EvalCase, 50)
	for i := range seeds {
		snapshot := source
		id, e := m.ids.Next()
		require.NoError(t, e)
		snapshot.ID = guid.ID(id)
		snapshot.Origin = "SEED"
		seeds[i] = &snapshot
	}
	require.NoError(t, db.Table(m.names.EvalCases.String()).Create(seeds).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, created, e := m.proposeEscapeEvalOn(ctx, tx, governance.EscapeEvalFact{TenantID: 1, ProjectID: 99, ID: 901, WorkItemID: 700, Title: "Human rejected", Snapshot: []byte(`{}`)})
		require.True(t, created)
		return e
	}))
	var active int64
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("active=?", true).Count(&active).Error)
	require.EqualValues(t, 50, active)
	var retained model.EvalCase
	require.NoError(t, db.Table(m.names.EvalCases.String()).Where("id=?", manual.ID).Take(&retained).Error)
	require.True(t, retained.Active)
}

func TestEvalIDSourceFailureRollsBackCaseAndRun(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	addEvalFixture(t, m, db, ctx, draft)
	injected := errors.New("identifier unavailable")
	m.ids = failingTemplateIDs{injected}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createEvalCaseOn(ctx, tx, draft); return e }), injected)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
		return e
	}), injected)
	var n int64
	require.NoError(t, db.Table(m.names.EvalRuns.String()).Count(&n).Error)
	require.Zero(t, n)
}
