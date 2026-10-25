package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type submissionMembers struct {
	testMembers
	active bool
	err    error
}

func (m submissionMembers) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return m.active, m.err
}

func TestSubmissionReadFailuresNeverExposePartialProjections(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
	for _, stage := range []string{"missing_members", "membership_error", "membership_revoked", "project_changed", "project_error", "round_query", "package_query", "bad_round", "terminal_milestone"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			failure := errors.New("read authority unavailable")
			switch stage {
			case "missing_members":
				copyModule.members = nil
			case "membership_error":
				copyModule.members = submissionMembers{err: failure}
			case "membership_revoked":
				copyModule.members = submissionMembers{}
			case "project_changed":
				calls := 0
				original := copyModule.intakeSources.OwnSubmissionOn
				copyModule.intakeSources.OwnSubmissionOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (intake.Submission, error) {
					source, err := original(ctx, tx, tenant, id)
					calls++
					if calls == 2 {
						source.ProjectID = 8
					}
					return source, err
				}
			case "project_error":
				copyModule.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "round_query", "package_query":
				target := m.names.ClarificationRound.String()
				if stage == "package_query" {
					target = m.names.DeliveryPackage.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_submission_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(failure)
					}
				}))
				defer db.Callback().Query().Remove("regression_submission_failure")
			case "bad_round":
				row := model.ClarificationRound{TenantID: 1, ProjectID: 3, ID: 401, IntakeID: 50, IntakeVersion: 1, RecipientID: 4, Questions: "broken", State: "OPEN", Version: 1, CreatedBy: 2, CreatedAt: now(), UpdatedAt: now()}
				require.NoError(t, db.Table(m.names.ClarificationRound.String()).Create(&row).Error)
				defer db.Table(m.names.ClarificationRound.String()).Where("id=401").Delete(&model.ClarificationRound{})
			case "terminal_milestone":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					detail, err := copyModule.submissionDetailOn(ctx, tx, intake.Submission{ID: 50, ProjectID: 3, Kind: "NEW", Status: "DECLINED"})
					require.NoError(t, err)
					require.Equal(t, "DECLINED", detail.Milestone)
					return nil
				}))
				return
			}
			if stage == "missing_members" || stage == "membership_error" || stage == "membership_revoked" || stage == "project_error" {
				out, err := copyModule.listSubmissionsHTTP(ctx, &SubmissionListInput{Limit: 10})
				require.Error(t, err)
				require.Nil(t, out)
				return
			}
			if stage == "package_query" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := copyModule.submissionDetailOn(ctx, tx, intake.Submission{ID: 50, ProjectID: 3, Kind: "NEW", Status: "CONVERTED"})
					require.Error(t, err)
					return nil
				}))
				return
			}
			out, err := copyModule.getSubmissionHTTP(ctx, &SubmissionObjectInput{ID: "50"})
			require.Error(t, err)
			require.Nil(t, out)
		})
	}
}

