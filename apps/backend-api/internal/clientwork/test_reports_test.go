package clientwork

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func reportFixture(t *testing.T) (*Module, *gorm.DB, *testProjects, Receipt, Receipt) {
	t.Helper()
	m, db, p, _ := harness(t)
	ctx := testContext(t, 2)
	var c, plan Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		c, err = m.writeCaseOn(ctx, tx, 3, 0, 0, TestCaseBody{Title: "Frozen title", Steps: "Private frozen steps", Expected: "Frozen expected", WorkItemID: 11})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = m.createPlanOn(ctx, tx, 3, TestPlanBody{Title: "Frozen plan", CaseIDs: []guid.ID{c.ID}})
		return err
	}))
	m.forecastQueue = &packageTestQueue{}
	return m, db, p, c, plan
}

func TestSavedTestReportFreezesLatestExactVersionsAndBuildsImmutablePDF(t *testing.T) {
	m, db, projects, c, plan := reportFixture(t)
	ctx := testContext(t, 2)
	for _, result := range []string{"FAILED", "PASSED"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.recordRunOn(ctx, tx, 3, plan.ID, 1, TestRunBody{CaseID: c.ID, CaseVersion: 1, Result: result, Evidence: "Observed " + result})
			return err
		}))
	}
	created, err := m.createTestReportHTTP(ctx, &CreateInput[TestReportBody]{EntityID: "3", Key: "saved-report", Body: TestReportBody{Title: "Saved report", PlanIDs: []guid.ID{plan.ID}}})
	require.NoError(t, err)
	repeated, err := m.createTestReportHTTP(ctx, &CreateInput[TestReportBody]{EntityID: "3", Key: "saved-report", Body: TestReportBody{Title: "Saved report", PlanIDs: []guid.ID{plan.ID}}})
	require.NoError(t, err)
	require.Equal(t, created.Body.Data, repeated.Body.Data)
	queue := m.forecastQueue.(*packageTestQueue)
	require.Len(t, queue.requests, 1)
	raw, err := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, err)
	job := jobs.Job{ID: 501, TenantID: 1, CreatedBy: 2, Payload: raw}
	// Report request freezes the observed pass; subsequent results do not rewrite it.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.recordRunOn(ctx, tx, 3, plan.ID, 1, TestRunBody{CaseID: c.ID, CaseVersion: 1, Result: "FAILED", Evidence: "Later observation"})
		return err
	}))
	var pdf []byte
	attached := 0
	m.bundleFiles.Stage = func(_ context.Context, p guid.ID, purpose string, r io.Reader, size int64) (*files.PreparedDeliveryBundle, error) {
		require.Equal(t, guid.ID(3), p)
		require.Equal(t, testReportPurpose, purpose)
		var err error
		pdf, err = io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, size, int64(len(pdf)))
		return &files.PreparedDeliveryBundle{}, nil
	}
	m.bundleFiles.AttachOn = func(_ context.Context, tx *gorm.DB, _ *files.PreparedDeliveryBundle, id guid.ID) (files.PinnedResource, error) {
		attached++
		return files.PinnedResource{BindingID: 701, FileID: 702, VersionID: 703, ResourceID: id, Purpose: testReportPurpose, SHA256: packageSHA(pdf), Size: int64(len(pdf))}, nil
	}
	require.NoError(t, m.testReportHandler().Run(ctx, job))
	require.NoError(t, m.testReportHandler().Run(ctx, job))
	require.Equal(t, 1, attached)
	out, err := m.getTestReportHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.NoError(t, err)
	require.Equal(t, TestReportSummary{Total: 1, Passed: 1, HumanRecorded: 1}, out.Body.Data.Summary)
	require.Equal(t, "BUILT", out.Body.Data.State)
	require.Equal(t, packageSHA(pdf), *out.Body.Data.ContentHash)
	require.Equal(t, guid.ID(702), *out.Body.Data.FileID)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.NotContains(t, string(pdf), "Private frozen steps")
	m.files.OpenOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{SHA256: packageSHA(pdf), MIME: "application/pdf", Name: "test-report.pdf", Size: int64(len(pdf))}, Reader: io.NopCloser(bytes.NewReader(pdf))}, nil
	}
	content, err := m.testReportContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.NoError(t, err)
	require.NotNil(t, content)
	projects.deny = true
	_, err = m.getTestReportHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.Error(t, err)
	_, err = m.testReportContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.Error(t, err)
	_, err = m.createTestReportHTTP(ctx, &CreateInput[TestReportBody]{EntityID: "3", Key: "saved-report", Body: TestReportBody{Title: "Saved report", PlanIDs: []guid.ID{plan.ID}}})
	require.Error(t, err)
	require.Error(t, m.testReportHandler().Run(ctx, job))
}

