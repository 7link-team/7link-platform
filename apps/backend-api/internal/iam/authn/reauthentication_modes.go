package authn

import (
	"github.com/danielgtaylor/huma/v2"
	"reflect"
)

const (
	ReauthenticationTeamModeChange  = "team_mode_change"
	ReauthenticationProjectTakeover = "project_takeover"
)

func reauthenticationModesReauthenticationPurpose(purpose string) bool {
	return purpose == ReauthenticationTeamModeChange || purpose == ReauthenticationProjectTakeover
}

// RegisterModesReauthentication exposes exactly the purposes accepted by the
// canonical session-bound, fresh and single-use proof consumer.
func RegisterModesReauthentication(api huma.API) {
	f, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	s := api.OpenAPI().Components.Schemas.Schema(f.Type, true, "ReauthenticationBeginInputBody")
	if s.Ref != "" {
		s = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if s == nil || s.Properties["purpose"] == nil {
		return
	}
	p := s.Properties["purpose"]
	for _, purpose := range []string{ReauthenticationTeamModeChange, ReauthenticationProjectTakeover} {
		found := false
		for _, value := range p.Enum {
			found = found || value == purpose
		}
		if !found {
			p.Enum = append(p.Enum, purpose)
		}
	}
	p.PrecomputeMessages()
}
