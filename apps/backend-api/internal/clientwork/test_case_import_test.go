package clientwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testCaseCSV = "title,criterion,preconditions,steps,expected,priority\n\"Login, quoted\",C1,\"Signed in\nwith session\",\"Click \"\"go\"\"\",Dashboard,P1\nUnknown,not-mapped,,Observe,Expected,\n"

func TestTestCaseCSVStrictParsingAndPhysicalRows(t *testing.T) {
	scope := delivery.ClientRequirement{Criteria: []delivery.ClientCriterion{{ID: 20, Code: "C1", Text: "Works"}}}
	out := parseTestCaseCSV("\ufeff"+testCaseCSV, 11, 10, scope)
	require.Empty(t, out.Errors)
	require.Len(t, out.Rows, 2)
	require.Equal(t, 2, out.Rows[0].Row)
	require.Equal(t, 4, out.Rows[1].Row)
	require.Equal(t, "Login, quoted", out.Rows[0].Case.Title)
	require.Equal(t, "Signed in\nwith session", out.Rows[0].Case.Preconditions)
	require.Equal(t, `Click "go"`, out.Rows[0].Case.Steps)
	require.EqualValues(t, 20, *out.Rows[0].Case.CriterionID)
	require.True(t, out.Rows[1].Unclassified)
	require.Equal(t, "P2", out.Rows[1].Case.Priority)
	for name, raw := range map[string]string{"empty": "", "malformed": strings.Replace(testCaseCSV, `"Click ""go"""`, `"unclosed`, 1), "headers": "title,title,x,y,z,q\na,b,c,d,e,f", "columns": "title,criterion,preconditions,steps,expected,priority\na,b,c", "required": "title,criterion,preconditions,steps,expected,priority\n,C1,,Step,Expected,P2", "priority": strings.Replace(testCaseCSV, "P1", "P9", 1), "large": strings.Repeat("x", testCaseImportMaxBytes+1), "utf8": string([]byte{0xff}), "rows": "title,criterion,preconditions,steps,expected,priority\n" + strings.Repeat("Case,C1,,Step,Expected,P2\n", 501)} {
		t.Run(name, func(t *testing.T) {
			bad := parseTestCaseCSV(raw, 11, 10, scope)
			require.NotEmpty(t, bad.Errors)
			require.Zero(t, bad.Imported)
		})
	}
	translated := parseTestCaseCSV("等级,预期,步骤,前置条件,验收标准,标题\nP3,Done,Run,,Works,中文", 11, 10, scope)
	require.Empty(t, translated.Errors)
	require.Equal(t, "priority", translated.Columns[0])
	require.Equal(t, "中文", translated.Rows[0].Case.Title)
	scope.Criteria = append(scope.Criteria, delivery.ClientCriterion{ID: 21, Code: "C1"})
	ambiguous := parseTestCaseCSV(testCaseCSV, 11, 10, scope)
	require.True(t, ambiguous.Rows[0].Unclassified)
}

