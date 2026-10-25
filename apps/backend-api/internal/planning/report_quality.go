package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
	"slices"
	"time"
)

func (m *Module) hydrateReportQualityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time, out *delivery.PlanningSnapshot) error {
	if m.reportQuality == nil {
		return nil
	}
	ids := make([]guid.ID, len(out.Work))
	for i, work := range out.Work {
		ids[i] = work.ID
	}
	quality, err := m.reportQuality(ctx, tx, tenant, project, ids, start, end)
	if err != nil {
		return err
	}
	out.QualityMetrics = quality
	if quality != nil {
		out.MissingSources = slices.DeleteFunc(out.MissingSources, func(s string) bool { return s == "QUALITY_METRICS" })
	} else if !slices.Contains(out.MissingSources, "QUALITY_METRICS") {
		out.MissingSources = append(out.MissingSources, "QUALITY_METRICS")
	}
	return nil
}

// Member quality uses every source-authorized work row in the project. A member
// may execute another person's accountable work; ownership never filters it.
func (m *Module) hydrateMemberQualityOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time, scope delivery.PlanningSnapshot, out *delivery.PlanningSnapshot) error {
	if m.memberQuality == nil {
		return nil
	}
	ids := make([]guid.ID, len(scope.Work))
	for i, work := range scope.Work {
		ids[i] = work.ID
	}
	quality, err := m.memberQuality(ctx, tx, tenant, project, subject, ids, start, end)
	if err != nil {
		return err
	}
	out.QualityMetrics = quality
	if quality != nil {
		out.MissingSources = slices.DeleteFunc(out.MissingSources, func(s string) bool { return s == "QUALITY_METRICS" })
	} else if !slices.Contains(out.MissingSources, "QUALITY_METRICS") {
		out.MissingSources = append(out.MissingSources, "QUALITY_METRICS")
	}
	return nil
}
