package yunxiao

import (
	"errors"
	"slices"
)

var ErrDefectFloor = errors.New("yunxiao: defect workflow floor cannot be weakened")

// Configuration may tighten the defect matrix, never delegate Human-only
// product decisions or replace machine/deployment evidence with a text field.
func defectFloor(kind string, from, to StateDefinition, t TransitionDefinition) error {
	if !t.Enabled || (kind != "BUG" && kind != "INCIDENT") {
		return nil
	}
	humanOnly := to.MapsTo == "ACCEPTED" || to.Key == "wont_fix" || to.Key == "invalid" || ((from.MapsTo == "ACCEPTED" || from.MapsTo == "NEEDS_HUMAN_REVIEW") && to.MapsTo == "REWORK")
	if humanOnly {
		human := false
		for _, c := range t.Conditions {
			if c.Kind == "ACTOR_SCOPE" && slices.Equal(c.ActorKinds, []string{"HUMAN"}) {
				human = true
			}
		}
		if !human {
			return ErrDefectFloor
		}
	}
	required := ""
	if to.Key == "merged" {
		required = "LINKED_MR_MERGED"
	}
	if to.Key == "deployed" {
		required = "DEPLOYMENT_CONTAINS_MERGE"
	}
	if required != "" {
		present := false
		for _, c := range t.Conditions {
			if c.Kind == "EVIDENCE" && c.Rule == required {
				present = true
			}
		}
		if !present {
			return ErrDefectFloor
		}
	}
	return nil
}
