package collab

import (
	"context"

	"7link.com/platform/internal/iam/core"
)

// mutationContext preserves relationship-based authorization while making
// the domain write visible to the project's transaction state fence.
func mutationContext(ctx context.Context) context.Context {
	return core.WithEntityMutation(ctx)
}
