package execution

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// VisualScreenshot contains actual browser bytes. SHA256 and viewport are
// revalidated before staging; successful collection never implies acceptance.
type VisualScreenshot struct {
	ReferenceSHA256 string `json:"reference_sha256,omitempty"`
	Comparison      string `json:"comparison,omitempty"`
	Name            string `json:"name" maxLength:"200"`
	Data            []byte `json:"data"`
	SHA256          string `json:"sha256" minLength:"64" maxLength:"64"`
	Width           int    `json:"width" minimum:"1" maximum:"4096"`
	Height          int    `json:"height" minimum:"1" maximum:"4096"`
	Route           string `json:"route" maxLength:"8192"`
}

func validVisualArguments(argv []string) bool {
	if len(argv) != 1 && len(argv) != 3 {
		return false
	}
	if len(argv) == 3 {
		for _, s := range argv[1:] {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 || n > 4096 {
				return false
			}
		}
	}
	u, e := url.Parse(argv[0])
	if e != nil || argv[0] == "" {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Host != "" && u.User == nil
	}
	return u.Scheme == "" && !filepath.IsAbs(argv[0]) && !strings.ContainsAny(argv[0], "\\:") && argv[0] != ".." && !strings.HasPrefix(filepath.Clean(argv[0]), ".."+string(filepath.Separator))
}

func validVisualScreenshotMetadata(p VerificationPlan, o VerificationObservation) bool {
	if len(o.Screenshots) > 2 || len(o.BrowserProvider) > 200 {
		return false
	}
	for i, s := range o.Screenshots {
		if p.ReportPath != "" {
			b, e := hex.DecodeString(s.ReferenceSHA256)
			if e != nil || len(b) != 32 || (s.Comparison != "IDENTICAL" && s.Comparison != "DIFFERENT") {
				return false
			}
		} else if s.ReferenceSHA256 != "" || s.Comparison != "" {
			return false
		}
		w, h := 1440, 900
		if i == 1 {
			w, h = 390, 844
		}
		if len(p.Argv) == 3 {
			w, _ = strconv.Atoi(p.Argv[1])
			h, _ = strconv.Atoi(p.Argv[2])
		}
		if s.Width != w || s.Height != h || s.Route != p.Argv[0] {
			return false
		}
		b, e := hex.DecodeString(s.SHA256)
		if e != nil || len(b) != 32 {
			return false
		}
		if _, _, e := files.PortableName(s.Name); e != nil {
			return false
		}
	}
	return true
}

func validVisualScreenshots(p VerificationPlan, o VerificationObservation) bool {
	if !validVisualScreenshotMetadata(p, o) {
		return false
	}
	for _, s := range o.Screenshots {
		if len(s.Data) == 0 || len(s.Data) > 4<<20 || hash(s.Data) != s.SHA256 {
			return false
		}
		cfg, e := png.DecodeConfig(bytes.NewReader(s.Data))
		if e != nil || cfg.Width != s.Width || cfg.Height != s.Height {
			return false
		}
		if _, e = png.Decode(bytes.NewReader(s.Data)); e != nil {
			return false
		}
	}
	return true
}

// File storage owns the bytes. Canonical receipt metadata includes their digest,
// avoiding duplicate base64 blobs and MySQL TEXT overflow in observation_json.
// The raw upload is still fully validated and attached before a task is DONE.
func storedVerificationObservation(o VerificationObservation) VerificationObservation {
	o.Screenshots = append([]VisualScreenshot(nil), o.Screenshots...)
	for i := range o.Screenshots {
		o.Screenshots[i].Data = nil
	}
	return o
}

func visualVerificationVerdict(p VerificationPlan, o VerificationObservation) (string, string) {
	if o.Reason != "VISUAL_REVIEW_REQUIRED" {
		return "INCONCLUSIVE", o.Reason
	}
	if o.HeadCommit != p.HeadCommit {
		return "INCONCLUSIVE", "HEAD_MISMATCH"
	}
	want := 2
	if len(p.Argv) == 3 {
		want = 1
	}
	if !validVisualArguments(p.Argv) || len(o.Screenshots) != want || !validVisualScreenshotMetadata(p, o) || strings.TrimSpace(o.BrowserProvider) == "" {
		return "INCONCLUSIVE", "SCREENSHOT_INVALID"
	}
	// The current catalog exposes no machine ToolCalibration capability. Neither
	// an available browser nor identical pixels establishes calibration or a
	// reviewer verdict. Existing authorized Human exceptions remain auditable.
	return "INCONCLUSIVE", "VISUAL_REVIEW_REQUIRED"
}

