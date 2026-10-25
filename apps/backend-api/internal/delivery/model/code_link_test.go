package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodeLinkTableNames(t *testing.T) {
	require.Equal(t, "delivery_code_links", CodeLink{}.TableName())
	require.Equal(t, "delivery_code_link_scans", CodeLinkScan{}.TableName())
}
