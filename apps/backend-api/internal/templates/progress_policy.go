package templates

import (
	"encoding/json"
	"path"
	"strings"
)

// ProgressPolicy is approved with the immutable template version. Counts are
// completed runtime actions with unchanged declared outputs / missing inputs;
// heartbeat and repeated tool names are never evidence of progress.
type ProgressPolicy struct {
	NoProgressLimit         int      `json:"no_progress_limit"`
	DependencyMissLimit     int      `json:"dependency_miss_limit"`
	ObservablePaths         []string `json:"observable_paths"`
	RequiredDependencyPaths []string `json:"required_dependency_paths"`
}

func progressPolicy(d TemplateDefinition) (*ProgressPolicy, error) {
	value, exists := d.Workflow.Parameters["execution_progress"]
	if !exists {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	var p ProgressPolicy
	if json.Unmarshal(raw, &p) != nil || p.NoProgressLimit < 0 || p.DependencyMissLimit < 0 || (p.NoProgressLimit == 0 && p.DependencyMissLimit == 0) || (p.NoProgressLimit > 0 && len(p.ObservablePaths) == 0) || (p.DependencyMissLimit > 0 && len(p.RequiredDependencyPaths) == 0) || len(p.ObservablePaths)+len(p.RequiredDependencyPaths) > 100 {
		return nil, ErrInvalid
	}
	for _, paths := range [][]string{p.ObservablePaths, p.RequiredDependencyPaths} {
		seen := map[string]bool{}
		for _, name := range paths {
			if name == "" || len(name) > 512 || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || seen[name] {
				return nil, ErrInvalid
			}
			seen[name] = true
		}
	}
	return &p, nil
}
