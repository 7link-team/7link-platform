package authn

import (
	"github.com/danielgtaylor/huma/v2"
	"reflect"
)

const ReauthenticationAcceptanceRecord = "acceptance_record"

func reauthenticationAcceptanceReauthenticationPurpose(purpose string) bool {
	return purpose == ReauthenticationAcceptanceRecord || purpose == "delivery_send"
}

// RegisterAcceptanceReauthentication declares the same session-bound purpose
// consumed through the canonical single-use proof service.
func RegisterAcceptanceReauthentication(api huma.API) {
	f, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	s := api.OpenAPI().Components.Schemas.Schema(f.Type, true, "ReauthenticationBeginInputBody")
	if s.Ref != "" {
		s = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if s == nil || s.Properties["purpose"] == nil {
		return
	}
	p := s.Properties["purpose"]
	for _, purpose := range []string{ReauthenticationAcceptanceRecord, "delivery_send"} {
		found := false
		for _, v := range p.Enum {
			if v == purpose {
				found = true
			}
		}
		if !found {
			p.Enum = append(p.Enum, purpose)
		}
	}
	p.PrecomputeMessages()
}
