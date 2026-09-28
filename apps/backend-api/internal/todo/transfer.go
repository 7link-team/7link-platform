package todo

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	todoi18n "7link.com/platform/internal/todo/i18n"
	"7link.com/platform/internal/todo/model"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/xuri/excelize/v2"
)

// Limits are the guardrails one transfer operation runs under. A zero field
// means that dimension does not constrain the request, so a new dimension can
// be added without revisiting every existing request type.
//
// They are guardrails protecting the server, not business knobs, so they are
// declared in code rather than exposed as configuration: letting the caller or
// the deployment raise them hands the protection to the party being protected
// against.
type Limits struct {
	MaxRows  int
	MaxBytes int64
}

// limited is implemented by every transfer request. A table-driven test asserts
// each one declares the dimensions that apply to it, because the type system
// cannot tell "deliberately unconstrained" from "forgotten".
type limited interface{ Limits() Limits }

func (ImportRequest) Limits() Limits { return Limits{MaxRows: 5000, MaxBytes: 10 << 20} }
func (ExportRequest) Limits() Limits { return Limits{MaxRows: 5000} }
func (TemplateRequest) Limits() Limits {
	// A template is generated, not received, and is a handful of rows.
	return Limits{}
}

// Format is the file format of a transfer.
type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
)

func (f Format) valid() bool { return f == FormatCSV || f == FormatXLSX }

