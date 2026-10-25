package notification

import (
	"context"
	"encoding/json"
	"strconv"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) ensembleAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.ensembleFacts.ReadOn == nil || m.ensembleFacts.EligibleOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	f, found, err := m.ensembleFacts.ReadOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || f.ProjectID != row.ProjectID {
		return false, err
	}
	if !(row.Type == "ensemble.approval_requested" && f.ApprovalPending || row.Type == "ensemble.selection_requested" && f.SelectionPending) {
		return false, nil
	}
	if row.Type == "ensemble.selection_requested" {
		lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
		if err != nil || !lead {
			return false, err
		}
	}
	return m.ensembleFacts.EligibleOn(ctx, tx, row.TenantID, id, actor)
}

func (m *Module) projectEnsembleOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if !validTransaction(tx) || e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "ensemble_plan" {
		return ErrInvalid
	}
	if m.ensembleFacts.ReadOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	f, found, err := m.ensembleFacts.ReadOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || f.ProjectID != e.ProjectID {
		return err
	}
	typ, key := "", ""
	if f.ApprovalPending {
		typ, key = "ensemble.approval_requested", "notification.ensemble_approval"
	} else if f.SelectionPending {
		typ, key = "ensemble.selection_requested", "notification.ensemble_selection"
	} else {
		return nil
	}
	params := map[string]string{"subject_id": e.SubjectID.String()}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, actor := range ids {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: typ, Permission: "work_item_assign", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: typ, Severity: Action, Permission: "work_item_assign", TemplateKey: key, Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/heavy?work_item_id=" + f.WorkItemID.String() + "&plan_id=" + e.SubjectID.String(), DedupeKey: typ + ":" + e.SubjectID.String() + ":" + strconv.Itoa(f.Version)}, e.SubjectID, e.SubjectID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
