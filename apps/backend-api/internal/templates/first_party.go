package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FirstPartyGoldenCase is a shipped test input and expected label, never an
// assertion that a runner has executed or that a provider is calibrated.
type FirstPartyGoldenCase struct {
	VerifierKey string         `json:"verifier_key"`
	Input       map[string]any `json:"input"`
	Expected    string         `json:"expected"`
}

type firstPartyPack struct {
	key, domain, name, zh, ms                                     string
	stages, artifacts, deterministic, judges, dimensions, weights []string
}

// GUID generation becomes active in Start, after dependency initialization.
// OpenAPI export initializes modules without starting application side effects.
func (m *Module) Start(ctx context.Context) error {
	return m.transaction(ctx, func(tx *gorm.DB) error { return m.seedFirstPartyOn(ctx, tx) })
}

// Every call returns fresh values: editing a team copy cannot mutate the seed.
func firstPartyDefinitions() map[string]TemplateDefinition {
	packs := []firstPartyPack{
		{"software-dev", "SOFTWARE", "Software development", "软件开发", "Pembangunan perisian",
			[]string{"Requirements review", "Architecture and interface design", "Plan and freeze acceptance tests", "Implementation", "Internal quality", "Release and delivery"},
			[]string{"requirement_doc", "design_doc", "ui_contract", "source_change", "test_report", "screenshot_set", "build_artifact", "release_notes"},
			[]string{"build", "unit_tests", "lint", "typecheck", "frozen_acceptance", "frozen_test_diff", "coverage", "dependency_scan", "secret_scan", "ui_screenshot", "ui_layout_rules", "a11y_basic"},
			[]string{"ui_visual_judge", "code_quality", "test_sufficiency", "delivery_documentation", "requirement_understanding"},
			[]string{"ui_quality", "code_quality", "test_sufficiency", "delivery_documentation", "requirement_understanding"}, []string{"0.25", "0.20", "0.20", "0.15", "0.20"}},
		{"video-production", "VIDEO", "Video production", "视频制作", "Penerbitan video",
			[]string{"Brief", "Script confirmation", "Shot list", "Human asset selection", "Shot generation", "Editing", "Sound and subtitles", "Quality control", "Delivery variants"},
			[]string{"brief", "script", "shot_list", "character_sheet", "shot_take", "edit_timeline", "audio_mix", "subtitle", "master", "deliverable_variant", "qc_report", "provenance"},
			[]string{"ffprobe_spec", "av_qc", "loudness", "subtitle_rules", "subtitle_alignment", "shot_cut_match"},
			[]string{"face_consistency", "vbench_subset", "shot_vs_storyboard", "brand_safety"},
			[]string{"brief_adherence", "narrative_pacing", "consistency", "image_quality", "sound_subtitles"}, []string{"0.30", "0.20", "0.20", "0.20", "0.10"}},
		{"novel-writing", "NOVEL", "Novel writing", "小说创作", "Penulisan novel",
			[]string{"Premise", "Story bible", "Character sheets", "Outline confirmation", "Chapter plan", "Chapter drafts and story states", "Whole book continuity", "Human editorial review", "Manuscript delivery"},
			[]string{"premise", "story_bible", "character_sheet", "beat_sheet", "event_outline", "chapter_plan", "chapter_draft", "story_state", "foreshadow_ledger", "consistency_report", "editorial_notes", "manuscript"},
			[]string{"word_count", "name_registry", "repetition", "beat_position", "foreshadow_closure"},
			[]string{"continuity_check", "outline_adherence", "style_consistency", "reader_rubric"},
			[]string{"outline_premise", "continuity", "character_arcs", "tension_pacing", "style_language"}, []string{"0.25", "0.20", "0.20", "0.15", "0.20"}},
		{"team-tasks", "TEAM_TASKS", "Human team tasks", "人类团队任务", "Tugas pasukan manusia",
			[]string{"Request collection", "Triage", "Breakdown", "Schedule and assignment", "Execution", "Status reconciliation", "Risk management", "Delivery and independent sign off", "Retrospective"},
			[]string{"request", "task", "meeting_note", "status_report", "risk_register", "deliverable_link", "checklist_completion", "sign_off", "retro_note"},
			[]string{"checklist_completion", "deliverable_version", "independent_sign_off", "task_state_reconciliation"},
			[]string{"action_recall", "duplicate_detection", "status_fidelity", "deliverable_pre_review"},
			[]string{"deliverable_completeness", "review_quality", "timeliness", "communication"}, []string{"0.30", "0.25", "0.20", "0.25"}},
	}
	out := map[string]TemplateDefinition{}
	for _, p := range packs {
		w := TemplateWorkflowDef{SchemaVersion: 1, WorkflowID: "first-party:" + p.key, Version: 1, Name: p.name, Parameters: map[string]any{}, OutputBindings: map[string]string{}, EntryNodeID: "stage-0"}
		for i, stage := range p.stages {
			id := fmt.Sprintf("stage-%d", i)
			w.Nodes = append(w.Nodes, TemplateNode{ID: id, Kind: "WORK_ITEM", Name: stage, Objective: stage, Criteria: []TemplateCriterion{{Text: "Retain the required artifacts and original evidence for " + stage, VerificationMethod: "Run the frozen verifier plan and obtain required Human review"}}})
			if i > 0 {
				w.Edges = append(w.Edges, TemplateEdge{ID: "edge-" + id, Source: fmt.Sprintf("stage-%d", i-1), Target: id})
			}
		}
		// Mandatory team/client gates survive copies through validateInheritedGatesOn.
		w.Nodes = append(w.Nodes, TemplateNode{ID: "team-review", Kind: "HUMAN_GATE", Name: "Independent team review", Gate: "TEAM", Mandatory: true}, TemplateNode{ID: "client-acceptance", Kind: "HUMAN_GATE", Name: "Human delivery acceptance", Gate: "CLIENT", Mandatory: true})
		w.Edges = append(w.Edges, TemplateEdge{ID: "team-edge", Source: fmt.Sprintf("stage-%d", len(p.stages)-1), Target: "team-review"}, TemplateEdge{ID: "client-edge", Source: "team-review", Target: "client-acceptance"})
		defs := []VerifierDefinition{}
		for _, key := range p.deterministic {
			defs = append(defs, VerifierDefinition{Key: key, Kind: "DETERMINISTIC", Runner: "COMMAND", Version: 1, Argv: []string{"{{param:" + key + "_command}}"}, TimeoutSec: 300, Limitations: "Requires the project constitution's actual command and trusted execution evidence; missing tools block verification."})
		}
		for _, key := range p.judges {
			defs = append(defs, VerifierDefinition{Key: key, Kind: "MODEL_SCORED", Runner: "AGENT_JUDGE", Version: 1, TimeoutSec: 300, Limitations: "Requires independent provider calibration and original evidence; advisory only in HUMAN_ONLY mode; never substitutes for Human acceptance."})
		}
		rubric := RubricDefinition{PassRule: RubricPassRule{HardGates: "all_pass", WeightedMin: "3.5"}, Dimensions: []RubricDimension{{Key: "required_evidence", Kind: "HARD_GATE", VerifierKeys: p.deterministic}}}
		for i, key := range p.dimensions {
			rubric.Dimensions = append(rubric.Dimensions, RubricDimension{Key: key, Kind: "WEIGHTED", Weight: json.Number(p.weights[i])})
		}
		modes := []string{"HUMAN_ONLY", "HYBRID", "AUTONOMOUS"}
		if p.domain == "TEAM_TASKS" {
			modes = []string{"HUMAN_ONLY"}
		}
		metadata := map[string]any{"team_modes": modes, "names": map[string]string{"en-US": p.name, "zh-CN": p.zh, "ms-MY": p.ms}, "artifacts": p.artifacts, "verifiers": defs, "rubric": rubric, "golden_set": firstPartyGoldens(p.domain), "calibration_state": "UNCALIBRATED", "max_internal_rounds": 5}
		if p.domain == "SOFTWARE" {
			metadata["methods"] = []string{"agile", "waterfall"}
			metadata["method"] = "agile"
			w.Parameters["method"] = "agile"
			metadata["waterfall_required_reviews"] = []string{"requirements", "design", "testing", "delivery"}
			metadata["coverage_min_percent"] = 70
		}
		if p.domain == "VIDEO" {
			metadata["takes_per_shot"] = 3
			metadata["max_takes_per_shot"] = 6
			metadata["provider_kind"] = "VIDEO_GEN"
		}
		if p.domain == "NOVEL" {
			metadata["whole_book_required"] = true
			metadata["word_count_tolerance_percent"] = 15
			metadata["ced_max"] = "0.5"
		}
		if p.domain == "TEAM_TASKS" {
			metadata["independent_human_review_min_risk"] = "MEDIUM"
			metadata["assistant_role"] = "ADVISORY_ONLY"
		}
		out[p.key] = TemplateDefinition{Domain: p.domain, Semver: "1.0.0", Workflow: w, Metadata: metadata, ConstitutionSeed: "# Working agreement\nConfigure real verifier commands and provider bindings. Freeze acceptance criteria and evidence versions. Missing tools or uncalibrated judges cannot pass. Required Human decisions cannot be delegated to AI."}
	}
	// Metadata is decoded into maps by versionView. Hash exactly that persisted
	// representation, rather than Go struct field order inside interface values.
	for key, definition := range out {
		raw, _ := json.Marshal(definition)
		var normalized TemplateDefinition
		_ = json.Unmarshal(raw, &normalized)
		out[key] = normalized
	}
	return out
}

