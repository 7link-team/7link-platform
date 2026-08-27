package service

import (
	"context"
)

type Status string

const (
	StatusUnknown    Status = "UNKNOWN"
	StatusServing    Status = "SERVING"
	StatusNotServing Status = "NOT_SERVING"
)

type CheckData struct {
	Status Status `json:"status" enum:"UNKNOWN,SERVING,NOT_SERVING" doc:"Current service health status" example:"SERVING"`
}

// Service contains the health application use cases and has no HTTP or gRPC dependency.
type Checker func(context.Context) error

type Service struct {
	checker Checker
}

func New(checkers ...Checker) *Service {
	var checker Checker
	if len(checkers) > 0 {
		checker = checkers[0]
	}
	return &Service{checker: checker}
}

// Check can be called directly in-process; protocol adapters expose the same data.
func (s *Service) Check(ctx context.Context) CheckData {
	if s.CheckHealth(ctx) != nil {
		return CheckData{Status: StatusNotServing}
	}
	return CheckData{Status: StatusServing}
}

func (s *Service) CheckHealth(ctx context.Context) error {
	if s.checker != nil {
		return s.checker(ctx)
	}
	return nil
}
