package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"reflect"
	"sync"
)

// GrantedSkill is an immutable content identity from the Human-managed catalog,
// never a skill observed in an Agent's home directory. This catalog does not
// store semantic tags or mount acknowledgements; neither fact is inferred.
type GrantedSkill struct {
	ID           guid.ID `json:"id"`
	Key          string  `json:"key"`
	Version      int     `json:"version"`
	SHA256       string  `json:"sha256"`
	GrantID      guid.ID `json:"grant_id"`
	GrantVersion int     `json:"grant_version"`
}
type SkillCatalogSnapshot struct {
	Skills          []GrantedSkill `json:"skills"`
	TagsAvailable   bool           `json:"tags_available"`
	MountsAvailable bool           `json:"mounts_available"`
}
type SkillCatalog struct {
	ForAgentOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (SkillCatalogSnapshot, error)
}

func (m *Module) provideSkillCatalog(s *module.Services) error {
	if err := m.provideMountedOperations(s); err != nil {
		return err
	}
	return module.Provide[SkillCatalog](s, SkillCatalog{ForAgentOn: m.skillCatalogOn})
}
func (m *Module) skillCatalogOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (SkillCatalogSnapshot, error) {
	out := SkillCatalogSnapshot{Skills: []GrantedSkill{}}
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 || tenantID(ctx) != tenant {
		return out, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalid
	}
	permission := "agent_read"
	if agent == actorID(ctx) {
		permission = "SELF"
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, permission, false); err != nil {
		return out, err
	}
	q := query.BindToolEntry(tx, m.names)
	g := query.BindToolGrant(tx, m.names)
	grants, err := g.WithContext(ctx).Where(g.TenantID.Eq(tenant), g.AgentPrincipalID.Eq(agent), g.State.Eq("ACTIVE")).Order(g.ID).Limit(501).Find()
	if err != nil {
		return out, err
	}
	if len(grants) > 500 {
		return out, ErrUnavailable
	}
	for _, grant := range grants {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(grant.ToolID), q.Kind.Eq("SKILL"), q.State.Eq("ACTIVE")).Find()
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			hash := sha256.Sum256([]byte(row.SkillBody))
			if row.ContentHash != hex.EncodeToString(hash[:]) || row.Version < 1 {
				return out, ErrUnavailable
			}
			out.Skills = append(out.Skills, GrantedSkill{ID: row.ID, Key: row.EntryKey, Version: row.Version, SHA256: row.ContentHash, GrantID: grant.ID, GrantVersion: grant.Version})
		}
	}
	return out, nil
}

// MountedOperation identifies a capability name and its actual Huma operation.
// Names are stable tool names; HTTPID is the owning module's route identity.
type MountedOperation struct{ Name, HTTPID, Permission string }

// MountedOperationCatalog completes dependent contracts after all HTTP routes
// mount. It never grants permission and cannot publish an absent endpoint.
type MountedOperationCatalog struct {
	Register func(...MountedOperation) error
	Finalize func(huma.API) error
}

func (m *Module) provideMountedOperations(s *module.Services) error {
	var mu sync.Mutex
	descriptors := map[string]MountedOperation{}
	finalized := false
	return module.Provide[MountedOperationCatalog](s, MountedOperationCatalog{
		Register: func(items ...MountedOperation) error {
			mu.Lock()
			defer mu.Unlock()
			if finalized {
				return ErrConflict
			}
			seen := map[string]bool{}
			for _, d := range items {
				if seen[d.Name] {
					return ErrConflict
				}
				seen[d.Name] = true
				if !operationName.MatchString(d.Name) || d.HTTPID == "" || d.Permission == "" {
					return ErrInvalid
				}
				if _, exists := descriptors[d.Name]; exists {
					return ErrConflict
				}
			}
			for _, d := range items {
				descriptors[d.Name] = d
			}
			return nil
		},
		Finalize: func(api huma.API) error {
			mu.Lock()
			defer mu.Unlock()
			if api == nil || m.registrar == nil {
				return ErrUnavailable
			}
			pending := NewOperationRegistry()
			for _, d := range descriptors {
				if _, ok := m.registrar.Registry().Find(d.Permission); !ok {
					return ErrInvalid
				}
				var route *huma.Operation
				path, method := "", ""
				for p, item := range api.OpenAPI().Paths {
					for verb, candidate := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PATCH": item.Patch, "PUT": item.Put, "DELETE": item.Delete} {
						if candidate != nil && candidate.OperationID == d.HTTPID {
							if route != nil {
								return ErrConflict
							}
							route, path, method = candidate, p, verb
						}
					}
				}
				if route == nil {
					return ErrUnavailable
				}
				input := json.RawMessage(`{"type":"object","additionalProperties":false}`)
				if route.RequestBody != nil {
					if body := route.RequestBody.Content["application/json"]; body != nil {
						var err error
						input, err = inlineSchema(api.OpenAPI(), body.Schema)
						if err != nil {
							return err
						}
					}
				}
				var output json.RawMessage
				for _, status := range []string{"200", "201", "202"} {
					if response := route.Responses[status]; response != nil {
						if media := response.Content["application/json"]; media != nil {
							var err error
							output, err = inlineSchema(api.OpenAPI(), media.Schema)
							if err != nil {
								return err
							}
							break
						}
					}
				}
				if output == nil {
					return ErrInvalid
				}
				params := []OperationParameter{}
				for _, p := range route.Parameters {
					schema, err := inlineSchema(api.OpenAPI(), p.Schema)
					if err != nil {
						return err
					}
					params = append(params, OperationParameter{Name: p.Name, In: p.In, Required: p.Required, Schema: schema})
				}
				writes := method != "GET"
				effects := []string{"READ_ONLY"}
				rule := ""
				if writes {
					effects = []string{"WRITES_STATE"}
					rule = "One Idempotency-Key per logical action; reuse the same key and body on retry"
				}
				if err := pending.Register(Operation{Name: d.Name, Summary: route.Summary, Method: method, Path: path, Parameters: params, InputSchema: input, OutputSchema: output, SideEffects: effects, IdempotencyRequired: writes, KeyRule: rule, RequiredPermission: d.Permission, Via: "ROLE"}); err != nil {
					return err
				}
			}
			// A separate provider-only composition gate extends the sealed base once,
			// atomically. General registry registration remains sealed throughout.
			additions := pending.List()
			m.registry.mu.Lock()
			defer m.registry.mu.Unlock()
			for _, op := range additions {
				if old, ok := m.registry.operations[op.Name]; ok && !reflect.DeepEqual(old, op) {
					return ErrConflict
				}
			}
			for _, op := range additions {
				m.registry.operations[op.Name] = op
			}
			finalized = true
			return nil
		},
	})
}