// seedFirstPartyOn only inserts absent immutable identities. A conflicting
// installed hash is an error, never an implicit migration of a published pack.
func (m *Module) seedFirstPartyOn(ctx context.Context, tx *gorm.DB) error {
	q := query.BindVersions(tx, m.names)
	for _, key := range []string{"software-dev", "video-production", "novel-writing", "team-tasks"} {
		d := firstPartyDefinitions()[key]
		if err := d.Validate(nil); err != nil {
			return err
		}
		hash, err := d.Hash()
		if err != nil {
			return err
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		row := &model.Version{ID: guid.ID(id), TenantID: 0, Key: key, Name: d.Workflow.Name, Origin: "FIRST_PARTY", Semver: d.Semver, State: "PUBLISHED", Definition: string(raw), SHA256: hash, Version: 1, CreatedAt: now, UpdatedAt: now}
		result := tx.WithContext(ctx).Table(m.names.Versions.String()).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			existing, e := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(0)), q.Key.Eq(key), q.Semver.Eq(d.Semver)).Take()
			if e != nil {
				return e
			}
			if existing.SHA256 != hash || existing.Origin != "FIRST_PARTY" || existing.State != "PUBLISHED" {
				return ErrConflict
			}
			continue
		}
		event := audit.DomainEvent{TenantID: 0, EventType: "template.published", TargetType: "template_version", TargetID: row.ID, Detail: map[string]any{"authority_source": "SYSTEM:first_party_catalog", "definition_sha256": hash, "template_key": key}}
		if err = m.audit(ctx, tx, event); err != nil {
			return err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{Type: "template.published", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:first_party_catalog"}, Subject: events.Subject{Kind: "template_version", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"hash": hash, "template_key": key, "origin": "FIRST_PARTY"}})
		if err != nil {
			return err
		}
	}
	return nil
}

