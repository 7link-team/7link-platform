package app

import (
	"context"

	iamaudit "7link.com/platform/internal/iam/audit"
)

// ListErasureObligations reads the database-external recovery ledger without
// initializing application modules or consulting restored database state.
func (*App) ListErasureObligations(
	ctx context.Context,
	config AppConfig,
) ([]iamaudit.VerifiedErasureObligation, error) {
	return iamaudit.ListErasureObligations(ctx, iamAuditConfig(config))
}
