package authn

import (
	"github.com/danielgtaylor/huma/v2"
	"reflect"
)

const ReauthenticationIntakeConvert = "intake_convert_privileged"

func reauthenticationIntakeReauthenticationPurpose(purpose string) bool {
	return purpose == ReauthenticationIntakeConvert
}

// RegisterIntakeReauthentication adds the same domain purpose accepted by the
// existing session-bound, fresh, single-use challenge consumer.
func RegisterIntakeReauthentication(api huma.API) {
	f, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	s := api.OpenAPI().Components.Schemas.Schema(f.Type, true, "ReauthenticationBeginInputBody")
	if s.Ref != "" {
		s = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if s == nil || s.Properties["purpose"] == nil {
		return
	}
	p := s.Properties["purpose"]
	for _, value := range p.Enum {
		if value == ReauthenticationIntakeConvert {
			return
		}
	}
	p.Enum = append(p.Enum, ReauthenticationIntakeConvert)
	p.PrecomputeMessages()
}
