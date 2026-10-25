package knowledge

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestSkillFrontmatterValidation(t *testing.T) {
	valid := "---\nname: review-code\ndescription: Review code with concrete evidence\nversion: 1.2.3\ntags: [Go, review]\nallowed-tools: [Read, Shell]\nlicense: MIT\nmetadata: {owner: engineering}\nfoo: ignored\n---\n# Review\nUse concrete evidence."
	f, raw, warnings, err := validateSkill(valid, "")
	require.NoError(t, err)
	require.Equal(t, "review-code", f.Name)
	require.Equal(t, []string{"go", "review"}, f.Tags)
	require.Contains(t, raw, "foo")
	require.Equal(t, []string{"Unknown frontmatter key: foo"}, warnings)
	_, _, _, err = validateSkill(strings.ReplaceAll(valid, "\n", "\r\n"), "")
	require.NoError(t, err)
	for _, input := range []string{strings.Replace(valid, "description: Review code with concrete evidence\n", "", 1), strings.Replace(valid, "review-code", "Review-Code", 1), strings.Replace(valid, "version: 1.2.3", "version: 01.2.3", 1), strings.Replace(valid, "tags: [Go, review]", "tags: wrong", 1), strings.Replace(valid, "allowed-tools: [Read, Shell]", "allowed-tools: [true]", 1), strings.Replace(valid, "metadata: {owner: engineering}", "metadata: wrong", 1), strings.Replace(valid, "license: MIT", "license: [MIT]", 1), strings.Replace(valid, "---\n# Review", "# Review", 1), strings.Repeat("a", 65537), "---\nname: review-code\nname: duplicate\n---\nbody"} {
		_, _, _, err = validateSkill(input, "")
		require.ErrorIs(t, err, ErrSkillFrontmatter, input)
	}
	_, _, _, err = validateSkill(valid, "another-key")
	require.ErrorIs(t, err, ErrSkillFrontmatter)
}