func firstPartyGoldens(domain string) []FirstPartyGoldenCase {
	type pair struct {
		key, field string
		good, bad  any
	}
	cases := map[string][]pair{
		"SOFTWARE": {
			{"build", "exit_code", 0, 1}, {"unit_tests", "failed_tests", 0, 1}, {"lint", "errors", 0, 1}, {"typecheck", "errors", 0, 1},
			{"frozen_acceptance", "failed_cases", 0, 1}, {"frozen_test_diff", "modified_frozen_tests", 0, 1}, {"coverage", "coverage_percent", 70, 69},
			{"secret_scan", "exposed_secrets", 0, 1}, {"ui_layout_rules", "horizontal_overflow_px", 0, 105}, {"ui_screenshot", "console_errors", 0, 1},
		},
		"VIDEO": {
			{"ffprobe_spec", "duration_error_seconds", 0.4, 3}, {"ffprobe_spec", "resolution_matches_brief", true, false},
			{"av_qc", "middle_black_seconds", 0, 3}, {"av_qc", "freeze_seconds", 0, 3}, {"loudness", "broadcast_lufs", -23, -18},
			{"subtitle_rules", "adult_english_cps", 17, 22}, {"subtitle_rules", "line_characters", 42, 43},
			{"subtitle_alignment", "offset_ms", 150, 350}, {"shot_cut_match", "shot_duration_error_seconds", 0.2, 0.4},
			{"vbench_subset", "dynamic_degree_meets_minimum", true, false},
		},
		"NOVEL": {
			{"word_count", "words_for_target_4000", 4000, 5600}, {"name_registry", "unresolved_unknown_names", 0, 1},
			{"repetition", "eight_gram_repeat_percent", 1, 3}, {"beat_position", "midpoint_percent", 50, 60},
			{"foreshadow_closure", "unresolved_unintentional_threads", 0, 2}, {"continuity_check", "chapters_checked_of_30", 30, 29},
			{"continuity_check", "severe_unexplained_contradictions", 0, 1}, {"continuity_check", "ced_per_10000_words", 0.4, 0.6},
			{"outline_adherence", "missing_planned_events", 0, 1}, {"style_consistency", "unexplained_pov_changes", 0, 1},
		},
		"TEAM_TASKS": {
			{"checklist_completion", "complete_percent", 100, 99}, {"deliverable_version", "accessible", true, false},
			{"deliverable_version", "modified_after_task_start", true, false}, {"deliverable_version", "version_pinned", true, false},
			{"independent_sign_off", "signer_is_active_human", true, false}, {"independent_sign_off", "signer_differs_from_owner", true, false},
			{"independent_sign_off", "designated_role_matches", true, false}, {"task_state_reconciliation", "all_referenced_ids_exist", true, false},
			{"task_state_reconciliation", "reported_states_match", true, false}, {"checklist_completion", "all_items_have_evidence", true, false},
		},
	}
	out := []FirstPartyGoldenCase{}
	for _, c := range cases[domain] {
		out = append(out, FirstPartyGoldenCase{VerifierKey: c.key, Input: map[string]any{c.field: c.good}, Expected: "PASS"}, FirstPartyGoldenCase{VerifierKey: c.key, Input: map[string]any{c.field: c.bad}, Expected: "FAIL"})
	}
	return out
}
