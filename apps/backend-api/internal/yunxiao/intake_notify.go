package yunxiao

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// IntakeNotifyTargets resolves current configured Human recipients for the
// actual receiving channel. Revoked membership/read scope removes recipients.
type IntakeNotifyTargets func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)

func (m *Module) provideIntakeNotify(s *module.Services) error {
	if err := module.Provide[WorklogApprovalCount](s, m.worklogApprovalCountOn); err != nil {
		return err
	}
	return module.Provide[IntakeNotifyTargets](s, m.intakeNotifyTargetsOn)
}

func (m *Module) intakeNotifyEligibleOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	target := authn.Claims{TenantID: tenant, EntityID: project, PrincipalID: id, SubjectType: authn.SubjectTypePrincipal}
	targetCtx := authn.WithClaims(ctx, &target)
	for _, permission := range []string{"project_read", "intake_read"} {
		if err := m.projectAuthorityOn(targetCtx, tx, tenant, project, permission); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) intakeNotifyTargetsOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID) ([]guid.ID, error) {
	q := query.BindIntakeChannels(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []guid.ID{}, nil
	}
	if err != nil {
		return nil, err
	}
	view, err := channelView(row)
	if err != nil {
		return nil, err
	}
	out := []guid.ID{}
	if !view.Settings.Enabled {
		return out, nil
	}
	for _, id := range view.Settings.NotifyPrincipalIDs {
		err := m.intakeNotifyEligibleOn(ctx, tx, tenant, project, id)
		if errors.Is(err, ErrForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}
