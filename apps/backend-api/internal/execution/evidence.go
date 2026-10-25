package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrEvidenceAttached = errors.New("execution: attached evidence is immutable")

type EvidenceCreate struct {
	SeenSeq          *int64     `json:"seen_seq,omitempty" minimum:"0"`
	EvidenceType     string     `json:"evidence_type" enum:"OBSERVATION,SCREENSHOT,ARTIFACT,PULL_REQUEST"`
	Title            string     `json:"title" minLength:"1" maxLength:"200"`
	ScopeText        string     `json:"scope_text,omitempty" maxLength:"4000"`
	Text             string     `json:"text,omitempty" maxLength:"65536"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	FileID           guid.ID    `json:"file_id,omitempty"`
	ArtifactID       guid.ID    `json:"artifact_id,omitempty" doc:"ID of an existing pinned FILE evidence artifact from this execution."`
	URL              string     `json:"url,omitempty" maxLength:"2000"`
	LimitationsExtra string     `json:"limitations_extra,omitempty" maxLength:"1000"`
}
type EvidenceUpdate struct {
	SeenSeq          *int64  `json:"seen_seq,omitempty" minimum:"0"`
	State            *string `json:"state,omitempty" enum:"DRAFT,WITHDRAWN"`
	Title            *string `json:"title,omitempty" minLength:"1" maxLength:"200"`
	LimitationsExtra *string `json:"limitations_extra,omitempty" maxLength:"1000"`
}
type EvidenceView struct {
	ID              guid.ID    `json:"id"`
	ExecutionID     guid.ID    `json:"execution_id"`
	Kind            string     `json:"kind"`
	State           string     `json:"state"`
	Title           string     `json:"title"`
	ScopeText       *string    `json:"scope_text,omitempty"`
	LimitationCodes []string   `json:"limitation_codes,omitempty"`
	Limitations     *string    `json:"limitations,omitempty"`
	ProducerKind    string     `json:"producer_kind"`
	ProducerID      guid.ID    `json:"producer_id"`
	ClaimID         *guid.ID   `json:"claim_id,omitempty"`
	Content         string     `json:"content"`
	ContentHash     string     `json:"content_hash"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
}

func evidenceView(r *model.Evidence) EvidenceView {
	codes := []string{}
	if r.Kind == "PULL_REQUEST" {
		codes = append(codes, "GIT_CONNECTOR_UNVERIFIED")
	}
	return EvidenceView{LimitationCodes: codes, ID: r.ID, ExecutionID: r.ExecutionID, Kind: r.Kind, State: r.State, Title: r.Title, ScopeText: r.ScopeText, Limitations: r.Limitations, ProducerKind: r.ProducerKind, ProducerID: r.ProducerID, ClaimID: r.ClaimID, Content: r.Content, ContentHash: r.ContentHash, ObservedAt: r.ObservedAt}
}

type manualEvidenceReadKey struct{}

func (m *Module) evidenceAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, row *model.Execution, seen *int64) (string, error) {
	kind, err := m.commentAuthority(ctx, tx, tenant, row.ProjectID, "evidence_submit")
	if err != nil {
		return "", err
	}
	if err = m.checkExecutionContextOn(ctx, tx, row, seen, kind == "AGENT"); err != nil {
		return "", err
	}
	return kind, nil
}