func TestSubmissionDeliveryProjectionFailsClosedForCorruptOrUnavailableFacts(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	row := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 70, Title: "Package", State: "PUBLISHED", Manifest: "broken", ContentHash: strings.Repeat("c", 64), RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&row).Error)
	for _, stage := range []string{"corrupt_manifest", "mismatched_refs", "empty_refs", "missing_provider", "provider_error", "missing_facts", "mismatched_work", "other_source", "change_source"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			manifest := packageManifest{Refs: []delivery.ClientArtifactRef{{WorkItemID: 11}}, Content: PackageContent{Artifacts: []delivery.ClientArtifact{{VersionID: 91, SHA256: strings.Repeat("d", 64)}}}}
			copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
				return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 50}}, nil
			}
			wantError := true
			switch stage {
			case "mismatched_refs":
				manifest.Refs = nil
			case "empty_refs":
				manifest.Refs = nil
				manifest.Content.Artifacts = nil
				wantError = false
			case "missing_provider":
				copyModule.packageSources.ReadOn = nil
			case "provider_error":
				copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
					return nil, errors.New("source facts failed")
				}
			case "missing_facts":
				copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
					return nil, nil
				}
			case "mismatched_work":
				copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
					return []delivery.PackageSource{{WorkItemID: 99, IntakeID: 50}}, nil
				}
			case "other_source":
				copyModule.packageSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
					return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 99}}, nil
				}
				wantError = false
			case "change_source":
				wantError = false
			}
			raw, err := json.Marshal(manifest)
			require.NoError(t, err)
			if stage == "corrupt_manifest" {
				raw = []byte("broken")
			}
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("manifest", string(raw)).Error)
			out := SubmissionDetail{SubmissionSummary: SubmissionSummary{Submission: intake.Submission{ID: 50, ProjectID: 3, Kind: "NEW"}}, Deliveries: []SubmissionDelivery{}}
			if stage == "change_source" {
				out.Kind = "CHANGE"
			}
			err = db.Transaction(func(tx *gorm.DB) error { return copyModule.submissionDeliveriesOn(ctx, tx, &out) })
			if wantError {
				require.Error(t, err)
				require.Empty(t, out.Deliveries)
			} else {
				require.NoError(t, err)
			}
			if stage == "other_source" || stage == "empty_refs" {
				require.Empty(t, out.Deliveries)
			}
			if stage == "change_source" {
				require.Empty(t, out.Milestone, "CHANGE must not invent NEW delivery milestones")
			}
		})
	}
}

func TestSubmissionHTTPRejectsMalformedBoundariesAndMissingCapabilities(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	assertStatus := func(err error, code int) {
		t.Helper()
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, code, status.GetStatus())
	}
	for _, in := range []*SubmissionListInput{{Limit: 0}, {Limit: 101}, {Limit: 1, ProjectID: "bad"}, {Limit: 1, ReceivedFrom: "yesterday"}, {Limit: 1, ReceivedTo: "tomorrow"}} {
		out, err := m.listSubmissionsHTTP(ctx, in)
		assertStatus(err, 422)
		require.Nil(t, out)
	}
	_, err := m.getSubmissionHTTP(ctx, &SubmissionObjectInput{ID: "0"})
	assertStatus(err, 422)
	for _, in := range []*SubmissionAnswerInput{{ID: "bad"}, {ID: "50", RoundID: "bad"}, {ID: "50", RoundID: "1", IfMatch: "bad"}} {
		out, err := m.answerSubmissionHTTP(ctx, in)
		assertStatus(err, 422)
		require.Nil(t, out)
	}
	copyModule := *m
	copyModule.runtime = nil
	_, err = copyModule.getSubmissionHTTP(ctx, &SubmissionObjectInput{ID: "50"})
	assertStatus(err, 503)
	answer := &SubmissionAnswerInput{ID: "50", RoundID: "1", QuestionNo: 1, IfMatch: `"1"`}
	answer.Body.AnswerText = "UTC"
	_, err = copyModule.answerSubmissionHTTP(ctx, answer)
	assertStatus(err, 503)
	copyModule = *m
	copyModule.intakeSources.OwnSubmissionsOn = nil
	_, err = copyModule.listSubmissionsHTTP(ctx, &SubmissionListInput{Limit: 1})
	assertStatus(err, 503)
	copyModule.intakeSources.OwnSubmissionOn = nil
	_, err = copyModule.getSubmissionHTTP(ctx, &SubmissionObjectInput{ID: "50"})
	assertStatus(err, 503)
	// Real timestamp filters are accepted and remain within the owner fence.
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
	page, err := m.listSubmissionsHTTP(ctx, &SubmissionListInput{Limit: 1, ProjectID: "3", ReceivedFrom: "2000-01-01T00:00:00Z", ReceivedTo: "2099-01-01T00:00:00Z"})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
}

