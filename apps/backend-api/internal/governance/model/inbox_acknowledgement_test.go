package model

import (
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
	"sync"
	"testing"
)

func TestInboxAcknowledgementPersistenceIsExactRecipientRevision(t *testing.T) {
	s, e := schema.Parse(&InboxAcknowledgement{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, e)
	require.Equal(t, "governance_inbox_acknowledgements", s.Table)
	for _, column := range []string{"id", "tenant_id", "principal_id", "kind", "source_id", "source_revision", "created_at"} {
		require.NotNil(t, s.FieldsByDBName[column], column)
	}
	require.Equal(t, 64, s.FieldsByDBName["source_revision"].Size)
	require.False(t, s.FieldsByDBName["id"].AutoIncrement)
	require.Equal(t, schema.TimeType(0), s.FieldsByDBName["created_at"].AutoCreateTime)
}
