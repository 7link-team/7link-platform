package audit

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
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