func TestSubmissionReadStorageFailureNeverReturnsPartialProjection(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
	for _, table := range []string{"g_intake_records", m.names.ClarificationRound.String()} {
		t.Run(table, func(t *testing.T) {
			failure := errors.New("private SQL diagnostic must not leak")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_submission_read", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(failure)
				}
			}))
			defer func() { require.NoError(t, db.Callback().Query().Remove("regression_submission_read")) }()
			for _, read := range []func() error{
				func() error {
					out, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
					require.Nil(t, out)
					return err
				},
				func() error {
					out, err := m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 50})
					require.Nil(t, out)
					return err
				},
			} {
				var status huma.StatusError
				err := read()
				require.ErrorAs(t, err, &status)
				require.Equal(t, 500, status.GetStatus())
				require.NotContains(t, err.Error(), "private SQL")
			}
		})
	}
}

func TestSubmissionOwnProjectionTextAnswerReplayAndRollback(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"submitted_by_principal_id": 2, "raw_content": "private original", "decline_reason": "internal triage rationale", "client_message": "Public response", "external_ref": "call-1"}).Error)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), &CreateInput[ClarificationRequest]{EntityID: "3", Key: "submission-questions", Body: ClarificationRequest{IntakeID: 50, IntakeVersion: 1, RecipientID: 4, Questions: []string{"Which timezone?", "Which format?"}}})
	require.NoError(t, err)
	view, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.Equal(t, 2, view.Body.Data.OpenQuestionsCount)
	require.Len(t, view.Body.Data.Questions, 2)
	raw, err := json.Marshal(view.Body.Data)
	require.NoError(t, err)
	require.Contains(t, string(raw), "Public response")
	require.NotContains(t, string(raw), "private original")
	require.NotContains(t, string(raw), "internal triage")
	require.NotContains(t, string(raw), "recipient_id")
	require.NotContains(t, string(raw), "answered_by")
	_, err = m.getSubmissionHTTP(testContext(t, 4), &SubmissionObjectInput{ID: "50"})
	require.Error(t, err)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 404, status.GetStatus())
	in := &SubmissionAnswerInput{ID: "50", RoundID: opened.Body.Data.ID.String(), QuestionNo: 1, Key: "submitter-answer", IfMatch: `"1"`}
	in.Body.AnswerText = "UTC"
	in.Body.UploadIDs = []guid.ID{91}
	_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus(), "unsupported attachments must not be silently accepted or persisted as a successful answer")
	in.Body.UploadIDs = nil
	// A failure after source progression must roll back round, source and replay.
	auditAppender := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
	require.Error(t, err)
	var row model.ClarificationRound
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Take(&row).Error)
	require.Equal(t, 1, row.Version)
	require.NotContains(t, row.Questions, "UTC")
	var state struct {
		Status  string
		Version int
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Scan(&state).Error)
	require.Equal(t, "CLARIFYING", state.Status)
	require.Equal(t, 2, state.Version)
	m.audit = auditAppender
	answered, err := m.answerSubmissionHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "OPEN", answered.Body.Data.State)
	replayed, err := m.answerSubmissionHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, answered.Body.Data, replayed.Body.Data)
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Update("intake_id", 999).Error)
	_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
	require.ErrorAs(t, err, &status)
	require.Equal(t, 404, status.GetStatus(), "current source-round ownership is checked before replay")
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Update("intake_id", 50).Error)
	projects.deny = true
	_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
	require.Error(t, err)
	projects.deny = false
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 9).Error)
	_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
	require.Error(t, err)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
	in.Key = "submitter-last"
	in.QuestionNo = 2
	in.IfMatch = `"2"`
	in.Body.AnswerText = "CSV"
	last, err := m.answerSubmissionHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "ANSWERED", last.Body.Data.State)
	view, err = m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.Zero(t, view.Body.Data.OpenQuestionsCount)
	require.Equal(t, "UTC", view.Body.Data.Questions[0].Answer)
	require.Equal(t, "CSV", view.Body.Data.Questions[1].Answer)
	// Ordinary recipient API stays external-client only, even for the submitter.
	_, err = m.answerClarificationHTTP(testContext(t, 2), &EditInput[ClarificationAnswer]{EntityID: "3", ID: opened.Body.Data.ID.String(), IfMatch: `"3"`, Key: "ordinary-still-denied", Body: ClarificationAnswer{QuestionNo: 1, Answer: "wrong route"}})
	require.Error(t, err)
}

