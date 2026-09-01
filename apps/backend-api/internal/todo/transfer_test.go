package todo

import (
	"bytes"
	"context"
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localeContext resolves messages in one specific locale, which is how the
// template renders headers for a caller who asked for that language.
func localeContext(code string) context.Context {
	return platformi18n.WithLocale(context.Background(), code)
}

func TestHeaderIndexHasNoCollisions(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc // the fixture registers the i18n catalog this index reads

	index, err := buildHeaderIndex()
	require.NoError(t, err, "two columns must never share a header in any locale")

	// Every column is reachable by its key and by each locale's header.
	for _, c := range columns {
		assert.Equal(t, c.key, index[normalizeHeader(c.key)], "the bare key must resolve")
		for _, locale := range platformi18n.Supported() {
			header := platformi18n.TContext(localeContext(locale.Code), c.headerKey)
			assert.Equal(t, c.key, index[normalizeHeader(header)],
				"%s header %q must resolve to %s", locale.Code, header, c.key)
		}
	}
}

func TestNormalizeHeaderFoldsCosmeticDifferences(t *testing.T) {
	// Spreadsheet editors introduce case, spacing and full-width punctuation
	// that carry no meaning.
	for _, raw := range []string{"Due at", "due_at", " DUE AT ", "due-at", "DueAt"} {
		assert.Equal(t, "dueat", normalizeHeader(raw), "for %q", raw)
	}
	assert.Equal(t, "", normalizeHeader("   "))
}

func TestTemplateCarriesOnlyImportableColumns(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc

	table := templateGrid(localeContext("en-US"), false)
	require.Len(t, table, 1, "without a sample the template is just a header")

	expected := make([]string, 0)
	for _, c := range importableColumns() {
		expected = append(expected, platformi18n.TContext(localeContext("en-US"), c.headerKey))
	}
	assert.Equal(t, expected, table[0])
	assert.NotContains(t, table[0], "ID", "server-maintained columns are not offered for import")
}

func TestTemplateSampleShowsAcceptedForms(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc

	table := templateGrid(localeContext("en-US"), true)
	require.Len(t, table, 2)
	// The sample row doubles as format documentation, so it must itself import.
	drafts, rowErrors, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.NoError(t, err)
	require.Empty(t, rowErrors, "the sample the template ships must be valid input")
	require.Len(t, drafts, 1)
}

// TestTemplateRoundTripsInEveryLocale is the reason the header index spans all
// locales: a user downloads the template in their language, fills it in, and
// uploads it unchanged.
func TestTemplateRoundTripsInEveryLocale(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc

	for _, locale := range platformi18n.Supported() {
		t.Run(locale.Code, func(t *testing.T) {
			ctx := localeContext(locale.Code)
			table := templateGrid(ctx, true)

			for _, format := range []Format{FormatCSV, FormatXLSX} {
				encoded, err := table.encode(format)
				require.NoError(t, err)

				decoded, err := decodeGrid(bytes.NewReader(encoded), format)
				require.NoError(t, err)

				drafts, rowErrors, err := parseImport(ctx, decoded, Limits{MaxRows: 10})
				require.NoError(t, err, "%s template must import as %s", locale.Code, format)
				require.Empty(t, rowErrors)
				assert.Len(t, drafts, 1)
			}
		})
	}
}

// TestExportRoundTripsBackIntoImport is what the importable flag buys: an
// export carries server-maintained columns for reference, and the importer
// ignores them instead of refusing the file.
func TestExportRoundTripsBackIntoImport(t *testing.T) {
	svc, _ := newTestService(t, "")
	seed(t, svc, draft("first"), draft("second"))

	page := listing(t, svc, "")
	ctx := localeContext("zh-CN")
	encoded, err := exportGrid(ctx, columns, page.Rows).encode(FormatCSV)
	require.NoError(t, err)

	decoded, err := decodeGrid(bytes.NewReader(encoded), FormatCSV)
	require.NoError(t, err)

	drafts, rowErrors, err := parseImport(ctx, decoded, Limits{MaxRows: 10})
	require.NoError(t, err)
	require.Empty(t, rowErrors)
	require.Len(t, drafts, 2, "a full export must import back without editing")
	assert.Equal(t, "second", drafts[0].Title)
}

func TestExportedCSVCarriesBOM(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	// Excel on Windows reads a BOM-less UTF-8 CSV as the system code page and
	// shows every non-ASCII header as mojibake.
	encoded, err := templateGrid(localeContext("zh-CN"), false).encode(FormatCSV)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(encoded, utf8BOM))
}

func TestImportRejectsUnknownColumn(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	table := grid{{"title", "nonsense"}, {"task", "x"}}
	_, _, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.Error(t, err, "a misspelled header must be reported, not silently dropped")
}

func TestImportRejectsMissingRequiredColumn(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	table := grid{{"status"}, {"open"}}
	_, _, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.Error(t, err)
}

func TestImportReportsEveryInvalidRowAndImportsNone(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	table := grid{
		{"title", "priority"},
		{"fine", "3"},
		{"", "3"},
		{"bad priority", "9"},
	}
	drafts, rowErrors, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.NoError(t, err)
	require.Nil(t, drafts, "no drafts are produced while any row is invalid")
	require.Len(t, rowErrors, 2)

	// Rows are numbered as the spreadsheet shows them, header included.
	assert.Equal(t, 3, rowErrors[0].Row)
	assert.Equal(t, "title", rowErrors[0].Column)
	assert.Equal(t, 4, rowErrors[1].Row)
	assert.Equal(t, "priority", rowErrors[1].Column)
}

