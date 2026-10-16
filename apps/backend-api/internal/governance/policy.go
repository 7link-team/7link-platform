package governance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"7link.com/platform/pkg/extension/authz"
)

// PolicyRegistry is the startup contribution contract frozen by spec08 §3.5.
type PolicyRegistry interface{ Register(PolicyRule) error }

// PolicyRule contributes a read-only S7 predicate and optional S8 denial reason.
// Errors fail closed; Evaluate must never reserve resources or mutate state.
type PolicyRule struct {
	ID       string
	Codes    []string
	Evaluate func(context.Context, AuthorityRequest) (bool, error)
	Explain  func(context.Context, AuthorityRequest) string
}

type policyRegistry struct {
	mu     sync.RWMutex
	rules  []PolicyRule
	sealed bool
}

func (p *policyRegistry) Register(rule PolicyRule) error {
	if p == nil {
		return ErrUnavailable
	}
	if !authoritySourceSuffixPattern.MatchString(rule.ID) || len(rule.Codes) == 0 || rule.Evaluate == nil {
		return fmt.Errorf("%w: invalid policy declaration", ErrInvalidArgument)
	}
	seen := make(map[string]bool, len(rule.Codes))
	for _, code := range rule.Codes {
		if code == "" || seen[code] || RulesForAction(code, "").NonDelegable || strings.HasPrefix(code, "decision_") {
			return fmt.Errorf("%w: policy %q cannot cover %q", ErrInvalidArgument, rule.ID, code)
		}
		seen[code] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sealed {
		return fmt.Errorf("%w: policy catalog is sealed", ErrInvalidArgument)
	}
	for _, existing := range p.rules {
		if existing.ID == rule.ID {
			return fmt.Errorf("%w: duplicate policy %q", ErrInvalidArgument, rule.ID)
		}
	}
	rule.Codes = slices.Clone(rule.Codes)
	p.rules = append(p.rules, rule)
	return nil
}

func (p *policyRegistry) seal(registry *authz.Registry) error {
	if p == nil || registry == nil {
		return ErrUnavailable
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sealed {
		return nil
	}
	for _, rule := range p.rules {
		for _, code := range rule.Codes {
			action, known := registry.Find(code)
			if !known || action.Scope != authz.ActionScopeTenant {
				return fmt.Errorf("%w: policy %q references unavailable action %q", ErrInvalidArgument, rule.ID, code)
			}
		}
	}
	p.sealed = true
	return nil
}

type policyOutcome struct{ source, reason string }

// evaluate is called only after the authority checker's earlier gates. Policies
// run in contribution order; first match wins. If none matches, retain the first
// nonempty explanation in that same order. No registry lock spans module code.
func (p *policyRegistry) evaluate(ctx context.Context, req AuthorityRequest) (policyOutcome, error) {
	if p == nil {
		return policyOutcome{}, ErrUnavailable
	}
	p.mu.RLock()
	if !p.sealed {
		p.mu.RUnlock()
		return policyOutcome{}, ErrUnavailable
	}
	rules := p.rules // immutable after sealing
	p.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return policyOutcome{}, err
	}
	for _, rule := range rules {
		if !slices.Contains(rule.Codes, req.Code) {
			continue
		}
		matched, err := rule.Evaluate(ctx, copyAuthorityRequest(req))
		if err != nil {
			return policyOutcome{}, err
		}
		if err = ctx.Err(); err != nil {
			return policyOutcome{}, err
		}
		if matched {
			return policyOutcome{source: "POLICY:" + rule.ID}, nil
		}
	}
	// Explain belongs to S8, after every S7 candidate has failed to match.
	// In particular it must not cancel or otherwise affect a later valid match.
	outcome := policyOutcome{reason: "NO_AUTHORITY"}
	for _, rule := range rules {
		if !slices.Contains(rule.Codes, req.Code) || rule.Explain == nil {
			continue
		}
		reason := rule.Explain(ctx, copyAuthorityRequest(req))
		if err := ctx.Err(); err != nil {
			return policyOutcome{}, err
		}
		if reason != "" {
			outcome.reason = reason
			break
		}
	}

	return outcome, nil
}

// Each callback gets its own facts, including nested slices, so an accidental
// mutation cannot alter another policy's evidence or the caller's request.
func copyAuthorityRequest(req AuthorityRequest) AuthorityRequest {
	if req.Facts != nil {
		facts := *req.Facts
		req.Facts = &facts
	}
	if req.Spawn != nil {
		spawn := *req.Spawn
		spawn.ProjectIDs = slices.Clone(spawn.ProjectIDs)
		req.Spawn = &spawn
	}
	return req
}

var _ PolicyRegistry = (*policyRegistry)(nil)
