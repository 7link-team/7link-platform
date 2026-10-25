package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIntakeChannelLogicalTableName(t *testing.T) {
	require.Equal(t, "intake_channel_settings", (IntakeChannel{}).TableName())
}