func (f Format) contentType() string {
	if f == FormatXLSX {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

// utf8BOM prefixes exported CSV. Excel on Windows reads a CSV without it as the
// system code page and renders every non-ASCII character as mojibake.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// sheetName is the single worksheet an exported workbook carries.
const sheetName = "Sheet1"

// grid is a rendered table: the first row is the header, the rest are data.
type grid [][]string

// headerRow renders the header for cols in the request locale.
func headerRow(ctx context.Context, cols []column) []string {
	out := make([]string, len(cols))
	for index, c := range cols {
		out[index] = platformi18n.TContext(ctx, c.headerKey)
	}
	return out
}

// templateGrid builds the import template: the header, plus one sample row when
// asked for. The sample doubles as format documentation, which is more use to
// an operator than a paragraph describing the date layout.
func templateGrid(ctx context.Context, withSample bool) grid {
	return templateGridColumns(ctx, withSample, importableColumns())
}

func templateGridColumns(ctx context.Context, withSample bool, cols []column) grid {
	out := grid{headerRow(ctx, cols)}
	if withSample {
		row := make([]string, len(cols))
		for index, c := range cols {
			row[index] = c.sample
		}
		out = append(out, row)
	}
	return out
}

// exportGrid renders rows through the shared column definitions, so an export
// and the template agree on every header and every value format.
func exportGrid(ctx context.Context, cols []column, rows []*model.TodoRow) grid {
	out := make(grid, 0, len(rows)+1)
	out = append(out, headerRow(ctx, cols))
	for _, row := range rows {
		record := make([]string, len(cols))
		for index, c := range cols {
			record[index] = c.render(row)
		}
		out = append(out, record)
	}
	return out
}

// encode writes the grid in the requested format.
func (g grid) encode(format Format) ([]byte, error) {
	if format == FormatXLSX {
		return g.encodeXLSX()
	}
	return g.encodeCSV()
}

func (g grid) encodeCSV() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.Write(utf8BOM)
	writer := csv.NewWriter(&buffer)
	if err := writer.WriteAll(g); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (g grid) encodeXLSX() ([]byte, error) {
	file := excelize.NewFile()
	defer func() { _ = file.Close() }()

	// The stream writer keeps memory proportional to one row rather than to the
	// whole sheet.
	stream, err := file.NewStreamWriter(sheetName)
	if err != nil {
		return nil, err
	}
	for index, record := range g {
		cell, err := excelize.CoordinatesToCellName(1, index+1)
		if err != nil {
			return nil, err
		}
		values := make([]any, len(record))
		for i, value := range record {
			values[i] = value
		}
		if err := stream.SetRow(cell, values); err != nil {
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if _, err := file.WriteTo(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// decode reads an uploaded file into a grid.
func decodeGrid(reader io.Reader, format Format) (grid, error) {
	if format == FormatXLSX {
		return decodeXLSX(reader)
	}
	return decodeCSV(reader)
}

func decodeCSV(reader io.Reader) (grid, error) {
	buffered, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	// A file produced by this service, or saved by Excel, starts with a BOM.
	buffered = bytes.TrimPrefix(buffered, utf8BOM)

	parser := csv.NewReader(bytes.NewReader(buffered))
	// Rows may legitimately differ in length when trailing columns are blank.
	parser.FieldsPerRecord = -1
	records, err := parser.ReadAll()
	if err != nil {
		return nil, err
	}
	return grid(records), nil
}

func decodeXLSX(reader io.Reader) (table grid, err error) {
	// Spreadsheet bytes are untrusted. Keep parser panics inside this boundary
	// so malformed workbooks become ordinary unreadable-upload errors.
	defer func() {
		if recovered := recover(); recovered != nil {
			table = nil
			err = fmt.Errorf("todo: malformed workbook: %v", recovered)
		}
	}()
	file, err := excelize.OpenReader(reader)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	sheets := file.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("todo: workbook has no sheets")
	}
	records, err := file.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	return grid(records), nil
}

// RowError names the row, and where possible the column, a value was rejected
// in. Rows are numbered as the spreadsheet shows them, header included, so the
// number matches what the user sees on screen.
type RowError struct {
	Row     int    `json:"row"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

// ImportReport is the outcome of an import: either everything was written, or
// nothing was and every problem is listed.
type ImportReport struct {
	Imported int        `json:"imported"`
	Errors   []RowError `json:"errors,omitempty"`
}

// parseImport turns an uploaded grid into drafts.
//
// It validates every row before returning, and returns no drafts at all when
// any row is invalid. Partial writes would leave the user unable to tell
// whether to re-upload the whole file or only the failures — and re-uploading
// duplicates, because a task has no business key to match on.
func parseImport(ctx context.Context, table grid, limits Limits) ([]Draft, []RowError, error) {
	if len(table) == 0 {
		return nil, nil, huma.Error422UnprocessableEntity(platformi18n.TContext(ctx, todoi18n.ImportEmpty))
	}

	index, err := buildHeaderIndex()
	if err != nil {
		return nil, nil, err
	}

	header := table[0]
	// positions maps a column key to its position in this file.
	positions := map[string]int{}
	var unknown []string
	for position, cell := range header {
		key, known := index[normalizeHeader(cell)]
		switch {
		case !known:
			if strings.TrimSpace(cell) != "" {
				unknown = append(unknown, cell)
			}
		case columnByKey(key).importable:
			positions[key] = position
		}
		// A known but non-importable header — id, created_at, version — is
		// skipped in silence. That is what lets a full export be re-imported:
		// the server-maintained columns are simply ignored.
	}
	if len(unknown) > 0 {
		return nil, nil, huma.Error422UnprocessableEntity(
			platformi18n.TContext(ctx, todoi18n.ImportUnknownColumn) + ": " + strings.Join(unknown, ", "))
	}
	for _, c := range importableColumns() {
		if c.required {
			if _, present := positions[c.key]; !present {
				return nil, nil, huma.Error422UnprocessableEntity(
					platformi18n.TContext(ctx, todoi18n.ImportMissingColumn) + ": " + c.key)
			}
		}
	}

	body := table[1:]
	// Blank rows are skipped below, so they must not count against the limit
	// either: re-saving a sheet appends trailing empties, and rejecting a file
	// for rows that import nothing would be a limit the caller cannot see.
	filled := 0
	for _, record := range body {
		if !blank(record) {
			filled++
		}
	}
	if limits.MaxRows > 0 && filled > limits.MaxRows {
		return nil, nil, huma.Error422UnprocessableEntity(
			platformi18n.TContext(ctx, todoi18n.ImportTooManyRows))
	}

	var (
		drafts []Draft
		errs   []RowError
	)
	for offset, record := range body {
		// +2: the header occupies row 1, and spreadsheets number from 1.
		number := offset + 2
		if blank(record) {
			continue
		}
		draft := Draft{Status: StatusOpen, Priority: DefaultPriority, provided: []string{}}
		for _, c := range importableColumns() {
			position, present := positions[c.key]
			if present {
				draft.provided = append(draft.provided, c.key)
			}
			raw := ""
			if present && position < len(record) {
				raw = record[position]
			}
			if err := c.parse(raw, &draft); err != nil {
				var fieldErr FieldError
				if errors.As(err, &fieldErr) {
					errs = append(errs, RowError{Row: number, Column: c.key, Message: fieldErr.Message})
				} else {
					errs = append(errs, RowError{Row: number, Column: c.key, Message: err.Error()})
				}
			}
		}
		drafts = append(drafts, draft)
	}
	if len(errs) > 0 {
		return nil, errs, nil
	}
	if len(drafts) == 0 {
		return nil, nil, huma.Error422UnprocessableEntity(platformi18n.TContext(ctx, todoi18n.ImportEmpty))
	}
	return drafts, nil, nil
}

func columnByKey(key string) column {
	for _, c := range columns {
		if c.key == key {
			return c
		}
	}
	return column{}
}

func blank(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
