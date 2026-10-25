package authn

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

const ReauthenticationProjectCancel = "project_cancel"

// RegisterCancellationReauthentication extends the same IAM challenge schema; proofs
// retain browser-session, fresh verification, target, expiry and single use.
func RegisterCancellationReauthentication(api huma.API) {
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
		if value == ReauthenticationProjectCancel {
			return
		}
	}
	p.Enum = append(p.Enum, ReauthenticationProjectCancel)
	p.PrecomputeMessages()
}
