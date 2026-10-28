package app

import (
	"fmt"

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
		if provider, ok := current.(jobs.NamespaceProvider); ok {
			owner = provider.JobNamespace()
		}
		if catalog == nil {
			var err error
			catalog, err = module.Resolve[jobs.Catalog](registry.Services())
			if err != nil {
				return fmt.Errorf("job declarations from %s: %w", current.ModuleName(), err)
			}
		}
		if hasHandlers {
			declared := append([]jobs.Handler(nil), handlers.JobHandlers()...)
			for i := range declared {
				declared[i].Module = current.ModuleName()
			}
			if err := catalog.AddHandlers(owner, declared...); err != nil {
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
