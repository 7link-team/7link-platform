package governance

import (
	"context"

	"gorm.io/gorm"
)

// codeMergeAuthority reuses direct Human role authority. Delegated Agent
// merges are evaluated by Check's existing delegation branch before this hook.
func (m *Module) codeMergeAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	return m.checkDeliveryAuthority(ctx, tx, req)
}
