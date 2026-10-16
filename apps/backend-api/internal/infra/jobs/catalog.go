package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/robfig/cron/v3"
)

var (
	ErrInvalidDeclaration = errors.New("jobs: invalid declaration")
	ErrCatalogSealed      = errors.New("jobs: catalog sealed")
	ErrUnknownKind        = errors.New("jobs: unknown kind")
	ErrInvalidPayload     = errors.New("jobs: payload must be a JSON object of at most 64 KiB")
	namespacePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	jobNamePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	cronParser            = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
)

// Job is the execution envelope passed to a registered handler.
type Job struct {
	ID, TenantID, CreatedBy    guid.ID
	Kind, Queue, CorrelationID string
	Payload                    json.RawMessage
	Attempt, MaxAttempts       int
}

// Handler declares execution limits. Zero values select the documented defaults.
// Executions are audited; declarations normalize Audit to true.
type Handler struct {
	Kind, Queue             string
	Priority                int
	Timeout                 time.Duration
	MaxAttempts             int
	BackoffBase, BackoffMax time.Duration
	Audit                   bool
	Run                     func(context.Context, Job) error
}

type TenantScope string

const (
	ScopePlatform  TenantScope = "PLATFORM"
	ScopePerTenant TenantScope = "PER_TENANT"
)

type Misfire string

const (
	MisfireCoalesce Misfire = "COALESCE"
	MisfireSkip     Misfire = "SKIP"
)

// Schedule declares a fixed interval or standard five-field cron in an IANA zone.
type Schedule struct {
	Name, Kind     string
	Interval       time.Duration
	Cron, Timezone string
	TenantScope    TenantScope
	Payload        any
	Misfire        Misfire
}
type HandlerProvider interface {
	module.Module
	JobHandlers() []Handler
}
type ScheduleProvider interface {
	module.Module
	JobSchedules() []Schedule
}

// Catalog accepts declarations before Initialize seals and validates references.
// Each call is atomic: a bad declaration leaves the whole batch unregistered.
type Catalog interface {
	AddHandlers(owner string, handlers ...Handler) error
	AddSchedules(owner string, schedules ...Schedule) error
}

func ownedName(owner, name string) bool {
	return namespacePattern.MatchString(owner) && len(name) <= 128 && jobNamePattern.MatchString(name) && strings.HasPrefix(name, owner+".")
}

func normalizeHandler(owner string, h Handler) (Handler, error) {
	if h.Queue == "" {
		h.Queue = "default"
	}
	if h.Priority == 0 {
		h.Priority = 5
	}
	if h.Timeout == 0 {
		h.Timeout = 5 * time.Minute
	}
	if h.MaxAttempts == 0 {
		h.MaxAttempts = 8
	}
	if h.BackoffBase == 0 {
		h.BackoffBase = defaultBackoffBase
	}
	if h.BackoffMax == 0 {
		h.BackoffMax = defaultBackoffMax
	}
	h.Audit = true
	if !ownedName(owner, h.Kind) || !namespacePattern.MatchString(h.Queue) || h.Run == nil || h.Priority < 1 || h.Priority > 9 || h.Timeout < time.Second || h.Timeout > time.Hour || h.Timeout%time.Millisecond != 0 || h.MaxAttempts < 1 || h.MaxAttempts > 50 || h.BackoffBase < time.Millisecond || h.BackoffMax < h.BackoffBase || h.BackoffBase%time.Millisecond != 0 || h.BackoffMax%time.Millisecond != 0 {
		return Handler{}, fmt.Errorf("%w: handler %q", ErrInvalidDeclaration, h.Kind)
	}
	return h, nil
}

func (m *Module) AddHandlers(owner string, handlers ...Handler) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if !namespacePattern.MatchString(owner) {
		return ErrInvalidDeclaration
	}
	pending := make(map[string]Handler, len(handlers))
	for _, h := range handlers {
		normalized, err := normalizeHandler(owner, h)
		if err != nil {
			return err
		}
		if _, exists := m.handlers[h.Kind]; exists {
			return fmt.Errorf("%w: duplicate handler %q", ErrInvalidDeclaration, h.Kind)
		}
		if _, exists := pending[h.Kind]; exists {
			return fmt.Errorf("%w: duplicate handler %q", ErrInvalidDeclaration, h.Kind)
		}
		pending[h.Kind] = normalized
	}
	if m.handlers == nil {
		m.handlers = make(map[string]Handler)
	}
	for name, h := range pending {
		m.handlers[name] = h
	}
	return nil
}

func encodePayload(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage(`{}`), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(data) > 64*1024 || len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, ErrInvalidPayload
	}
	return data, nil
}

func normalizeSchedule(owner string, s Schedule) (Schedule, error) {
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	if s.TenantScope == "" {
		s.TenantScope = ScopePlatform
	}
	if s.Misfire == "" {
		s.Misfire = MisfireCoalesce
	}
	if !ownedName(owner, s.Name) || !jobNamePattern.MatchString(s.Kind) || len(s.Kind) > 128 || (s.Interval == 0) == (s.Cron == "") || len(s.Cron) > 128 || len(s.Timezone) > 64 || s.Timezone == "Local" || (s.TenantScope != ScopePlatform && s.TenantScope != ScopePerTenant) || (s.Misfire != MisfireCoalesce && s.Misfire != MisfireSkip) {
		return Schedule{}, fmt.Errorf("%w: schedule %q", ErrInvalidDeclaration, s.Name)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return Schedule{}, fmt.Errorf("%w: timezone: %w", ErrInvalidDeclaration, err)
	}
	if s.Interval != 0 {
		if s.Interval < time.Second || s.Interval%time.Millisecond != 0 {
			return Schedule{}, ErrInvalidDeclaration
		}
	} else {
		// The timezone has its own field. Do not accept CRON_TZ or descriptors as
		// a second source of scheduling semantics inside a supposedly five-field cron.
		if len(strings.Fields(s.Cron)) != 5 {
			return Schedule{}, ErrInvalidDeclaration
		}
		if _, err := cronParser.Parse(s.Cron); err != nil {
			return Schedule{}, fmt.Errorf("%w: cron: %w", ErrInvalidDeclaration, err)
		}
	}
	payload, err := encodePayload(s.Payload)
	if err != nil {
		return Schedule{}, err
	}
	s.Payload = payload
	return s, nil
}

func (m *Module) AddSchedules(owner string, schedules ...Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return ErrCatalogSealed
	}
	if !namespacePattern.MatchString(owner) {
		return ErrInvalidDeclaration
	}
	pending := make(map[string]Schedule, len(schedules))
	for _, s := range schedules {
		normalized, err := normalizeSchedule(owner, s)
		if err != nil {
			return err
		}
		if _, exists := m.schedules[s.Name]; exists {
			return fmt.Errorf("%w: duplicate schedule %q", ErrInvalidDeclaration, s.Name)
		}
		if _, exists := pending[s.Name]; exists {
			return fmt.Errorf("%w: duplicate schedule %q", ErrInvalidDeclaration, s.Name)
		}
		pending[s.Name] = normalized
	}
	if m.schedules == nil {
		m.schedules = make(map[string]Schedule)
	}
	for name, s := range pending {
		m.schedules[name] = s
	}
	return nil
}
