package execution

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type verifierRegistryServices struct {
	explain core.AuthorizationExplanation
	work    delivery.VerifierWorkPolicy
}

type VerifierPolicy struct {
	WorkItemTypes []string  `json:"work_item_types" maxItems:"11"`
	RiskLevels    []string  `json:"risk_levels" maxItems:"3"`
	ProjectIDs    []guid.ID `json:"project_ids" maxItems:"200"`
}
type VerifierSpec struct {
	Argv             []string          `json:"argv,omitempty" maxItems:"32"`
	Workdir          string            `json:"workdir,omitempty" maxLength:"255"`
	Env              map[string]string `json:"env,omitempty"`
	ReportGlobs      []string          `json:"report_globs,omitempty" maxItems:"10"`
	ReportFormat     string            `json:"report_format,omitempty" enum:",NONE,JUNIT_XML,GO_TEST_JSON,TAP"`
	StdoutTailBytes  int               `json:"stdout_tail_bytes,omitempty"`
	ToolVersionArgv  []string          `json:"tool_version_argv,omitempty" maxItems:"32"`
	AgentID          guid.ID           `json:"agent_id,omitempty"`
	PromptTemplate   string            `json:"prompt_template,omitempty" maxLength:"16384"`
	MaxCostMicros    int64             `json:"max_cost_micros,omitempty"`
	Instructions     string            `json:"instructions,omitempty" maxLength:"4000"`
	EligibleRoleKeys []string          `json:"eligible_role_keys,omitempty" maxItems:"50"`
}
type TenantVerifierDefinition struct {
	Key        string         `json:"key" pattern:"^[a-z][a-z0-9-]{1,63}$"`
	Name       string         `json:"name" minLength:"1" maxLength:"200"`
	Layer      string         `json:"layer" enum:"AUTOMATED,AI_ASSISTED,HUMAN"`
	State      string         `json:"state" enum:"ACTIVE,ARCHIVED"`
	AppliesTo  VerifierPolicy `json:"applies_to"`
	Required   bool           `json:"required"`
	Spec       VerifierSpec   `json:"spec"`
	TimeoutMS  int64          `json:"timeout_ms" minimum:"1000" maximum:"3600000"`
	ChangeNote string         `json:"change_note" maxLength:"2000"`
}
type VerifierVersionView struct {
	ID         guid.ID      `json:"id"`
	VersionNo  int          `json:"version_no"`
	Spec       VerifierSpec `json:"spec"`
	TimeoutMS  int64        `json:"timeout_ms"`
	ChangeNote string       `json:"change_note"`
	CreatedBy  guid.ID      `json:"created_by"`
	CreatedAt  time.Time    `json:"created_at"`
}
type VerifierView struct {
	ID               guid.ID             `json:"id"`
	Key              string              `json:"key"`
	Name             string              `json:"name"`
	Layer            string              `json:"layer"`
	State            string              `json:"state"`
	Required         bool                `json:"required"`
	AppliesTo        VerifierPolicy      `json:"applies_to"`
	CurrentVersionNo int                 `json:"current_version_no"`
	Version          int                 `json:"version"`
	Current          VerifierVersionView `json:"current"`
}

var verifierKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
var verifierEnvPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