func TestTestCaseImportAtomicReplayFrozenMetadataAndRevocation(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	count := func(table string) int64 { var n int64; require.NoError(t, db.Table(table).Count(&n).Error); return n }
	preview, err := m.importTestCases(ctx, 3, 11, 10, testCaseCSV, true, "preview")
	require.NoError(t, err)
	require.True(t, preview.Body.Data.Preview)
	require.Zero(t, count("g_clientwork_test_cases"))
	invalid, err := m.importTestCases(ctx, 3, 11, 10, strings.Replace(testCaseCSV, "P1", "P9", 1), false, "invalid")
	require.NoError(t, err)
	require.NotEmpty(t, invalid.Body.Data.Errors)
	require.Zero(t, count("g_clientwork_test_cases"))
	beforeAudit, beforeEvents := count("audit_probe"), count("g_domain_events")
	auditCalls := 0
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		auditCalls++
		if auditCalls == 2 {
			return errors.New("audit failed")
		}
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", e.EventType).Error
	}
	_, err = m.importTestCases(ctx, 3, 11, 10, testCaseCSV, false, "atomic")
	require.Error(t, err)
	require.Zero(t, count("g_clientwork_test_cases"))
	require.Equal(t, beforeAudit, count("audit_probe"))
	require.Equal(t, beforeEvents, count("g_domain_events"))
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", e.EventType).Error
	}
	result, err := m.importTestCases(ctx, 3, 11, 10, testCaseCSV, false, "atomic")
	require.NoError(t, err)
	require.Equal(t, 2, result.Body.Data.Imported)
	require.False(t, result.Body.Data.Preview)
	require.EqualValues(t, 2, count("g_clientwork_test_cases"))
	retry, err := m.importTestCases(ctx, 3, 11, 10, testCaseCSV, false, "atomic")
	require.NoError(t, err)
	require.Equal(t, "true", retry.Replayed)
	require.Equal(t, result.Body.Data, retry.Body.Data)
	require.EqualValues(t, 2, count("g_clientwork_test_cases"))
	_, err = m.importTestCases(ctx, 3, 11, 10, strings.Replace(testCaseCSV, "Dashboard", "Other", 1), false, "atomic")
	require.Error(t, err)
	first := result.Body.Data.Rows[0]
	var plan Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = m.createPlanOn(ctx, tx, 3, TestPlanBody{Title: "Frozen", CaseIDs: []guid.ID{*first.CaseID}})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		body := *first.Case
		body.Preconditions = "New precondition"
		body.Priority = "P0"
		_, err := m.writeCaseOn(ctx, tx, 3, *first.CaseID, 1, body)
		return err
	}))
	frozen, err := m.get_test_planHTTP(ctx, &ObjectInput{EntityID: "3", ID: plan.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "Signed in\nwith session", frozen.Body.Data.Cases[0].Preconditions)
	require.Equal(t, "P1", frozen.Body.Data.Cases[0].Priority)
	require.Equal(t, 1, frozen.Body.Data.Cases[0].Version)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		suite, err := m.requirementCasesOn(ctx, tx, 1, 3, []guid.ID{11})
		require.Len(t, suite, 1, "unclassified case must not enter acceptance coverage")
		require.Equal(t, "P0", suite[0].Priority)
		return err
	}))
	projects.deny = true
	_, err = m.importTestCases(ctx, 3, 11, 10, testCaseCSV, false, "atomic")
	require.Error(t, err, "replay rechecks current authority")
	projects.deny = false
	for _, actor := range []guid.ID{4, 7, 9} {
		_, err = m.importTestCases(testContext(t, actor), 3, 11, 10, testCaseCSV, false, fmt.Sprint(actor))
		require.Error(t, err)
	}
	_, err = m.importTestCases(ctx, 3, 99, 10, testCaseCSV, false, "foreign-work")
	require.Error(t, err)
	_, err = m.importTestCases(ctx, 3, 11, 99, testCaseCSV, false, "foreign-requirement")
	require.Error(t, err)
}

func TestTestCaseImportMultipartAndOpenAPI(t *testing.T) {
	m, _, _, _ := harness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "csv-test", Method: "POST", Path: entityBase + "/test-case-imports", MaxBodyBytes: testCaseImportMaxBytes + (1 << 20)}, func(_ context.Context, in *TestCaseImportInput) (*TestCaseImportOutput, error) {
		return m.testCaseImportHTTP(testContext(t, 2), in)
	})
	op := api.OpenAPI().Paths[entityBase+"/test-case-imports"].Post
	require.NotNil(t, op.RequestBody.Content["multipart/form-data"])
	for i, contentType := range []string{"application/octet-stream", "text/csv"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="file"; filename="cases.csv"`}, "Content-Type": {contentType}})
		require.NoError(t, err)
		_, err = part.Write([]byte(testCaseCSV))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		response := api.Post("/entities/3/test-case-imports?work_item_id=11&requirement_id=10", "Content-Type: "+writer.FormDataContentType(), fmt.Sprintf("Idempotency-Key: multipart-%d", i), bytes.NewReader(body.Bytes()))
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"imported":2`)
	}
}
