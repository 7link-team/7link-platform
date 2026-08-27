package service

import (
	"context"
	"errors"
	"fmt"
)

type Status string

const (
	StatusUnknown    Status = "UNKNOWN"
	StatusServing    Status = "SERVING"
	StatusNotServing Status = "NOT_SERVING"
)

type CheckData struct {
	Status  Status         `json:"status" enum:"UNKNOWN,SERVING,NOT_SERVING" doc:"Current service health status" example:"SERVING"`
	Modules []ModuleStatus `json:"modules" doc:"Health status of each registered application module"`
}

type ModuleStatus struct {
	Name   string `json:"name" doc:"Application module name" example:"dlock"`
	Status Status `json:"status" enum:"UNKNOWN,SERVING,NOT_SERVING" doc:"Current module health status" example:"SERVING"`
}

type CheckFunc func(context.Context) error

type Checker struct {
	Name  string
	Check CheckFunc
}

type CheckerProvider func() []Checker

type Service struct {
	provider CheckerProvider
}

// New creates the health service. The provider is evaluated for every request
// so modules registered during bootstrap are reflected in later checks.
func New(providers ...CheckerProvider) *Service {
	var provider CheckerProvider
	if len(providers) > 0 {
		provider = providers[0]
	}
	return &Service{provider: provider}
}

// Check can be called directly in-process; protocol adapters expose the same data.
func (s *Service) Check(ctx context.Context) CheckData {
	data := CheckData{Status: StatusServing, Modules: make([]ModuleStatus, 0)}
	for _, checker := range s.checkers() {
		status := StatusUnknown
		if checker.Check != nil {
			status = StatusServing
		}
		if checker.Check != nil && checker.Check(ctx) != nil {
			status = StatusNotServing
			data.Status = StatusNotServing
		}
		data.Modules = append(data.Modules, ModuleStatus{Name: checker.Name, Status: status})
	}
	return data
}

// Liveness reports only that the process can serve requests. It deliberately
// excludes dependency checks so a database outage does not trigger restarts.
func (*Service) Liveness() CheckData {
	return CheckData{Status: StatusServing, Modules: make([]ModuleStatus, 0)}
}

func (s *Service) CheckHealth(ctx context.Context) error {
	var errs []error
	for _, checker := range s.checkers() {
		if checker.Check == nil {
			continue
		}
		if err := checker.Check(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", checker.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) checkers() []Checker {
	if s.provider == nil {
		return nil
	}
	return s.provider()
}
