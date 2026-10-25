package clientwork

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const testCaseImportMaxBytes = 2 << 20

var testCaseCSVColumns = []string{"title", "criterion", "preconditions", "steps", "expected", "priority"}

type TestCaseImportIssue struct {
	Row    int    `json:"row"`
	Column string `json:"column"`
	Code   string `json:"code"`
}
type TestCaseImportRow struct {
	Row          int           `json:"row"`
	Source       []string      `json:"source"`
	Case         *TestCaseBody `json:"case,omitempty"`
	Unclassified bool          `json:"unclassified"`
	CaseID       *guid.ID      `json:"case_id,omitempty"`
	Version      *int          `json:"version,omitempty"`
}
type TestCaseImportReport struct {
	Imported     int                   `json:"imported"`
	Unclassified int                   `json:"unclassified"`
	Preview      bool                  `json:"preview"`
	Columns      []string              `json:"columns"`
	Rows         []TestCaseImportRow   `json:"rows"`
	Errors       []TestCaseImportIssue `json:"errors"`
}
type TestCaseImportInput struct {
	EntityID      string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key           string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	WorkItemID    string `query:"work_item_id" pattern:"^[1-9][0-9]*$" required:"true" maxLength:"19"`
	RequirementID string `query:"requirement_id" pattern:"^[1-9][0-9]*$" required:"true" maxLength:"19"`
	Preview       bool   `query:"preview" default:"false"`
	RawBody       huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true"`
	}]
}
type TestCaseImportOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[TestCaseImportReport]
}

func (m *Module) registerTestCaseImportHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_case.import", Method: "POST", Path: entityBase + "/test-case-imports", Summary: "Preview or atomically import Testhub CSV test cases", Description: "UTF-8 CSV, optional BOM, six named columns in English or Chinese. Maximum 2 MiB and 500 rows. Quoted commas and line breaks are supported. Unknown or ambiguous criteria remain unclassified and do not count toward acceptance coverage. Preview and invalid reports write no cases; successful imports are atomic and idempotent.", Tags: []string{"entities"}, MaxBodyBytes: testCaseImportMaxBytes + (1 << 20), Errors: []int{401, 403, 404, 409, 413, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "channel", Verb: "read"}, m.testCaseImportHTTP)
}

// parseTestCaseCSV uses the standard quoted-CSV parser, matching the existing
// transfer implementation. Source cells are retained for preview/failure export.
func parseTestCaseCSV(raw string, work, requirement guid.ID, scope delivery.ClientRequirement) TestCaseImportReport {
	out := TestCaseImportReport{Preview: true, Columns: append([]string{}, testCaseCSVColumns...), Rows: []TestCaseImportRow{}, Errors: []TestCaseImportIssue{}}
	fail := func(row int, column, code string) {
		out.Errors = append(out.Errors, TestCaseImportIssue{row, column, code})
	}
	if len(raw) > testCaseImportMaxBytes {
		fail(0, "file", "LIMIT")
		return out
	}
	if !utf8.ValidString(raw) {
		fail(0, "file", "UTF8")
		return out
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff")))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		fail(1, "file", "CSV")
		return out
	}
	aliases := map[string]string{"标题": "title", "验收标准": "criterion", "前置条件": "preconditions", "步骤": "steps", "预期": "expected", "等级": "priority"}
	indexes := map[string]int{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		if c, ok := aliases[name]; ok {
			name = c
		}
		header[i] = name
		for _, known := range testCaseCSVColumns {
			if name == known {
				if _, exists := indexes[name]; exists {
					fail(1, name, "HEADER")
				}
				indexes[name] = i
			}
		}
	}
	if len(header) != 6 || len(indexes) != 6 || len(out.Errors) > 0 {
		fail(1, "file", "HEADER")
		return out
	}
	out.Columns = header
	for {
		cells, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		line := len(out.Rows) + 2
		if len(cells) > 0 {
			line, _ = r.FieldPos(0)
		}
		if readErr != nil {
			var pe *csv.ParseError
			if errors.As(readErr, &pe) {
				line = pe.Line
			}
			fail(line, "file", "CSV")
			break
		}
		if len(out.Rows) == 500 {
			fail(line, "file", "LIMIT")
			break
		}
		row := TestCaseImportRow{Row: line, Source: cells}
		out.Rows = append(out.Rows, row)
		if len(cells) != 6 {
			fail(line, "file", "COLUMNS")
			continue
		}
		get := func(name string) string { return cells[indexes[name]] }
		priority := strings.TrimSpace(get("priority"))
		if priority == "" {
			priority = "P2"
		}
		in := TestCaseBody{Title: get("title"), Steps: get("steps"), Expected: get("expected"), WorkItemID: work, RequirementID: &requirement, CriterionSource: get("criterion"), Preconditions: get("preconditions"), Priority: priority}
		for _, field := range []struct {
			name, value string
			limit       int
		}{{"title", in.Title, 200}, {"steps", in.Steps, 8000}, {"expected", in.Expected, 2000}} {
			if strings.TrimSpace(field.value) == "" {
				fail(line, field.name, "REQUIRED")
			} else if !validText(field.value, field.limit) {
				fail(line, field.name, "FIELD_LENGTH")
			}
		}
		if utf8.RuneCountInString(in.Preconditions) > 8000 {
			fail(line, "preconditions", "FIELD_LENGTH")
		}
		if utf8.RuneCountInString(in.CriterionSource) > 2000 {
			fail(line, "criterion", "FIELD_LENGTH")
		}
		if len(priority) != 2 || priority[0] != 'P' || priority[1] < '0' || priority[1] > '5' {
			fail(line, "priority", "PRIORITY")
		}
		needle := strings.TrimSpace(in.CriterionSource)
		matches := []guid.ID{}
		for _, c := range scope.Criteria {
			if needle != "" && (needle == c.ID.String() || needle == c.Code || needle == c.Text) {
				matches = append(matches, c.ID)
			}
		}
		if len(matches) == 1 {
			in.CriterionID = &matches[0]
		} else {
			out.Unclassified++
			row.Unclassified = true
		}
		row.Case = &in
		out.Rows[len(out.Rows)-1] = row
	}
	if len(out.Rows) == 0 && len(out.Errors) == 0 {
		fail(2, "file", "EMPTY")
	}
	return out
}

