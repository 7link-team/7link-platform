package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const submissionsPath = "/intake-submissions"

type SubmissionListInput struct {
	Sort         string `query:"sort" enum:"received_at,-received_at" default:"-received_at"`
	ProjectID    string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor       string `query:"cursor" maxLength:"512"`
	Limit        int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Status       string `query:"status" enum:"RECEIVED,TRIAGED,CLARIFYING,CONVERTED,MERGED,DECLINED"`
	ExternalRef  string `query:"external_ref" maxLength:"128"`
	ReceivedFrom string `query:"received_from" format:"date-time"`
	ReceivedTo   string `query:"received_to" format:"date-time"`
}

type SubmissionObjectInput struct {
	ID string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type SubmissionQuestion struct {
	Attachments  []AnswerAttachment `json:"attachments,omitempty"`
	RoundID      guid.ID            `json:"round_id"`
	RoundVersion int                `json:"round_version"`
	Number       int                `json:"number"`
	Text         string             `json:"text"`
	Answer       string             `json:"answer,omitempty"`
	State        string             `json:"state"`
}

type SubmissionSummary struct {
	intake.Submission
	Milestone          string `json:"milestone,omitempty"`
	OpenQuestionsCount int    `json:"open_questions_count"`
}

type SubmissionDetail struct {
	SubmissionSummary
	Questions           []SubmissionQuestion `json:"questions"`
	Deliveries          []SubmissionDelivery `json:"deliveries"`
	DownloadsAvailable  bool                 `json:"downloads_available"`
	ChangeViewAvailable bool                 `json:"change_view_available"`
}

type SubmissionDelivery struct {
	Manifest             []SubmissionBundleFile `json:"manifest"`
	DownloadURL          string                 `json:"download_url,omitempty"`
	DownloadURLExpiresAt *time.Time             `json:"download_url_expires_at,omitempty"`
	ID                   guid.ID                `json:"id"`
	State                string                 `json:"state"`
	Items                []PackageItem          `json:"items"`
}

type SubmissionAnswerInput struct {
	ID         string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RoundID    string `path:"round_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	QuestionNo int    `path:"question_no" minimum:"1" maximum:"10"`
	Key        string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch    string `header:"If-Match" required:"true"`
	Body       struct {
		AnswerText string    `json:"answer_text" minLength:"1" maxLength:"8000"`
		UploadIDs  []guid.ID `json:"upload_ids,omitempty" maxItems:"5" doc:"Completed native Files upload file-node IDs; raw upload/session IDs are not accepted."`
	}
}

func (m *Module) registerSubmissionHTTP(api huma.API) error {
	errors := []int{401, 403, 404, 409, 422, 500, 503}
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.submission.bundle", Method: "GET", Path: submissionsPath + "/{id}/deliveries/{delivery_id}/bundle", Summary: "Download only this submission's immutable published artifacts using a current signed URL", Tags: []string{"tenants"}, Responses: binaryContentResponses(), Errors: errors}, m.submissionBundleHTTP)
	// Tenant membership alone never exposes a row: every handler applies current
	// intake_submit authority and the immutable submitted-by fence itself.
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.submission.list", Method: "GET", Path: submissionsPath, Summary: "List only this authenticated submitter's currently authorized Intake submissions", Tags: []string{"tenants"}, Errors: errors}, m.listSubmissionsHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.submission.get", Method: "GET", Path: submissionsPath + "/{id}", Summary: "Read a submitter-safe Intake projection and sent clarification questions", Tags: []string{"tenants"}, Errors: errors}, m.getSubmissionHTTP)
	path := submissionsPath + "/{id}/clarification-rounds/{round_id}/answers/{question_no}"
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.submission.answer", Method: "PUT", Path: path, Summary: "Answer one question on an owned submission using the current clarification round version", Tags: []string{"tenants"}, Errors: errors}, m.answerSubmissionHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.submission.attachment", Method: "GET", Path: path + "/attachments/{attachment_id}/content", Summary: "Download an exact answer attachment on a currently authorized owned submission", Tags: []string{"tenants"}, Responses: binaryContentResponses(), Errors: errors}, m.submissionAttachmentHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path)
}

func submissionContext(ctx context.Context, project guid.ID) context.Context {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil {
		return ctx
	}
	copyClaims := *claims
	copyClaims.EntityID = project
	return authn.WithClaims(ctx, &copyClaims)
}

func (m *Module) ownedSubmissionOn(ctx context.Context, tx *gorm.DB, id guid.ID) (intake.Submission, context.Context, error) {
	if m.intakeSources.OwnSubmissionOn == nil {
		return intake.Submission{}, ctx, ErrUnavailable
	}
	source, err := m.intakeSources.OwnSubmissionOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return source, ctx, err
	}
	projectCtx := submissionContext(ctx, source.ProjectID)
	if _, err = m.authorize(projectCtx, tx, source.ProjectID, "intake_submit", false); err != nil {
		return intake.Submission{}, ctx, err
	}
	current, err := m.intakeSources.OwnSubmissionOn(projectCtx, tx, authn.TenantIDFromContext(ctx), id)
	if err == nil && current.ProjectID != source.ProjectID {
		err = ErrConflict
	}
	return current, projectCtx, err
}

func (m *Module) submissionDetailOn(ctx context.Context, tx *gorm.DB, source intake.Submission) (SubmissionDetail, error) {
	out := SubmissionDetail{SubmissionSummary: SubmissionSummary{Submission: source}, Questions: []SubmissionQuestion{}, Deliveries: []SubmissionDelivery{}}
	out.ChangeViewAvailable = source.ChangeView != nil
	if source.Kind != "CHANGE" {
		switch source.Status {
		case "RECEIVED", "TRIAGED":
			out.Milestone = "RECEIVED"
		case "CLARIFYING":
			out.Milestone = "CLARIFYING"
		case "CONVERTED":
			out.Milestone = "IN_PROGRESS"
		case "MERGED", "DECLINED":
			out.Milestone = source.Status
		}
	}
	q := query.BindClarificationRound(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(source.ProjectID), q.IntakeID.Eq(source.ID)).Order(q.ID.Asc()).Find()
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		if row.State == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
			continue
		}
		view, err := clarificationView(row)
		if err != nil {
			return out, err
		}
		for _, question := range view.Questions {
			out.Questions = append(out.Questions, SubmissionQuestion{RoundID: row.ID, RoundVersion: row.Version, Number: question.Number, Text: question.Text, Answer: question.Answer, State: question.State, Attachments: question.Attachments})
			if question.State == "SENT" {
				out.OpenQuestionsCount++
			}
		}
	}
	if source.Status == "CONVERTED" {
		if err := m.submissionDeliveriesOn(ctx, tx, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Return only items whose canonical requirement points to this source. A
// package can contain several submissions; its full private manifest and other
// sources' artifacts must never enter this projection.
func (m *Module) submissionDeliveriesOn(ctx context.Context, tx *gorm.DB, out *SubmissionDetail) error {
	q := query.BindDeliveryPackage(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(out.ProjectID), q.ArchivedAt.IsNull(), q.State.Neq("DRAFT")).Order(q.ID.Desc()).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		var manifest packageManifest
		if err = json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
			return err
		}
		if len(manifest.Refs) != len(manifest.Content.Artifacts) {
			return ErrConflict
		}
		if len(manifest.Refs) == 0 {
			continue
		}
		if m.packageSources.ReadOn == nil {
			return ErrUnavailable
		}
		facts, err := m.packageSources.ReadOn(ctx, tx, row.TenantID, row.ProjectID, manifest.Refs)
		if err != nil {
			return err
		}
		if len(facts) != len(manifest.Refs) {
			return ErrUnavailable
		}
		items := packageItems(row, manifest.Content)
		visible := []PackageItem{}
		for i, fact := range facts {
			if fact.WorkItemID != manifest.Refs[i].WorkItemID {
				return ErrConflict
			}
			if fact.IntakeID == out.ID {
				visible = append(visible, items[i])
			}
		}
		if len(visible) == 0 {
			continue
		}
		view := SubmissionDelivery{ID: row.ID, State: row.State, Items: visible, Manifest: submissionBundleManifest(visible)}
		if m.config.SubmissionBundles.SigningKey != "" && manifest.Bundle != nil && manifest.Bundle.State == "BUILT" {
			if err = m.signSubmissionBundle(ctx, out.ID, row, &view, now()); err != nil {
				return err
			}
			out.DownloadsAvailable = true
		}
		out.Deliveries = append(out.Deliveries, view)
		if len(out.Deliveries) == 1 && out.Kind != "CHANGE" {
			if row.State == "PUBLISHED" {
				out.Milestone = "AWAITING_ACCEPTANCE"
			} else if row.State == "ACCEPTED" {
				out.Milestone = "DELIVERED"
			}
		}
	}
	return nil
}

func (m *Module) listSubmissionsHTTP(ctx context.Context, in *SubmissionListInput) (*respx.Body[Page[SubmissionSummary]], error) {
	if m.writer() == nil || m.intakeSources.OwnSubmissionsOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	sort := in.Sort
	if sort == "" {
		sort = "-received_at"
	}
	if sort != "received_at" && sort != "-received_at" {
		return nil, httpError(ctx, ErrInvalid)
	}
	filter := intake.SubmissionFilter{Status: in.Status, ExternalRef: in.ExternalRef, Sort: sort}
	if in.ProjectID != "" {
		id, err := guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, httpError(ctx, ErrInvalid)
		}
		filter.ProjectID = id
	}
	for _, bound := range []struct {
		text   string
		target **time.Time
	}{{in.ReceivedFrom, &filter.ReceivedFrom}, {in.ReceivedTo, &filter.ReceivedTo}} {
		if bound.text != "" {
			at, err := time.Parse(time.RFC3339Nano, bound.text)
			if err != nil {
				return nil, httpError(ctx, ErrInvalid)
			}
			*bound.target = &at
		}
	}
	raw, _ := json.Marshal(filter)
	filterHash := sha256.Sum256(raw)
	scope := cursorScope(ctx, 0, fmt.Sprintf("submissions:%x", filterHash))
	before, err := position(in.Cursor, scope)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := Page[SubmissionSummary]{Items: []SubmissionSummary{}}
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidates, err := m.intakeSources.OwnSubmissionsOn(ctx, tx, authn.TenantIDFromContext(ctx), before, in.Limit, filter)
		if err != nil {
			return err
		}
		if m.members == nil {
			return ErrUnavailable
		}
		active, err := m.members.IsMemberActiveOn(ctx, tx, authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return err
		}
		if !active {
			return ErrNotFound
		}
		// Lock project fences in one stable order, including cursor/filter pages.
		projects := []guid.ID{}
		for _, source := range candidates.Items {
			projects = append(projects, source.ProjectID)
		}
		slices.Sort(projects)
		projects = slices.Compact(projects)
		allowed := map[guid.ID]bool{}
		for _, project := range projects {
			_, err = m.authorize(submissionContext(ctx, project), tx, project, "intake_submit", false)
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrProjectForbidden) || errors.Is(err, delivery.ErrWorkForbidden) {
				continue
			}
			if err != nil {
				return err
			}
			allowed[project] = true
		}
		for _, source := range candidates.Items {
			if allowed[source.ProjectID] {
				view, err := m.submissionDetailOn(submissionContext(ctx, source.ProjectID), tx, source)
				if err != nil {
					return err
				}
				out.Items = append(out.Items, view.SubmissionSummary)
			}
		}
		out.HasMore = candidates.HasMore
		if out.HasMore && len(candidates.Items) > 0 {
			out.NextCursor = cursor(scope, candidates.Items[len(candidates.Items)-1].ID)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) getSubmissionHTTP(ctx context.Context, in *SubmissionObjectInput) (*respx.Body[SubmissionDetail], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out SubmissionDetail
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		source, projectCtx, err := m.ownedSubmissionOn(ctx, tx, id)
		if err != nil {
			return err
		}
		out, err = m.submissionDetailOn(projectCtx, tx, source)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) answerSubmissionHTTP(ctx context.Context, in *SubmissionAnswerInput) (*CommandOutput, error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	round, err := guid.RequireID(in.RoundID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	expected, err := version(in.IfMatch)
	if err != nil || in.QuestionNo < 1 || in.QuestionNo > 10 || !validText(in.Body.AnswerText, 8000) || len(in.Body.UploadIDs) > 5 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var source intake.Submission
	var projectCtx context.Context
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		source, projectCtx, err = m.ownedSubmissionOn(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	check := func(tx *gorm.DB) error {
		current, _, err := m.ownedSubmissionOn(projectCtx, tx, id)
		if err != nil {
			return err
		}
		if current.ProjectID != source.ProjectID {
			return ErrConflict
		}
		row, err := m.loadClarificationOn(projectCtx, tx, source.ProjectID, round, false)
		if err != nil {
			return err
		}
		if row.IntakeID != id {
			return ErrNotFound
		}
		return nil
	}
	path := fmt.Sprintf("%s/%s/clarification-rounds/%s/answers/%d", submissionsPath, id.String(), round.String(), in.QuestionNo)
	return m.command(projectCtx, source.ProjectID, "clientwork.submission.answer", "PUT", path, in.Key, 200, in, "intake_submit", false, check, func(tx *gorm.DB) (Receipt, error) {
		row, err := m.loadClarificationOn(projectCtx, tx, source.ProjectID, round, false)
		if err != nil {
			return Receipt{}, err
		}
		if row.IntakeID != id {
			return Receipt{}, ErrNotFound
		}
		return m.answerClarificationQuestionOn(projectCtx, tx, source.ProjectID, row, expected, ClarificationAnswer{QuestionNo: in.QuestionNo, Answer: in.Body.AnswerText, UploadIDs: in.Body.UploadIDs}, "intake_submit")
	})
}
