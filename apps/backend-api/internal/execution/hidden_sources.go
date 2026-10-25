package execution

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// HiddenSourceFacts exposes only frozen execution provenance to the private
// verifier. It neither authorizes public reads nor publishes hidden payloads.
type HiddenSourceFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (HiddenSource, error)
}
type HiddenSource struct {
	PrincipalID                   guid.ID
	Family, Model, RepositoryPath string
}

func (m *Module) provideHiddenSource(s *module.Services) error {
	return module.Provide[HiddenSourceFacts](s, HiddenSourceFacts{ReadOn: m.hiddenSourceOn})
}
func (m *Module) hiddenSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, work, id guid.ID) (HiddenSource, error) {
	empty := HiddenSource{}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return empty, err
	}
	if row.ProjectID != project || row.WorkItemID != work || row.State != "SUCCEEDED" {
		return empty, ErrConflict
	}
	v, err := view(row)
	if err != nil {
		return empty, err
	}
	if v.Plan.ExecutorPrincipalID <= 0 || v.Plan.ModelFamily == "" || v.Plan.Model == "" {
		return empty, ErrCapabilityUnavailable
	}
	return HiddenSource{PrincipalID: v.Plan.ExecutorPrincipalID, Family: v.Plan.ModelFamily, Model: v.Plan.Model, RepositoryPath: v.Plan.Workdir}, nil
}
