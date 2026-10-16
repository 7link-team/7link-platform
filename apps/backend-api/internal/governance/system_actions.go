package governance

import (
	"fmt"
	"regexp"
	"sync"

	"7link.com/platform/pkg/extension/authz"
)

var authoritySourceSuffixPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,80}$`)

// SystemActions is the startup contribution contract frozen by spec08.
// A job may register several actions; each pair is declared explicitly.
type SystemActions interface {
	Register(job, code string) error
}

// systemActionRegistry is read only after governance initialization. Registration
// is an S1 prerequisite, never a substitute for the complete authority decision.
type systemActionRegistry struct {
	mu     sync.RWMutex
	jobs   map[string]map[string]bool
	sealed bool
}

func (s *systemActionRegistry) Register(job, code string) error {
	if s == nil {
		return ErrUnavailable
	}
	if !authoritySourceSuffixPattern.MatchString(job) || code == "" || RulesForAction(code, "SYSTEM").NonDelegable {
		return fmt.Errorf("%w: invalid system action %q for %q", ErrInvalidArgument, code, job)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		return fmt.Errorf("%w: system action catalog is sealed", ErrInvalidArgument)
	}
	if s.jobs[job][code] {
		return fmt.Errorf("%w: duplicate system action %q for %q", ErrInvalidArgument, code, job)
	}
	if s.jobs == nil {
		s.jobs = make(map[string]map[string]bool)
	}
	if s.jobs[job] == nil {
		s.jobs[job] = make(map[string]bool)
	}
	s.jobs[job][code] = true
	return nil
}

func (s *systemActionRegistry) seal(registry *authz.Registry) error {
	if s == nil || registry == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		return nil
	}
	for job, codes := range s.jobs {
		for code := range codes {
			action, known := registry.Find(code)
			if !known || action.Scope != authz.ActionScopeTenant || RulesForAction(code, "SYSTEM").NonDelegable {
				return fmt.Errorf("%w: system job %q references unavailable or prohibited action %q", ErrInvalidArgument, job, code)
			}
		}
	}
	s.sealed = true
	return nil
}

// allows reports an exact registered pair only after successful initialization.
// An unregistered job, wildcard or unknown code never gains authority by prefix.
func (s *systemActionRegistry) allows(job, code string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sealed && s.jobs[job][code]
}

var _ SystemActions = (*systemActionRegistry)(nil)
