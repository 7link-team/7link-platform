package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
	"time"
)

type workBaselineBody struct {
	RequirementVersionNo int       `json:"requirement_version_no" minimum:"1"`
	CriterionIDs         []guid.ID `json:"criterion_ids" maxItems:"50"`
}
type workBaselineView struct {
	WorkItemID        guid.ID `json:"work_item_id"`
	Version           int     `json:"version"`
	BaselineVersionNo int     `json:"baseline_version_no"`
	ContractVersionNo int     `json:"contract_version_no"`
	ContentHash       string  `json:"content_hash"`
}

// W6 reaffirms an existing Human brief against the current Requirement. Task
// clauses keep their IDs and text; selected Requirement clauses are explicitly
// captured in the new immutable contract, without inferring a text mapping.
func (m *Module) workBaselineOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, expected int, in workBaselineBody) (workBaselineView, error) {
	var empty workBaselineView
	if in.RequirementVersionNo < 1 || len(in.CriterionIDs) > 50 {
		return empty, ErrInvalidArgument
	}
	_, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
	if err != nil {
		return empty, err
	}
	if work.Version != expected {
		return empty, ErrWorkVersion
	}
	if work.RequirementID == nil || work.CurrentExecutionID != nil || work.PausedAt != nil || !slices.Contains([]string{"DRAFT", "READY", "BLOCKED", "CLAIMED", "IN_PROGRESS", "REWORK"}, work.State) {
		return empty, ErrWorkPreparationState
	}
	requirement, err := m.readRequirement(ctx, tx, tenant, project, *work.RequirementID, actor)
	if err != nil {
		return empty, err
	}
	if requirement.Status != "BASELINED" || requirement.CurrentVersionNo != in.RequirementVersionNo {
		return empty, ErrRequirementVersion
	}
	valid := map[guid.ID]bool{}
	for _, c := range requirement.Criteria {
		valid[c.ID] = true
	}
	selection := slices.Clone(in.CriterionIDs)
	slices.Sort(selection)
	for i, cid := range selection {
		if !valid[cid] || i > 0 && cid == selection[i-1] {
			return empty, ErrInvalidArgument
		}
	}
	q := query.BindWorkContracts(tx, m.service.names)
	latest, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(id)).Order(q.VersionNo.Desc()).First()
	if err != nil {
		return empty, err
	}
	var snapshot workContractSnapshot
	if err = json.Unmarshal([]byte(latest.Snapshot), &snapshot); err != nil {
		return empty, err
	}
	oldSelection := slices.Clone(snapshot.RequirementCriterionIDs)
	slices.Sort(oldSelection)
	if work.BaselineVersionNo == in.RequirementVersionNo && !work.BaselineStale && slices.Equal(selection, oldSelection) {
		return workBaselineView{WorkItemID: id, Version: work.Version, BaselineVersionNo: work.BaselineVersionNo, ContractVersionNo: latest.VersionNo, ContentHash: latest.ContentHash}, nil
	}
	snapshot.BaselineVersionNo = in.RequirementVersionNo
	snapshot.RequirementCriterionIDs = selection
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	contract := &model.WorkItemContract{TenantID: tenant, ID: guid.ID(value), ProjectID: project, WorkItemID: id, VersionNo: latest.VersionNo + 1, Snapshot: string(encoded), ContentHash: hash, ConfirmedAt: now, ConfirmedBy: actor}
	if err = q.WithContext(ctx).Create(contract); err != nil {
		return empty, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.Version.Eq(expected)).UpdateSimple(w.BaselineVersionNo.Value(in.RequirementVersionNo), w.BaselineStale.Value(false), w.BaselineStaleSince.Null())
	if err != nil {
		return empty, err
	}
	if changed.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	work.BaselineVersionNo = in.RequirementVersionNo
	work.BaselineStale = false
	work.BaselineStaleSince = nil
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "WORK_BASELINE_CONFIRMED", "permission:work_item_write", 0, now); err != nil {
		return empty, err
	}
	return workBaselineView{WorkItemID: id, Version: work.Version, BaselineVersionNo: work.BaselineVersionNo, ContractVersionNo: contract.VersionNo, ContentHash: hash}, nil
}
