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
	service := New(func(context.Context) error { return expected })

	result := service.Check(t.Context())
	assert.Equal(t, StatusNotServing, result.Status)
	assert.ErrorIs(t, service.CheckHealth(t.Context()), expected)
}
