package notification

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strconv"
)

const executionAttentionType = "execution.attention"

func (m *Module) attentionAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	if m.decisionFacts.AttentionOn == nil || m.decisionFacts.WorkReadableOn == nil {
		return false, ErrUnavailable
	}
	workID := id
	if p["subkind"] == "INTERRUPTED" {
		if m.executionFacts == nil {
			return false, ErrUnavailable
		}
		f, err := m.executionFacts(ctx, tx, row.TenantID, id)
		if errors.Is(err, execution.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if f.State != "INTERRUPTED" || f.ProjectID != row.ProjectID || strconv.FormatInt(f.DispatchEpoch, 10) != p["episode"] {
			return false, nil
		}
		workID = f.WorkItemID
	}
	f, found, err := m.decisionFacts.AttentionOn(ctx, tx, row.TenantID, workID)
	if err != nil || !found || f.ProjectID != row.ProjectID {
		return false, err
	}
	if p["subkind"] != "INTERRUPTED" && p["subkind"] != "EXECUTION_FAILED" {
		return false, ErrInvalid
	}
	if p["subkind"] == "EXECUTION_FAILED" && (f.State != "EXECUTION_FAILED" || strconv.Itoa(f.Version) != p["episode"]) {
		return false, nil
	}
	if p["subkind"] == "INTERRUPTED" && f.ExecutionID != id {
		return false, nil
	}
	yes, err := m.decisionFacts.WorkReadableOn(ctx, tx, row.TenantID, workID, actor)
	if err != nil || !yes {
		return false, err
	}
	if f.ExecutorKind == "HUMAN" {
		return f.ExecutorID == actor, nil
	}
	if f.ExecutorKind != "AGENT" {
		return false, nil
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
}
func (m *Module) attentionParamsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (map[string]string, error) {
	p := map[string]string{"subject_id": id.String(), "subkind": kind}
	if kind == "INTERRUPTED" {
		f, err := m.executionFacts(ctx, tx, tenant, id)
		if errors.Is(err, execution.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if f.State != "INTERRUPTED" {
			return nil, nil
		}
		p["episode"] = strconv.FormatInt(f.DispatchEpoch, 10)
	} else {
		f, found, err := m.decisionFacts.AttentionOn(ctx, tx, tenant, id)
		if err != nil || !found || f.State != "EXECUTION_FAILED" {
			return nil, err
		}
		p["episode"] = strconv.Itoa(f.Version)
	}
	return p, nil
}
func (m *Module) projectAttentionOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, kind string) error {
	if m.candidates == nil || m.decisionFacts.AttentionOn == nil || m.executionFacts == nil {
		return ErrUnavailable
	}
	p, err := m.attentionParamsOn(ctx, tx, e.TenantID, e.SubjectID, kind)
	if err != nil || p == nil {
		return err
	}
	raw, _ := json.Marshal(p)
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
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: executionAttentionType, Permission: "execution_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			path := "/work-items/" + e.SubjectID.String()
			if kind == "INTERRUPTED" {
				path = "/executions/" + e.SubjectID.String()
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: executionAttentionType, Severity: Action, Permission: "execution_read", TemplateKey: "notification.execution_attention", Params: p, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + path, DedupeKey: "attention:" + kind + ":" + e.SubjectID.String() + ":" + p["episode"]}, e.ID, e.SubjectID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
func (m *Module) attentionInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, governance.ErrForbidden
	}
	if req.Kind != "EXECUTION_ATTENTION" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, governance.ErrInvalidArgument
	}
	if m.attentionExecutions.ListOn == nil || m.decisionFacts.AttentionWorksOn == nil {
		return nil, governance.ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	out := []governance.InboxItem{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		xs, err := m.attentionExecutions.ListOn(ctx, tx, req.TenantID, req.ProjectID)
		if err != nil {
			return err
		}
		ws, err := m.decisionFacts.AttentionWorksOn(ctx, tx, req.TenantID, req.ProjectID)
		if err != nil {
			return err
		}
		for _, id := range ws {
			f, found, err := m.decisionFacts.AttentionOn(ctx, tx, req.TenantID, id)
			if err != nil {
				return err
			}
			if found {
				xs = append(xs, execution.AttentionExecution{ID: id, ProjectID: f.ProjectID, CreatedAt: f.ChangedAt})
			}
		}
		for _, x := range xs {
			kind := "INTERRUPTED"
			object := "execution"
			if x.WorkItemID == 0 {
				kind = "EXECUTION_FAILED"
				object = "work_item"
			}
			p, err := m.attentionParamsOn(ctx, tx, req.TenantID, x.ID, kind)
			if err != nil {
				return err
			}
			if p == nil {
				continue
			}
			raw, _ := json.Marshal(p)
			row := &model.Notification{TenantID: req.TenantID, ProjectID: x.ProjectID, RecipientPrincipalID: req.PrincipalID, Type: executionAttentionType, Permission: "execution_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, req.PrincipalID)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "HIGH", CreatedAt: x.CreatedAt, Kind: req.Kind, ID: x.ID}, ProjectID: x.ProjectID, Title: kind, Reason: kind, ObjectType: object, ObjectID: x.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
