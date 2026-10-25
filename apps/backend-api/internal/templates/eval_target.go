package templates

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (m *Module) pinEvalTargetOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, target *EvalTarget, v VerifierDefinition) error {
	if v.PluginKey == "http-v1" {
		row, pin, e := m.verifierProviderOn(ctx, tx, tenant, target.ProviderRef, target.MachineID, v)
		if e != nil {
			return e
		}
		target.ProviderRef = pin
		if target.Role != "" {
			cfg, e := parseVerifierHTTPConfig(json.RawMessage(row.Config))
			if e != nil {
				return e
			}
			if cfg.Model != target.Model {
				return ErrConflict
			}
		}
	}
	if target.Role == "" {
		if target.AgentID != 0 || target.Model != "" || target.ConfigurationSHA256 != "" {
			return ErrInvalid
		}
		return nil
	}
	if v.PluginKey != "http-v1" || v.Kind != "MODEL_SCORED" || target.Role != "ACCEPTOR" || target.AgentID <= 0 || strings.TrimSpace(target.Model) == "" || len(target.Model) > 200 {
		return ErrInvalid
	}
	a, e := m.evalAgentOn(ctx, tx, tenant, *target)
	if e != nil {
		return e
	}
	target.ConfigurationSHA256 = a.SHA256
	return nil
}
func (m *Module) evalAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, target EvalTarget) (fleet.VerifierAgentConfiguration, error) {
	var out fleet.VerifierAgentConfiguration
	if m.evalAgents.ReadOn == nil {
		return out, ErrUnavailable
	}
	out, e := m.evalAgents.ReadOn(ctx, tx, tenant, target.AgentID, target.Role, target.Model)
	if e != nil {
		return out, e
	}
	if target.ConfigurationSHA256 != "" && target.ConfigurationSHA256 != out.SHA256 {
		return out, ErrConflict
	}
	return out, nil
}

type evalFileInput struct {
	EvalFileRef
	Data []byte `json:"data"`
}

func evalActorContext(ctx context.Context, tenant, actor, project guid.ID) context.Context {
	return authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, EntityID: project, SubjectType: authn.SubjectTypePrincipal})
}

// Resolve actual bytes through Files, never a caller URL or a storage table.
// Limits bound one case and the HTTP request. Readers are closed immediately.
func (m *Module) evalSampleInput(ctx context.Context, tenant, actor guid.ID, c EvalCaseView, readBytes bool) (json.RawMessage, error) {
	ctx = evalActorContext(ctx, tenant, actor, c.ProjectID)
	input := struct {
		Files  []evalFileInput `json:"files"`
		Source json.RawMessage `json:"source"`
	}{Files: []evalFileInput{}, Source: c.SourceSnapshot}
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		if e := m.evalProjectOn(ctx, tx, c.ProjectID, false); e != nil {
			return e
		}
		if len(c.InputRef) > 0 && m.evalFiles == nil {
			return ErrUnavailable
		}
		total := 0
		for _, pin := range c.InputRef {
			opened, e := m.evalFiles.OpenOn(ctx, tx, pin.BindingID)
			if e != nil {
				return evalFileError(e)
			}
			if opened.Reader == nil {
				return ErrUnavailable
			}
			valid := opened.FileID == pin.FileID && opened.VersionID == pin.VersionID && opened.SHA256 == pin.SHA256 && opened.BindingID == pin.BindingID
			var data []byte
			if valid && readBytes {
				data, e = io.ReadAll(io.LimitReader(opened.Reader, (1<<20)+1))
			}
			closeErr := opened.Reader.Close()
			if !valid {
				return ErrConflict
			}
			if e != nil || closeErr != nil {
				return ErrUnavailable
			}
			if readBytes {
				total += len(data)
				if len(data) > 1<<20 || total > 2<<20 || evalDigest(data) != pin.SHA256 {
					return ErrConflict
				}
				input.Files = append(input.Files, evalFileInput{EvalFileRef: pin, Data: data})
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(input)
	return raw, e
}
