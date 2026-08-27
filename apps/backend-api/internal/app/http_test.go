package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDocsURLUsesReachableHostForWildcardListeners(t *testing.T) {
	assert.Equal(t, "http://localhost:8080/docs/", docsURL("0.0.0.0", 8080))
	assert.Equal(t, "http://localhost:8080/docs/", docsURL("::", 8080))
	assert.Equal(t, "http://127.0.0.1:8080/docs/", docsURL("127.0.0.1", 8080))
	assert.Equal(t, "http://[::1]:8080/docs/", docsURL("::1", 8080))
}
