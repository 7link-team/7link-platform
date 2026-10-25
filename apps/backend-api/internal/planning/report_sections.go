package planning

import (
	"reflect"
	"unicode/utf8"
)

// Structured retrospective is additive: old immutable reports keep their text.
// New editors submit these explicit sections alongside the legacy summary.
type PlanningRetrospective struct {
	WentWell   []string `json:"went_well" maxItems:"50"`
	Problems   []string `json:"problems" maxItems:"50"`
	RootCauses []string `json:"root_causes" maxItems:"50"`
}

func validRetrospective(v *PlanningRetrospective, required bool) bool {
	if v == nil {
		return true
	}
	for _, items := range [][]string{v.WentWell, v.Problems, v.RootCauses} {
		if len(items) > 50 || (required && len(items) == 0) {
			return false
		}
		for _, item := range items {
			if !utf8.ValidString(item) || utf8.RuneCountInString(item) > 500 || (required && !validText(item, 500)) {
				return false
			}
		}
	}
	return true
}
func reportSectionsReady(body PlanningReportInput) bool {
	if body.RetrospectiveAIDrafted || !validRetrospective(body.RetrospectiveSections, true) {
		return false
	}
	for _, item := range body.UncompletedItems {
		if item.DetailAIDrafted {
			return false
		}
	}
	return true
}
func normalizeReportSections(previous PlanningReportInput, in *PlanningReportInput) error {
	if !validRetrospective(in.RetrospectiveSections, false) {
		return ErrInvalid
	}
	// An omitted client flag cannot silently accept unchanged generated prose.
	in.RetrospectiveAIDrafted = previous.RetrospectiveAIDrafted && !in.RetrospectiveConfirmed && in.Retrospective == previous.Retrospective && reflect.DeepEqual(in.RetrospectiveSections, previous.RetrospectiveSections)
	in.RetrospectiveConfirmed = false
	for i := range in.UncompletedItems {
		item := &in.UncompletedItems[i]
		for _, old := range previous.UncompletedItems {
			if old.Key == item.Key {
				item.DetailAIDrafted = old.DetailAIDrafted && !item.DetailConfirmed && item.Detail == old.Detail
				break
			}
		}
		item.DetailConfirmed = false
	}
	return nil
}
