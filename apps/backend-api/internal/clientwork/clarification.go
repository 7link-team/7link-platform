package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type ClarificationRequest struct {
	IntakeID      guid.ID  `json:"intake_id"`
	IntakeVersion int      `json:"intake_version" minimum:"1"`
	RecipientID   guid.ID  `json:"recipient_id"`
	Questions     []string `json:"questions" minItems:"1" maxItems:"10"`
}
type ClarificationAnswer struct {
	QuestionNo int       `json:"question_no" minimum:"1" maximum:"10"`
	Answer     string    `json:"answer" minLength:"1" maxLength:"8000"`
	UploadIDs  []guid.ID `json:"upload_ids,omitempty" maxItems:"5" doc:"Completed native Files upload file-node IDs; raw upload/session IDs are not accepted."`
}

type ClarificationCancellation struct {
	Reason string `json:"reason" minLength:"1" maxLength:"2000"`
}
type ClarificationQuestion struct {
	Attachments []AnswerAttachment `json:"attachments,omitempty"`
	Number      int                `json:"number"`
	Text        string             `json:"text"`
	Answer      string             `json:"answer,omitempty"`
	AnsweredBy  *guid.ID           `json:"answered_by,omitempty"`
	AnsweredAt  *time.Time         `json:"answered_at,omitempty"`
	State       string             `json:"state" enum:"SENT,ANSWERED,WITHDRAWN,EXPIRED"`
}
type ClarificationView struct {
	DueAt           *time.Time `json:"due_at,omitempty"`
	NextReminderAt  *time.Time `json:"next_reminder_at,omitempty"`
	RemindersSent   int        `json:"reminders_sent"`
	TimingAvailable bool       `json:"timing_available"`
	ID              guid.ID    `json:"id"`
	IntakeID        guid.ID    `json:"intake_id"`
	RecipientID     guid.ID    `json:"recipient_id"`
	Version         int        `json:"version"`
	State           string     `json:"state"`
	// IN_APP is availability, not proof of notification/transport delivery.
	AvailableVia string                  `json:"available_via"`
	Questions    []ClarificationQuestion `json:"questions"`
	CreatedAt    time.Time               `json:"created_at"`
	ClosedAt     *time.Time              `json:"closed_at,omitempty"`
	CloseReason  *string                 `json:"close_reason,omitempty"`
	CloseNote    *string                 `json:"close_note,omitempty"`
}

func clarificationView(row *model.ClarificationRound) (ClarificationView, error) {
	out := ClarificationView{DueAt: row.DueAt, NextReminderAt: row.NextReminderAt, RemindersSent: row.RemindersSent, TimingAvailable: row.DueAt != nil, ID: row.ID, IntakeID: row.IntakeID, RecipientID: row.RecipientID, Version: row.Version, State: row.State, AvailableVia: "IN_APP", CreatedAt: row.CreatedAt, ClosedAt: row.ClosedAt, CloseReason: row.CloseReason, CloseNote: row.CloseNote}
	err := json.Unmarshal([]byte(row.Questions), &out.Questions)
	for i := range out.Questions {
		out.Questions[i].State = "SENT"
		if out.Questions[i].AnsweredAt != nil {
			out.Questions[i].State = "ANSWERED"
		} else if row.State == "CANCELLED" { //nolint:misspell // Frozen domain state.
			out.Questions[i].State = "WITHDRAWN"
		} else if row.State == "EXPIRED" {
			out.Questions[i].State = "EXPIRED"
		}
	}
	return out, err
}

