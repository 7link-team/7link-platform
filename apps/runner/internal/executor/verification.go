package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// VerificationPlan is a platform-pinned command, separate from model execution.
// SourceWorkdir comes from the registered workspace, never a posted receipt.
type VerificationPlan struct {
	RegistryVerifiers json.RawMessage `json:"registry_verifiers,omitempty"`
	Correlation       *Correlation    `json:"correlation,omitempty"`
	TemplateVerifier  json.RawMessage `json:"template_verifier,omitempty"`
	ID                string          `json:"id"`
	Nonce             string          `json:"nonce"`
	ResultID          string          `json:"result_id"`
	ResultVersion     int             `json:"result_version"`
	CriterionID       string          `json:"criterion_id"`
	Method            string          `json:"method"`
	SourceWorkdir     string          `json:"source_workdir"`
	HeadCommit        string          `json:"head_commit"`
	Argv              []string        `json:"argv"`
	TimeoutSec        int             `json:"timeout_sec"`
	ReportPath        string          `json:"report_path"`
	ReviewerID        string          `json:"reviewer_id"`
	ReviewerRuntime   string          `json:"reviewer_runtime"`
	ReviewerModel     string          `json:"reviewer_model"`
	ReviewerFamily    string          `json:"reviewer_family"`
	Stage             string          `json:"stage"`
	CriterionText     string          `json:"criterion_text"`
	FamilyFallback    bool            `json:"family_fallback"`
}

// VerificationObservation describes an actual process, not an executor claim.
// Output is deliberately omitted: command output can contain secrets. Digest
// covers the complete stream and is not itself proof of test coverage.
type VerificationObservation struct {
	Unverified      []string                 `json:"unverified,omitempty"`
	Screenshots     []VisualScreenshot       `json:"screenshots,omitempty"`
	BrowserProvider string                   `json:"browser_provider,omitempty"`
	PlanHash        string                   `json:"plan_hash"`
	HeadCommit      string                   `json:"head_commit"`
	ExitCode        *int                     `json:"exit_code,omitempty"`
	Reason          string                   `json:"reason"`
	OutputSHA256    string                   `json:"output_sha256"`
	ReportSHA256    string                   `json:"report_sha256"`
	StartedAt       time.Time                `json:"started_at"`
	EndedAt         time.Time                `json:"ended_at"`
	Passed          *int                     `json:"passed,omitempty"`
	Failed          *int                     `json:"failed,omitempty"`
	Skipped         *int                     `json:"skipped,omitempty"`
	AgentReport     *VerificationAgentReport `json:"agent_report,omitempty"`
}

type VerificationAgentFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}
type VerificationAgentReport struct {
	CriterionID string                     `json:"criterion_id"`
	HeadCommit  string                     `json:"head_commit"`
	Verdict     string                     `json:"verdict"`
	Findings    []VerificationAgentFinding `json:"findings"`
	Unverified  []string                   `json:"unverified"`
}

func validVerificationPlan(p VerificationPlan) bool {
	if !validCorrelation(p.Correlation) || p.ID == "" || p.Nonce == "" || p.ResultID == "" || p.ResultVersion < 1 || p.CriterionID == "" || (p.Method != "TEST" && p.Method != "COMMAND" && p.Method != "AGENT_JUDGE" && p.Method != "VISUAL") || !filepath.IsAbs(p.SourceWorkdir) || p.TimeoutSec < 1 || p.TimeoutSec > 3600 {
		return false
	}
	if p.Method == "AGENT_JUDGE" {
		if p.ReviewerID == "" || p.ReviewerFamily == "" || p.ReviewerModel == "" || (p.ReviewerRuntime != "CODEX" && p.ReviewerRuntime != "CLAUDE_CODE") || (p.Stage != "AGENT_TEST" && p.Stage != "AGENT_ACCEPTANCE") || strings.TrimSpace(p.CriterionText) == "" || len(p.CriterionText) > 10000 {
			return false
		}
	} else if len(p.Argv) < 1 || len(p.Argv) > 100 || p.Argv[0] == "" || strings.HasPrefix(p.Argv[0], "-") {
		return false
	}
	if len(p.HeadCommit) != 40 && len(p.HeadCommit) != 64 {
		return false
	}
	if _, err := hex.DecodeString(p.HeadCommit); err != nil || p.HeadCommit != strings.ToLower(p.HeadCommit) {
		return false
	}
	for _, arg := range p.Argv {
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return false
		}
	}
	if p.Method == "TEST" && (p.ReportPath == "" || filepath.IsAbs(p.ReportPath) || strings.Contains(p.ReportPath, "\\") || strings.Contains(p.ReportPath, ":") || strings.HasPrefix(filepath.Clean(p.ReportPath), "..")) {
		return false
	}
	return true
}

