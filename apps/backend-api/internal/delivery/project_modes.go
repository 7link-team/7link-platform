package delivery

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

// ProjectModeSwitcher permits the governance owner to execute an already
// authorized takeover without opening a second transaction.
type ProjectModeSwitcher struct {
	TakeOverOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, string, string, []guid.ID) (ProjectModeReceipt, error)
}

func (m *Module) provideProjectModes(s *module.Services) error {
	return module.Provide[ProjectModeSwitcher](s, ProjectModeSwitcher{TakeOverOn: func(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, version int, reason, cookie, proof string, locked []guid.ID) (ProjectModeReceipt, error) {
		return m.changeProjectModeOn(ctx, tx, tenant, project, actor, version, projectModeBody{ToMode: "HYBRID", Reason: reason, ReauthProof: proof}, cookie, true, locked)
	}})
}

func (m *Module) injectModes(s *module.Services) error {
	capability, err := module.Resolve[governance.ProjectModeGovernance](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	m.modeGovernance = capability
	return nil
}
