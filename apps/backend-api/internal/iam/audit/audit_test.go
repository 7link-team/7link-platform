package audit

import (
	"context"
	"io/fs"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var _ Appender = func(context.Context, *gorm.DB, DomainEvent) error { return nil }

func TestNewDomainEventUsesVerifiedPrincipal(t *testing.T) {
	event := NewDomainEvent(t.Context(), guid.ID(1), "changed", "principal", guid.ID(2))
	assert.Equal(t, guid.ID(1), event.TenantID)
	assert.Zero(t, event.PrincipalID)
	assert.Equal(t, "changed", event.EventType)
	assert.Equal(t, "principal", event.TargetType)
	assert.Equal(t, guid.ID(2), event.TargetID)
	assert.NotNil(t, event.Detail)

	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: guid.ID(9)})
	event = NewDomainEvent(ctx, guid.ID(3), "created", "tenant", guid.ID(4))
	assert.Equal(t, guid.ID(9), event.PrincipalID)
	assert.Equal(t, guid.ID(3), event.TenantID)
	assert.Equal(t, guid.ID(4), event.TargetID)
}

func TestModuleMigrationContract(t *testing.T) {
	module := &Module{}
	assert.Equal(t, Name, module.ModuleName())
	source := module.MigrationSource()
	assert.Equal(t, "goose_audit_domain", source.TableName)
	files, err := fs.ReadDir(source.FS, "sql/postgres")
	require.NoError(t, err)
	require.Len(t, files, 3)
	milestones := module.MigrationMilestones()
	require.Len(t, milestones, 2)
	assert.Equal(t, "base", milestones[0].Name)
	assert.Equal(t, int64(2), milestones[0].Version)
	assert.Equal(t, "archive_jobs", milestones[1].Name)
	assert.Equal(t, int64(3), milestones[1].Version)
}
