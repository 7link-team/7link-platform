package respx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrefersHTML(t *testing.T) {
	tests := map[string]bool{
		"":                 false,
		"*/*":              false,
		"application/json": false,
		"text/html":        true,
		"text/html,application/xhtml+xml,*/*;q=0.8":      true,
		"application/json,text/html;q=0.5":               false,
		"application/json;q=0.5,text/html;q=0.8":         true,
		"application/problem+json;q=0.9,text/html;q=0.8": false,
		"text/html;q=0": false,
		"text/html;q=invalid,application/json;q=0.5":         false,
		"text/html;q=1.5,application/json;q=0.5":             false,
		"invalid, text/html; q=0.8, application/json; q=0.2": true,
	}
	for accept, expected := range tests {
		assert.Equal(t, expected, PrefersHTML(accept), accept)
	}
}
