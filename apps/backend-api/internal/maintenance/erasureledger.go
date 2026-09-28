package maintenance

import (
	"context"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
)

// ListErasureObligations reads the database-external recovery ledger without
// initializing application modules or consulting restored database state.
func ListErasureObligations(
	ctx context.Context,
	config app.AppConfig,
) ([]iamaudit.VerifiedErasureObligation, error) {
	audit := config.IAMAudit
	return iamaudit.ListErasureObligations(ctx, iamaudit.Config{
		Anchor: iamaudit.AnchorConfig{
			Enabled: audit.Anchor.Enabled, Endpoint: audit.Anchor.Endpoint,
			Bucket: audit.Anchor.Bucket, Region: audit.Anchor.Region,
			AccessKey: audit.Anchor.AccessKey, SecretKey: audit.Anchor.SecretKey,
			RetentionDays: audit.Anchor.RetentionDays, SigningKey: audit.Anchor.SigningKey,
		},
		ErasureLedger: iamaudit.ErasureLedgerConfig{
			Enabled: audit.ErasureLedger.Enabled,
			Timeout: audit.ErasureLedger.Timeout,
		},
	})
}
