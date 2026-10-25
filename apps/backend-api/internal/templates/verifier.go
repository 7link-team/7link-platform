package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// VerifierDefinition describes supported platform runners. Version identifies
// the platform adapter protocol; it never asserts an external binary version.
type VerifierDefinition struct {
	PluginKey     string         `json:"plugin_key,omitempty" maxLength:"64"`
	RequiredTools []string       `json:"required_tools,omitempty" maxItems:"20"`
	Key           string         `json:"verifier_key" maxLength:"64"`
	Kind          string         `json:"kind" enum:"DETERMINISTIC,MODEL_SCORED"`
	Runner        string         `json:"runner" enum:"COMMAND,TEST,VISUAL,AGENT_JUDGE"`
	Version       int            `json:"version" minimum:"1" maximum:"1"`
	Argv          []string       `json:"argv,omitempty" maxItems:"100"`
	TimeoutSec    int            `json:"timeout_sec" minimum:"1" maximum:"3600"`
	ReportPath    string         `json:"report_path,omitempty" maxLength:"512"`
	Params        map[string]any `json:"params,omitempty"`
	Limitations   string         `json:"limitations" minLength:"1" maxLength:"2000"`
}
type VerifierBinding struct {
	ProgressPolicy    *ProgressPolicy      `json:"progress_policy,omitempty"`
	TemplateVersionID guid.ID              `json:"template_version_id"`
	DefinitionHash    string               `json:"definition_hash"`
	Parameters        map[string]any       `json:"params_resolved"`
	Verifiers         []VerifierDefinition `json:"verifiers"`
	Rubric            *RubricDefinition    `json:"rubric,omitempty"`
}

// VerifierCatalog is trusted, transaction-bound access to an actual materialized
// work node. Readers must authorize the work before requesting its frozen pin.
type VerifierCatalog struct {
	GetOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (VerifierDefinition, string, error)
	ForWorkOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VerifierBinding, bool, error)
	ToolCalibration func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (string, error)
}

func (m *Module) provideVerifiers(s *module.Services) error {
	return module.Provide[VerifierCatalog](s, VerifierCatalog{GetOn: m.verifierGetOn, ForWorkOn: m.verifiersForWorkOn, ToolCalibration: m.toolCalibrationOn})
}
func verifierDefinitions(d TemplateDefinition) ([]VerifierDefinition, error) {
	value, exists := d.Metadata["verifiers"]
	if !exists {
		return []VerifierDefinition{}, nil
	}
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > 64000 {
		return nil, ErrInvalid
	}
	var defs []VerifierDefinition
	if json.Unmarshal(raw, &defs) != nil || len(defs) > 50 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, v := range defs {
		if !validVerifierPlugin(v) {
			return nil, ErrInvalid
		}
		if strings.TrimSpace(v.Key) == "" || len(v.Key) > 64 || seen[v.Key] || v.Version != 1 || v.TimeoutSec < 1 || v.TimeoutSec > 3600 || strings.TrimSpace(v.Limitations) == "" || len(v.Limitations) > 2000 || !slices.Contains([]string{"COMMAND", "TEST", "VISUAL", "AGENT_JUDGE"}, v.Runner) {
			return nil, ErrInvalid
		}
		seen[v.Key] = true
		if v.Kind != "DETERMINISTIC" && v.Kind != "MODEL_SCORED" || (v.Runner == "AGENT_JUDGE") != (v.Kind == "MODEL_SCORED") || v.Runner != "AGENT_JUDGE" && (len(v.Argv) == 0 || len(v.Argv) > 100) {
			return nil, ErrInvalid
		}
		for _, arg := range v.Argv {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return nil, ErrInvalid
			}
		}
		if len(v.ReportPath) > 512 {
			return nil, ErrInvalid
		}
	}
	return defs, nil
}
func resolveVerifiers(defs []VerifierDefinition, parameters map[string]any) ([]VerifierDefinition, error) {
	out := make([]VerifierDefinition, len(defs))
	for i, v := range defs {
		v.Argv = slices.Clone(v.Argv)
		v.Params = copyParameters(v.Params)
		for key, value := range parameters {
			v.Params[key] = value
		}
		for j, arg := range v.Argv {
			if strings.HasPrefix(arg, "{{param:") && strings.HasSuffix(arg, "}}") {
				key := strings.TrimSuffix(strings.TrimPrefix(arg, "{{param:"), "}}")
				value, ok := v.Params[key]
				if !ok {
					return nil, ErrInvalid
				}
				switch value.(type) {
				case string, bool, float64, int:
					v.Argv[j] = fmt.Sprint(value)
				default:
					return nil, ErrInvalid
				}
			}
		}
		out[i] = v
	}
	return out, nil
}
func copyParameters(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
func (m *Module) verifiersForWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (VerifierBinding, bool, error) {
	var empty VerifierBinding
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return empty, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, false, ErrInvalid
	}
	q := query.BindBindings(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, false, nil
	}
	if e != nil {
		return empty, false, e
	}
	var runs []TemplateNodeRun
	if json.Unmarshal([]byte(row.Runs), &runs) != nil {
		return empty, false, ErrUnavailable
	}
	for _, run := range runs {
		if run.WorkItemID == nil || *run.WorkItemID != work {
			continue
		}
		id, digest := run.TemplateVersionID, run.DefinitionHash
		if id == 0 {
			id, digest = row.TemplateVersionID, row.SHA256
		}
		versions := query.BindVersions(tx, m.names)
		version, e := versions.WithContext(ctx).Where(versions.TenantID.Eq(tenant), versions.ID.Eq(id)).Take()
		if e != nil {
			return empty, false, e
		}
		if version.SHA256 != digest {
			return empty, false, ErrConflict
		}
		v, e := versionView(version)
		if e != nil {
			return empty, false, e
		}
		defs, e := verifierDefinitions(v.Definition)
		if e != nil {
			return empty, false, e
		}
		parameters := map[string]any{}
		if len(run.Parameters) > 0 {
			if json.Unmarshal(run.Parameters, &parameters) != nil {
				return empty, false, ErrUnavailable
			}
		} else if len(defs) > 0 {
			return empty, false, ErrUnavailable
		}
		defs, e = resolveVerifiers(defs, parameters)
		if e != nil {
			return empty, false, e
		}
		rubric, e := rubricDefinition(v.Definition)
		if e != nil {
			return empty, false, e
		}
		progress, e := progressPolicy(v.Definition)
		if e != nil {
			return empty, false, e
		}
		return VerifierBinding{ProgressPolicy: progress, TemplateVersionID: id, DefinitionHash: digest, Parameters: parameters, Verifiers: defs, Rubric: rubric}, true, nil
	}
	return empty, false, nil
}
