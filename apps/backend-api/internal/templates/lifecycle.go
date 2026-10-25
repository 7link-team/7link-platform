package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
)

// ObservationSource fences trusted observations against an immutable template
// version and the previous receipt. SourceKey identifies the originating result,
// not a user-supplied assertion. Recorder capabilities are deliberately not HTTP
// inputs: adapters must obtain results from their own authoritative source.
type ObservationSource struct {
	TemplateVersionID guid.ID   `json:"template_version_id"`
	ProjectID         guid.ID   `json:"project_id"`
	SourceKey         string    `json:"source_key"`
	Reference         string    `json:"reference"`
	PriorID           guid.ID   `json:"prior_id"`
	EvidenceIDs       []guid.ID `json:"evidence_ids"`
}

type CalibrationObservation struct {
	Source         ObservationSource `json:"source"`
	Verifier       string            `json:"verifier"`
	ProviderRef    string            `json:"provider_ref"`
	Method         string            `json:"method"`
	MachineID      guid.ID           `json:"machine_id"`
	State          string            `json:"state"`
	AgreementPPM   int               `json:"agreement_ppm"`
	TargetPPM      int               `json:"target_ppm"`
	Samples        int               `json:"samples"`
	FailedProbes   int               `json:"failed_probes"`
	QualityOwnerID guid.ID           `json:"quality_owner_id"`
}
type ProviderObservation struct {
	Source             ObservationSource `json:"source"`
	State              string            `json:"state"`
	AffectedProjectIDs []guid.ID         `json:"affected_project_ids"`
	// BlockedWorkIDs are the actual distinct work objects observed blocked by
	// this provider, retained as immutable evidence rather than a display count.
	BlockedWorkIDs []guid.ID `json:"blocked_work_ids"`
}
type EvalObservation struct {
	Source       ObservationSource `json:"source"`
	State        string            `json:"state"`
	Total        int               `json:"total"`
	Passed       int               `json:"passed"`
	Failed       int               `json:"failed"`
	AgreementPPM int               `json:"agreement_ppm"`
	TargetPPM    int               `json:"target_ppm"`
}
type RubricDimensionObservation struct {
	Key           string `json:"key"`
	InternalMilli int    `json:"internal_milli"`
	ExternalMilli int    `json:"external_milli"`
}
type RubricObservation struct {
	Source          ObservationSource            `json:"source"`
	InternalScoreID guid.ID                      `json:"internal_score_id"`
	ExternalScoreID guid.ID                      `json:"external_score_id"`
	Dimensions      []RubricDimensionObservation `json:"dimensions"`
}
type ToolObservation struct {
	Source           ObservationSource `json:"source"`
	EligibleMachines int               `json:"eligible_machines"`
}

// LifecycleView contains only source-owned notification facts. Payloads retain
// complete observations privately; notification consumers cannot rewrite them.
type LifecycleView struct {
	ID                 guid.ID           `json:"id"`
	TemplateVersionID  guid.ID           `json:"template_version_id"`
	ProjectID          guid.ID           `json:"project_id"`
	AffectedProjectIDs []guid.ID         `json:"affected_project_ids,omitempty"`
	ObjectKey          string            `json:"object_key"`
	EventType          string            `json:"event_type"`
	State              string            `json:"state"`
	Reference          string            `json:"reference"`
	NoticeType         string            `json:"notice_type"`
	InitiatorID        guid.ID           `json:"initiator_id"`
	QualityOwnerID     guid.ID           `json:"quality_owner_id"`
	Params             map[string]string `json:"params"`
	CreatedAt          time.Time         `json:"created_at"`
}

type LifecycleRecorder struct {
	CalibrationOn func(context.Context, *gorm.DB, CalibrationObservation) (LifecycleView, error)
	ProviderOn    func(context.Context, *gorm.DB, ProviderObservation) (LifecycleView, error)
	EvalOn        func(context.Context, *gorm.DB, EvalObservation) (LifecycleView, error)
	RubricOn      func(context.Context, *gorm.DB, RubricObservation) (LifecycleView, error)
	ToolOn        func(context.Context, *gorm.DB, ToolObservation) (LifecycleView, error)
}
type LifecycleFacts struct {
	CurrentOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (LifecycleView, bool, error)
}

