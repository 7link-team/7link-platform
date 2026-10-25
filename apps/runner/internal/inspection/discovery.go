package inspection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Discovery contains allowlisted identifiers only. Config bodies, URLs, commands,
// environment variables, authentication fields and skill contents never leave disk.
type Discovery struct {
	Runtime   string           `json:"runtime"`
	State     string           `json:"state"`
	Reason    string           `json:"reason,omitempty"`
	Source    string           `json:"source"`
	Models    []string         `json:"models"`
	Skills    []Skill          `json:"skills"`
	MCP       []string         `json:"mcp"`
	Reasoning []ModelReasoning `json:"reasoning,omitempty"`
}
type ModelReasoning struct {
	Model   string   `json:"model"`
	Efforts []string `json:"efforts"`
}
type Skill struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func identifier(v string, limit int) bool {
	return v != "" && len(v) <= limit && strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func readLocal(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular configuration file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 512*1024+1))
	if err != nil || len(b) > 512*1024 {
		return nil, errors.New("local configuration exceeds discovery limit")
	}
	return b, nil
}

func Discover(kind string) []Discovery { return DiscoverScope(kind, "") }
func DiscoverScope(kind, workdir string) []Discovery {
	home, err := os.UserHomeDir()
	if err != nil {
		return []Discovery{{Runtime: "CODEX", State: "ERROR", Reason: "LOCAL_CONFIG_UNREADABLE", Source: "LOCAL_CONFIG"}, {Runtime: "CLAUDE_CODE", State: "ERROR", Reason: "LOCAL_CONFIG_UNREADABLE", Source: "LOCAL_CONFIG"}}
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return discoverScope(kind, home, codex, workdir)
}
func discover(kind, home, codex string) []Discovery { return discoverScope(kind, home, codex, "") }
func discoverScope(kind, home, codex, workdir string) []Discovery {
	out := []Discovery{}
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		d := Discovery{Runtime: runtime, State: "READY", Source: "LOCAL_CONFIG", Models: []string{}, Skills: []Skill{}, MCP: []string{}}
		if kind == "MODELS" {
			var b []byte
			var err error
			if runtime == "CODEX" {
				b, err = readLocal(filepath.Join(codex, "models_cache.json"))
				var cache struct {
					Models []struct {
						Slug   string `json:"slug"`
						Levels []struct {
							Effort string `json:"effort"`
						} `json:"supported_reasoning_levels"`
					} `json:"models"`
				}
				if err == nil {
					err = json.Unmarshal(b, &cache)
					for _, m := range cache.Models {
						d.Models = append(d.Models, m.Slug)
						if m.Levels != nil {
							efforts := []string{}
							for _, level := range m.Levels {
								efforts = append(efforts, level.Effort)
							}
							d.Reasoning = append(d.Reasoning, ModelReasoning{Model: m.Slug, Efforts: efforts})
						}
					}
				}
				d.Source = "CLI_CACHE"
			} else {
				b, err = readLocal(filepath.Join(home, ".claude", "settings.json"))
				var settings struct {
					AvailableModels []string `json:"availableModels"`
				}
				if err == nil {
					err = json.Unmarshal(b, &settings)
					d.Models = settings.AvailableModels
				}
			}
			if errors.Is(err, os.ErrNotExist) || err == nil && len(d.Models) == 0 {
				d.State = "UNSUPPORTED"
				d.Reason = "NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND"
				d.Models = []string{}
			} else if err != nil {
				d.State = "ERROR"
				d.Reason = "LOCAL_CONFIG_UNREADABLE"
				d.Models = []string{}
			}
			for _, id := range d.Models {
				if !identifier(id, 160) {
					d.State = "ERROR"
					d.Reason = "INVALID_MODEL_LIST"
				}
			}
			if len(d.Models) > 1000 {
				d.State = "ERROR"
				d.Reason = "MODEL_LIST_TOO_LARGE"
			}
			for _, model := range d.Reasoning {
				if len(model.Efforts) > 16 {
					d.State, d.Reason = "ERROR", "INVALID_MODEL_LIST"
				}
				for _, effort := range model.Efforts {
					if !identifier(effort, 16) {
						d.State, d.Reason = "ERROR", "INVALID_MODEL_LIST"
					}
				}
			}
			if d.State == "ERROR" {
				d.Models = []string{}
				d.Reasoning = nil
			}
			slices.SortFunc(d.Models, func(a, b string) int { return -strings.Compare(a, b) })
			d.Models = slices.Compact(d.Models)
		} else {
			dirs := []string{filepath.Join(codex, "skills"), filepath.Join(home, ".agents", "skills")}
			if runtime == "CLAUDE_CODE" {
				dirs = []string{filepath.Join(home, ".claude", "skills")}
			}
			if workdir != "" && filepath.IsAbs(workdir) {
				projectDir := ".agents"
				if runtime == "CLAUDE_CODE" {
					projectDir = ".claude"
				}
				dirs = append(dirs, filepath.Join(workdir, projectDir, "skills"))
			}
			for _, dir := range dirs {
				entries, err := readSkillDirectory(dir)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					d.State = "ERROR"
					d.Reason = "LOCAL_CONFIG_UNREADABLE"
				}
				for _, entry := range entries {
					if !entry.IsDir() || !identifier(entry.Name(), 200) {
						continue
					}
					b, err := readLocal(filepath.Join(dir, entry.Name(), "SKILL.md"))
					if err != nil {
						if errors.Is(err, os.ErrNotExist) {
							continue
						}
						d.State = "ERROR"
						d.Reason = "LOCAL_SKILL_UNREADABLE"
						continue
					}
					h := sha256.Sum256(b)
					d.Skills = append(d.Skills, Skill{Name: entry.Name(), SHA256: hex.EncodeToString(h[:])})
				}
			}
			path := filepath.Join(home, ".claude.json")
			if runtime == "CODEX" {
				path = filepath.Join(codex, "config.toml")
			}
			paths := []string{path}
			if workdir != "" && filepath.IsAbs(workdir) {
				if runtime == "CLAUDE_CODE" {
					paths = append(paths, filepath.Join(workdir, ".mcp.json"))
				} else {
					paths = append(paths, filepath.Join(workdir, ".codex", "config.toml"))
				}
			}
			for _, path := range paths {
				b, err := readLocal(path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					d.State = "ERROR"
					d.Reason = "LOCAL_CONFIG_UNREADABLE"
				}
				if err == nil {
					if runtime == "CLAUDE_CODE" {
						var config struct {
							MCPServers map[string]json.RawMessage `json:"mcpServers"`
						}
						if json.Unmarshal(b, &config) != nil {
							d.State = "ERROR"
							d.Reason = "LOCAL_CONFIG_UNREADABLE"
						} else {
							for name := range config.MCPServers {
								if identifier(name, 200) {
									d.MCP = append(d.MCP, name)
								}
							}
						}
					} else {
						// Read table names only, never values. Other TOML syntax is ignored.
						for _, line := range strings.Split(string(b), "\n") {
							line = strings.TrimSpace(line)
							if strings.HasPrefix(line, "mcp_servers") && strings.Contains(line, "=") {
								d.State = "ERROR"
								d.Reason = "LOCAL_CONFIG_UNREADABLE"
							}
							if !strings.HasPrefix(line, "[mcp_servers.") {
								continue
							}
							end := strings.Index(line, "]")
							if end < 0 {
								d.State = "ERROR"
								d.Reason = "LOCAL_CONFIG_UNREADABLE"
								continue
							}
							name := strings.TrimSpace(strings.TrimPrefix(line[:end], "[mcp_servers."))
							quoted := strings.HasPrefix(name, "\"") || strings.HasPrefix(name, "'")
							if strings.HasPrefix(name, "\"") {
								unquoted, e := strconv.Unquote(name)
								if e != nil {
									d.State = "ERROR"
									d.Reason = "LOCAL_CONFIG_UNREADABLE"
									continue
								}
								name = unquoted
							}
							if strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'") {
								name = strings.Trim(name, "'")
							}
							if identifier(name, 200) && (quoted || !strings.Contains(name, ".")) {
								d.MCP = append(d.MCP, name)
							}
						}
					}
				}
			}
			slices.Sort(d.MCP)
			d.MCP = slices.Compact(d.MCP)
			if len(d.Skills) > 2000 || len(d.MCP) > 2000 {
				d.State = "ERROR"
				d.Reason = "CAPABILITY_LIST_TOO_LARGE"
				d.Skills = []Skill{}
				d.MCP = []string{}
			}
		}
		out = append(out, d)
	}
	return out
}

func readSkillDirectory(path string) ([]os.DirEntry, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	entries, e := f.ReadDir(2001)
	if errors.Is(e, io.EOF) {
		e = nil
	}
	if len(entries) > 2000 {
		return nil, errors.New("skill directory exceeds discovery limit")
	}
	return entries, e
}
