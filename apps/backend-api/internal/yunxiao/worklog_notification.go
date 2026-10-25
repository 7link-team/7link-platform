package yunxiao

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// WorklogApprovalCount counts only current independent Human approvals from
// submissions already present at the summary cutoff. It exposes no work text.
type WorklogApprovalCount func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (int, error)

func (m *Module) worklogApprovalCountOn(ctx context.Context, tx *gorm.DB, tenant, actor guid.ID, cutoff time.Time) (int, error) {
	if tx == nil || tenant <= 0 || actor <= 0 || cutoff.IsZero() {
		return 0, ErrInvalid
	}
	targetCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	q := query.BindWorklogs(tx, m.names)
	count := 0
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.PrincipalID.Neq(actor), q.State.Eq("SUBMITTED"), q.ActorKind.Eq("HUMAN"), q.UpdatedAt.Lte(cutoff)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return 0, err
		}
		for _, row := range rows {
			after = row.ID
			for _, permission := range []string{"work_item_read", "worklog_approve"} {
				err = m.projectAuthorityOn(targetCtx, tx, tenant, row.ProjectID, permission)
				if err != nil {
					break
				}
			}
			if errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrInvalidMember) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrWorkProjectReadOnly) {
				continue
			}
			if err != nil {
				return 0, err
			}
			count++
		}
		if len(rows) < 100 {
			return count, nil
		}
	}
}