func (m *Module) CreateEvidenceOn(ctx context.Context, tx *gorm.DB, tenant, execution guid.ID, in EvidenceCreate) (EvidenceView, error) {
	var out EvidenceView
	if err := transaction(tx, tenant, execution); err != nil {
		return out, err
	}
	row, err := m.rowOn(ctx, tx, tenant, execution, false)
	if err != nil {
		return out, err
	}
	kind, err := m.evidenceAuthorityOn(ctx, tx, tenant, row, in.SeenSeq)
	if err != nil {
		return out, err
	}
	if !bounded(in.Title, 200) || !utf8.ValidString(in.Text) || len(in.Text) > 65536 || !utf8.ValidString(in.ScopeText) || utf8.RuneCountInString(in.ScopeText) > 4000 || !utf8.ValidString(in.LimitationsExtra) || utf8.RuneCountInString(in.LimitationsExtra) > 1000 || !slices.Contains([]string{"OBSERVATION", "SCREENSHOT", "ARTIFACT", "PULL_REQUEST"}, in.EvidenceType) {
		return out, ErrInvalidArgument
	}
	// Once a completion has captured the evidence set it is immutable. Lock the
	// execution after its work row to serialize with the real claim producer.
	row, err = m.rowOn(ctx, tx, tenant, execution, true)
	if err != nil {
		return out, err
	}
	if row.State != "CREATED" && row.State != "RUNNING" && row.State != "PAUSED" {
		return out, ErrEvidenceAttached
	}
	draftQuery := query.BindEvidence(tx, m.names)
	drafts, err := draftQuery.WithContext(ctx).Where(draftQuery.TenantID.Eq(tenant), draftQuery.ExecutionID.Eq(execution), draftQuery.State.Eq("DRAFT")).Count()
	if err != nil {
		return out, err
	}
	if drafts >= 200 {
		return out, ErrConflict
	}
	content, limitations := in.Text, in.LimitationsExtra
	var candidate *files.PinnedCandidate
	var artifactVersion guid.ID
	var artifactHash string
	switch in.EvidenceType {
	case "OBSERVATION":
		if strings.TrimSpace(in.ScopeText) == "" || strings.TrimSpace(in.Text) == "" || in.FileID != 0 || in.ArtifactID != 0 || in.URL != "" {
			return out, ErrInvalidArgument
		}
	case "PULL_REQUEST":
		u, e := url.Parse(in.URL)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || len(in.URL) > 2000 || in.FileID != 0 || in.ArtifactID != 0 {
			return out, ErrInvalidArgument
		}
		content = in.URL

	case "SCREENSHOT", "ARTIFACT":
		if m.commentFiles == nil {
			return out, ErrCapabilityUnavailable
		}
		file := in.FileID
		if in.EvidenceType == "ARTIFACT" {
			if in.ArtifactID <= 0 || in.FileID != 0 {
				return out, ErrInvalidArgument
			}
			q := query.BindEvidence(tx, m.names)
			source, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution), q.ID.Eq(in.ArtifactID), q.Kind.Eq("FILE")).First()
			if e != nil {
				return out, e
			}
			var fact ArtifactFact
			if json.Unmarshal([]byte(source.Content), &fact) != nil || fact.FileID <= 0 {
				return out, ErrUnavailable
			}
			file = fact.FileID
			artifactVersion, artifactHash = fact.VersionID, fact.SHA256
		} else if file <= 0 || in.ArtifactID != 0 {
			return out, ErrInvalidArgument
		}
		pinCtx := context.WithValue(ctx, manualEvidenceReadKey{}, true)
		c, e := m.commentFiles.InspectOn(pinCtx, tx, file)
		if e != nil {
			return out, e
		}
		if in.EvidenceType == "SCREENSHOT" && (!slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, c.MIME) || c.Size > 20*1024*1024) {
			return out, ErrInvalidArgument
		}
		if in.EvidenceType == "ARTIFACT" && (c.VersionID != artifactVersion || c.SHA256 != artifactHash) {
			return out, ErrConflict
		}
		candidate = &c
		content = c.SHA256
	}
	id, err := m.nextID()
	if err != nil {
		return out, err
	}
	// Including the producer preserves distinct actual observations of identical
	// text while retaining the immutable evidence content fingerprint.
	if candidate == nil {
		encoded, e := json.Marshal(struct {
			Text         string     `json:"text"`
			Scope        string     `json:"scope_text"`
			At           *time.Time `json:"observed_at,omitempty"`
			Producer     guid.ID    `json:"producer_id"`
			Verification string     `json:"verification,omitempty"`
		}{content, in.ScopeText, in.ObservedAt, authn.PrincipalIDFromContext(ctx), manualEvidenceVerification(in.EvidenceType)})
		if e != nil {
			return out, e
		}
		content = string(encoded)
	}
	evidence := &model.Evidence{ID: id, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: execution, Kind: in.EvidenceType, Title: in.Title, State: "DRAFT", ProducerKind: kind, ProducerID: authn.PrincipalIDFromContext(ctx), ScopeText: &in.ScopeText, Limitations: &limitations, ObservedAt: in.ObservedAt, Content: content, ContentHash: hash([]byte(content)), CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}

	if err = query.BindEvidence(tx, m.names).WithContext(ctx).Create(evidence); err != nil {
		return out, err
	}
	if candidate != nil {
		bid, e := m.nextID()
		if e != nil {
			return out, e
		}
		pin, e := m.commentFiles.PinOn(context.WithValue(ctx, manualEvidenceReadKey{}, true), tx, files.ResourceBindingInput{ID: bid, SpaceID: candidate.SpaceID, FileID: candidate.FileID, ExpectedFileVersion: candidate.NodeVersion, VersionID: &candidate.VersionID, Purpose: "execution_evidence", ResourceKind: "execution_evidence", ResourceID: id, Slot: "manual", ActorID: evidence.ProducerID})
		if e != nil {
			return out, e
		}
		data, e := json.Marshal(ArtifactFact{BindingID: pin.BindingID, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: pin.VersionID, SHA256: pin.SHA256, FileVersion: candidate.NodeVersion})
		if e != nil {
			return out, e
		}
		evidence.Content = string(data)
		evidence.ContentHash = hash(data)
		q := query.BindEvidence(tx, m.names)
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"content": evidence.Content, "content_hash": evidence.ContentHash}); err != nil {
			return out, err
		}
	}
	if err = m.recordExecutionActivityOn(ctx, tx, row, "EVIDENCE_ADDED", "evidence", id, kind); err != nil {
		return out, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.evidence_recorded", kind, "permission:evidence_submit", map[string]any{"evidence_id": id.String(), "state": "DRAFT"}); err != nil {
		return out, err
	}
	return evidenceView(evidence), nil
}

