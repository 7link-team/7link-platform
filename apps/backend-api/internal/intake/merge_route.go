package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
)

// MergeRoute is an internal routing fact, never a client projection. The caller
// holds the current project fence; source records and threads stay immutable.
type MergeRoute struct {
	SourceID, TargetID, TargetContactID guid.ID
	SourceRef                           int
	TargetRequirementID                 *guid.ID
}

type ClientMergeProjection struct {
	IntakeID  guid.ID  `json:"intake_id"`
	State     string   `json:"state"`
	Merged    bool     `json:"merged"`
	TargetID  *guid.ID `json:"target_id,omitempty"`
	TargetRef *int     `json:"target_ref,omitempty"`
}

func (m *Module) mergeRecordsOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) (*model.Record, *model.Record, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || source <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	first, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(source)).First()
	if err != nil {
		return nil, nil, err
	}
	row := first
	seen := map[guid.ID]bool{}
	for row.Status == "MERGED" {
		if seen[row.ID] || len(seen) >= 32 || row.MergedIntoIntakeID == nil || *row.MergedIntoIntakeID <= 0 {
			return nil, nil, ErrConflict
		}
		seen[row.ID] = true
		row, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(*row.MergedIntoIntakeID)).First()
		if err != nil {
			return nil, nil, err
		}
	}
	return first, row, nil
}

func (m *Module) mergeRouteOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) (MergeRoute, error) {
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
		return MergeRoute{}, err
	}
	first, target, err := m.mergeRecordsOn(ctx, tx, tenant, project, source)
	if err != nil {
		return MergeRoute{}, err
	}
	return MergeRoute{SourceID: first.ID, SourceRef: first.RefNo, TargetID: target.ID, TargetContactID: target.ContactID, TargetRequirementID: target.RequirementID}, nil
}

// Only the original active contact can see this projection. Every contact in
// the chain must match before revealing any target identity, even if two
// contacts happen to share a principal. No title/content or internal note leaks.
func (m *Module) clientMergeOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) (ClientMergeProjection, error) {
	first, target, err := m.mergeRecordsOn(ctx, tx, tenant, project, source)
	if err != nil {
		return ClientMergeProjection{}, err
	}
	c := query.BindContacts(tx, m.names)
	_, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(first.ContactID), c.PrincipalID.Eq(authn.PrincipalIDFromContext(ctx)), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).First()
	if err != nil {
		return ClientMergeProjection{}, err
	}
	out := ClientMergeProjection{IntakeID: first.ID, State: first.Status, Merged: first.Status == "MERGED"}
	if !out.Merged {
		return out, nil
	}
	q := query.BindRecords(tx, m.names)
	row := first
	for row.ID != target.ID {
		row, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(*row.MergedIntoIntakeID)).First()
		if err != nil {
			return ClientMergeProjection{}, err
		}
		if row.ContactID != first.ContactID {
			return out, nil
		}
	}
	out.TargetID = &target.ID
	out.TargetRef = &target.RefNo
	return out, nil
}
