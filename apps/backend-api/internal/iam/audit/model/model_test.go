package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface{ TableName() string }

func TestAuditModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{Event{}, Head{}, RetentionPolicy{}}
	expected := []string{"iam_audit_events", "iam_audit_heads", "iam_audit_retention_policies"}
	require.Len(t, models, 3)
	for index, item := range models {
		assert.Equal(t, expected[index], item.TableName())
	}
}