func (m *Module) UpdateEvidenceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in EvidenceUpdate) (EvidenceView, error) {
	var out EvidenceView
	if err := transaction(tx, tenant, id); err != nil {
		return out, err
	}
	q := query.BindEvidence(tx, m.names)
	evidenceRow, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	evidence := *evidenceRow
	row, err := m.rowOn(ctx, tx, tenant, evidence.ExecutionID, false)
	if err != nil {
		return out, err
	}
	kind, err := m.evidenceAuthorityOn(ctx, tx, tenant, row, in.SeenSeq)
	if err != nil {
		return out, err
	}
	row, err = m.rowOn(ctx, tx, tenant, row.ID, true)
	if err != nil {
		return out, err
	}
	evidenceRow, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	evidence = *evidenceRow
	if evidence.State == "ATTACHED" || evidence.ClaimID != nil || !slices.Contains([]string{"CREATED", "RUNNING", "PAUSED"}, row.State) {
		return out, ErrEvidenceAttached
	}

	if in.State == nil && in.Title == nil && in.LimitationsExtra == nil {
		return out, ErrInvalidArgument
	}
	if in.State != nil {
		if !slices.Contains([]string{"DRAFT", "WITHDRAWN"}, *in.State) {
			return out, ErrInvalidArgument
		}
		evidence.State = *in.State
	}
	if in.Title != nil {
		if !bounded(*in.Title, 200) {
			return out, ErrInvalidArgument
		}
		evidence.Title = *in.Title
	}
	if in.LimitationsExtra != nil {
		if !utf8.ValidString(*in.LimitationsExtra) || utf8.RuneCountInString(*in.LimitationsExtra) > 1000 {
			return out, ErrInvalidArgument
		}
		limitations := *in.LimitationsExtra
		evidence.Limitations = &limitations
	}
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"title": evidence.Title, "state": evidence.State, "limitations": evidence.Limitations}); err != nil {
		return out, err
	}
	if err = m.recordExecutionActivityOn(ctx, tx, row, "EVIDENCE_ADDED", "evidence", id, kind); err != nil {
		return out, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.evidence_recorded", kind, "permission:evidence_submit", map[string]any{"evidence_id": id.String(), "state": evidence.State}); err != nil {
		return out, err
	}
	return evidenceView(&evidence), nil
}

// attachEvidenceOn is called only by the actual claim producer, in the same
// transaction. It neither creates claims nor accepts their result.
func (m *Module) attachEvidenceOn(ctx context.Context, tx *gorm.DB, row *model.Execution, claim guid.ID, ids []guid.ID) error {
	if transaction(tx, row.TenantID, row.ID) != nil || claim <= 0 || len(ids) > 50 {
		return ErrInvalidArgument
	}
	for _, id := range ids {
		q := query.BindEvidence(tx, m.names)
		evidence, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(row.TenantID), q.ExecutionID.Eq(row.ID), q.ID.Eq(id)).First()
		if err != nil {
			return err
		}
		if evidence.State == "ATTACHED" && evidence.ClaimID != nil && *evidence.ClaimID == claim {
			continue
		}
		if evidence.State != "DRAFT" || evidence.ClaimID != nil {
			return ErrEvidenceAttached
		}
		if _, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Updates(map[string]any{"state": "ATTACHED", "claim_id": claim}); err != nil {
			return err
		}
	}
	return nil
}

func manualEvidenceVerification(kind string) string {
	if kind == "PULL_REQUEST" {
		return "GIT_CONNECTOR_UNVERIFIED"
	}
	return ""
}
