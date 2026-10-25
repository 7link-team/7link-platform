package authn

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

const ReauthenticationExecutionFullAuto = "execution_full_auto"

// RegisterExecutionReauthentication extends canonical issuance; it does not bypass
// browser binding, verification, purpose/target checks, expiry or consumption.
func RegisterExecutionReauthentication(api huma.API) {
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
		if value == ReauthenticationExecutionFullAuto {
			return
		}
	}
	p.Enum = append(p.Enum, ReauthenticationExecutionFullAuto)
	p.PrecomputeMessages()
}
