package authn

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

const (
	ReauthenticationDelegationGrant  = "delegation_grant"
	ReauthenticationSpawnGrantManage = "spawn_grant_manage"
)

func reauthenticationPurpose(purpose string) bool {
	return purpose == ReauthenticationDelegationGrant || purpose == ReauthenticationSpawnGrantManage
}

// RegisterGovernanceReauthentication adds the two governance purposes to the existing
// IAM challenge schema. Issuance and consumption keep all canonical proof checks.
func RegisterGovernanceReauthentication(api huma.API) {
	f, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	s := api.OpenAPI().Components.Schemas.Schema(f.Type, true, "ReauthenticationBeginInputBody")
	if s.Ref != "" {
		s = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if s == nil || s.Properties["purpose"] == nil {
		return
	}
	p := s.Properties["purpose"]
	for _, purpose := range []string{ReauthenticationDelegationGrant, ReauthenticationSpawnGrantManage} {
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
