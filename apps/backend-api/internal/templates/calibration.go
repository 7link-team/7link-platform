package templates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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

var (
	ErrCalibrationSamples = errors.New("templates: insufficient labelled calibration samples") //nolint:misspell // Preserve the published domain or serialized contract spelling.
	ErrCalibrationPending = errors.New("templates: calibration evaluation not completed")
	ErrCalibrationTarget  = errors.New("templates: calibration agreement below target")
)

type CalibrationSampleDraft struct {
	TemplateVersionID guid.ID         `json:"template_version_id"`
	VerifierKey       string          `json:"verifier_key" minLength:"1" maxLength:"64"`
	Input             json.RawMessage `json:"input"`
	Expected          string          `json:"expected" enum:"PASS,WARN,FAIL"`
}
type CalibrationSampleView struct {
	ID                guid.ID         `json:"id"`
	TemplateVersionID guid.ID         `json:"template_version_id"`
	VerifierKey       string          `json:"verifier_key"`
	Input             json.RawMessage `json:"input"`
	Expected          string          `json:"expected"`
	Origin            string          `json:"origin"`
	SHA256            string          `json:"sha256"`
	CreatedBy         guid.ID         `json:"created_by"`
}
type CalibrationStart struct {
	TemplateVersionID guid.ID `json:"template_version_id"`
	VerifierKey       string  `json:"verifier_key" minLength:"1" maxLength:"64"`
	ProviderRef       string  `json:"provider_ref" minLength:"1" maxLength:"120"`
	Method            string  `json:"method" enum:"AGREEMENT,PROBE"`
	MachineID         guid.ID `json:"machine_id"`
}
type CalibrationView struct {
	ID                guid.ID             `json:"id"`
	TemplateVersionID guid.ID             `json:"template_version_id"`
	TemplateKey       string              `json:"template_key"`
	VerifierKey       string              `json:"verifier_key"`
	ProviderRef       string              `json:"provider_ref"`
	Method            string              `json:"method"`
	MachineID         guid.ID             `json:"machine_id"`
	State             string              `json:"state" enum:"UNCALIBRATED,CALIBRATING,CALIBRATED,DRIFTED"`
	RunID             guid.ID             `json:"run_id"`
	RunState          string              `json:"run_state" enum:"PENDING,COMPLETED,UNAVAILABLE,FAILED"`
	AgreementPPM      *int                `json:"agreement_ppm,omitempty"`
	TargetPPM         int                 `json:"target_ppm"`
	SampleSize        int                 `json:"sample_size"`
	CalibratedAt      *time.Time          `json:"calibrated_at,omitempty"`
	CalibratedBy      *guid.ID            `json:"calibrated_by,omitempty"`
	DriftCheckedAt    *time.Time          `json:"drift_checked_at,omitempty"`
	NextReviewAt      time.Time           `json:"next_review_at"`
	Version           int                 `json:"version"`
	Limitations       string              `json:"limitations"`
	Results           []CalibrationResult `json:"results"`
}

// Evaluators receive frozen input bytes, never the Human answer key. The
// source-owned adapter must return actual verdicts with immutable evidence.
type CalibrationInput struct {
	SampleID guid.ID         `json:"sample_id"`
	Input    json.RawMessage `json:"input"`
	SHA256   string          `json:"sha256"`
}
type CalibrationEvaluationRequest struct {
	EvalCases         []EvalCaseView
	Target            EvalTarget
	InitiatorID       guid.ID
	TenantID          guid.ID
	RunID             guid.ID
	TemplateVersionID guid.ID
	DefinitionSHA256  string
	Verifier          VerifierDefinition
	ProviderRef       string
	MachineID         guid.ID
	Samples           []CalibrationInput
}
type CalibrationResult struct {
	SampleID     guid.ID  `json:"sample_id"`
	Verdict      string   `json:"verdict" enum:"PASS,WARN,FAIL,INCONCLUSIVE"`
	EvidenceRefs []string `json:"evidence_refs"`
}
type CalibrationEvaluation struct {
	ProviderRef string
	MachineID   guid.ID
	Results     []CalibrationResult
}
type CalibrationEvaluator func(context.Context, CalibrationEvaluationRequest) (CalibrationEvaluation, error)
type CalibrationEvaluatorConnector struct {
	Bind func(CalibrationEvaluator) error
}

func (m *Module) provideCalibration(s *module.Services) error {
	return module.Provide[CalibrationEvaluatorConnector](s, CalibrationEvaluatorConnector{Bind: func(e CalibrationEvaluator) error {
		if e == nil || m.calibrationEvaluator != nil || m.calibrationSealed {
			return ErrInvalid
		}
		m.calibrationEvaluator = e
		return nil
	}})
}

