package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"fmt"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"
)

type scopeCriterionReplacement struct {
	CriterionID guid.ID `json:"criterion_id"`
	Text        string  `json:"text" minLength:"1" maxLength:"2000"`
	Required    bool    `json:"required"`
}
type scopeCriteriaBody struct {
	Add       []requirementCriterionInput `json:"add,omitempty" maxItems:"50"`
	Retire    []guid.ID                   `json:"retire,omitempty" maxItems:"50"`
	Supersede []scopeCriterionReplacement `json:"supersede,omitempty" maxItems:"50"`
}
type scopeCriteriaDiff struct {
	Added      []guid.ID            `json:"added"`
	Retired    []guid.ID            `json:"retired"`
	Superseded []scopeCriterionPair `json:"superseded"`
}
type scopeCriterionPair struct {
	From guid.ID `json:"from"`
	To   guid.ID `json:"to"`
}

// applyScopeCriteriaOn preserves old rows and stable codes. Replacement creates
// a new criterion; neither the retired row nor an older snapshot is overwritten.
func (m *Module) applyScopeCriteriaOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, next int, in scopeCriteriaBody) ([]requirementCriterionView, scopeCriteriaDiff, error) {
	diff := scopeCriteriaDiff{Added: []guid.ID{}, Retired: []guid.ID{}, Superseded: []scopeCriterionPair{}}
	if len(in.Add) > 50 || len(in.Retire) > 50 || len(in.Supersede) > 50 {
		return nil, diff, ErrInvalidArgument
	}
	q := query.BindCriteria(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.RequirementID.Eq(id)).Order(q.Position, q.ID).Find()
	if err != nil {
		return nil, diff, err
	}
	active := map[guid.ID]*model.AcceptanceCriterion{}
	maxCode, maxPosition := 0, -1
	for _, r := range rows {
		n, e := strconv.Atoi(strings.TrimPrefix(r.Code, "C"))
		if e != nil || n < 1 || !strings.HasPrefix(r.Code, "C") {
			return nil, diff, ErrUnavailable
		}
		maxCode = max(maxCode, n)
		if r.ArchivedAt == nil && r.RetiredInVersion == nil {
			active[r.ID] = r
			maxPosition = max(maxPosition, r.Position)
		}
	}
	retire := func(cid guid.ID) error {
		r, ok := active[cid]
		if !ok {
			return ErrInvalidArgument
		}
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(cid), q.RetiredInVersion.IsNull()).UpdateSimple(q.RetiredInVersion.Value(next))
		if e != nil {
			return e
		}
		if result.RowsAffected != 1 {
			return ErrRequirementVersion
		}
		delete(active, r.ID)
		return nil
	}
	for _, cid := range in.Retire {
		if err = retire(cid); err != nil {
			return nil, diff, err
		}
		diff.Retired = append(diff.Retired, cid)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	add := func(item requirementCriterionInput, supersedes *guid.ID) (guid.ID, error) {
		validated, e := validateRequirement(requirementCreateBody{Title: "scope", Criteria: []requirementCriterionInput{item}})
		if e != nil {
			return 0, e
		}
		value, e := m.ids.Next()
		if e != nil {
			return 0, e
		}
		maxCode++
		maxPosition++
		r := &model.AcceptanceCriterion{TenantID: tenant, ID: guid.ID(value), ProjectID: project, RequirementID: id, Code: fmt.Sprintf("C%d", maxCode), Text: validated.Criteria[0].Text, Required: item.Required, Position: maxPosition, IntroducedInVersion: next, SupersedesID: supersedes, CreatedAt: now, CreatedBy: &actor}
		if e = q.WithContext(ctx).Create(r); e != nil {
			return 0, e
		}
		active[r.ID] = r
		return r.ID, nil
	}
	for _, replacement := range in.Supersede {
		if err = retire(replacement.CriterionID); err != nil {
			return nil, diff, err
		}
		newID, e := add(requirementCriterionInput{Text: replacement.Text, Required: replacement.Required}, &replacement.CriterionID)
		if e != nil {
			return nil, diff, e
		}
		diff.Superseded = append(diff.Superseded, scopeCriterionPair{From: replacement.CriterionID, To: newID})
	}
	for _, item := range in.Add {
		cid, e := add(item, nil)
		if e != nil {
			return nil, diff, e
		}
		diff.Added = append(diff.Added, cid)
	}
	if len(active) > 50 {
		return nil, diff, ErrCriterionLimit
	}
	current, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.RequirementID.Eq(id), q.ArchivedAt.IsNull(), q.RetiredInVersion.IsNull()).Order(q.Position, q.ID).Find()
	if err != nil {
		return nil, diff, err
	}
	view := make([]requirementCriterionView, 0, len(current))
	required := false
	for _, r := range current {
		required = required || r.Required
		view = append(view, requirementCriterionRecord(r))
	}
	if !required {
		return nil, diff, ErrInvalidArgument
	}
	return view, diff, nil
}
