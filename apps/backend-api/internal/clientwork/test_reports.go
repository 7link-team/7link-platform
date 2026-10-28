package clientwork

import (
	activityi18n "7link.com/platform/internal/clientwork/i18n"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const testReportJob = "clientwork.test_report"
const testReportPurpose = "test_report"

type TestReportBody struct {
	Title   string    `json:"title" minLength:"1" maxLength:"200"`
	PlanIDs []guid.ID `json:"plan_ids" minItems:"1" maxItems:"15"`
}
type TestReportSummary struct {
	Total         int `json:"total"`
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`
	Blocked       int `json:"blocked"`
	Unexecuted    int `json:"unexecuted"`
	HumanRecorded int `json:"human_recorded"`
}
type TestReportView struct {
	ID          guid.ID           `json:"id"`
	Title       string            `json:"title"`
	State       string            `json:"state" enum:"PENDING,BUILT,FAILED"`
	JobID       guid.ID           `json:"job_id"`
	FileID      *guid.ID          `json:"file_id"`
	VersionID   *guid.ID          `json:"version_id"`
	ContentHash *string           `json:"content_hash"`
	Summary     TestReportSummary `json:"summary"`
	CreatedAt   time.Time         `json:"created_at"`
	BuiltAt     *time.Time        `json:"built_at"`
}
type testReportPlan struct {
	Plan TestPlanView  `json:"plan"`
	Runs []TestRunView `json:"runs"`
}
type testReportSnapshot struct {
	Plans   []testReportPlan  `json:"plans"`
	Summary TestReportSummary `json:"summary"`
}
type testReportJobBody struct {
	ProjectID guid.ID `json:"project_id"`
	ReportID  guid.ID `json:"report_id"`
	ActorID   guid.ID `json:"actor_id"`
}

func (m *Module) reportOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (*model.TestReport, error) {
	q := query.BindTestReport(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
}
func testReportView(row *model.TestReport) (TestReportView, error) {
	var snapshot testReportSnapshot
	if err := json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
		return TestReportView{}, err
	}
	return TestReportView{ID: row.ID, Title: row.Title, State: row.State, JobID: row.JobID, FileID: row.FileID, VersionID: row.VersionID, ContentHash: row.ContentHash, Summary: snapshot.Summary, CreatedAt: row.CreatedAt, BuiltAt: row.BuiltAt}, nil
}

func (m *Module) createTestReportOn(ctx context.Context, tx *gorm.DB, project guid.ID, in TestReportBody) (Receipt, error) {
	if !validText(in.Title, 200) || len(in.PlanIDs) < 1 || len(in.PlanIDs) > 15 {
		return Receipt{}, ErrInvalid
	}
	if m.forecastQueue == nil {
		return Receipt{}, ErrUnavailable
	}
	if _, err := m.authorize(ctx, tx, project, "test_plan_manage", false); err != nil {
		return Receipt{}, err
	}
	ids := slices.Clone(in.PlanIDs)
	slices.Sort(ids)
	if ids[0] <= 0 || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return Receipt{}, ErrInvalid
	}
	snapshot := testReportSnapshot{Plans: []testReportPlan{}}
	for _, id := range ids {
		row, err := m.planOn(ctx, tx, project, id)
		if err != nil {
			return Receipt{}, err
		}
		if row.State != "FROZEN" {
			return Receipt{}, ErrConflict
		}
		plan, err := planView(row)
		if err != nil {
			return Receipt{}, err
		}
		item := testReportPlan{Plan: plan, Runs: []TestRunView{}}
		for _, c := range plan.Cases {
			if c.IsHiddenAcceptance {
				return Receipt{}, ErrNotFound
			}
			snapshot.Summary.Total++
			q := query.BindTestRun(tx, m.names)
			runs, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(project), q.PlanID.Eq(id), q.CaseID.Eq(c.ID), q.CaseVersion.Eq(c.Version)).Order(q.ID.Desc()).Limit(1).Find()
			if err != nil {
				return Receipt{}, err
			}
			if len(runs) == 0 {
				snapshot.Summary.Unexecuted++
				continue
			}
			run := runView(runs[0])
			item.Runs = append(item.Runs, run)
			snapshot.Summary.HumanRecorded++
			switch run.Result {
			case "PASSED":
				snapshot.Summary.Passed++
			case "FAILED":
				snapshot.Summary.Failed++
			case "BLOCKED":
				snapshot.Summary.Blocked++
			default:
				return Receipt{}, ErrConflict
			}
		}
		snapshot.Plans = append(snapshot.Plans, item)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return Receipt{}, err
	}
	if len(raw) > 8<<20 {
		return Receipt{}, ErrInvalid
	}
	id, err := m.nextID()
	if err != nil {
		return Receipt{}, err
	}
	actor, tenant := authn.PrincipalIDFromContext(ctx), authn.TenantIDFromContext(ctx)
	job, err := m.forecastQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: tenant, Kind: testReportJob, Payload: testReportJobBody{project, id, actor}, CreatedBy: actor, IdempotencyKey: "test-report:" + id.String(), ConcurrencyKey: "test-report:" + id.String()})
	if err != nil {
		return Receipt{}, err
	}
	row := &model.TestReport{TenantID: tenant, ProjectID: project, ID: id, ActorID: actor, Title: in.Title, Snapshot: string(raw), State: "PENDING", JobID: job.JobID, CreatedAt: now()}
	if err = query.BindTestReport(tx, m.names).WithContext(ctx).Create(row); err != nil {
		return Receipt{}, err
	}
	if err = m.event(ctx, tx, project, id, "test_report_requested", "test_plan_manage", 1); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}

func (m *Module) testReportResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	if owner.Scope != "entity" || owner.ID != authn.EntityIDFromContext(ctx) {
		return ErrNotFound
	}
	if _, err := m.authorize(ctx, tx, owner.ID, "test_plan_manage", false); err != nil {
		return err
	}
	if id == 0 {
		if files.DeliveryBundleWrite(ctx) {
			return nil
		}
		return ErrNotFound
	}
	row, err := m.reportOn(ctx, tx, owner.ID, id)
	if err != nil {
		return err
	}
	if files.DeliveryBundleWrite(ctx) {
		if row.State != "PENDING" || row.ActorID != authn.PrincipalIDFromContext(ctx) {
			return ErrConflict
		}
	} else if row.State != "BUILT" {
		return ErrConflict
	}
	return nil
}
func (m *Module) testReportHandler() jobs.Handler {
	return jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_clientwork_test_report_name", DescriptionKey: activityi18n.Namespace + ".activity_job_clientwork_test_report_description", Kind: testReportJob, Timeout: 3 * time.Minute, MaxAttempts: 4, Run: func(ctx context.Context, job jobs.Job) error {
		var in testReportJobBody
		if json.Unmarshal(job.Payload, &in) != nil || job.TenantID <= 0 || in.ProjectID <= 0 || in.ReportID <= 0 || in.ActorID <= 0 || in.ActorID != job.CreatedBy {
			return jobs.Permanent(ErrInvalid)
		}
		ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: job.TenantID, EntityID: in.ProjectID, PrincipalID: in.ActorID, SubjectType: authn.SubjectTypePrincipal})
		err := m.buildTestReport(ctx, in, job.ID)
		if err != nil && job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts && m.writer() != nil {
			statusErr := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if _, e := m.authorize(ctx, tx, in.ProjectID, "test_plan_manage", false); e != nil {
					return e
				}
				row, e := m.reportOn(ctx, tx, in.ProjectID, in.ReportID)
				if e != nil {
					return e
				}
				if row.JobID != job.ID || row.ActorID != in.ActorID {
					return ErrConflict
				}
				if row.State != "PENDING" {
					return nil
				}
				q := query.BindTestReport(tx, m.names)
				if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID)).Update(q.State, "FAILED"); e != nil {
					return e
				}
				return m.event(ctx, tx, in.ProjectID, row.ID, "test_report_failed", "test_plan_manage", 1)
			})
			if statusErr != nil {
				return statusErr
			}
		}
		return err
	}}
}
func (m *Module) buildTestReport(ctx context.Context, in testReportJobBody, jobID guid.ID) error {
	if m.writer() == nil || m.bundleFiles.Stage == nil || m.bundleFiles.AttachOn == nil {
		return ErrUnavailable
	}
	var raw []byte
	err := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.lockRecordedParticipantsOn(ctx, tx, in.ActorID); err != nil {
			return err
		}
		if _, err := m.authorize(ctx, tx, in.ProjectID, "test_plan_manage", false); err != nil {
			return err
		}
		row, err := m.reportOn(ctx, tx, in.ProjectID, in.ReportID)
		if err != nil {
			return err
		}
		if row.JobID != jobID || row.ActorID != in.ActorID {
			return ErrConflict
		}
		if row.State == "BUILT" {
			return nil
		}
		if row.State != "PENDING" {
			return ErrConflict
		}
		var snapshot testReportSnapshot
		if err = json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
			return err
		}
		raw = renderTestReportPDF(row.Title, snapshot)
		return nil
	})
	if err != nil || raw == nil {
		return err
	}
	staged, err := m.bundleFiles.Stage(ctx, in.ProjectID, testReportPurpose, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return err
	}
	return m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.lockRecordedParticipantsOn(ctx, tx, in.ActorID); err != nil {
			return err
		}
		if _, err := m.authorize(ctx, tx, in.ProjectID, "test_plan_manage", false); err != nil {
			return err
		}
		row, err := m.reportOn(ctx, tx, in.ProjectID, in.ReportID)
		if err != nil {
			return err
		}
		if row.JobID != jobID || row.ActorID != in.ActorID {
			return ErrConflict
		}
		if row.State == "BUILT" {
			return nil
		}
		pin, err := m.bundleFiles.AttachOn(ctx, tx, staged, row.ID)
		if err != nil {
			return err
		}
		if pin.SHA256 != packageSHA(raw) || pin.Size != int64(len(raw)) || pin.FileID <= 0 || pin.VersionID <= 0 || pin.BindingID <= 0 || pin.ResourceID != row.ID || pin.Purpose != testReportPurpose {
			return ErrConflict
		}
		q := query.BindTestReport(tx, m.names)
		at := now()
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.State.Eq("PENDING")).UpdateSimple(q.State.Value("BUILT"), q.BindingID.Value(pin.BindingID), q.FileID.Value(pin.FileID), q.VersionID.Value(pin.VersionID), q.ContentHash.Value(pin.SHA256), q.BuiltAt.Value(at)); err != nil {
			return err
		}
		return m.event(ctx, tx, in.ProjectID, row.ID, "test_report_built", "test_plan_manage", 1)
	})
}

func (m *Module) registerTestReportsHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.owner)
	path := entityBase + "/test-reports"
	errs := []int{401, 403, 404, 409, 422, 500, 503}
	guard := authz.Guard{Resource: "test_plan", Verb: "manage"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_report.create", Method: "POST", Path: path, Summary: "Freeze a saved PDF test report", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, guard, m.createTestReportHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_report.get", Method: "GET", Path: path + "/{id}", Summary: "Read an immutable test report receipt", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, guard, m.getTestReportHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.test_report.content", Method: "GET", Path: path + "/{id}/content", Summary: "Download the saved test report PDF", Responses: binaryContentResponses(), Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, guard, m.testReportContentHTTP)
}
func (m *Module) createTestReportHTTP(ctx context.Context, in *CreateInput[TestReportBody]) (*CommandOutput, error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, project, "clientwork.test_report.create", "POST", entityBase+"/test-reports", in.Key, 201, in.Body, "test_plan_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.createTestReportOn(ctx, tx, project, in.Body) })
}
func (m *Module) getTestReportHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[TestReportView], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out TestReportView
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, project, "test_plan_manage", false); err != nil {
			return err
		}
		row, err := m.reportOn(ctx, tx, project, id)
		if err != nil {
			return err
		}
		out, err = testReportView(row)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) testReportContentHTTP(ctx context.Context, in *ObjectInput) (*huma.StreamResponse, error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.streamContent(ctx, project, func(tx *gorm.DB, _ bool) (guid.ID, string, error) {
		if _, err := m.authorize(ctx, tx, project, "test_plan_manage", false); err != nil {
			return 0, "", err
		}
		row, err := m.reportOn(ctx, tx, project, id)
		if err != nil {
			return 0, "", err
		}
		if row.State != "BUILT" || row.BindingID == nil || row.ContentHash == nil {
			return 0, "", ErrConflict
		}
		return *row.BindingID, *row.ContentHash, nil
	})
}

// A report contains summary/result identities, never private case instructions.
// Text is rendered as UTF-16 CID text so titles retain Chinese/Malay characters.
func renderTestReportPDF(title string, snapshot testReportSnapshot) []byte {
	lines := []string{title, fmt.Sprintf("Total %d | Passed %d | Failed %d | Blocked %d | Unexecuted %d", snapshot.Summary.Total, snapshot.Summary.Passed, snapshot.Summary.Failed, snapshot.Summary.Blocked, snapshot.Summary.Unexecuted), fmt.Sprintf("Human recorded: %d", snapshot.Summary.HumanRecorded)}
	for _, item := range snapshot.Plans {
		lines = append(lines, fmt.Sprintf("Plan %s / version %d", item.Plan.ID, item.Plan.Version))
		for _, run := range item.Runs {
			lines = append(lines, fmt.Sprintf("Case %s v%d: %s / run %s", run.CaseID, run.CaseVersion, run.Result, run.ID))
		}
	}
	return testReportPDF(lines)
}
