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
type Service struct{}

func New() *Service {
	return &Service{}
}

// Check can be called directly in-process; protocol adapters expose the same data.
func (s *Service) Check(context.Context) CheckData {
	return CheckData{Status: StatusServing}
}

func (s *Service) CheckHealth(context.Context) error {
	return nil
}
