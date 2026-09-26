package federation

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"7link.com/platform/internal/iam/federation/query"
	"gorm.io/gorm"
)

var ErrLegacyQuarantineRequired = errors.New("retained external identity links require quarantine and independent ownership evidence before authentication startup; enable federation for remediation")

// RequireLegacyQuarantine checks retained data when federation is disabled.
// It does not activate provider routes or mutate a disabled module's records.
func RequireLegacyQuarantine(ctx context.Context, db *gorm.DB, prefix string) error {
	if db == nil {
		return ErrUnavailable
	}
	db = db.WithContext(ctx)
	table := query.BindNames(prefix).IdentityLinks.String()
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("inspect retained federation tables: %w", err)
	}
	if !slices.Contains(tables, table) {
		return nil
	}
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect retained identity links: %w", err)
	}
	var provenance, quarantine, evidence bool
	for _, column := range columns {
		provenance = provenance || column.Name() == "binding_provenance"
		quarantine = quarantine || column.Name() == "quarantined_at"
		evidence = evidence || column.Name() == "recovery_evidence"
	}
	statement := db.Table(table)
	if provenance && quarantine && evidence {
		statement = statement.Where("(binding_provenance NOT IN ? OR binding_provenance IS NULL) AND (quarantined_at IS NULL OR TRIM(COALESCE(recovery_evidence, '')) = '')", []string{BindingNewPrincipal, BindingPrincipalVerified})
	}
	var pending int64
	if err := statement.Count(&pending).Error; err != nil {
		return fmt.Errorf("check retained legacy identity quarantine: %w", err)
	}
	if pending != 0 {
		return ErrLegacyQuarantineRequired
	}
	return nil
}
