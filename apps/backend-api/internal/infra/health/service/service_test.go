package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceCheck(t *testing.T) {
	result := New().Check(t.Context())
	assert.Equal(t, StatusServing, result.Status)
}

func TestServiceCheckNotServing(t *testing.T) {
	expected := errors.New("database unavailable")
	service := New(func() []Checker {
		return []Checker{
			{Name: "http", Check: nil},
			{Name: "database", Check: func(context.Context) error { return expected }},
		}
	})

	result := service.Check(t.Context())
	assert.Equal(t, StatusNotServing, result.Status)
	assert.Equal(t, []ModuleStatus{
		{Name: "http", Status: StatusUnknown},
		{Name: "database", Status: StatusNotServing},
	}, result.Modules)
	assert.ErrorIs(t, service.CheckHealth(t.Context()), expected)
}

func TestOptionalCheckerIsReportedWithoutFailingReadiness(t *testing.T) {
	expected := errors.New("replica unavailable")
	service := New(func() []Checker {
		return []Checker{
			{Name: "database", Check: func(context.Context) error { return nil }},
			{Name: "database.replica", Check: func(context.Context) error { return expected }, Optional: true},
		}
	})

	result := service.Check(t.Context())
	assert.Equal(t, StatusServing, result.Status)
	assert.Equal(t, []ModuleStatus{
		{Name: "database", Status: StatusServing},
		{Name: "database.replica", Status: StatusNotServing},
	}, result.Modules)
	assert.NoError(t, service.CheckHealth(t.Context()))
}
