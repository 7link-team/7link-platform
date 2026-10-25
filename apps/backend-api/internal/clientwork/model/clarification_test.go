package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClarificationRoundLogicalTable(t *testing.T) {
	require.Equal(t, "clientwork_clarification_rounds", (ClarificationRound{}).TableName())
}
