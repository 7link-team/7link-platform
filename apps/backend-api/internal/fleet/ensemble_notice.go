package fleet

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// EnsembleDecisionFacts returns current persisted requests and actual actors.
// Creating a historical comparison plan never implies a request or approval.
type EnsembleDecisionFacts struct {
	PendingOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]EnsemblePendingDecision, error)
	ReadOn     func(context.Context, *gorm.DB, guid.ID, guid.ID) (EnsembleDecisionFact, bool, error)
	EligibleOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}

type EnsemblePendingDecision struct {
	ID, ProjectID, WorkItemID guid.ID
	Kind                      string
	CreatedAt                 time.Time
}

type EnsembleDecisionFact struct {
	ProjectID, WorkItemID             guid.ID
	Version                           int
	ApprovalPending, SelectionPending bool
	ApprovedBy, SelectedBy            *guid.ID
}

func (m *Module) provideEnsembleNotices(s *module.Services) error {
	return module.Provide[EnsembleDecisionFacts](s, EnsembleDecisionFacts{PendingOn: m.ensemblePendingDecisionsOn, ReadOn: m.ensembleDecisionFactOn, EligibleOn: m.ensembleDecisionEligibleOn})
}

// Pending candidates are unpaginated so consumers can apply current eligibility
// before their shared inbox ordering and limit.
func (m *Module) ensemblePendingDecisionsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]EnsemblePendingDecision, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	q := query.BindEnsemblePlans(tx, m.names)
	scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("DRAFT", "COMPARING"))
	if project > 0 {
		scope = scope.Where(q.ProjectID.Eq(project))
	}
	rows, err := scope.Find()
	if err != nil {
		return nil, err
	}
	out := []EnsemblePendingDecision{}
	for _, r := range rows {
		if r.State == "DRAFT" && r.ApprovalRequestedAt != nil && r.ApprovedBy == nil {
			out = append(out, EnsemblePendingDecision{r.ID, r.ProjectID, r.WorkItemID, "ENSEMBLE_APPROVAL", *r.ApprovalRequestedAt})
		} else if r.State == "COMPARING" && r.SelectedBy == nil {
			out = append(out, EnsemblePendingDecision{r.ID, r.ProjectID, r.WorkItemID, "ENSEMBLE_SELECTION", r.UpdatedAt})
		}
	}
	return out, nil
}

func (m *Module) ensembleDecisionFactOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (EnsembleDecisionFact, bool, error) {
	var out EnsembleDecisionFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindEnsemblePlans(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out = EnsembleDecisionFact{ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, Version: r.Version, ApprovalPending: r.State == "DRAFT" && r.ApprovalRequestedAt != nil && r.ApprovedBy == nil, SelectionPending: r.State == "COMPARING" && r.SelectedBy == nil, ApprovedBy: r.ApprovedBy, SelectedBy: r.SelectedBy}
	return out, true, nil
}

func (m *Module) ensembleDecisionEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID) (bool, error) {
	f, found, err := m.ensembleDecisionFactOn(ctx, tx, tenant, id)
	if err != nil || !found {
		return false, err
	}
	if actor <= 0 || m.principals.ActiveHumanByID == nil {
		return false, ErrUnavailable
	}
	p, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil || !active || p.ID != actor {
		return false, err
	}
	c := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	err = m.ensembleAuthority(c, tx, tenant, f.ProjectID, f.WorkItemID, "work_item_assign")
	if errors.Is(err, ErrMachineForbidden) || errors.Is(err, ErrMachineNotFound) {
		return false, nil
	}
	return err == nil, err
}
