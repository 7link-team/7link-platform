package notification

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) reviewSheetAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.decisionFacts.ReviewSheetOn == nil || m.decisionFacts.ReviewSheetEligibleOn == nil {
		return false, ErrUnavailable
	}
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	f, found, err := m.decisionFacts.ReviewSheetOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || f.ProjectID != row.ProjectID {
		return false, err
	}
	if row.Type == "review_sheet.due" {
		ordinal, err := strconv.Atoi(p["ordinal"])
		if err != nil || ordinal < 1 || ordinal > 100000 {
			return false, ErrInvalid
		}
		if ordinal != 1 {
			return false, nil
		}
		if f.DueAt == nil || m.now().Before(f.DueAt.Add(time.Duration(ordinal-2)*24*time.Hour)) {
			return false, nil
		}
	}
	return m.decisionFacts.ReviewSheetEligibleOn(ctx, tx, row.TenantID, id, actor, row.Type)
}

func (m *Module) projectReviewSheetOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "review_sheet" {
		return ErrInvalid
	}
	if m.decisionFacts.ReviewSheetOn == nil {
		return ErrUnavailable
	}
	f, found, err := m.decisionFacts.ReviewSheetOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || f.ProjectID != e.ProjectID {
		return err
	}
	typ, key := "review_sheet.opened", "review_sheet_opened"
	recipients := f.Reviewers
	if e.Type == "review_sheet.closed" {
		typ, key = "review_sheet.rejected", "review_sheet_rejected"
		recipients = []guid.ID{f.InitiatorID}
		if f.State != "REJECTED" {
			return nil
		}
	} else if f.State != "OPEN" {
		return nil
	}
	params := map[string]string{"subject_id": e.SubjectID.String()}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	recipients = slices.Clone(recipients)
	slices.Sort(recipients)
	for _, id := range slices.Compact(recipients) {
		row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Type: typ, Permission: "requirement_read", Params: string(raw)}
		yes, err := m.visibleOn(ctx, tx, row, id)
		if err != nil {
			return err
		}
		if !yes {
			continue
		}
		_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: Action, TemplateKey: "notification." + key, Permission: "requirement_read", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/review-sheet?review_sheet_id=" + e.SubjectID.String(), DedupeKey: typ + ":" + e.SubjectID.String()})
		if err != nil {
			return err
		}
	}
	if typ == "review_sheet.opened" && f.DueAt != nil {
		return m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: "review", SourceID: e.SubjectID, Ordinal: 1})
	}
	return nil
}
