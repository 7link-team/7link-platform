package inspection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putLocal(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestDiscoveryLocalCatalogsAndUnsupportedAreIndependent(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, "codex")
	putLocal(t, filepath.Join(codex, "models_cache.json"), `{"models":[{"slug":"gpt-5.2"},{"slug":"gpt-5.3"}],"access_token":"PRIVATE_SECRET"}`)
	facts := discover("MODELS", home, codex)
	if facts[0].State != "READY" || len(facts[0].Models) != 2 || facts[0].Models[0] != "gpt-5.3" || facts[1].State != "UNSUPPORTED" || facts[1].Reason == "" {
		t.Fatalf("catalog facts incorrect: %+v", facts)
	}
	putLocal(t, filepath.Join(home, ".claude", "settings.json"), `{"availableModels":["claude-opus-4-6"],"env":{"ANTHROPIC_AUTH_TOKEN":"PRIVATE_SECRET"}}`)
	facts = discover("MODELS", home, codex)
	if facts[1].State != "READY" || facts[1].Models[0] != "claude-opus-4-6" {
		t.Fatal(facts)
	}
	b, _ := json.Marshal(facts)
	if strings.Contains(string(b), "PRIVATE_SECRET") {
		t.Fatal("secret leaked")
	}
	putLocal(t, filepath.Join(codex, "models_cache.json"), `{"models":[{"slug":"invalid model"}]}`)
	if discover("MODELS", home, codex)[0].State != "ERROR" {
		t.Fatal("invalid catalog accepted")
	}
	putLocal(t, filepath.Join(codex, "models_cache.json"), `not-json`)
	if discover("MODELS", home, codex)[0].Reason != "LOCAL_CONFIG_UNREADABLE" {
		t.Fatal("malformed config not reported")
	}
}

func TestDiscoveryReasoningUsesOnlyModelCatalogEvidence(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, "codex")
	putLocal(t, filepath.Join(codex, "models_cache.json"), `{"models":[{"slug":"gpt-5","supported_reasoning_levels":[{"effort":"low","description":"PRIVATE_DESCRIPTION"},{"effort":"high"}]},{"slug":"gpt-other"}],"token":"PRIVATE_SECRET"}`)
	facts := discover("MODELS", home, codex)
	if facts[0].State != "READY" || len(facts[0].Reasoning) != 1 || facts[0].Reasoning[0].Model != "gpt-5" || strings.Join(facts[0].Reasoning[0].Efforts, ",") != "low,high" || len(facts[1].Reasoning) != 0 {
		t.Fatalf("incorrect evidence: %+v", facts)
	}
	b, _ := json.Marshal(facts)
	if strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("catalog leaked private fields")
	}
	putLocal(t, filepath.Join(codex, "models_cache.json"), `{"models":[{"slug":"gpt-5","supported_reasoning_levels":[{"effort":"invalid effort"}]}]}`)
	if got := discover("MODELS", home, codex)[0]; got.State != "ERROR" || len(got.Reasoning) != 0 {
		t.Fatal("invalid effort accepted", got)
	}
}
func TestDiscoveryCapabilitiesAllowlistNamesAndHashesOnly(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, "codex")
	putLocal(t, filepath.Join(codex, "skills", "local-skill", "SKILL.md"), "PRIVATE_SKILL_CONTENT")
	putLocal(t, filepath.Join(codex, "config.toml"), "[mcp_servers.reader]\nurl=\"https://private/?token=PRIVATE_SECRET\"\n[mcp_servers.\"quoted-name\"]\ncommand=\"PRIVATE_COMMAND\"\n")
	putLocal(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"local-reader":{"env":{"KEY":"PRIVATE_SECRET"},"args":["PRIVATE_COMMAND"]}}}`)
	facts := discover("CAPABILITIES", home, codex)
	if len(facts[0].Skills) != 1 || len(facts[0].Skills[0].SHA256) != 64 || len(facts[0].MCP) != 2 || len(facts[1].MCP) != 1 {
		t.Fatalf("missing discovery facts %+v", facts)
	}
	b, _ := json.Marshal(facts)
	for _, secret := range []string{"PRIVATE_SKILL_CONTENT", "PRIVATE_SECRET", "PRIVATE_COMMAND", "https://private"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("payload leaked config values")
		}
	}
	if facts[0].State != "READY" || facts[1].State != "READY" {
		t.Fatal(facts)
	}
	putLocal(t, filepath.Join(codex, "config.toml"), strings.Repeat("x", 512*1024+1))
	if discover("CAPABILITIES", home, codex)[0].State != "ERROR" {
		t.Fatal("unbounded config accepted")
	}
}

func TestDiscoveryWorkdirMCPAndSkillsExcludeConfigurationValues(t *testing.T) {
	home, workdir := t.TempDir(), t.TempDir()
	codex := filepath.Join(home, "codex")
	putLocal(t, filepath.Join(workdir, ".agents", "skills", "project-skill", "SKILL.md"), "PRIVATE_CONTENT")
	putLocal(t, filepath.Join(workdir, ".codex", "config.toml"), "[mcp_servers.project]\ncommand=\"PRIVATE_COMMAND\"\n")
	putLocal(t, filepath.Join(workdir, ".mcp.json"), `{"mcpServers":{"project-reader":{"url":"PRIVATE_URL","env":{"TOKEN":"PRIVATE_TOKEN"}}}}`)
	facts := discoverScope("CAPABILITIES", home, codex, workdir)
	if facts[0].State != "READY" || len(facts[0].Skills) != 1 || len(facts[0].MCP) != 1 || facts[0].MCP[0] != "project" || len(facts[1].MCP) != 1 || facts[1].MCP[0] != "project-reader" {
		t.Fatalf("missing project discovery: %+v", facts)
	}
	b, _ := json.Marshal(facts)
	if strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("project discovery leaked config values")
	}
	if len(discoverScope("CAPABILITIES", home, codex, "")[0].Skills) != 0 {
		t.Fatal("project skills leaked into home scope")
	}
	putLocal(t, filepath.Join(workdir, ".codex", "config.toml"), "mcp_servers = { reader = {} }")
	if discoverScope("CAPABILITIES", home, codex, workdir)[0].State != "ERROR" {
		t.Fatal("unsupported TOML form reported a successful empty discovery")
	}
	if _, err := readLocal(workdir); err == nil {
		t.Fatal("nonregular config accepted")
	}
}
