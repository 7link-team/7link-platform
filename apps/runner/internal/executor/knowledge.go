package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

func validateKnowledge(items []KnowledgeItem) error {
	if len(items) > 50 {
		return ErrUnsupported
	}
	seen := map[string]bool{}
	knowledgeN, ruleN := 0, 0
	for _, item := range items {
		label := ""
		if strings.HasPrefix(item.Label, "[R") {
			ruleN++
			label = fmt.Sprintf("[R%d]", ruleN)
		} else {
			knowledgeN++
			label = fmt.Sprintf("[K%d]", knowledgeN)
		}
		id, err := strconv.ParseInt(item.EntryID, 10, 64)
		if err != nil || id <= 0 || seen[item.EntryID] || item.Version < 1 || item.Label != label || item.Title == "" || item.Body == "" || !utf8.ValidString(item.Title) || !utf8.ValidString(item.Body) || utf8.RuneCountInString(item.Title) > 200 || utf8.RuneCountInString(item.Body) > 8000 || ruleN > 20 {
			return ErrUnsupported
		}
		seen[item.EntryID] = true
		if hashBytes, err := hex.DecodeString(item.ContentHash); err != nil || len(hashBytes) != 32 {
			return ErrUnsupported
		}
		expected := item.ContentHash
		if item.SnapshotHash != "" {
			expected = item.SnapshotHash
		}
		if fmt.Sprintf("%x", sha256.Sum256([]byte(item.Title+"\n"+item.Body))) != expected {
			return ErrUnsupported
		}
	}
	return nil
}

// CitedOutput selects model-authored text only. Tool events, echoed request
// payloads and reference delivery never establish an output citation.
func CitedOutput(plan Plan, observation Observation) string {
	text := modelOutput(plan, observation)
	for _, label := range feedbackLabels(plan.QualityFeedback) {
		if strings.Contains(text, label) {
			return text
		}
	}
	for _, item := range plan.Knowledge {
		if strings.Contains(text, item.Label) {
			return text
		}
	}
	return ""
}

// DistilledOutput retains only model-authored SKILL.md candidates for the
// explicit server purpose. Tool output and ordinary work never become drafts.
func DistilledOutput(plan Plan, observation Observation) string {
	if plan.Distillation == nil {
		return ""
	}
	text := modelOutput(plan, observation)
	if !strings.HasPrefix(text, "---\n") {
		return ""
	}
	return text
}

func modelOutput(plan Plan, observation Observation) string {
	text := ""
	if plan.Runtime == "CODEX" && observation.Kind == "LOG" {
		text = observation.Content
	}
	if plan.Runtime == "CLAUDE_CODE" && observation.Kind == "RUNTIME_COMPLETED" {
		var result struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
		}
		if json.Unmarshal([]byte(observation.Content), &result) == nil && result.Type == "result" && result.Subtype == "success" && !result.IsError {
			text = result.Result
		}
	}
	if len(text) > 65536 || !utf8.ValidString(text) {
		return ""
	}
	return text
}

// Feedback labels refer only to the immutable source-owned structured failures.
func feedbackLabels(raw string) []string {
	var in struct {
		Feedback []json.RawMessage `json:"feedback"`
	}
	if len(raw) > 20000 || json.Unmarshal([]byte(raw), &in) != nil || len(in.Feedback) > 100 {
		return nil
	}
	labels := make([]string, len(in.Feedback))
	for i := range labels {
		labels[i] = fmt.Sprintf("[F%d]", i+1)
	}
	return labels
}
func feedbackPrompt(raw string) string {
	var in struct {
		Feedback []json.RawMessage `json:"feedback"`
	}
	labels := feedbackLabels(raw)
	if len(labels) == 0 {
		return raw
	}
	_ = json.Unmarshal([]byte(raw), &in)
	out := raw + "\nStructured feedback references (cite the label when addressed):"
	for i, body := range in.Feedback {
		out += "\n" + labels[i] + " " + string(body)
	}
	return out
}
