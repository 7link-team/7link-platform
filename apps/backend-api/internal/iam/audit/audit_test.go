package audit

import (
	"bytes"
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

func TestNewDomainEventPreservesAccountViewOperator(t *testing.T) {
	ctx := authn.WithClaims(t.Context(), &authn.Claims{
		PrincipalID: guid.ID(9),
		AccountView: &authn.AccountViewIdentity{ID: guid.ID(50), PrincipalID: guid.ID(7), ReadOnly: true},
	})
	event := NewDomainEvent(ctx, guid.ID(3), "changed", "role", guid.ID(4))
	assert.Equal(t, guid.ID(7), event.PrincipalID)
	assert.Equal(t, guid.ID(9), authn.PrincipalIDFromContext(ctx))
	assert.Equal(t, guid.ID(4), event.TargetID)
	assert.Equal(t, "50", event.Detail["account_view_id"])
	assert.Equal(t, "9", event.Detail["viewed_principal_id"])
	assert.Equal(t, true, event.Detail["account_view_read_only"])
	other := NewDomainEvent(ctx, guid.ID(3), "changed", "role", guid.ID(5))
	event.Detail["viewed_principal_id"] = "altered"
	assert.Equal(t, "9", other.Detail["viewed_principal_id"])
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

// TestVerifyAndExportSurfaceRepositoryFailures pins that the integrity and
// export surface refuses to answer from an unreachable repository. A Verify
// that returned a zero Integrity, or an export that stopped after the manifest
// and reported success, would both look like a genuinely empty, valid chain.
func TestVerifyAndExportSurfaceRepositoryFailures(t *testing.T) {
	tenantID := guid.ID(901)
	frozen := ExportSnapshot{
		Filter:      Filter{TenantID: tenantID},
		Integrity:   Integrity{TenantID: tenantID, Valid: true, HeadSequence: 2},
		GeneratedAt: auditTestNow,
	}
	var unavailable *Service
	_, err := unavailable.Verify(t.Context(), tenantID)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, unavailable.WriteExport(t.Context(), frozen, &bytes.Buffer{}), errRepositoryUnavailable)

	h := newAuditHarness(t, "")
	for range 2 {
		_, appendErr := h.service.Append(t.Context(), EventInput{
			TenantID: tenantID, EventType: "created", Outcome: "success",
		})
		require.NoError(t, appendErr)
	}
	snapshot, err := h.service.PrepareExport(t.Context(), Filter{TenantID: tenantID})
	require.NoError(t, err)
	require.Equal(t, int64(2), snapshot.Integrity.HeadSequence)
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = h.service.Verify(t.Context(), tenantID)
	assert.ErrorContains(t, err, "read audit head")
	_, err = h.service.PrepareExport(t.Context(), Filter{TenantID: tenantID})
	assert.ErrorContains(t, err, "read audit head")

	var written bytes.Buffer
	assert.ErrorContains(t, h.service.WriteExport(t.Context(), snapshot, &written), "read audit export")
	assert.NotContains(t, written.String(), `"type":"complete"`,
		"a truncated export must not be signed off as complete")
}
