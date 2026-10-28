package audit

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/guid"
)

// PrincipalLabel is a current, authorized identity projection, not a fact
// covered by the historical event hash. IDs remain on the original event.
type PrincipalLabel struct {
	LoginName   *string `json:"login_name,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Anonymized  bool    `json:"anonymized,omitempty"`
}

// PrincipalLabelLookup resolves one bounded, deduplicated read batch. The
// identity owner applies its membership, account and profile-field permissions.
type PrincipalLabelLookup func(context.Context, guid.ID, []guid.ID) (map[guid.ID]PrincipalLabel, error)

// PrincipalLabelConnector lets identity supply its read capability without
// reversing the dependency from identity to audit.
type PrincipalLabelConnector struct {
	Bind func(PrincipalLabelLookup) error
}

func viewedPrincipalID(event Event) guid.ID {
	var detail struct {
		ViewedPrincipalID string `json:"viewed_principal_id"`
	}
	if json.Unmarshal(event.Detail, &detail) != nil {
		return 0
	}
	id, _ := guid.Parse(detail.ViewedPrincipalID)
	return id
}

func principalTarget(event Event) bool {
	return event.TargetType == "principal" || event.TargetType == "service_account"
}

func (s *Service) labelEvents(ctx context.Context, tenant guid.ID, events []Event) error {
	if s.principalLabels == nil || len(events) == 0 {
		return nil
	}
	seen := make(map[guid.ID]bool)
	ids := make([]guid.ID, 0, len(events)*2)
	add := func(id guid.ID) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, event := range events {
		add(event.PrincipalID)
		if principalTarget(event) {
			add(event.TargetID)
		}
		add(viewedPrincipalID(event))
	}
	if len(ids) == 0 {
		return nil
	}
	labels, err := s.principalLabels(ctx, tenant, ids)
	if err != nil {
		return err
	}
	label := func(id guid.ID) *PrincipalLabel {
		value, ok := labels[id]
		if !ok {
			return nil
		}
		return &value
	}
	for i := range events {
		events[i].Actor = label(events[i].PrincipalID)
		if principalTarget(events[i]) {
			events[i].TargetPrincipal = label(events[i].TargetID)
		}
		events[i].ViewedPrincipal = label(viewedPrincipalID(events[i]))
	}
	return nil
}