func TestImportSkipsBlankRows(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	table := grid{{"title"}, {"kept"}, {"   "}, {""}}
	drafts, rowErrors, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.NoError(t, err)
	require.Empty(t, rowErrors)
	require.Len(t, drafts, 1, "trailing blank rows a spreadsheet leaves behind are not errors")
}

func TestImportEnforcesRowLimit(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	table := grid{{"title"}}
	for range 5 {
		table = append(table, []string{"task"})
	}
	_, _, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 3})
	require.Error(t, err)
}

func TestImportAllWritesAtomically(t *testing.T) {
	svc, _ := newTestService(t, "")
	imported, err := svc.ImportAll(t.Context(), []Draft{
		{Title: "one", Status: StatusOpen, Priority: 3},
		{Title: "two", Status: StatusDone, Priority: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, imported)

	page := listing(t, svc, "")
	assert.Equal(t, int64(2), page.Total)

	// A task imported as done carries its completion time, exactly as one
	// created through the API would.
	done := listing(t, svc, "status=eq:done")
	require.Len(t, done.Rows, 1)
	assert.NotNil(t, done.Rows[0].CompletedAt)
}

func TestExportColumnsSelection(t *testing.T) {
	all, err := exportColumns("")
	require.NoError(t, err)
	assert.Len(t, all, len(columns), "an empty selection exports everything")

	some, err := exportColumns("title, status")
	require.NoError(t, err)
	require.Len(t, some, 2)
	assert.Equal(t, "title", some[0].key)
	assert.Equal(t, "status", some[1].key, "columns keep their declared order, not the request's")

	_, err = exportColumns("title,nonsense")
	var fieldErr FieldError
	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "fields", fieldErr.Field)
}

func TestTransferLimitsAreDeclaredWhereTheyApply(t *testing.T) {
	// The type system cannot tell "deliberately unconstrained" from
	// "forgotten", so the intent is asserted here instead.
	for name, tc := range map[string]struct {
		request  limited
		wantRows bool
		wantSize bool
	}{
		"import":   {ImportRequest{}, true, true},
		"export":   {ExportRequest{}, true, false},
		"template": {TemplateRequest{}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			limits := tc.request.Limits()
			assert.Equal(t, tc.wantRows, limits.MaxRows > 0, "row limit")
			assert.Equal(t, tc.wantSize, limits.MaxBytes > 0, "size limit")
		})
	}
}

func TestParseTransferTimeAcceptsBothLayouts(t *testing.T) {
	// The template advertises the readable layout; an export writes it too. RFC
	// 3339 is accepted so a file assembled by a script also imports.
	for _, raw := range []string{"2026-12-31 18:00:00", "2026-12-31T18:00:00Z"} {
		parsed, err := parseTransferTime(raw)
		require.NoError(t, err, "for %q", raw)
		assert.Equal(t, 2026, parsed.Year())
	}
	_, err := parseTransferTime("31/12/2026")
	assert.Error(t, err)
}

func TestFormatValidation(t *testing.T) {
	assert.True(t, FormatCSV.valid())
	assert.True(t, FormatXLSX.valid())
	assert.False(t, Format("pdf").valid())
	assert.Contains(t, FormatXLSX.contentType(), "spreadsheetml")
	assert.Contains(t, FormatCSV.contentType(), "text/csv")
	assert.Contains(t, FormatCSV.contentType(), "utf-8")
}

func TestParseFormatDefaultsAndRejects(t *testing.T) {
	// An omitted format is the common case for a browser link.
	format, err := parseFormat("")
	require.NoError(t, err)
	assert.Equal(t, FormatCSV, format)

	format, err = parseFormat(" XLSX ")
	require.NoError(t, err)
	assert.Equal(t, FormatXLSX, format, "case and padding are cosmetic")

	_, err = parseFormat("pdf")
	assert.Error(t, err)
}

func TestRowErrorDetailCarriesEveryFailure(t *testing.T) {
	// The detail travels through huma's error channel, so the envelope keeps
	// its shape while still naming every rejected row.
	detail := &rowErrorDetail{Errors: []RowError{{Row: 3, Column: "title", Message: "must not be empty"}}}
	assert.Equal(t, "import rows are invalid", detail.Error())
	require.Len(t, detail.Errors, 1)
	assert.Equal(t, 3, detail.Errors[0].Row)
}

func TestDecodeRejectsMalformedWorkbook(t *testing.T) {
	_, err := decodeGrid(bytes.NewReader([]byte("not a workbook")), FormatXLSX)
	assert.Error(t, err)
}

func TestImportAcceptsExportedTimeFormat(t *testing.T) {
	svc, _ := newTestService(t, "")
	_ = svc
	// The exporter writes the readable layout; a script may write RFC 3339.
	table := grid{
		{"title", "due_at"},
		{"readable", "2026-12-31 18:00:00"},
		{"rfc3339", "2026-12-31T18:00:00Z"},
	}
	drafts, rowErrors, err := parseImport(localeContext("en-US"), table, Limits{MaxRows: 10})
	require.NoError(t, err)
	require.Empty(t, rowErrors)
	require.Len(t, drafts, 2)
	require.NotNil(t, drafts[0].DueAt)
	assert.Equal(t, drafts[0].DueAt.UTC(), drafts[1].DueAt.UTC(), "both layouts mean the same instant")
}

func TestImportAllReportsUnavailableDatasource(t *testing.T) {
	svc := closedService(t)
	_, err := svc.ImportAll(t.Context(), []Draft{{Title: "task", Status: StatusOpen, Priority: 3}})
	assert.Error(t, err)

	empty, err := svc.ImportAll(t.Context(), nil)
	require.NoError(t, err, "an empty import is a no-op, not a failure")
	assert.Zero(t, empty)
}