func (m *Module) requestClarificationOn(ctx context.Context, tx *gorm.DB, p guid.ID, in ClarificationRequest) (Receipt, error) {
	if in.IntakeID <= 0 || in.IntakeVersion < 1 || in.RecipientID <= 0 || len(in.Questions) < 1 || len(in.Questions) > 10 {
		return Receipt{}, ErrInvalid
	}
	for _, text := range in.Questions {
		if !validText(text, 2000) {
			return Receipt{}, ErrInvalid
		}
	}
	if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
		return Receipt{}, err
	}
	if m.intakeSources.SourceOn == nil || m.intakeSources.ClarificationOn == nil {
		return Receipt{}, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	active, err := m.clients.IsClientOn(ctx, tx, tenant, p, in.RecipientID)
	if err != nil {
		return Receipt{}, err
	}
	if !active {
		return Receipt{}, ErrNotFound
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, in.RecipientID)
	if err != nil {
		return Receipt{}, err
	}
	if !active || human.ID != in.RecipientID {
		return Receipt{}, ErrNotFound
	}
	source, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, in.IntakeID)
	if err != nil {
		return Receipt{}, err
	}
	if source.Version != in.IntakeVersion || (source.State != "RECEIVED" && source.State != "TRIAGED") {
		return Receipt{}, ErrConflict
	}
	q := query.BindClarificationRound(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.IntakeID.Eq(in.IntakeID)).Count()
	if err != nil {
		return Receipt{}, err
	}
	if count >= 20 {
		return Receipt{}, ErrConflict
	}
	id, err := m.nextID()
	if err != nil {
		return Receipt{}, err
	}
	version, err := m.intakeSources.ClarificationOn(ctx, tx, tenant, p, in.IntakeID, in.IntakeVersion, id, intake.ClarificationRequest)
	if err != nil {
		return Receipt{}, err
	}
	questions := make([]ClarificationQuestion, len(in.Questions))
	for i, text := range in.Questions {
		questions[i] = ClarificationQuestion{Number: i + 1, Text: text}
	}
	raw, err := json.Marshal(questions)
	if err != nil {
		return Receipt{}, err
	}
	at := now()
	row := &model.ClarificationRound{ID: id, TenantID: tenant, ProjectID: p, IntakeID: in.IntakeID, IntakeVersion: version, RecipientID: in.RecipientID, State: "OPEN", Questions: string(raw), Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: at, UpdatedAt: at}
	if m.intakeSources.TimingOn != nil {
		timing, available, err := m.intakeSources.TimingOn(ctx, tx, tenant, p, in.IntakeID)
		if err != nil {
			return Receipt{}, err
		}
		if available {
			if timing.ChannelID <= 0 || timing.ChannelVersion < 1 || timing.ReminderHours < 1 || timing.TimeoutHours <= timing.ReminderHours || timing.TimeoutHours > 8760 || timing.MaxReminders < 0 || timing.MaxReminders > 5 {
				return Receipt{}, ErrInvalid
			}
			due, next := at.Add(time.Duration(timing.TimeoutHours)*time.Hour), at.Add(time.Duration(timing.ReminderHours)*time.Hour)
			row.ChannelID, row.ChannelVersion, row.ReminderHours, row.MaxReminders, row.DueAt = &timing.ChannelID, &timing.ChannelVersion, &timing.ReminderHours, &timing.MaxReminders, &due
			if timing.MaxReminders > 0 {
				row.NextReminderAt = &next
			}
		}
	}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return Receipt{}, err
	}
	if err = m.event(ctx, tx, p, id, "clarification_requested", "scope_change", 1); err != nil {
		return Receipt{}, err
	}
	for _, question := range in.Questions {
		if err := m.postLinkedIntakeMessageOn(ctx, tx, p, in.IntakeID, question); err != nil {
			return Receipt{}, err
		}
	}
	return Receipt{ID: id, Version: 1, State: "OPEN"}, nil
}

func (m *Module) loadClarificationOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, external bool) (*model.ClarificationRound, error) {
	q := query.BindClarificationRound(tx, m.names)
	r := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
	if external {
		r = r.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)))
	}
	return r.Take()
}

func (m *Module) answerClarificationOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, expected int, in ClarificationAnswer) (Receipt, error) {
	if in.QuestionNo < 1 || in.QuestionNo > 10 || !validText(in.Answer, 8000) {
		return Receipt{}, ErrInvalid
	}
	external, err := m.authorize(ctx, tx, p, "channel_read", true)
	if err != nil {
		return Receipt{}, err
	}
	if !external {
		return Receipt{}, ErrForbidden
	}
	row, err := m.loadClarificationOn(ctx, tx, p, id, true)
	if err != nil {
		return Receipt{}, err
	}
	return m.answerClarificationQuestionOn(ctx, tx, p, row, expected, in, "channel_read")
}