func (m *Module) testCaseImportHTTP(ctx context.Context, in *TestCaseImportInput) (*TestCaseImportOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	work, err := guid.RequireID(in.WorkItemID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	requirement, err := guid.RequireID(in.RequirementID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	file := in.RawBody.Data().File
	raw, err := io.ReadAll(io.LimitReader(file, testCaseImportMaxBytes+1))
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.importTestCases(ctx, p, work, requirement, string(raw), in.Preview, in.Key)
}

func (m *Module) importTestCases(ctx context.Context, p, work, requirement guid.ID, raw string, preview bool, key string) (*TestCaseImportOutput, error) {
	var report TestCaseImportReport
	// Preview and validation outcomes use exactly the same current source fence as
	// a write; parsing a file never grants project or requirement authority.
	err := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, p, "test_case_manage", false); err != nil {
			return err
		}
		scope, err := m.projects.TestCaseScopeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, work, requirement)
		if err != nil {
			return err
		}
		report = parseTestCaseCSV(raw, work, requirement, scope)
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if preview || len(report.Errors) > 0 {
		return &TestCaseImportOutput{Status: 200, Body: respx.OK(ctx, report).Body}, nil
	}
	request := struct {
		Work, Requirement guid.ID
		CSV               string
	}{work, requirement, raw}
	result, err := m.command(ctx, p, "clientwork.test_case.import", "POST", entityBase+"/test-case-imports", key, 200, request, "test_case_manage", false, nil, func(tx *gorm.DB) (Receipt, error) {
		// Re-resolve matching inside the write transaction; a preview is not a lease.
		scope, err := m.projects.TestCaseScopeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, work, requirement)
		if err != nil {
			return Receipt{}, err
		}
		actual := parseTestCaseCSV(raw, work, requirement, scope)
		if len(actual.Errors) > 0 {
			return Receipt{}, ErrInvalid
		}
		actual.Preview = false
		var first Receipt
		for i := range actual.Rows {
			row := &actual.Rows[i]
			receipt, err := m.writeCaseOn(ctx, tx, p, 0, 0, *row.Case)
			if err != nil {
				return Receipt{}, err
			}
			row.CaseID = &receipt.ID
			row.Version = &receipt.Version
			if i == 0 {
				first = receipt
			}
			actual.Imported++
		}
		first.TestCaseImport = &actual
		return first, nil
	})
	if err != nil {
		return nil, err
	}
	if result.Body.Data.TestCaseImport == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	return &TestCaseImportOutput{Status: result.Status, Replayed: result.Replayed, Body: respx.OK(ctx, *result.Body.Data.TestCaseImport).Body}, nil
}