func validVerifierPolicy(p VerifierPolicy) bool {
	if len(p.WorkItemTypes) > 11 || len(p.RiskLevels) > 3 || len(p.ProjectIDs) > 200 {
		return false
	}
	for _, v := range p.WorkItemTypes {
		if !slices.Contains(strings.Fields("TASK FEATURE BUG CHORE DOC RESEARCH DESIGN TEST RISK INCIDENT CONTENT"), v) {
			return false
		}
	}
	for _, v := range p.RiskLevels {
		if !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, v) {
			return false
		}
	}
	for _, v := range p.ProjectIDs {
		if v <= 0 {
			return false
		}
	}
	return true
}
func verifierMatches(p VerifierPolicy, project guid.ID, kind, risk string) bool {
	return (len(p.ProjectIDs) == 0 || slices.Contains(p.ProjectIDs, project)) && (len(p.WorkItemTypes) == 0 || slices.Contains(p.WorkItemTypes, kind)) && (len(p.RiskLevels) == 0 || slices.Contains(p.RiskLevels, risk))
}
func verifierRelative(v string) bool {
	return v != "" && !path.IsAbs(v) && !strings.ContainsAny(v, "\\:\x00") && path.Clean(v) != ".." && !strings.HasPrefix(path.Clean(v), "../") && len(v) <= 255
}
func verifierText(v string, n int, required bool) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) <= n && !strings.ContainsRune(v, 0) && (!required || strings.TrimSpace(v) != "")
}
func validVerifierSpec(layer string, s VerifierSpec) bool {
	argv := func(a []string, required bool) bool {
		if len(a) > 32 || (required && len(a) == 0) {
			return false
		}
		for _, v := range a {
			if !verifierText(v, 1024, true) {
				return false
			}
		}
		return len(a) == 0 || !strings.HasPrefix(a[0], "-")
	}
	switch layer {
	case "AUTOMATED":
		if !argv(s.Argv, true) || !argv(s.ToolVersionArgv, false) || s.AgentID != 0 || s.PromptTemplate != "" || s.MaxCostMicros != 0 || s.Instructions != "" || len(s.EligibleRoleKeys) > 0 {
			return false
		}
		if s.Workdir != "" && !verifierRelative(s.Workdir) {
			return false
		}
		if !slices.Contains([]string{"NONE", "JUNIT_XML", "GO_TEST_JSON", "TAP"}, s.ReportFormat) || s.StdoutTailBytes < 1024 || s.StdoutTailBytes > 65536 || len(s.Env) > 20 || len(s.ReportGlobs) > 10 {
			return false
		}
		for k, v := range s.Env {
			if !verifierEnvPattern.MatchString(k) || !verifierText(v, 1024, false) {
				return false
			}
		}
		for _, v := range s.ReportGlobs {
			if !verifierRelative(v) {
				return false
			}
		}
	case "AI_ASSISTED":
		if s.AgentID <= 0 || !verifierText(s.PromptTemplate, 16384, true) || s.MaxCostMicros < 1 || s.MaxCostMicros > 50000000 || s.Instructions != "" || len(s.EligibleRoleKeys) > 0 {
			return false
		}
		if len(s.Argv) > 0 || s.Workdir != "" || len(s.Env) > 0 || len(s.ReportGlobs) > 0 || s.ReportFormat != "" || s.StdoutTailBytes != 0 || len(s.ToolVersionArgv) > 0 {
			return false
		}
	case "HUMAN":
		if !verifierText(s.Instructions, 4000, true) || len(s.EligibleRoleKeys) < 1 || len(s.EligibleRoleKeys) > 50 || s.AgentID != 0 || s.PromptTemplate != "" || s.MaxCostMicros != 0 {
			return false
		}
		for _, k := range s.EligibleRoleKeys {
			if !verifierRoleKey(k) {
				return false
			}
		}
		if len(s.Argv) > 0 || s.Workdir != "" || len(s.Env) > 0 || len(s.ReportGlobs) > 0 || s.ReportFormat != "" || s.StdoutTailBytes != 0 || len(s.ToolVersionArgv) > 0 {
			return false
		}
	default:
		return false
	}
	return true
}
func verifierRoleKey(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func (m *Module) bindVerifierRegistry(s *module.Services) error {
	var err error
	m.verifierRegistry.explain, err = module.Resolve[core.AuthorizationExplanation](s)
	if err != nil {
		return err
	}
	m.verifierRegistry.work, err = module.Resolve[delivery.VerifierWorkPolicy](s)
	return err
}
func (m *Module) verifierAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if transaction(tx, tenant, actor) != nil || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if m.verifierRegistry.explain == nil || m.principals.ActiveHumanByID == nil {
		return ErrUnavailable
	}
	if err := m.principalLocks(ctx, tx, actor); err != nil {
		return err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	e, err := m.verifierRegistry.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: actor, Permission: permission})
	if err != nil {
		return err
	}
	if !e.Allowed || e.Reason == "explicit_deny" || e.Reason == "administrator_scope_denied" {
		return ErrForbidden
	}
	return nil
}
func (m *Module) verifierEventOn(ctx context.Context, tx *gorm.DB, r model.Verifier, kind string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	detail := map[string]any{"verifier_id": r.ID.String(), "version_no": r.CurrentVersionNo, "state": r.State, "required": r.Required}
	e := audit.NewDomainEvent(ctx, r.TenantID, kind, "verifier", r.ID)
	e.Detail = detail
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: kind, Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "permission:verifier_manage"}, Subject: events.Subject{Kind: "verifier", ID: r.ID}, SubjectVersion: int64(r.Version), Data: detail})
	return err
}
func verifierVersionView(r model.VerifierVersion) (VerifierVersionView, error) {
	v := VerifierVersionView{ID: r.ID, VersionNo: r.VersionNo, TimeoutMS: r.TimeoutMS, ChangeNote: r.ChangeNote, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
	if json.Unmarshal([]byte(r.Spec), &v.Spec) != nil {
		return v, ErrUnavailable
	}
	return v, nil
}
func (m *Module) verifierViewOn(ctx context.Context, tx *gorm.DB, r model.Verifier) (VerifierView, error) {
	v := VerifierView{ID: r.ID, Key: r.Key, Name: r.Name, Layer: r.Layer, State: r.State, Required: r.Required, CurrentVersionNo: r.CurrentVersionNo, Version: r.Version}
	if json.Unmarshal([]byte(r.AppliesTo), &v.AppliesTo) != nil || !validVerifierPolicy(v.AppliesTo) {
		return v, ErrUnavailable
	}
	var version model.VerifierVersion
	if err := tx.WithContext(ctx).Table(m.names.VerifierVersions.String()).Where("tenant_id = ? AND verifier_id = ? AND version_no = ?", r.TenantID, r.ID, r.CurrentVersionNo).First(&version).Error; err != nil {
		return v, err
	}
	var err error
	v.Current, err = verifierVersionView(version)
	if err == nil && !validVerifierSpec(v.Layer, v.Current.Spec) {
		return v, ErrUnavailable
	}
	return v, err
}
func (m *Module) putVerifierOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, expected int, in TenantVerifierDefinition) (VerifierView, error) {
	var out VerifierView
	if err := m.verifierAuthorityOn(ctx, tx, tenant, "verifier_manage"); err != nil {
		return out, err
	}
	if in.State == "" {
		in.State = "ACTIVE"
	}
	if in.TimeoutMS == 0 {
		in.TimeoutMS = 600000
	}
	if in.Layer == "AUTOMATED" {
		if in.Spec.ReportFormat == "" {
			in.Spec.ReportFormat = "NONE"
		}
		if in.Spec.StdoutTailBytes == 0 {
			in.Spec.StdoutTailBytes = 8192
		}
	}
	if !verifierKeyPattern.MatchString(in.Key) || !bounded(in.Name, 200) || !validVerifierPolicy(in.AppliesTo) || !validVerifierSpec(in.Layer, in.Spec) || in.TimeoutMS < 1000 || in.TimeoutMS > 3600000 || !verifierText(in.ChangeNote, 2000, false) || !slices.Contains([]string{"ACTIVE", "ARCHIVED"}, in.State) {
		return out, ErrInvalidArgument
	}
	policy, _ := json.Marshal(in.AppliesTo)
	spec, _ := json.Marshal(in.Spec)
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := model.Verifier{TenantID: tenant, Key: in.Key, Name: in.Name, Layer: in.Layer, State: in.State, AppliesTo: string(policy), Required: in.Required, CurrentVersionNo: 1, Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	newVersion := true
	var err error
	if id > 0 {
		err = tx.WithContext(ctx).Table(m.names.Verifiers.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenant, id).First(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		if r.Version != expected {
			return out, ErrConflict
		}
		if in.Key != r.Key || in.Layer != r.Layer {
			return out, ErrInvalidArgument
		}
		old, err := m.verifierViewOn(ctx, tx, r)
		if err != nil {
			return out, err
		}
		oldSpec, _ := json.Marshal(old.Current.Spec)
		newVersion = string(oldSpec) != string(spec) || old.Current.TimeoutMS != in.TimeoutMS
		if newVersion {
			r.CurrentVersionNo++
		}
		r.Version++
		r.Name = in.Name
		r.State = in.State
		r.AppliesTo = string(policy)
		r.Required = in.Required
		r.UpdatedAt = now
		result := tx.WithContext(ctx).Table(m.names.Verifiers.String()).Where("tenant_id = ? AND id = ? AND version = ?", tenant, id, expected).Updates(map[string]any{"name": r.Name, "state": r.State, "applies_to": r.AppliesTo, "required": r.Required, "current_version_no": r.CurrentVersionNo, "version": r.Version, "updated_at": now})
		if result.Error != nil {
			return out, result.Error
		}
		if result.RowsAffected != 1 {
			return out, ErrConflict
		}
	} else {
		if expected != 0 {
			return out, ErrInvalidArgument
		}
		r.ID, err = m.nextID()
		if err != nil {
			return out, err
		}
		created := tx.WithContext(ctx).Table(m.names.Verifiers.String()).Clauses(clause.OnConflict{DoNothing: true}).Create(&r)
		if created.Error != nil {
			return out, created.Error
		}
		if created.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	if newVersion {
		vid, err := m.nextID()
		if err != nil {
			return out, err
		}
		version := model.VerifierVersion{ID: vid, TenantID: tenant, VerifierID: r.ID, VersionNo: r.CurrentVersionNo, Spec: string(spec), TimeoutMS: in.TimeoutMS, ChangeNote: in.ChangeNote, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now}
		if err = tx.WithContext(ctx).Table(m.names.VerifierVersions.String()).Create(&version).Error; err != nil {
			return out, err
		}
	}
	if err = m.verifierEventOn(ctx, tx, r, "execution.verifier_changed"); err != nil {
		return out, err
	}
	return m.verifierViewOn(ctx, tx, r)
}

// selectVerifiersOn is a trusted transaction-local policy selector. Matching is
// exact and precedes pagination; each result contains the immutable chosen version.
func (m *Module) selectVerifiersOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind, risk string) ([]VerifierView, error) {
	out := []VerifierView{}
	if transaction(tx, tenant, project) != nil || !validVerifierPolicy(VerifierPolicy{WorkItemTypes: []string{kind}, RiskLevels: []string{risk}}) {
		return out, ErrInvalidArgument
	}
	after := guid.ID(0)
	for {
		var rows []model.Verifier
		if err := tx.WithContext(ctx).Table(m.names.Verifiers.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND state = ? AND id > ?", tenant, "ACTIVE", after).Order("id ASC").Limit(100).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			after = r.ID
			if r.State != "ACTIVE" {
				continue
			}
			v, err := m.verifierViewOn(ctx, tx, r)
			if err != nil {
				return nil, err
			}
			if verifierMatches(v.AppliesTo, project, kind, risk) {
				out = append(out, v)
			}
			if len(out) > 200 {
				return nil, ErrCapabilityUnavailable
			}
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

// RegistryVerifierPin identifies an exact immutable registry definition used by
// an already-authorized criterion command. A registry match is not a verdict.
type RegistryVerifierPin struct {
	VerifierID     guid.ID `json:"verifier_id"`
	VersionID      guid.ID `json:"version_id"`
	VersionNo      int     `json:"version_no"`
	Key            string  `json:"key"`
	Required       bool    `json:"required"`
	DefinitionHash string  `json:"definition_hash"`
}

func (m *Module) pinRegistryVerifiersOn(ctx context.Context, tx *gorm.DB, in VerificationRequest, p *VerificationPlan) error {
	if m.verifierRegistry.work == nil {
		return nil
	} // Isolated legacy consumers have no registry integration.
	kind, risk, err := m.verifierRegistry.work(ctx, tx, in.TenantID, in.ProjectID, in.WorkItemID)
	if err != nil {
		return err
	}
	definitions, err := m.selectVerifiersOn(ctx, tx, in.TenantID, in.ProjectID, kind, risk)
	if err != nil {
		return err
	}
	for _, v := range definitions {
		s := v.Current.Spec
		if v.Layer != "AUTOMATED" || (in.Method != "COMMAND" && in.Method != "TEST") || !slices.Equal(in.Argv, s.Argv) || int64(in.TimeoutSec)*1000 != v.Current.TimeoutMS {
			continue
		}
		// Do not claim a definition was run if the existing runner protocol cannot
		// represent its workdir/env/version probe or report collector semantics.
		if s.Workdir != "" || len(s.Env) > 0 || len(s.ToolVersionArgv) > 0 {
			continue
		}
		if in.Method == "COMMAND" && (s.ReportFormat != "NONE" || len(s.ReportGlobs) > 0) {
			continue
		}
		if in.Method == "TEST" && (len(s.ReportGlobs) != 1 || s.ReportGlobs[0] != in.ReportPath || s.ReportFormat != "JUNIT_XML") {
			continue
		}
		encoded, _ := json.Marshal(struct {
			Layer     string
			Spec      VerifierSpec
			TimeoutMS int64
		}{v.Layer, s, v.Current.TimeoutMS})
		p.RegistryVerifiers = append(p.RegistryVerifiers, RegistryVerifierPin{VerifierID: v.ID, VersionID: v.Current.ID, VersionNo: v.Current.VersionNo, Key: v.Key, Required: v.Required, DefinitionHash: hash(encoded)})
	}
	return nil
}