func (m *Module) authorizeVisualArtifactOn(ctx context.Context, tx *gorm.DB, a files.ExecutionArtifactAuthority, purpose string, resourceID guid.ID, owner files.ResourceOwner) error {
	if purpose != "execution_evidence" || owner.Scope != "entity" || owner.ID != a.EntityID {
		return ErrForbidden
	}
	if e := m.validateRunner(ctx, tx, a.TenantID, RunnerFence{MachineID: a.MachineID, SessionID: a.SessionID, ConnEpoch: a.ConnEpoch}); e != nil {
		return e
	}
	q := query.BindVerificationTasks(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(a.VerificationTaskID), q.TenantID.Eq(a.TenantID), q.ProjectID.Eq(a.EntityID), q.ExecutionID.Eq(a.ExecutionID), q.MachineID.Eq(a.MachineID)).First()
	if e != nil {
		return e
	}
	var p VerificationPlan
	if r.State != "LEASED" || r.SessionID != a.SessionID || r.ConnEpoch != a.ConnEpoch || r.LeaseExpiresAt == nil || !r.LeaseExpiresAt.After(time.Now()) || hash([]byte(r.PlanJSON)) != r.PlanHash || json.Unmarshal([]byte(r.PlanJSON), &p) != nil || p.Method != "VISUAL" {
		return ErrFenced
	}
	if resourceID > 0 {
		evidence := query.BindEvidence(tx, m.names)
		_, e = evidence.WithContext(ctx).Where(evidence.ID.Eq(resourceID), evidence.TenantID.Eq(a.TenantID), evidence.ExecutionID.Eq(a.ExecutionID)).First()
	}
	return e
}

// Stage uses exactly the same immutable account objects and cleanup path as
// ordinary runner uploads, before the receipt transaction takes business locks.
func (m *Module) prepareVisualEvidence(ctx context.Context, tenant, id guid.ID, f RunnerFence, nonce string, o VerificationObservation) (VerificationObservation, error) {
	if len(o.Screenshots) == 0 {
		return o, nil
	}
	if m.artifacts.stage == nil {
		return o, ErrCapabilityUnavailable
	}
	db, e := m.writer()
	if e != nil {
		return o, e
	}
	q := query.BindVerificationTasks(db, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.MachineID.Eq(f.MachineID)).First()
	if e != nil {
		return o, e
	}
	var p VerificationPlan
	if json.Unmarshal([]byte(r.PlanJSON), &p) != nil || hash([]byte(r.PlanJSON)) != r.PlanHash {
		return o, ErrUnavailable
	}
	if p.Nonce != nonce || o.PlanHash != r.PlanHash || r.SessionID != f.SessionID || r.ConnEpoch != f.ConnEpoch {
		return o, ErrFenced
	}
	if p.Method != "VISUAL" || !validVisualArguments(p.Argv) || !validVisualScreenshots(p, o) {
		return o, ErrInvalidArgument
	}
	if r.State == "DONE" {
		return o, nil
	}
	a := files.ExecutionArtifactAuthority{VerificationTaskID: id, TenantID: tenant, EntityID: r.ProjectID, ExecutionID: r.ExecutionID, MachineID: f.MachineID, SessionID: f.SessionID, ConnEpoch: f.ConnEpoch}
	for i, s := range o.Screenshots {
		prepared, e := m.artifacts.stage(ctx, a, "execution_evidence", s.Name, fmt.Sprintf("visual.%s.%d", id.String(), i), bytes.NewReader(s.Data), int64(len(s.Data)))
		if e != nil {
			return o, e
		}
		o.preparedVisual = append(o.preparedVisual, prepared)
	}
	return o, nil
}

func (m *Module) attachVisualEvidenceOn(ctx context.Context, tx *gorm.DB, r *model.VerificationTask, p VerificationPlan, o VerificationObservation) error {
	if !validVisualScreenshots(p, o) {
		return ErrInvalidArgument
	}
	if len(o.Screenshots) == 0 {
		return nil
	}
	if len(o.preparedVisual) != len(o.Screenshots) || m.artifacts.attach == nil {
		return ErrCapabilityUnavailable
	}
	criterion, e := guid.RequireID(p.CriterionID)
	if e != nil {
		return ErrInvalidArgument
	}
	q := query.BindEvidence(tx, m.names)
	for i, s := range o.Screenshots {
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ExecutionID.Eq(r.ExecutionID), q.ContentHash.Eq(s.SHA256)).First()
		if e == nil {
			continue
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		id, e := m.nextID()
		if e != nil {
			return e
		}
		row := &model.Evidence{ID: id, TenantID: r.TenantID, ProjectID: r.ProjectID, ExecutionID: r.ExecutionID, Kind: "FILE", Title: fmt.Sprintf("Visual verification %s %dx%d", r.ID.String(), s.Width, s.Height), ContentHash: s.SHA256, CreatedAt: time.Now().UTC()}
		if e = q.WithContext(ctx).Create(row); e != nil {
			return e
		}
		pin, e := m.artifacts.attach(ctx, tx, o.preparedVisual[i], id)
		if e != nil {
			return e
		}
		if pin.SHA256 != s.SHA256 {
			return ErrConflict
		}
		fact := ArtifactFact{BindingID: pin.BindingID, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: pin.VersionID, FileVersion: 1, CriterionID: &criterion, Coverage: "PARTIAL", Note: fmt.Sprintf("Browser %s; viewport %dx%d; reviewer and tool calibration not established.", o.BrowserProvider, s.Width, s.Height), SHA256: s.SHA256}
		b, e := json.Marshal(fact)
		if e != nil {
			return e
		}
		if _, e = q.WithContext(ctx).Where(q.ID.Eq(id)).Update(q.Content, string(b)); e != nil {
			return e
		}
	}
	return nil
}