// Both recipient and submission-owner paths establish their own current
// authority before sharing this versioned state transition.
func (m *Module) answerClarificationQuestionOn(ctx context.Context, tx *gorm.DB, p guid.ID, row *model.ClarificationRound, expected int, in ClarificationAnswer, permission string) (Receipt, error) {
	if in.QuestionNo < 1 || in.QuestionNo > 10 || !validText(in.Answer, 8000) {
		return Receipt{}, ErrInvalid
	}
	id := row.ID
	if (row.State != "OPEN" && row.State != "EXPIRED") || row.Version != expected {
		return Receipt{}, ErrConflict
	}
	view, err := clarificationView(row)
	if err != nil {
		return Receipt{}, err
	}
	if in.QuestionNo > len(view.Questions) || view.Questions[in.QuestionNo-1].AnsweredAt != nil {
		return Receipt{}, ErrConflict
	}
	attachments, err := m.pinAnswerAttachmentsOn(ctx, tx, row.ID, in.QuestionNo, in.UploadIDs)
	if err != nil {
		return Receipt{}, err
	}
	at, actor := now(), authn.PrincipalIDFromContext(ctx)
	question := &view.Questions[in.QuestionNo-1]
	question.Attachments = attachments
	question.Answer, question.AnsweredBy, question.AnsweredAt = in.Answer, &actor, &at
	complete := true
	for _, question := range view.Questions {
		if question.AnsweredAt == nil {
			complete = false
		}
	}
	state := "OPEN"
	action := intake.ClarificationProgress
	if row.State == "EXPIRED" {
		state = "EXPIRED"
		action = intake.ClarificationLateProgress
	} else if complete {
		state = "ANSWERED"
		action = intake.ClarificationComplete
	}
	if m.intakeSources.ClarificationOn == nil {
		return Receipt{}, ErrUnavailable
	}
	intakeVersion, err := m.intakeSources.ClarificationOn(ctx, tx, row.TenantID, p, row.IntakeID, row.IntakeVersion, id, action)
	if err != nil {
		return Receipt{}, err
	}
	raw, err := json.Marshal(view.Questions)
	if err != nil {
		return Receipt{}, err
	}
	q := query.BindClarificationRound(tx, m.names)
	changes := map[string]any{"questions": string(raw), "state": state, "intake_version": intakeVersion, "version": expected + 1, "updated_at": at}
	if complete && row.State != "EXPIRED" {
		changes["next_reminder_at"] = nil
		changes["closed_at"], changes["close_reason"] = at, "ALL_ANSWERED"
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id), q.Version.Eq(expected)).Updates(changes)
	if err != nil {
		return Receipt{}, err
	}
	if result.RowsAffected != 1 {
		return Receipt{}, ErrConflict
	}
	if err = m.event(ctx, tx, p, id, "clarification_answered", permission, expected+1); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: id, Version: expected + 1, State: state}, nil
}

func (m *Module) cancelClarificationOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, expected int, reason string) (Receipt, error) {
	if !validText(reason, 2000) {
		return Receipt{}, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
		return Receipt{}, err
	}
	row, err := m.loadClarificationOn(ctx, tx, p, id, false)
	if err != nil {
		return Receipt{}, err
	}
	if row.State != "OPEN" || row.Version != expected {
		return Receipt{}, ErrConflict
	}
	if m.intakeSources.ClarificationOn == nil {
		return Receipt{}, ErrUnavailable
	}
	version, err := m.intakeSources.ClarificationOn(ctx, tx, row.TenantID, p, row.IntakeID, row.IntakeVersion, id, intake.ClarificationCancel)
	if err != nil {
		return Receipt{}, err
	}
	at := now()
	q := query.BindClarificationRound(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(p), q.ID.Eq(id), q.Version.Eq(expected)).Updates(map[string]any{"state": "CANCELLED", "next_reminder_at": nil, "version": expected + 1, "intake_version": version, "closed_at": at, "close_reason": "CANCELLED_BY_TEAM", "close_note": reason, "updated_at": at}) //nolint:misspell // Frozen domain states.
	if err != nil {
		return Receipt{}, err
	}
	if result.RowsAffected != 1 {
		return Receipt{}, ErrConflict
	}
	if err = m.event(ctx, tx, p, id, "clarification_cancelled", "scope_change", expected+1); err != nil { //nolint:misspell // Frozen event name.
		return Receipt{}, err
	}
	return Receipt{ID: id, Version: expected + 1, State: "CANCELLED"}, nil //nolint:misspell // Frozen domain state.
}
