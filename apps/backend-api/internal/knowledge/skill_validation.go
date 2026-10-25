package knowledge

import (
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrSkillFrontmatter = errors.Join(ErrInvalid, errors.New("knowledge: invalid skill frontmatter"))
var ErrSkillNotMountable = errors.Join(ErrInvalid, errors.New("knowledge: skill not mountable"))
var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
var skillSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$`)

type SkillValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
type SkillFrontmatterError struct {
	Errors []SkillValidationError `json:"errors"`
}

func (e *SkillFrontmatterError) Error() string { return "knowledge: invalid SKILL.md" }
func (e *SkillFrontmatterError) Unwrap() error { return ErrSkillFrontmatter }

type SkillFrontmatter struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Version      string         `json:"version,omitempty"`
	Tags         []string       `json:"tags"`
	AllowedTools []string       `json:"allowed-tools,omitempty"`
	License      string         `json:"license,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func validateSkill(content, expectedName string) (SkillFrontmatter, string, []string, error) {
	f := SkillFrontmatter{Tags: []string{}}
	warnings := []string{}
	invalid := func(field, message string) (SkillFrontmatter, string, []string, error) {
		return f, "", warnings, &SkillFrontmatterError{Errors: []SkillValidationError{{Field: field, Message: message}}}
	}
	if !utf8.ValidString(content) || len(content) > 65536 || strings.IndexByte(content, 0) >= 0 {
		return invalid("content", "must be valid UTF-8 and at most 64 KiB")
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return invalid("frontmatter", "opening delimiter required")
	}
	if !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return invalid("frontmatter", "closing delimiter required")
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(normalized[4:4+end]), &values); err != nil || values == nil {
		return invalid("frontmatter", "must be a YAML mapping with unique keys")
	}
	known := []string{"name", "description", "version", "tags", "allowed-tools", "license", "metadata"}
	for key := range values {
		if !slices.Contains(known, key) {
			warnings = append(warnings, "Unknown frontmatter key: "+key)
		}
	}
	slices.Sort(warnings)
	stringValue := func(key string) (string, bool) {
		v, ok := values[key]
		if !ok {
			return "", true
		}
		s, ok := v.(string)
		return s, ok
	}
	var ok bool
	if f.Name, ok = stringValue("name"); !ok || !skillNamePattern.MatchString(f.Name) || expectedName != "" && f.Name != expectedName {
		return invalid("name", "must match the skill key and lowercase name pattern")
	}
	if f.Description, ok = stringValue("description"); !ok || utf8.RuneCountInString(strings.TrimSpace(f.Description)) < 10 || utf8.RuneCountInString(f.Description) > 1024 {
		return invalid("description", "must contain 10–1024 characters")
	}
	if f.Version, ok = stringValue("version"); !ok || f.Version != "" && !skillSemverPattern.MatchString(f.Version) {
		return invalid("version", "must be a semantic version string")
	}
	if f.License, ok = stringValue("license"); !ok {
		return invalid("license", "must be a string")
	}
	for _, key := range []string{"tags", "allowed-tools"} {
		value, exists := values[key]
		if !exists {
			continue
		}
		list, ok := value.([]any)
		if !ok || key == "tags" && len(list) > 10 {
			return invalid(key, "must be an array of strings; at most ten tags")
		}
		out := []string{}
		for _, v := range list {
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return invalid(key, "must contain nonempty strings")
			}
			if key == "tags" {
				s = strings.ToLower(s)
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		if key == "tags" {
			f.Tags = out
		} else {
			f.AllowedTools = out
		}
	}
	if v, exists := values["metadata"]; exists {
		f.Metadata, ok = v.(map[string]any)
		if !ok {
			return invalid("metadata", "must be an object")
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return invalid("frontmatter", "must contain JSON-compatible values")
	}
	return f, string(raw), warnings, nil
}