// verificationEnvironment excludes runtime login profiles and machine/API
// credentials, and suppresses global Git config and hooks during snapshotting.
func verificationEnvironment() []string {
	out := []string{}
	for _, item := range Environment(os.Environ()) {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA":
			continue
		}
		out = append(out, item)
	}
	return append(out, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
}

// RunVerification creates a disposable detached copy at the exact claimed
// commit. It never runs in or copies uncommitted files from the delivery
// workspace. This is workspace isolation, not an operating-system sandbox.
// Unsupported methods never launch a process or manufacture a verdict.
func RunVerification(ctx context.Context, p VerificationPlan) (VerificationObservation, error) {
	var out VerificationObservation
	if !validVerificationPlan(p) {
		return out, ErrUnsupported
	}
	encoded, _ := json.Marshal(p)
	digest := sha256.Sum256(encoded)
	out.PlanHash = hex.EncodeToString(digest[:])
	out.StartedAt = time.Now().UTC()
	finish := func(reason string) VerificationObservation {
		out.Reason, out.EndedAt = reason, time.Now().UTC()
		return out
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSec)*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "verification-")
	if err != nil {
		return finish("SNAPSHOT_UNAVAILABLE"), err
	}
	defer os.RemoveAll(root)
	workdir := filepath.Join(root, "snapshot")
	git := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(runCtx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)...)
		hideWindow(cmd)
		configureCancellation(cmd)
		cmd.Env = verificationEnvironment()
		cmd.Stderr = io.Discard
		return cmd.Output()
	}
	// --no-local avoids shared object files and alternates with the source.
	if _, err = git("clone", "--no-local", "--no-checkout", "--", p.SourceWorkdir, workdir); err != nil {
		return finish("SNAPSHOT_UNAVAILABLE"), nil
	}
	if _, err = git("-C", workdir, "checkout", "--detach", p.HeadCommit, "--"); err != nil {
		return finish("SNAPSHOT_UNAVAILABLE"), nil
	}
	head, err := git("-C", workdir, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != p.HeadCommit {
		return finish("HEAD_MISMATCH"), nil
	}
	out.HeadCommit = p.HeadCommit
	if p.Method == "VISUAL" {
		return runVisualVerification(runCtx, p, workdir, out), nil
	}
	if p.Method == "AGENT_JUDGE" {
		return runAgentVerification(runCtx, p, workdir, out), nil
	}
	if p.Method == "TEST" {
		// A committed report is stale input, never output of this invocation.
		if _, statErr := os.Lstat(filepath.Join(workdir, p.ReportPath)); !errors.Is(statErr, os.ErrNotExist) {
			return finish("REPORT_PREEXISTS"), nil
		}
	}
	cmd := exec.CommandContext(runCtx, p.Argv[0], p.Argv[1:]...)
	hideWindow(cmd)
	configureCancellation(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir, cmd.Env = workdir, correlationEnvironment(verificationEnvironment(), p.Correlation)
	output := sha256.New()
	// os/exec serializes writes when stdout and stderr share one writer.
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	out.OutputSHA256 = hex.EncodeToString(output.Sum(nil))
	if runCtx.Err() != nil {
		return finish("TIMEOUT_OR_CANCELLED"), nil
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return finish("COMMAND_NOT_RUNNABLE"), nil
	}
	code := 0
	if exit != nil {
		code = exit.ExitCode()
	}
	if code < 0 {
		return finish("PROCESS_OUTCOME_UNKNOWN"), nil
	}
	out.ExitCode = &code
	head, err = git("-C", workdir, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != p.HeadCommit {
		return finish("HEAD_MISMATCH"), nil
	}
	if p.Method == "TEST" && code == 0 {
		path, pathErr := filepath.EvalSymlinks(filepath.Join(workdir, p.ReportPath))
		rel, relErr := filepath.Rel(workdir, path)
		if pathErr != nil || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return finish("REPORT_UNPARSEABLE"), nil
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return finish("REPORT_UNPARSEABLE"), nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return finish("REPORT_UNPARSEABLE"), nil
		}
		defer file.Close()
		var report struct {
			Passed  *int `json:"passed"`
			Failed  *int `json:"failed"`
			Skipped *int `json:"skipped"`
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 65537))
		if readErr != nil || len(data) > 65536 || json.Unmarshal(data, &report) != nil || report.Passed == nil || report.Failed == nil || report.Skipped == nil || *report.Passed < 0 || *report.Failed < 0 || *report.Skipped < 0 || (*report.Passed == 0 && *report.Failed == 0) {
			return finish("REPORT_UNPARSEABLE"), nil
		}
		out.Passed, out.Failed, out.Skipped = report.Passed, report.Failed, report.Skipped
		reportHash := sha256.Sum256(data)
		out.ReportSHA256 = hex.EncodeToString(reportHash[:])
	}
	return finish("PROCESS_EXITED"), nil
}
