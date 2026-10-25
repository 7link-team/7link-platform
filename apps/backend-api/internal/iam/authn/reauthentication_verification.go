package authn

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

const (
	ReauthenticationVerificationOverride   = "verification_override"
	ReauthenticationIntegrationCredentials = "integration_credentials"
	ReauthenticationAutomaticOverride      = "automatic_verdict_override"
	ReauthenticationGovernanceSoDRelax     = "governance_sod_relax"
	ReauthenticationEscalationRuleRelax    = "escalation_rule_relax"
)

// RegisterVerificationReauthentication extends the existing challenge contract before
// serving requests. Issuance and consumption retain the IAM session, expiry,
// verification and single-use checks; no additional authentication path exists.
func RegisterVerificationReauthentication(api huma.API) {
	field, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	body := field.Type
	schema := api.OpenAPI().Components.Schemas.Schema(body, true, "ReauthenticationBeginInputBody")
	if schema.Ref != "" {
		schema = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if schema == nil || schema.Properties["purpose"] == nil {
		return
	}
	purpose := schema.Properties["purpose"]
	for _, value := range []string{ReauthenticationIntegrationCredentials, ReauthenticationAutomaticOverride, ReauthenticationVerificationOverride, ReauthenticationGovernanceSoDRelax, ReauthenticationEscalationRuleRelax} {
		found := false
		for _, existing := range purpose.Enum {
			found = found || existing == value
		}
		if !found {
			purpose.Enum = append(purpose.Enum, value)
		}
	}
	// Huma caches validation messages when schemas are registered.
	purpose.PrecomputeMessages()
}