func TestTestReportRejectsInvalidSourcesAndRollsBackFailedCompletion(t *testing.T) {
	for _, fault := range []string{"duplicate", "too_many", "missing", "hidden", "foreign", "snapshot", "stage", "attach", "integrity", "event", "revoked"} {
		t.Run(fault, func(t *testing.T) {
			m, db, projects, _, plan := reportFixture(t)
			ctx := testContext(t, 2)
			in := TestReportBody{Title: "Report", PlanIDs: []guid.ID{plan.ID}}
			switch fault {
			case "duplicate":
				in.PlanIDs = append(in.PlanIDs, plan.ID)
			case "too_many":
				in.PlanIDs = make([]guid.ID, 16)
			case "missing":
				in.PlanIDs = []guid.ID{900}
			case "hidden", "snapshot":
				row, err := m.planOn(ctx, db, 3, plan.ID)
				require.NoError(t, err)
				var cases []TestCaseView
				require.NoError(t, json.Unmarshal([]byte(row.Cases), &cases))
				cases[0].IsHiddenAcceptance = true
				raw, _ := json.Marshal(cases)
				if fault == "snapshot" {
					raw = []byte("{")
				}
				q := query.BindTestPlan(db, m.names)
				_, err = q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).Update(q.Cases, string(raw))
				require.NoError(t, err)
			case "foreign":
				q := query.BindTestPlan(db, m.names)
				_, err := q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).Update(q.ProjectID, 99)
				require.NoError(t, err)
			}
			created, err := m.createTestReportHTTP(ctx, &CreateInput[TestReportBody]{EntityID: "3", Key: "failure-report", Body: in})
			if slices.Contains([]string{"duplicate", "too_many", "missing", "hidden", "foreign", "snapshot"}, fault) {
				require.Error(t, err)
				var count int64
				require.NoError(t, db.Table(m.names.TestReport.String()).Count(&count).Error)
				require.Zero(t, count)
				return
			}
			require.NoError(t, err)
			var pdf []byte
			m.bundleFiles.Stage = func(_ context.Context, _ guid.ID, _ string, r io.Reader, _ int64) (*files.PreparedDeliveryBundle, error) {
				if fault == "stage" {
					return nil, ErrUnavailable
				}
				pdf, _ = io.ReadAll(r)
				return &files.PreparedDeliveryBundle{}, nil
			}
			m.bundleFiles.AttachOn = func(_ context.Context, tx *gorm.DB, _ *files.PreparedDeliveryBundle, id guid.ID) (files.PinnedResource, error) {
				if fault == "attach" {
					return files.PinnedResource{}, ErrUnavailable
				}
				hash := packageSHA(pdf)
				if fault == "integrity" {
					hash = strings.Repeat("0", 64)
				}
				require.NoError(t, tx.Exec("INSERT INTO baseline_probe VALUES (701)").Error)
				return files.PinnedResource{BindingID: 701, FileID: 702, VersionID: 703, ResourceID: id, Purpose: testReportPurpose, SHA256: hash, Size: int64(len(pdf))}, nil
			}
			if fault == "event" {
				m.events = failPublisher{}
			}
			if fault == "revoked" {
				projects.deny = true
			}
			err = m.buildTestReport(ctx, testReportJobBody{3, created.Body.Data.ID, 2}, 501)
			require.Error(t, err)
			row, err := m.reportOn(ctx, db, 3, created.Body.Data.ID)
			require.NoError(t, err)
			require.Equal(t, "PENDING", row.State)
			require.Nil(t, row.ContentHash)
			require.Nil(t, row.BindingID)
			var count int64
			require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

type failureDefectsFunc func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TestFailureDefect) (guid.ID, error)

func (f failureDefectsFunc) CreateOn(ctx context.Context, tx *gorm.DB, t, p guid.ID, in delivery.TestFailureDefect) (guid.ID, error) {
	return f(ctx, tx, t, p, in)
}

func TestFailedRunDefectConversionFreezesSourceAndRollsBackTogether(t *testing.T) {
	for _, fault := range []string{"none", "provider", "run_write", "event", "passed", "both"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _, c, plan := reportFixture(t)
			ctx := testContext(t, 2)
			calls := 0
			m.testDefects = failureDefectsFunc(func(_ context.Context, tx *gorm.DB, tenant, p guid.ID, in delivery.TestFailureDefect) (guid.ID, error) {
				calls++
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(3), p)
				require.Equal(t, plan.ID, in.PlanID)
				require.Equal(t, 1, in.PlanVersion)
				require.Equal(t, c.ID, in.CaseID)
				require.Equal(t, 1, in.CaseVersion)
				require.Positive(t, in.RunID)
				require.Equal(t, "Private frozen steps", in.Steps)
				require.Equal(t, "Frozen expected", in.Expected)
				require.Equal(t, "Actual failure", in.Observed)
				if fault == "provider" {
					return 0, ErrForbidden
				}
				return 701, tx.Exec("INSERT INTO baseline_probe VALUES (701)").Error
			})
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.writeCaseOn(ctx, tx, 3, c.ID, 1, TestCaseBody{Title: "Changed", Steps: "Mutable steps", Expected: "Mutable expected", WorkItemID: 11})
				return err
			}))
			if fault == "run_write" {
				require.NoError(t, db.Exec("CREATE TRIGGER test_run_failure BEFORE INSERT ON g_clientwork_test_runs BEGIN SELECT RAISE(ABORT,'unavailable'); END").Error)
			}
			if fault == "event" {
				m.events = failPublisher{}
			}
			body := TestRunBody{CaseID: c.ID, CaseVersion: 1, Result: "FAILED", Evidence: "Actual failure", Defect: &delivery.TestFailureDefect{Title: "Actual bug", Severity: "NORMAL", Priority: "MEDIUM", AccountablePrincipalID: 2}}
			if fault == "passed" {
				body.Result = "PASSED"
			}
			if fault == "both" {
				id := guid.ID(99)
				body.DefectID = &id
			}
			var receipt Receipt
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = m.recordRunOn(ctx, tx, 3, plan.ID, 1, body)
				return err
			})
			var defects, runs int64
			require.NoError(t, db.Table("baseline_probe").Count(&defects).Error)
			require.NoError(t, db.Table(m.names.TestRun.String()).Count(&runs).Error)
			if fault == "none" {
				require.NoError(t, err)
				require.EqualValues(t, 1, defects)
				require.EqualValues(t, 1, runs)
				q := query.BindTestRun(db, m.names)
				row, err := q.WithContext(ctx).Where(q.ID.Eq(receipt.ID)).First()
				require.NoError(t, err)
				require.Equal(t, guid.ID(701), *row.DefectID)
			} else {
				require.Error(t, err)
				require.Zero(t, defects)
				require.Zero(t, runs)
			}
			if fault == "passed" || fault == "both" {
				require.Zero(t, calls)
			}
		})
	}
	require.Error(t, (&Module{}).testReportHandler().Run(t.Context(), jobs.Job{}))
	require.ErrorIs(t, (&Module{}).buildTestReport(t.Context(), testReportJobBody{}, 0), ErrUnavailable)
	_, err := testReportView(&model.TestReport{Snapshot: "{"})
	require.Error(t, err)
}