type calibrationSnapshot struct {
	InitiatorID      guid.ID                 `json:"initiator_id,omitempty"`
	DefinitionSHA256 string                  `json:"definition_sha256"`
	Verifier         VerifierDefinition      `json:"verifier"`
	Samples          []CalibrationSampleView `json:"samples"`
}

func calibrationSampleView(row *model.CalibrationSample) CalibrationSampleView {
	return CalibrationSampleView{row.ID, row.TemplateVersionID, row.VerifierKey, json.RawMessage(row.Input), row.Expected, row.Origin, row.SHA256, row.CreatedBy}
}
func calibrationVerifier(row *model.Version, key string) (VerifierDefinition, error) {
	view, err := versionView(row)
	if err != nil {
		return VerifierDefinition{}, err
	}
	defs, err := verifierDefinitions(view.Definition)
	if err != nil {
		return VerifierDefinition{}, err
	}
	for _, def := range defs {
		if def.Key == key {
			return def, nil
		}
	}
	return VerifierDefinition{}, ErrInvalid
}
func calibrationParameter(verifier VerifierDefinition, key string, fallback, maximum int) (int, error) {
	value, exists := verifier.Params[key]
	if !exists {
		return fallback, nil
	}
	n, err := strconv.Atoi(fmt.Sprint(value))
	if err != nil || n < 1 || n > maximum {
		return 0, ErrInvalid
	}
	return n, nil
}
func (m *Module) addCalibrationSampleOn(ctx context.Context, tx *gorm.DB, in CalibrationSampleDraft, origin string) (CalibrationSampleView, error) {
	if len(in.Input) == 0 || len(in.Input) > 32768 || !json.Valid(in.Input) || !slices.Contains([]string{"PASS", "WARN", "FAIL"}, in.Expected) || !slices.Contains([]string{"MANUAL", "ESCAPE"}, origin) {
		return CalibrationSampleView{}, ErrInvalid
	}
	version, err := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage")
	if err != nil {
		return CalibrationSampleView{}, err
	}
	if version.State != "PUBLISHED" {
		return CalibrationSampleView{}, ErrConflict
	}
	if _, err = calibrationVerifier(version, in.VerifierKey); err != nil {
		return CalibrationSampleView{}, err
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return CalibrationSampleView{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return CalibrationSampleView{}, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(in.Input))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return CalibrationSampleView{}, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return CalibrationSampleView{}, err
	}
	// The input digest prevents duplicated or conflicting labels from inflating
	// the independent sample count; the run snapshot also freezes the label.
	digest := sha256.Sum256(canonical)
	q := query.BindCalibrationSamples(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(version.TenantID), q.TemplateVersionID.Eq(version.ID), q.VerifierKey.Eq(in.VerifierKey), q.SHA256.Eq(hex.EncodeToString(digest[:]))).Count()
	if err != nil {
		return CalibrationSampleView{}, err
	}
	if count != 0 {
		return CalibrationSampleView{}, ErrConflict
	}
	row := &model.CalibrationSample{TenantID: version.TenantID, ID: guid.ID(id), TemplateVersionID: version.ID, VerifierKey: in.VerifierKey, Origin: origin, Input: string(canonical), Expected: in.Expected, SHA256: hex.EncodeToString(digest[:]), CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	if err = query.BindCalibrationSamples(tx, m.names).WithContext(ctx).Create(row); err != nil {
		return CalibrationSampleView{}, err
	}
	err = m.calibrationEventOn(ctx, tx, row.TenantID, row.ID, "template.calibration_sample_added", "HUMAN", row.CreatedBy, map[string]any{"template_version_id": row.TemplateVersionID, "verifier_key": row.VerifierKey, "sha256": row.SHA256})
	return calibrationSampleView(row), err
}
func (m *Module) calibrationOn(ctx context.Context, tx *gorm.DB, id guid.ID, permission string) (*model.Calibration, error) {
	if err := m.authorizeOn(ctx, tx, permission, 0); err != nil {
		return nil, err
	}
	q := query.BindCalibrations(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = m.versionOn(ctx, tx, row.TemplateVersionID, permission); err != nil {
		return nil, err
	}
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).First()
}
func (m *Module) calibrationViewOn(ctx context.Context, tx *gorm.DB, row *model.Calibration) (CalibrationView, error) {
	q := query.BindCalibrationRuns(tx, m.names)
	run, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.CurrentRunID), q.CalibrationID.Eq(row.ID)).First()
	if err != nil {
		return CalibrationView{}, err
	}
	out := CalibrationView{ID: row.ID, TemplateVersionID: row.TemplateVersionID, TemplateKey: row.TemplateKey, VerifierKey: row.VerifierKey, ProviderRef: row.ProviderRef, Method: row.Method, MachineID: row.MachineID, State: row.State, RunID: run.ID, RunState: run.State, AgreementPPM: run.AgreementPPM, TargetPPM: row.TargetPPM, SampleSize: run.SampleSize, CalibratedAt: row.CalibratedAt, CalibratedBy: row.CalibratedBy, DriftCheckedAt: row.DriftCheckedAt, NextReviewAt: row.NextReviewAt, Version: row.Version, Limitations: run.Limitations, Results: []CalibrationResult{}}
	if err = json.Unmarshal([]byte(run.Results), &out.Results); err != nil {
		return CalibrationView{}, err
	}
	return out, nil
}
func (m *Module) startCalibrationOn(ctx context.Context, tx *gorm.DB, in CalibrationStart) (CalibrationView, error) {
	if strings.TrimSpace(in.ProviderRef) != in.ProviderRef || in.ProviderRef == "" || len(in.ProviderRef) > 120 || !slices.Contains([]string{"AGREEMENT", "PROBE"}, in.Method) || in.Method == "AGREEMENT" && in.MachineID != 0 || in.Method == "PROBE" && in.MachineID <= 0 {
		return CalibrationView{}, ErrInvalid
	}
	version, err := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage")
	if err != nil {
		return CalibrationView{}, err
	}
	if version.State != "PUBLISHED" {
		return CalibrationView{}, ErrConflict
	}
	verifier, err := calibrationVerifier(version, in.VerifierKey)
	if err != nil {
		return CalibrationView{}, err
	}
	if (in.Method == "AGREEMENT") != (verifier.Kind == "MODEL_SCORED") {
		return CalibrationView{}, ErrInvalid
	}
	if verifier.PluginKey == "http-v1" {
		_, pin, e := m.verifierProviderOn(ctx, tx, version.TenantID, in.ProviderRef, in.MachineID, verifier)
		if e != nil {
			return CalibrationView{}, e
		}
		in.ProviderRef = pin
	}
	q := query.BindCalibrations(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(version.TenantID), q.TemplateVersionID.Eq(version.ID), q.VerifierKey.Eq(in.VerifierKey), q.ProviderRef.Eq(in.ProviderRef), q.MachineID.Eq(in.MachineID)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return CalibrationView{}, err
	}
	if row != nil {
		view, e := m.calibrationViewOn(ctx, tx, row)
		if e != nil {
			return CalibrationView{}, e
		}
		if view.RunState == "PENDING" {
			return CalibrationView{}, ErrConflict
		}
	} else {
		if m.ids == nil {
			return CalibrationView{}, ErrUnavailable
		}
		id, e := m.ids.Next()
		if e != nil {
			return CalibrationView{}, e
		}
		target := 850000
		if in.Method == "PROBE" {
			target = 1000000
		} else {
			target, e = calibrationParameter(verifier, "calibration_target_ppm", target, 1000000)
			if e != nil {
				return CalibrationView{}, e
			}
		}
		row = &model.Calibration{TenantID: version.TenantID, ID: guid.ID(id), TemplateVersionID: version.ID, TemplateKey: version.Key, VerifierKey: in.VerifierKey, ProviderRef: in.ProviderRef, Method: in.Method, MachineID: in.MachineID, State: "UNCALIBRATED", TargetPPM: target, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC(), NextReviewAt: time.Now().UTC(), Version: 1}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return CalibrationView{}, err
		}
	}
	if err = m.newCalibrationRunOn(ctx, tx, row, version, verifier, false); err != nil {
		return CalibrationView{}, err
	}
	return m.calibrationViewOn(ctx, tx, row)
}
func (m *Module) newCalibrationRunOn(ctx context.Context, tx *gorm.DB, row *model.Calibration, version *model.Version, verifier VerifierDefinition, recheck bool) error {
	interval, err := calibrationParameter(verifier, "calibration_interval_days", 30, 3650)
	if err != nil {
		return err
	}
	q := query.BindCalibrationSamples(tx, m.names)
	samples, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.TemplateVersionID.Eq(row.TemplateVersionID), q.VerifierKey.Eq(row.VerifierKey)).Order(q.ID).Limit(1001).Find()
	if err != nil {
		return err
	}
	if len(samples) > 1000 {
		return ErrUnavailable
	}
	pass, fail := false, false
	escape := 0
	snapshot := calibrationSnapshot{InitiatorID: authn.PrincipalIDFromContext(ctx), DefinitionSHA256: version.SHA256, Verifier: verifier, Samples: []CalibrationSampleView{}}
	for _, s := range samples {
		pass = pass || s.Expected == "PASS"
		fail = fail || s.Expected == "FAIL"
		if s.Origin == "ESCAPE" {
			escape++
		}
		snapshot.Samples = append(snapshot.Samples, calibrationSampleView(s))
	}
	if row.Method == "AGREEMENT" && len(samples) < 20 || row.Method == "PROBE" && (!pass || !fail) {
		return ErrCalibrationSamples
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run := &model.CalibrationRun{TenantID: row.TenantID, ID: guid.ID(id), CalibrationID: row.ID, State: "PENDING", Snapshot: string(data), SHA256: hex.EncodeToString(digest[:]), Results: "[]", SampleSize: len(samples), EscapeCount: escape, Recheck: recheck, CreatedAt: now, Limitations: "Awaiting actual source evaluator; no accepted calibration"}
	if m.calibrationEvaluator == nil {
		run.State = "UNAVAILABLE"
		run.Limitations = "Actual calibration evaluator is unavailable; agreement is unknown"
		run.CompletedAt = &now
	}
	if err = query.BindCalibrationRuns(tx, m.names).WithContext(ctx).Create(run); err != nil {
		return err
	}
	row.CurrentRunID = run.ID
	row.Version++
	row.NextReviewAt = now.Add(time.Duration(interval) * 24 * time.Hour)
	if !recheck {
		row.State = "CALIBRATING"
		row.CalibratedAt = nil
		row.CalibratedBy = nil
		if run.State == "UNAVAILABLE" {
			row.State = "UNCALIBRATED"
		}
	}
	if recheck && run.State == "UNAVAILABLE" && row.State == "CALIBRATED" {
		row.State = "DRIFTED"
		row.CalibratedAt, row.CalibratedBy = nil, nil
	}
	if err = m.saveCalibrationOn(ctx, tx, row); err != nil {
		return err
	}
	return m.calibrationEventOn(ctx, tx, row.TenantID, row.ID, "template.calibration_started", calibrationActor(recheck), authn.PrincipalIDFromContext(ctx), map[string]any{"run_id": run.ID, "run_state": run.State, "sample_size": run.SampleSize, "snapshot_sha256": run.SHA256})
}
func calibrationActor(system bool) string {
	if system {
		return "SYSTEM"
	}
	return "HUMAN"
}
func (m *Module) saveCalibrationOn(ctx context.Context, tx *gorm.DB, row *model.Calibration) error {
	result := tx.WithContext(ctx).Table(m.names.Calibrations.String()).Where("tenant_id=? AND id=? AND version=?", row.TenantID, row.ID, row.Version-1).Updates(map[string]any{"current_run_id": row.CurrentRunID, "state": row.State, "calibrated_at": row.CalibratedAt, "calibrated_by": row.CalibratedBy, "drift_checked_at": row.DriftCheckedAt, "next_review_at": row.NextReviewAt, "version": row.Version})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func (m *Module) confirmCalibrationOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, accept bool, note string) (CalibrationView, error) {
	if len(note) > 2000 {
		return CalibrationView{}, ErrInvalid
	}
	row, err := m.calibrationOn(ctx, tx, id, "template_manage")
	if err != nil {
		return CalibrationView{}, err
	}
	if row.Version != version {
		return CalibrationView{}, ErrConflict
	}
	view, err := m.calibrationViewOn(ctx, tx, row)
	if err != nil {
		return CalibrationView{}, err
	}
	if view.RunState != "COMPLETED" || view.AgreementPPM == nil {
		return CalibrationView{}, ErrCalibrationPending
	}
	if row.Method != "AGREEMENT" {
		return CalibrationView{}, ErrInvalid
	}
	if accept && *view.AgreementPPM < row.TargetPPM {
		return CalibrationView{}, ErrCalibrationTarget
	}
	row.State = "UNCALIBRATED"
	row.CalibratedAt = nil
	row.CalibratedBy = nil
	if accept {
		now := time.Now().UTC()
		actor := authn.PrincipalIDFromContext(ctx)
		row.State = "CALIBRATED"
		row.CalibratedAt = &now
		row.CalibratedBy = &actor
	}
	row.Version++
	if err = m.saveCalibrationOn(ctx, tx, row); err != nil {
		return CalibrationView{}, err
	}
	if err = m.calibrationEventOn(ctx, tx, row.TenantID, row.ID, "template.calibration_confirmed", "HUMAN", authn.PrincipalIDFromContext(ctx), map[string]any{"run_id": row.CurrentRunID, "accept": accept, "note": note, "state": row.State}); err != nil {
		return CalibrationView{}, err
	}
	return m.calibrationViewOn(ctx, tx, row)
}
func (m *Module) calibrationEventOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, name, kind string, actor guid.ID, data map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	authority := "IAM:template_manage"
	if kind == "SYSTEM" {
		authority = "SYSTEM:templates.calibration"
		actor = 0
	}
	a := audit.NewDomainEvent(ctx, tenant, name, "verifier_calibration", id)
	a.Detail["authority_source"] = authority
	a.Detail["actor_kind"] = kind
	for k, v := range data {
		a.Detail[k] = v
	}
	if err := m.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: name, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "verifier_calibration", ID: id}, SubjectVersion: 1, Data: data})
	return err
}