func TestSubmissionDeliveriesExcludeOtherSourcesAndPrivateBindings(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"submitted_by_principal_id": 2, "status": "CONVERTED"}).Error)
	manifest := packageManifest{Content: PackageContent{Title: "Shared package", Artifacts: []delivery.ClientArtifact{{VersionID: 91, SHA256: strings.Repeat("a", 64)}, {VersionID: 92, SHA256: strings.Repeat("b", 64)}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 11}, {WorkItemID: 12}}, Bindings: []packageFile{{VersionID: 91, BindingID: 901}, {VersionID: 92, BindingID: 902}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	row := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 70, Title: "Shared package", State: "PUBLISHED", Manifest: string(raw), ContentHash: strings.Repeat("c", 64), RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&row).Error)
	m.packageSources.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.Len(t, refs, 2)
		return []delivery.PackageSource{{WorkItemID: 11, IntakeID: 50}, {WorkItemID: 12, IntakeID: 999}}, nil
	}
	view, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "AWAITING_ACCEPTANCE", view.Body.Data.Milestone)
	require.Len(t, view.Body.Data.Deliveries, 1)
	require.Len(t, view.Body.Data.Deliveries[0].Items, 1)
	require.EqualValues(t, 91, view.Body.Data.Deliveries[0].Items[0].Artifact.VersionID)
	encoded, err := json.Marshal(view.Body.Data)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"bindings"`)
	require.NotContains(t, string(encoded), `"binding_id"`)
	require.NotContains(t, string(encoded), strings.Repeat("b", 64))
	require.False(t, view.Body.Data.DownloadsAvailable)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=70").Update("state", "ACCEPTED").Error)
	view, err = m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "DELIVERED", view.Body.Data.Milestone)
}

func TestSubmissionListCurrentPermissionAndCursorFilterIsolation(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"submitted_by_principal_id": 2, "external_ref": "mine"}).Error)
	// A second genuinely owned row gives the first page a continuation cursor.
	require.NoError(t, db.Exec("INSERT INTO g_intake_records(id,tenant_id,ref_no,channel_id,channel_kind,contact_id,title,raw_content_hash,received_at,reply_token,status,project_id,created_at,updated_at,submitted_by_principal_id,external_ref) SELECT 51,tenant_id,2,channel_id,channel_kind,contact_id,title,raw_content_hash,received_at,'source-51',status,project_id,created_at,updated_at,2,'other' FROM g_intake_records WHERE id=50").Error)
	page, err := m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 1})
	require.NoError(t, err)
	require.True(t, page.Body.Data.HasMore)
	require.Len(t, page.Body.Data.Items, 1)
	require.EqualValues(t, 51, page.Body.Data.Items[0].ID)
	_, err = m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 1, Cursor: page.Body.Data.NextCursor, ExternalRef: "mine"})
	require.Error(t, err)
	_, err = m.listSubmissionsHTTP(testContext(t, 4), &SubmissionListInput{Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.Error(t, err)
	_, err = m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 1, Cursor: page.Body.Data.NextCursor, Sort: "received_at"})
	require.Error(t, err, "changing sort cannot reuse a cursor")
	_, err = m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 1, Sort: "raw_content"})
	require.Error(t, err)
	next, err := m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, err)
	require.Len(t, next.Body.Data.Items, 1)
	require.Equal(t, guid.ID(50), next.Body.Data.Items[0].ID)
	projects.deny = true
	denied, err := m.listSubmissionsHTTP(testContext(t, 2), &SubmissionListInput{Limit: 50})
	require.NoError(t, err)
	require.Empty(t, denied.Body.Data.Items)
	_, err = m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
	require.Error(t, err)
}
