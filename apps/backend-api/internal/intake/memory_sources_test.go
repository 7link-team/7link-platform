package intake

import (
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemorySourceTextUsesExactTenantProjectAndOriginal(t *testing.T) {
	m, db := referencesHarness(t)
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Records.String()).Create(map[string]any{"id": 30, "tenant_id": 7, "project_id": 9, "requirement_id": 21, "channel_id": 1, "contact_id": 1, "channel_kind": "API", "ref_no": 1, "kind": "NEW", "status": "CONVERTED", "title": "Original", "raw_content": "Immutable external request", "raw_content_format": "PLAIN", "raw_content_hash": strings.Repeat("a", 64), "reply_token": "private", "received_at": at, "created_at": at, "updated_at": at}).Error)
	s := module.NewServices()
	require.NoError(t, m.provideMemorySourceText(s))
	require.NoError(t, s.Seal())
	reader, err := module.Resolve[MemorySourceText](s)
	require.NoError(t, err)
	_, err = reader.ReadOn(t.Context(), db, 7, 9, 30)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		text, e := reader.ReadOn(t.Context(), tx, 7, 9, 30)
		require.NoError(t, e)
		require.Equal(t, "Immutable external request", text)
		for _, scope := range [][2]int64{{8, 9}, {7, 10}} {
			_, e = reader.ReadOn(t.Context(), tx, guid.ID(scope[0]), guid.ID(scope[1]), 30)
			require.Error(t, e)
		}
		return nil
	}))
}
