package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type EvalFileRef struct {
	FileID    guid.ID `json:"file_id"`
	VersionID guid.ID `json:"version_id"`
	SHA256    string  `json:"sha256"`
	BindingID guid.ID `json:"binding_id"`
}
type EvalCaseDraft struct {
	TemplateVersionID guid.ID           `json:"template_version_id"`
	TemplateKey       string            `json:"template_key" minLength:"1" maxLength:"64"`
	ProjectID         guid.ID           `json:"project_id"`
	Title             string            `json:"title" minLength:"1" maxLength:"200"`
	Kind              string            `json:"kind" enum:"JUDGEMENT,TOOL_PROBE"`
	InputRef          []EvalFileRef     `json:"input_ref" minItems:"1" maxItems:"20"`
	Expected          map[string]string `json:"expected"`
}
type EvalCaseView struct {
	ID                guid.ID           `json:"id"`
	TemplateVersionID guid.ID           `json:"template_version_id"`
	TemplateKey       string            `json:"template_key"`
	ProjectID         guid.ID           `json:"project_id"`
	Title             string            `json:"title"`
	Kind              string            `json:"kind"`
	Origin            string            `json:"origin"`
	InputRef          []EvalFileRef     `json:"input_ref"`
	Expected          map[string]string `json:"expected"`
	SourceEscapeID    *guid.ID          `json:"source_escape_id"`
	SourceSnapshot    json.RawMessage   `json:"source_snapshot"`
	Active            bool              `json:"active"`
	Version           int               `json:"version"`
	CreatedBy         guid.ID           `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
}
type EvalCasePatch struct {
	Title    *string           `json:"title,omitempty" maxLength:"200"`
	Expected map[string]string `json:"expected,omitempty"`
	Active   *bool             `json:"active,omitempty"`
}
type EvalTarget struct {
	AgentID             guid.ID `json:"agent_id,omitempty"`
	Role                string  `json:"role,omitempty" enum:",ACCEPTOR"`
	Model               string  `json:"model,omitempty" maxLength:"200"`
	ConfigurationSHA256 string  `json:"configuration_sha256,omitempty" maxLength:"64"`
	VerifierKey         string  `json:"verifier_key" minLength:"1" maxLength:"64"`
	ProviderRef         string  `json:"provider_ref" minLength:"1" maxLength:"120"`
	MachineID           guid.ID `json:"machine_id"`
}
type EvalStart struct {
	TemplateKey       string     `json:"template_key" minLength:"1" maxLength:"64"`
	TemplateVersionID guid.ID    `json:"template_version_id"`
	Target            EvalTarget `json:"target"`
}
type EvalRunSnapshot struct {
	TemplateVersionID guid.ID            `json:"template_version_id"`
	DefinitionSHA256  string             `json:"definition_sha256"`
	Target            EvalTarget         `json:"target"`
	Verifier          VerifierDefinition `json:"verifier"`
	Cases             []EvalCaseView     `json:"cases"`
}
type EvalRunView struct {
	FalsePositives    int                 `json:"false_positives"`
	FalseNegatives    int                 `json:"false_negatives"`
	ID                guid.ID             `json:"id"`
	TemplateVersionID guid.ID             `json:"template_version_id"`
	TemplateKey       string              `json:"template_key"`
	State             string              `json:"state" enum:"RUNNING,COMPLETED,UNAVAILABLE,FAILED"`
	Snapshot          EvalRunSnapshot     `json:"snapshot"`
	SHA256            string              `json:"sha256"`
	Results           []CalibrationResult `json:"results"`
	CasesTotal        int                 `json:"cases_total"`
	Passed            int                 `json:"passed"`
	Failed            int                 `json:"failed"`
	Inconclusive      int                 `json:"inconclusive"`
	AgreementPPM      *int                `json:"agreement_ppm"`
	Limitations       string              `json:"limitations"`
	CreatedBy         guid.ID             `json:"created_by"`
	StartedAt         time.Time           `json:"started_at"`
	FinishedAt        *time.Time          `json:"finished_at"`
}

// EvalSetWriter is called only by an authoritative source adapter, never from
// an HTTP claim or a second quality.escaped subscriber.
type EvalSetWriter struct {
	ProposeCase func(context.Context, *gorm.DB, governance.EscapeEvalFact) (guid.ID, bool, error)
}

func evalDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func (m *Module) provideEval(s *module.Services) error {
	return module.Provide[EvalSetWriter](s, EvalSetWriter{m.proposeEscapeEvalOn})
}
func (m *Module) injectEval(s *module.Services) error {
	var err error
	if m.evalFiles, err = module.Resolve[files.PinnedResources](s); err != nil {
		return err
	}
	if m.evalBindings, err = module.Resolve[*files.ResourceBindings](s); err != nil {
		return err
	}
	connector, err := module.Resolve[governance.EscapeEvalConnector](s)
	if err != nil {
		return err
	}
	return connector.Bind(m.proposeEscapeEvalOn)
}
func (m *Module) initializeEval() error {
	if m.evalBindings == nil || m.evalFiles == nil {
		return ErrUnavailable
	}
	return m.evalBindings.RegisterPurpose(files.ResourcePurpose{Name: "template_eval", ResourceKind: "template_eval_case", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
		row, err := m.evalCaseOn(ctx, tx, id, "template_read")
		if err != nil {
			return err
		}
		if row.ProjectID != owner.ID {
			return ErrForbidden
		}
		return nil
	}})
}
func evalCaseView(row *model.EvalCase) (EvalCaseView, error) {
	out := EvalCaseView{ID: row.ID, TemplateVersionID: row.TemplateVersionID, TemplateKey: row.TemplateKey, ProjectID: row.ProjectID, Title: row.Title, Kind: row.Kind, Origin: row.Origin, SourceEscapeID: row.SourceEscapeID, SourceSnapshot: json.RawMessage(row.SourceSnapshot), Active: row.Active, Version: row.Version, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
	if json.Unmarshal([]byte(row.InputRef), &out.InputRef) != nil || json.Unmarshal([]byte(row.Expected), &out.Expected) != nil || !json.Valid(out.SourceSnapshot) {
		return out, ErrConflict
	}
	return out, nil
}
func evalRunView(row *model.EvalRun) (EvalRunView, error) {
	out := EvalRunView{ID: row.ID, TemplateVersionID: row.TemplateVersionID, TemplateKey: row.TemplateKey, State: row.State, SHA256: row.SHA256, CasesTotal: row.CasesTotal, Passed: row.Passed, Failed: row.Failed, Inconclusive: row.Inconclusive, AgreementPPM: row.AgreementPPM, Limitations: row.Limitations, CreatedBy: row.CreatedBy, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt}
	digest := sha256.Sum256([]byte(row.Snapshot))
	if hex.EncodeToString(digest[:]) != row.SHA256 || json.Unmarshal([]byte(row.Snapshot), &out.Snapshot) != nil || json.Unmarshal([]byte(row.Results), &out.Results) != nil {
		return out, ErrConflict
	}
	if out.Snapshot.Target.Role != "" {
		labels := map[guid.ID]string{}
		for _, c := range out.Snapshot.Cases {
			labels[c.ID] = c.Expected[out.Snapshot.Target.VerifierKey]
		}
		for _, r := range out.Results {
			if labels[r.SampleID] == "PASS" && r.Verdict == "FAIL" {
				out.FalsePositives++
			}
			if labels[r.SampleID] == "FAIL" && r.Verdict == "PASS" {
				out.FalseNegatives++
			}
		}
	}
	return out, nil
}
func evalTitle(title string) bool {
	return strings.TrimSpace(title) != "" && utf8.RuneCountInString(title) <= 200
}
func evalExpected(version *model.Version, expected map[string]string) error {
	if len(expected) < 1 || len(expected) > 100 {
		return ErrInvalid
	}
	for key, value := range expected {
		if !slices.Contains([]string{"PASS", "WARN", "FAIL"}, value) {
			return ErrInvalid
		}
		if _, err := calibrationVerifier(version, key); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) evalProjectOn(ctx context.Context, tx *gorm.DB, project guid.ID, write bool) error {
	if project <= 0 || m.scope.AuthorizeOn == nil {
		return ErrUnavailable
	}
	permission := "project_read"
	if write {
		permission = "project_manage"
	}
	return m.scope.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), project, permission)
}
func (m *Module) evalCaseOn(ctx context.Context, tx *gorm.DB, id guid.ID, permission string) (*model.EvalCase, error) {
	if err := m.authorizeOn(ctx, tx, permission, 0); err != nil {
		return nil, err
	}
	var row model.EvalCase
	err := tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND id=?", authn.TenantIDFromContext(ctx), id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = m.versionOn(ctx, tx, row.TemplateVersionID, permission); err != nil {
		return nil, err
	}
	if err = m.evalProjectOn(ctx, tx, row.ProjectID, permission == "template_manage"); err != nil {
		return nil, err
	}
	err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", row.TenantID, id).Take(&row).Error
	return &row, err
}
func (m *Module) createEvalCaseOn(ctx context.Context, tx *gorm.DB, in EvalCaseDraft) (EvalCaseView, error) {
	if !evalTitle(in.Title) || !slices.Contains([]string{"JUDGEMENT", "TOOL_PROBE"}, in.Kind) || len(in.InputRef) < 1 || len(in.InputRef) > 20 {
		return EvalCaseView{}, ErrInvalid
	}
	version, err := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage")
	if err != nil {
		return EvalCaseView{}, err
	}
	if version.Key != in.TemplateKey || version.State != "PUBLISHED" {
		return EvalCaseView{}, ErrConflict
	}
	if err = evalExpected(version, in.Expected); err != nil {
		return EvalCaseView{}, err
	}
	if err = m.evalProjectOn(ctx, tx, in.ProjectID, true); err != nil {
		return EvalCaseView{}, err
	}
	if m.ids == nil || m.evalFiles == nil {
		return EvalCaseView{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return EvalCaseView{}, err
	}
	expected, _ := json.Marshal(in.Expected)
	row := &model.EvalCase{TenantID: version.TenantID, ID: guid.ID(id), TemplateVersionID: version.ID, TemplateKey: version.Key, ProjectID: in.ProjectID, Origin: "MANUAL", Kind: in.Kind, Title: in.Title, InputRef: "[]", Expected: string(expected), SourceSnapshot: "{}", Active: true, Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Create(row).Error; err != nil {
		return EvalCaseView{}, err
	}
	refs := []EvalFileRef{}
	seen := map[guid.ID]bool{}
	for _, ref := range in.InputRef {
		if ref.FileID <= 0 || ref.VersionID <= 0 || seen[ref.FileID] {
			return EvalCaseView{}, ErrInvalid
		}
		seen[ref.FileID] = true
		candidate, e := m.evalFiles.InspectOn(ctx, tx, ref.FileID)
		if e != nil {
			return EvalCaseView{}, evalFileError(e)
		}
		if candidate.VersionID != ref.VersionID || candidate.SHA256 != ref.SHA256 {
			return EvalCaseView{}, ErrConflict
		}
		binding, e := m.ids.Next()
		if e != nil {
			return EvalCaseView{}, e
		}
		pin, e := m.evalFiles.PinOn(ctx, tx, files.ResourceBindingInput{ID: guid.ID(binding), SpaceID: candidate.SpaceID, FileID: ref.FileID, VersionID: &ref.VersionID, ExpectedFileVersion: candidate.NodeVersion, Purpose: "template_eval", ResourceKind: "template_eval_case", ResourceID: row.ID, Slot: ref.FileID.String(), ActorID: row.CreatedBy})
		if e != nil {
			return EvalCaseView{}, evalFileError(e)
		}
		refs = append(refs, EvalFileRef{FileID: pin.FileID, VersionID: pin.VersionID, SHA256: pin.SHA256, BindingID: pin.BindingID})
	}
	raw, _ := json.Marshal(refs)
	row.InputRef = string(raw)
	if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND id=?", row.TenantID, row.ID).Update("input_ref", row.InputRef).Error; err != nil {
		return EvalCaseView{}, err
	}
	if err = m.evalEventOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, "template.eval_case_changed", false, map[string]any{"version": 1, "origin": "MANUAL"}); err != nil {
		return EvalCaseView{}, err
	}
	return evalCaseView(row)
}

func (m *Module) patchEvalCaseOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, in EvalCasePatch) (EvalCaseView, error) {
	row, err := m.evalCaseOn(ctx, tx, id, "template_manage")
	if err != nil {
		return EvalCaseView{}, err
	}
	if row.Version != version {
		return EvalCaseView{}, ErrConflict
	}
	if in.Title == nil && in.Expected == nil && in.Active == nil {
		return EvalCaseView{}, ErrInvalid
	}
	if in.Title != nil {
		if !evalTitle(*in.Title) {
			return EvalCaseView{}, ErrInvalid
		}
		row.Title = *in.Title
	}
	if in.Expected != nil {
		v, e := m.versionOn(ctx, tx, row.TemplateVersionID, "template_manage")
		if e != nil {
			return EvalCaseView{}, e
		}
		if e = evalExpected(v, in.Expected); e != nil {
			return EvalCaseView{}, e
		}
		raw, _ := json.Marshal(in.Expected)
		row.Expected = string(raw)
	}
	if in.Active != nil {
		row.Active = *in.Active
	}
	result := tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND id=? AND version=?", row.TenantID, id, version).Updates(map[string]any{"title": row.Title, "expected": row.Expected, "active": row.Active, "version": version + 1})
	if result.Error != nil {
		return EvalCaseView{}, result.Error
	}
	if result.RowsAffected != 1 {
		return EvalCaseView{}, ErrConflict
	}
	row.Version++
	if err = m.evalEventOn(ctx, tx, row.TenantID, row.ProjectID, id, "template.eval_case_changed", false, map[string]any{"version": row.Version, "active": row.Active}); err != nil {
		return EvalCaseView{}, err
	}
	return evalCaseView(row)
}
func (m *Module) evalEventOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, name string, system bool, data map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	kind, authority, actor := "HUMAN", "IAM:template_manage", authn.PrincipalIDFromContext(ctx)
	if system {
		kind, authority, actor = "SYSTEM", "SYSTEM:templates.escape_eval", 0
	}
	a := audit.NewDomainEvent(ctx, tenant, name, "template_eval", id)
	a.PrincipalID = actor
	a.Detail["project_id"] = project
	a.Detail["actor_kind"] = kind
	a.Detail["authority_source"] = authority
	for k, v := range data {
		a.Detail[k] = v
	}
	if err := m.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: name, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "template_eval", ID: id}, SubjectVersion: 1, Data: data})
	return err
}

func (m *Module) proposeEscapeEvalOn(ctx context.Context, tx *gorm.DB, fact governance.EscapeEvalFact) (guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || fact.TenantID <= 0 || fact.ProjectID <= 0 || fact.ID <= 0 || fact.WorkItemID <= 0 || !evalTitle(fact.Title) || !json.Valid(fact.Snapshot) {
		return 0, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalid
	}
	// This capability is bound directly into the existing authoritative owner.
	// The owner supplies a frozen Human rejection; no verifier label is inferred.
	q := query.BindBindings(tx, m.names)
	binding, err := q.WithContext(ctx).Where(q.TenantID.Eq(fact.TenantID), q.ProjectID.Eq(fact.ProjectID)).Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	v := query.BindVersions(tx, m.names)
	version, err := v.WithContext(ctx).Where(v.TenantID.Eq(fact.TenantID), v.ID.Eq(binding.TemplateVersionID)).First()
	if err != nil {
		return 0, false, err
	}
	if _, err = v.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(v.TenantID.Eq(fact.TenantID), v.Key.Eq(version.Key)).Order(v.ID).First(); err != nil {
		return 0, false, err
	}
	var old model.EvalCase
	err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND template_key=? AND source_escape_id=?", fact.TenantID, version.Key, fact.ID).Take(&old).Error
	if err == nil {
		return old.ID, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, err
	}
	if m.ids == nil {
		return 0, false, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, false, err
	}
	row := &model.EvalCase{TenantID: fact.TenantID, ID: guid.ID(id), TemplateVersionID: version.ID, TemplateKey: version.Key, ProjectID: fact.ProjectID, Origin: "ESCAPE", Kind: "JUDGEMENT", Title: fact.Title, InputRef: "[]", Expected: "{}", SourceEscapeID: &fact.ID, SourceSnapshot: string(fact.Snapshot), Active: true, Version: 1, CreatedAt: time.Now().UTC()}
	if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Create(row).Error; err != nil {
		return 0, false, err
	}
	// Retain ESCAPE cases; only oldest SEED cases are eligible for retirement.
	var count int64
	if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND template_key=? AND active=? AND kind=?", row.TenantID, row.TemplateKey, true, "JUDGEMENT").Count(&count).Error; err != nil {
		return 0, false, err
	}
	if count > 50 {
		var seeds []model.EvalCase
		if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND template_key=? AND active=? AND origin=?", row.TenantID, row.TemplateKey, true, "SEED").Order("created_at,id").Limit(int(count - 50)).Find(&seeds).Error; err != nil {
			return 0, false, err
		}
		for _, seed := range seeds {
			if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND id=?", seed.TenantID, seed.ID).Updates(map[string]any{"active": false, "version": seed.Version + 1}).Error; err != nil {
				return 0, false, err
			}
		}
	}
	err = m.evalEventOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, "template.eval_case_changed", true, map[string]any{"source_escape_id": fact.ID, "origin": "ESCAPE", "label_status": "Human rejection retained; verifier labels unavailable"})
	if err == nil {
		err = m.enqueueEscapeEvaluationsOn(ctx, tx, row)
	}
	return row.ID, true, err
}

func (m *Module) startEvalOn(ctx context.Context, tx *gorm.DB, in EvalStart) (EvalRunView, error) {
	if strings.TrimSpace(in.Target.ProviderRef) == "" || len(in.Target.ProviderRef) > 120 || in.Target.MachineID < 0 {
		return EvalRunView{}, ErrInvalid
	}
	if err := m.authorizeOn(ctx, tx, "template_manage", 0); err != nil {
		return EvalRunView{}, err
	}
	// Serialize a target across all versions in this template family. Locking
	// only the selected version would admit simultaneous runs after an upgrade.
	q := query.BindVersions(tx, m.names)
	if _, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.Key.Eq(in.TemplateKey)).Order(q.ID).First(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return EvalRunView{}, ErrNotFound
		}
		return EvalRunView{}, err
	}
	version, err := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage")
	if err != nil {
		return EvalRunView{}, err
	}
	if version.Key != in.TemplateKey || version.State != "PUBLISHED" {
		return EvalRunView{}, ErrConflict
	}
	verifier, err := calibrationVerifier(version, in.Target.VerifierKey)
	if err != nil {
		return EvalRunView{}, err
	}
	if err = m.pinEvalTargetOn(ctx, tx, version.TenantID, &in.Target, verifier); err != nil {
		return EvalRunView{}, err
	}
	targetRaw, _ := json.Marshal(in.Target)
	targetDigest := sha256.Sum256(targetRaw)
	targetKey := hex.EncodeToString(targetDigest[:])
	var running int64
	if err = tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND template_key=? AND target_key=? AND state=?", version.TenantID, version.Key, targetKey, "RUNNING").Count(&running).Error; err != nil {
		return EvalRunView{}, err
	}
	if running > 0 {
		return EvalRunView{}, ErrConflict
	}
	var cases []model.EvalCase
	if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND template_key=? AND active=?", version.TenantID, version.Key, true).Order("id").Limit(1001).Find(&cases).Error; err != nil {
		return EvalRunView{}, err
	}
	if len(cases) > 1000 {
		return EvalRunView{}, ErrUnavailable
	}
	snapshot := EvalRunSnapshot{TemplateVersionID: version.ID, DefinitionSHA256: version.SHA256, Target: in.Target, Verifier: verifier, Cases: []EvalCaseView{}}
	for _, row := range cases {
		if err = m.evalProjectOn(ctx, tx, row.ProjectID, false); err != nil {
			return EvalRunView{}, err
		}
		view, e := evalCaseView(&row)
		if e != nil {
			return EvalRunView{}, e
		}
		snapshot.Cases = append(snapshot.Cases, view)
	}
	if len(snapshot.Cases) == 0 {
		return EvalRunView{}, ErrInvalid
	}
	raw, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(raw)
	id, err := m.ids.Next()
	if err != nil {
		return EvalRunView{}, err
	}
	now := time.Now().UTC()
	run := &model.EvalRun{TenantID: version.TenantID, ID: guid.ID(id), TemplateVersionID: version.ID, TemplateKey: version.Key, TargetKey: targetKey, Snapshot: string(raw), SHA256: hex.EncodeToString(digest[:]), State: "RUNNING", Results: "[]", CasesTotal: len(cases), Inconclusive: len(cases), CreatedBy: authn.PrincipalIDFromContext(ctx), StartedAt: now, Limitations: "Awaiting actual evaluator; no golden results inferred"}
	if m.calibrationEvaluator == nil {
		run.State = "UNAVAILABLE"
		run.FinishedAt = &now
		run.Limitations = "Actual evaluator unavailable; agreement unknown"
	}
	if err = tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Create(run).Error; err != nil {
		return EvalRunView{}, err
	}
	if err = m.evalEventOn(ctx, tx, run.TenantID, 0, run.ID, "eval.run_changed", false, map[string]any{"state": run.State, "snapshot_sha256": run.SHA256}); err != nil {
		return EvalRunView{}, err
	}
	return evalRunView(run)
}

func evalFileError(err error) error {
	switch {
	case errors.Is(err, files.ErrPinnedForbidden):
		return ErrForbidden
	case errors.Is(err, files.ErrPinnedConflict):
		return ErrConflict
	case errors.Is(err, files.ErrPinnedUnavailable):
		return ErrUnavailable
	case errors.Is(err, files.ErrPinnedInvalid), errors.Is(err, files.ErrNotFound):
		return ErrInvalid
	default:
		return err
	}
}

func (m *Module) evalRunOn(ctx context.Context, tx *gorm.DB, id guid.ID) (EvalRunView, error) {
	if err := m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
		return EvalRunView{}, err
	}
	var row model.EvalRun
	err := tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND id=?", authn.TenantIDFromContext(ctx), id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EvalRunView{}, ErrNotFound
	}
	if err != nil {
		return EvalRunView{}, err
	}
	if _, err = m.versionOn(ctx, tx, row.TemplateVersionID, "template_read"); err != nil {
		return EvalRunView{}, err
	}
	view, err := evalRunView(&row)
	if err != nil {
		return view, err
	}
	for _, c := range view.Snapshot.Cases {
		if err = m.evalProjectOn(ctx, tx, c.ProjectID, false); err != nil {
			return EvalRunView{}, err
		}
	}
	return view, nil
}
