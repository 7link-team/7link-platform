package governance

import "7link.com/platform/pkg/module"

// SystemActionAllowed exposes only the sealed startup catalog. It grants no
// creator authority and cannot register or expand an action at runtime.
type SystemActionAllowed func(job, code string) bool

func (m *Module) provideSystemActions(s *module.Services) error {
	return module.Provide[SystemActionAllowed](s, SystemActionAllowed(m.systemActions.allows))
}