func (m *Module) provideLifecycle(s *module.Services) error {
	if err := module.Provide[LifecycleRecorder](s, LifecycleRecorder{m.recordCalibrationOn, m.recordProviderOn, m.recordEvalOn, m.recordRubricOn, m.recordToolOn}); err != nil {
		return err
	}
	return module.Provide[LifecycleFacts](s, LifecycleFacts{CurrentOn: m.lifecycleCurrentOn})
}

func validSource(s ObservationSource) bool {
	if s.TemplateVersionID <= 0 || s.ProjectID < 0 || s.PriorID < 0 || len(s.SourceKey) == 0 || len(s.SourceKey) > 128 || strings.TrimSpace(s.SourceKey) != s.SourceKey || len(s.Reference) == 0 || len(s.Reference) > 120 || strings.TrimSpace(s.Reference) != s.Reference || len(s.EvidenceIDs) > 100 {
		return false
	}
	for _, id := range s.EvidenceIDs {
		if id <= 0 {
			return false
		}
	}
	return true
}
func ppmValid(agreement, target int) bool {
	return agreement >= -1000000 && agreement <= 1000000 && target >= 0 && target <= 1000000
}
func lifecycleObject(s ObservationSource, kind string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s", s.TemplateVersionID, s.ProjectID, kind, s.Reference)))
	return hex.EncodeToString(h[:])
}

type lifecyclePayload struct {
	View        LifecycleView   `json:"view"`
	Observation json.RawMessage `json:"observation"`
}

func lifecycleView(row *model.LifecycleReceipt) (LifecycleView, error) {
	var p lifecyclePayload
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		return LifecycleView{}, err
	}
	v := p.View
	v.ID, v.TemplateVersionID, v.ProjectID, v.ObjectKey, v.EventType, v.State, v.CreatedAt = row.ID, row.TemplateVersionID, row.ProjectID, row.ObjectKey, row.EventType, row.State, row.CreatedAt
	return v, nil
}
func (m *Module) latestLifecycleOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, key string) (*model.LifecycleReceipt, error) {
	q := query.BindLifecycleReceipts(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ObjectKey.Eq(key)).Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return row, err
}

func (m *Module) lifecycleAuthorityOn(ctx context.Context, tx *gorm.DB, s ObservationSource) (*model.Version, error) {
	version, err := m.versionOn(ctx, tx, s.TemplateVersionID, "template_manage")
	if err != nil {
		return nil, err
	}
	if version.State != "PUBLISHED" {
		return nil, ErrConflict
	}
	if s.ProjectID > 0 {
		if m.scope.AuthorizeOn == nil {
			return nil, ErrUnavailable
		}
		if err = m.scope.AuthorizeOn(ctx, tx, version.TenantID, s.ProjectID, "project_manage"); err != nil {
			return nil, err
		}
		b := query.BindBindings(tx, m.names)
		binding, e := b.WithContext(ctx).Where(b.TenantID.Eq(version.TenantID), b.ProjectID.Eq(s.ProjectID)).Order(b.ID.Desc()).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if binding.TemplateVersionID != version.ID {
			return nil, ErrConflict
		}
	}
	return version, nil
}

