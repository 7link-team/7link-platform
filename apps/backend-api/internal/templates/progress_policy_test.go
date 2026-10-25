package templates

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProgressPolicyRequiresExplicitApprovedCountsAndSafeDeclaredPaths(t *testing.T) {
	d := TemplateDefinition{Workflow: TemplateWorkflowDef{Parameters: map[string]any{}}}
	p, e := progressPolicy(d)
	require.NoError(t, e)
	require.Nil(t, p)
	for _, value := range []any{nil, map[string]any{}, map[string]any{"no_progress_limit": 2}, map[string]any{"no_progress_limit": 1.5, "observable_paths": []string{"out"}}, map[string]any{"dependency_miss_limit": 2, "required_dependency_paths": []string{"../secret"}}} {
		d.Workflow.Parameters["execution_progress"] = value
		_, e = progressPolicy(d)
		require.ErrorIs(t, e, ErrInvalid)
	}
	d.Workflow.Parameters["execution_progress"] = ProgressPolicy{NoProgressLimit: 3, ObservablePaths: []string{"src/result.txt"}, DependencyMissLimit: 2, RequiredDependencyPaths: []string{"input.txt"}}
	p, e = progressPolicy(d)
	require.NoError(t, e)
	require.Equal(t, 3, p.NoProgressLimit)
	require.Equal(t, 2, p.DependencyMissLimit)
}
