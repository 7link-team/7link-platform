package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// ReportExperiences accepts only EXPERIENCE content, in the caller's transaction.
// Scope authorization remains owned by the canonical knowledge command.
type ReportExperiences struct {
	ProposeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, Scope, string) (EntryView, error)
}

func (m *Module) provideReportExperiences(s *module.Services) error {
	return module.Provide[ReportExperiences](s, ReportExperiences{ProposeOn: m.proposeReportExperienceOn})
}

func (m *Module) proposeReportExperienceOn(ctx context.Context, tx *gorm.DB, tenant, report guid.ID, scope Scope, body string) (EntryView, error) {
	if report <= 0 || (scope.Scope != "AGENT" && scope.Scope != "PROJECT") {
		return EntryView{}, ErrInvalid
	}
	return m.writeOn(ctx, tx, tenant, EntryInput{Scope: scope, Kind: "EXPERIENCE", Title: "Report " + report.String(), Body: body, Provenance: "Confirmed report " + report.String()})
}