func TestTestReportExhaustedJobRecordsHonestFailure(t *testing.T) {
	m, db, _, _, plan := reportFixture(t)
	ctx := testContext(t, 2)
	created, err := m.createTestReportHTTP(ctx, &CreateInput[TestReportBody]{EntityID: "3", Key: "exhausted", Body: TestReportBody{Title: "Report", PlanIDs: []guid.ID{plan.ID}}})
	require.NoError(t, err)
	queue := m.forecastQueue.(*packageTestQueue)
	payload, err := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, err)
	m.bundleFiles.Stage = func(context.Context, guid.ID, string, io.Reader, int64) (*files.PreparedDeliveryBundle, error) {
		return nil, ErrUnavailable
	}
	m.bundleFiles.AttachOn = func(context.Context, *gorm.DB, *files.PreparedDeliveryBundle, guid.ID) (files.PinnedResource, error) {
		t.Fatal("failed stage cannot attach")
		return files.PinnedResource{}, nil
	}
	err = m.testReportHandler().Run(ctx, jobs.Job{ID: 501, TenantID: 1, CreatedBy: 2, Payload: payload, Attempt: 4, MaxAttempts: 4})
	require.Error(t, err)
	row, err := m.reportOn(ctx, db, 3, created.Body.Data.ID)
	require.NoError(t, err)
	require.Equal(t, "FAILED", row.State)
	require.Nil(t, row.ContentHash)
}
