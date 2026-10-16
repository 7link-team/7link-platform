package app

import (
	"fmt"

	businessgovernance "7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
)

// wireJobCatalog collects only activated modules, after dependency injection
// and before jobs.Initialize validates references and seals the catalog.
func wireJobCatalog(registry *module.Registry) error {
	var catalog jobs.Catalog
	for _, current := range registry.Modules() {
		handlers, hasHandlers := current.(jobs.HandlerProvider)
		schedules, hasSchedules := current.(jobs.ScheduleProvider)
		if !hasHandlers && !hasSchedules {
			continue
		}
		owner := current.ModuleName()
		if _, ok := current.(*businessgovernance.Module); ok {
			owner = businessgovernance.JobNamespace
		}
		if catalog == nil {
			var err error
			catalog, err = module.Resolve[jobs.Catalog](registry.Services())
			if err != nil {
				return fmt.Errorf("job declarations from %s: %w", current.ModuleName(), err)
			}
		}
		if hasHandlers {
			if err := catalog.AddHandlers(owner, handlers.JobHandlers()...); err != nil {
				return fmt.Errorf("job handlers from %s: %w", current.ModuleName(), err)
			}
		}
		if hasSchedules {
			if err := catalog.AddSchedules(owner, schedules.JobSchedules()...); err != nil {
				return fmt.Errorf("job schedules from %s: %w", current.ModuleName(), err)
			}
		}
	}
	return nil
}