// recordLifecycleOn serializes through the existing template/principal fences.
// Authorization is repeated before replay, so stale permissions cannot use an
// old idempotency receipt to recover protected quality observations.
func (m *Module) recordLifecycleOn(ctx context.Context, tx *gorm.DB, s ObservationSource, kind, event, state string, observation any, view LifecycleView, check func(*model.LifecycleReceipt, *LifecycleView) error) (LifecycleView, error) {
	empty := LifecycleView{}
	if !validSource(s) {
		return empty, ErrInvalid
	}
	if m == nil || m.ids == nil || m.audit == nil || m.events == nil {
		return empty, ErrUnavailable
	}
	version, err := m.lifecycleAuthorityOn(ctx, tx, s)
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return empty, err
	}
	hash := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(hash[:])
	sourceHash := sha256.Sum256([]byte(s.SourceKey))
	sourceKey := hex.EncodeToString(sourceHash[:])
	q := query.BindLifecycleReceipts(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(version.TenantID), q.SourceKey.Eq(sourceKey)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	if old != nil {
		if old.RequestHash != requestHash || old.EventType != event {
			return empty, ErrConflict
		}
		return lifecycleView(old)
	}
	object := lifecycleObject(s, kind)
	prior, err := m.latestLifecycleOn(ctx, tx, version.TenantID, object)
	if err != nil {
		return empty, err
	}
	if prior == nil && s.PriorID != 0 || prior != nil && prior.ID != s.PriorID {
		return empty, ErrConflict
	}
	view.TemplateVersionID, view.ProjectID, view.ObjectKey, view.EventType, view.State, view.Reference = s.TemplateVersionID, s.ProjectID, object, event, state, s.Reference
	view.InitiatorID = authn.PrincipalIDFromContext(ctx)
	if check != nil {
		if err = check(prior, &view); err != nil {
			return empty, err
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	view.ID, view.CreatedAt = guid.ID(id), time.Now().UTC().Truncate(time.Millisecond)
	payload, err := json.Marshal(lifecyclePayload{View: view, Observation: raw})
	if err != nil {
		return empty, err
	}
	row := model.LifecycleReceipt{ID: view.ID, TenantID: version.TenantID, ProjectID: s.ProjectID, TemplateVersionID: s.TemplateVersionID, ObjectKey: object, SourceKey: sourceKey, EventType: event, State: state, Payload: string(payload), RequestHash: requestHash, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: view.CreatedAt}
	if err = q.WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	authority := "IAM:template_manage"
	a := audit.NewDomainEvent(ctx, version.TenantID, event, "template_lifecycle", row.ID)
	a.Detail["authority_source"], a.Detail["project_id"], a.Detail["actor_kind"], a.Detail["source_key"] = authority, s.ProjectID, "HUMAN", s.SourceKey
	if err = m.audit(ctx, tx, a); err != nil {
		return empty, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: version.TenantID, ProjectID: s.ProjectID, Type: event, Actor: events.Actor{Kind: "HUMAN", ID: row.CreatedBy, AuthoritySource: authority}, Subject: events.Subject{Kind: "template_lifecycle", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"state": state, "template_version_id": s.TemplateVersionID, "object_key": object}})
	return view, err
}

func (m *Module) recordCalibrationOn(ctx context.Context, tx *gorm.DB, in CalibrationObservation) (LifecycleView, error) {
	if len(in.Verifier) == 0 || len(in.Verifier) > 64 || len(in.ProviderRef) == 0 || len(in.ProviderRef) > 120 || in.MachineID < 0 || in.QualityOwnerID < 0 || !ppmValid(in.AgreementPPM, in.TargetPPM) || in.Samples < 0 || in.FailedProbes < 0 || !slices.Contains([]string{"UNCALIBRATED", "CALIBRATING", "CALIBRATED", "DRIFTED"}, in.State) || !slices.Contains([]string{"AGREEMENT", "PROBE"}, in.Method) {
		return LifecycleView{}, ErrInvalid
	}
	if in.Method == "PROBE" && in.MachineID <= 0 || in.Method == "AGREEMENT" && in.MachineID != 0 {
		return LifecycleView{}, ErrInvalid
	}
	if in.State == "CALIBRATED" && (in.Method == "AGREEMENT" && (in.Samples < 20 || in.AgreementPPM < in.TargetPPM) || in.Method == "PROBE" && (in.Samples == 0 || in.FailedProbes != 0)) {
		return LifecycleView{}, ErrInvalid
	}
	if (in.State == "CALIBRATED" || in.State == "DRIFTED") && len(in.Source.EvidenceIDs) == 0 {
		return LifecycleView{}, ErrInvalid
	}
	if in.State == "DRIFTED" && (in.Method == "PROBE" && in.FailedProbes == 0 || in.Method == "AGREEMENT" && in.Samples < 20) {
		return LifecycleView{}, ErrInvalid
	}
	if in.State == "DRIFTED" && in.Method == "AGREEMENT" && in.AgreementPPM >= in.TargetPPM {
		return LifecycleView{}, ErrInvalid
	}
	view := LifecycleView{QualityOwnerID: in.QualityOwnerID, Params: map[string]string{"verifier": in.Verifier, "agreement": fmt.Sprintf("%.4f", float64(in.AgreementPPM)/1000000)}}
	if in.State == "DRIFTED" {
		view.NoticeType = "verifier.calibration_drifted"
	}
	return m.recordLifecycleOn(ctx, tx, in.Source, "calibration", "verifier.calibration_changed", in.State, in, view, func(prior *model.LifecycleReceipt, _ *LifecycleView) error {
		if in.State == "DRIFTED" && (prior == nil || prior.State != "CALIBRATED" && prior.State != "DRIFTED") {
			return ErrConflict
		}
		return nil
	})
}
func (m *Module) recordProviderOn(ctx context.Context, tx *gorm.DB, in ProviderObservation) (LifecycleView, error) {
	if !slices.Contains([]string{"ENABLED", "DISABLED", "UNAVAILABLE"}, in.State) || len(in.BlockedWorkIDs) > 1000 || len(in.AffectedProjectIDs) > 100 {
		return LifecycleView{}, ErrInvalid
	}
	if in.State == "UNAVAILABLE" && len(in.Source.EvidenceIDs) == 0 {
		return LifecycleView{}, ErrInvalid
	}
	seen := map[guid.ID]bool{}
	for _, id := range in.BlockedWorkIDs {
		if id <= 0 || seen[id] {
			return LifecycleView{}, ErrInvalid
		}
		seen[id] = true
	}
	projects := map[guid.ID]bool{}
	for _, id := range in.AffectedProjectIDs {
		if id <= 0 || projects[id] || in.Source.ProjectID > 0 && id != in.Source.ProjectID {
			return LifecycleView{}, ErrInvalid
		}
		projects[id] = true
	}
	view := LifecycleView{AffectedProjectIDs: slices.Clone(in.AffectedProjectIDs), Params: map[string]string{"provider": in.Source.Reference, "n": fmt.Sprint(len(in.BlockedWorkIDs))}}
	if in.State == "UNAVAILABLE" {
		view.NoticeType = "provider.unavailable"
	}
	return m.recordLifecycleOn(ctx, tx, in.Source, "provider", "provider.state_changed", in.State, in, view, func(prior *model.LifecycleReceipt, _ *LifecycleView) error {
		if prior != nil && prior.State == in.State {
			return ErrConflict
		}
		return nil
	})
}
func (m *Module) recordEvalOn(ctx context.Context, tx *gorm.DB, in EvalObservation) (LifecycleView, error) {
	if !slices.Contains([]string{"RUNNING", "COMPLETED", "FAILED"}, in.State) || in.Total < 0 || in.Total > 1000000 || in.Passed < 0 || in.Failed < 0 || in.Passed > in.Total || in.Failed > in.Total || !ppmValid(in.AgreementPPM, in.TargetPPM) || in.State == "COMPLETED" && (in.Total == 0 || in.Passed+in.Failed != in.Total) {
		return LifecycleView{}, ErrInvalid
	}
	if in.State != "RUNNING" && len(in.Source.EvidenceIDs) == 0 {
		return LifecycleView{}, ErrInvalid
	}
	event := "eval.run_changed"
	view := LifecycleView{Params: map[string]string{"template": in.Source.TemplateVersionID.String(), "agreement": fmt.Sprintf("%.4f", float64(in.AgreementPPM)/1000000), "failed": fmt.Sprint(in.Failed)}}
	if in.State == "COMPLETED" {
		event = "eval.run_completed"
		if in.AgreementPPM < in.TargetPPM {
			view.NoticeType = "eval.below_target"
		}
	}
	return m.recordLifecycleOn(ctx, tx, in.Source, "eval", event, in.State, in, view, func(prior *model.LifecycleReceipt, v *LifecycleView) error {
		if in.State == "RUNNING" {
			if prior != nil {
				return ErrConflict
			}
			return nil
		}
		if prior == nil || prior.State != "RUNNING" {
			return ErrConflict
		}
		previous, err := lifecycleView(prior)
		if err != nil {
			return err
		}
		var payload lifecyclePayload
		if err = json.Unmarshal([]byte(prior.Payload), &payload); err != nil {
			return err
		}
		var started EvalObservation
		if err = json.Unmarshal(payload.Observation, &started); err != nil {
			return err
		}
		if started.TargetPPM != in.TargetPPM {
			return ErrConflict
		}
		v.InitiatorID = previous.InitiatorID
		return nil
	})
}
func (m *Module) recordRubricOn(ctx context.Context, tx *gorm.DB, in RubricObservation) (LifecycleView, error) {
	if in.Source.ProjectID <= 0 || in.InternalScoreID <= 0 || in.ExternalScoreID <= 0 || in.InternalScoreID == in.ExternalScoreID || len(in.Dimensions) == 0 || len(in.Dimensions) > 100 {
		return LifecycleView{}, ErrInvalid
	}
	dimensions := []string{}
	seen := map[string]bool{}
	for _, d := range in.Dimensions {
		if len(d.Key) == 0 || len(d.Key) > 64 || seen[d.Key] || d.InternalMilli < 1000 || d.InternalMilli > 5000 || d.ExternalMilli < 1000 || d.ExternalMilli > 5000 {
			return LifecycleView{}, ErrInvalid
		}
		seen[d.Key] = true
		if d.InternalMilli-d.ExternalMilli >= 1000 || d.ExternalMilli-d.InternalMilli >= 1000 {
			dimensions = append(dimensions, d.Key)
		}
	}
	view := LifecycleView{Params: map[string]string{"subject": in.Source.Reference, "dimensions": strings.Join(dimensions, ", ")}}
	event := "rubric.scores_compared"
	if len(dimensions) > 0 {
		view.NoticeType = "rubric.divergence_detected"
		event = "rubric.divergence_detected"
	}
	return m.recordLifecycleOn(ctx, tx, in.Source, "rubric", event, "RECORDED", in, view, nil)
}
func (m *Module) recordToolOn(ctx context.Context, tx *gorm.DB, in ToolObservation) (LifecycleView, error) {
	if in.EligibleMachines < 0 || len(in.Source.EvidenceIDs) == 0 {
		return LifecycleView{}, ErrInvalid
	}
	view := LifecycleView{Params: map[string]string{"tool": in.Source.Reference}}
	state := "AVAILABLE"
	if in.EligibleMachines == 0 {
		state = "UNAVAILABLE"
		view.NoticeType = "verifier.tool_unavailable"
	}
	return m.recordLifecycleOn(ctx, tx, in.Source, "tool", "verifier.tool_availability_changed", state, in, view, nil)
}

func (m *Module) lifecycleCurrentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (LifecycleView, bool, error) {
	if tx == nil || tenant <= 0 || id <= 0 {
		return LifecycleView{}, false, ErrInvalid
	}
	q := query.BindLifecycleReceipts(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return LifecycleView{}, false, nil
	}
	if err != nil {
		return LifecycleView{}, false, err
	}
	latest, err := m.latestLifecycleOn(ctx, tx, tenant, row.ObjectKey)
	if err != nil {
		return LifecycleView{}, false, err
	}
	if latest == nil {
		return LifecycleView{}, false, nil
	}
	if latest.ID != row.ID {
		previous, e := lifecycleView(row)
		if e != nil {
			return LifecycleView{}, false, e
		}
		current, e := lifecycleView(latest)
		if e != nil {
			return LifecycleView{}, false, e
		}
		if previous.NoticeType == "" || previous.NoticeType != current.NoticeType {
			return LifecycleView{}, false, nil
		}
	}
	if row.ProjectID > 0 {
		b := query.BindBindings(tx, m.names)
		binding, e := b.WithContext(ctx).Where(b.TenantID.Eq(tenant), b.ProjectID.Eq(row.ProjectID)).Order(b.ID.Desc()).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return LifecycleView{}, false, nil
		}
		if e != nil {
			return LifecycleView{}, false, e
		}
		if binding.TemplateVersionID != row.TemplateVersionID {
			return LifecycleView{}, false, nil
		}
	}
	v, err := lifecycleView(row)
	if err != nil {
		return LifecycleView{}, false, err
	}
	current, err := lifecycleView(latest)
	if err != nil {
		return LifecycleView{}, false, err
	}
	v.QualityOwnerID = current.QualityOwnerID
	if v.EventType == "provider.state_changed" && v.ProjectID == 0 {
		v.AffectedProjectIDs = []guid.ID{}
		b := query.BindBindings(tx, m.names)
		for _, project := range current.AffectedProjectIDs {
			binding, e := b.WithContext(ctx).Where(b.TenantID.Eq(tenant), b.ProjectID.Eq(project)).Order(b.ID.Desc()).First()
			if errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			}
			if e != nil {
				return LifecycleView{}, false, e
			}
			if binding.TemplateVersionID == row.TemplateVersionID {
				v.AffectedProjectIDs = append(v.AffectedProjectIDs, project)
			}
		}
	}
	return v, true, nil
}
