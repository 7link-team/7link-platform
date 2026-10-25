package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAttentionSessionTableName(t *testing.T) {
	require.Equal(t, "delivery_attention_sessions", (AttentionSession{}).TableName())
}
