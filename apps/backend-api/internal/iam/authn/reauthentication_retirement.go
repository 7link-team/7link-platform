package authn

import (
	"github.com/danielgtaylor/huma/v2"
	"reflect"
	"slices"
)

const ReauthenticationKnowledgeEmbeddingWrite = "knowledge_embedding_write"
const ReauthenticationAgentForcedRetire = "agent_forced_retire"

func reauthenticationRetirementReauthenticationPurpose(purpose string) bool {
	return purpose == ReauthenticationKnowledgeEmbeddingWrite || purpose == ReauthenticationAgentForcedRetire
}

func registerReauthentication(api huma.API) {
	f, _ := reflect.TypeFor[reauthenticationBeginInput]().FieldByName("Body")
	s := api.OpenAPI().Components.Schemas.Schema(f.Type, true, "ReauthenticationBeginInputBody")
	if s.Ref != "" {
		s = api.OpenAPI().Components.Schemas.Map()["ReauthenticationBeginInputBody"]
	}
	if s == nil || s.Properties["purpose"] == nil {
		return
	}
	p := s.Properties["purpose"]
	for _, purpose := range []string{ReauthenticationKnowledgeEmbeddingWrite, ReauthenticationAgentForcedRetire} {
		if !slices.Contains(p.Enum, any(purpose)) {
			p.Enum = append(p.Enum, purpose)
		}
	}
	p.PrecomputeMessages()
}
