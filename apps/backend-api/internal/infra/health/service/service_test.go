package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceCheck(t *testing.T) {
	result := New().Check(t.Context())
	assert.Equal(t, StatusServing, result.Status)
}
