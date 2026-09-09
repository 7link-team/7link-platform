// Package audit owns the IAM append-only domain audit chain. It is separate
// from internal/infra/audit, which records protocol operation telemetry.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var (
	ErrInvalidEvent  = errors.New("invalid audit event")
	ErrInvalidFilter = errors.New("invalid audit filter")
	ErrIntegrity     = errors.New("audit integrity check failed")
)

const exportBatchSize = 500

// DomainEvent describes one IAM state change written in the same transaction
// as that change.
type DomainEvent struct {
	TenantID    guid.ID
	PrincipalID guid.ID
	EventType   string
	TargetType  string
	TargetID    guid.ID
	Detail      map[string]any
}

// Appender writes an event through db. Callers pass their active transaction
// so the state change and its audit event commit or roll back together.
type Appender func(ctx context.Context, db *gorm.DB, event DomainEvent) error

// NewDomainEvent initializes an event and copies the verified principal from
// the authentication context when one is present.
func NewDomainEvent(ctx context.Context, tenantID guid.ID, eventType, targetType string, targetID guid.ID) DomainEvent {
	return DomainEvent{
		TenantID: tenantID, PrincipalID: authn.PrincipalIDFromContext(ctx),
		EventType: eventType, TargetType: targetType, TargetID: targetID, Detail: map[string]any{},
	}
}

type EventInput struct {
	TenantID    guid.ID
	PrincipalID guid.ID
	EventType   string
	TargetType  string
	TargetID    guid.ID
	Outcome     string
	IPAddress   string
	UserAgent   string
	Detail      map[string]any
}

type Event struct {
	ID           guid.ID         `json:"id"`
	TenantID     guid.ID         `json:"tenant_id"`
	PrincipalID  guid.ID         `json:"principal_id,omitempty"`
	EventType    string          `json:"event_type"`
	TargetType   string          `json:"target_type,omitempty"`
	TargetID     guid.ID         `json:"target_id,omitempty"`
	Outcome      string          `json:"outcome"`
	IPAddress    string          `json:"ip_address,omitempty"`
	UserAgent    string          `json:"user_agent,omitempty"`
	Detail       json.RawMessage `json:"detail"`
	Sequence     int64           `json:"sequence"`
	PreviousHash string          `json:"previous_hash,omitempty"`
	EventHash    string          `json:"event_hash"`
	CreatedAt    time.Time       `json:"created_at"`
}

type Filter struct {
	TenantID    guid.ID
	PrincipalID guid.ID
	EventType   string
	Outcome     string
	TargetType  string
	TargetID    guid.ID
	From        time.Time
	To          time.Time
	Offset      int
	Limit       int
}

type Integrity struct {
	TenantID       guid.ID      `json:"tenant_id"`
	Valid          bool         `json:"valid"`
	VerifiedEvents int64        `json:"verified_events"`
	HeadSequence   int64        `json:"head_sequence"`
	HeadHash       string       `json:"head_hash,omitempty"`
	Anchor         AnchorStatus `json:"anchor"`
}

type ExportSnapshot struct {
	Filter        Filter
	Integrity     Integrity
	GeneratedAt   time.Time
	AfterSequence int64
	PreviousHash  string
}

type exportCriteria struct {
	PrincipalID guid.ID `json:"principal_id,omitempty"`
	EventType   string  `json:"event_type,omitempty"`
	Outcome     string  `json:"outcome,omitempty"`
	TargetType  string  `json:"target_type,omitempty"`
	TargetID    guid.ID `json:"target_id,omitempty"`
	From        string  `json:"from,omitempty"`
	To          string  `json:"to,omitempty"`
}

type exportManifest struct {
	Type           string         `json:"type"`
	Schema         string         `json:"schema"`
	TenantID       guid.ID        `json:"tenant_id"`
	GeneratedAt    time.Time      `json:"generated_at"`
	HeadSequence   int64          `json:"head_sequence"`
	HeadHash       string         `json:"head_hash,omitempty"`
	VerifiedEvents int64          `json:"verified_events"`
	Filter         exportCriteria `json:"filter"`
	AfterSequence  int64          `json:"after_sequence,omitempty"`
	PreviousHash   string         `json:"previous_hash,omitempty"`
}

type exportEvent struct {
	Type  string `json:"type"`
	Event Event  `json:"event"`
}

type exportComplete struct {
	Type           string `json:"type"`
	ExportedEvents int64  `json:"exported_events"`
	ContentSHA256  string `json:"content_sha256"`
}

type Service struct {
	repo           *Repository
	now            func() time.Time
	anchor         *AnchorStore
	signer         *RootSigner
	archive        *AnchorStore
	erasureLedger  *ErasureLedger
	erasureTimeout time.Duration
}

func NewService(runtime *appenv.Runtime, ids guid.Source) *Service {
	return NewServiceWithAnchor(runtime, nil, ids)
}

func NewServiceWithAnchor(runtime *appenv.Runtime, anchor *AnchorStore, ids guid.Source) *Service {
	return &Service{repo: NewRepository(runtime, ids), now: time.Now, anchor: anchor}
}

func NewServiceWithAnchorAndSigner(runtime *appenv.Runtime, anchor *AnchorStore, signer *RootSigner, ids guid.Source) *Service {
	service := NewServiceWithAnchor(runtime, anchor, ids)
	service.signer = signer
	return service
}

// DomainAppender returns the module's transaction-local capability.
func (s *Service) DomainAppender() Appender {
	return func(ctx context.Context, db *gorm.DB, event DomainEvent) error {
		if s == nil || s.repo == nil {
			return errRepositoryUnavailable
		}
		_, err := s.appendTo(ctx, db, EventInput{
			TenantID: event.TenantID, PrincipalID: event.PrincipalID,
			EventType: event.EventType, TargetType: event.TargetType, TargetID: event.TargetID,
			Outcome: "success", Detail: event.Detail,
		})
		return err
	}
}

func (s *Service) Append(ctx context.Context, input EventInput) (Event, error) {
	if s == nil || s.repo == nil || validate(input) != nil {
		return Event{}, ErrInvalidEvent
	}
	var event Event
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		var err error
		event, err = s.appendTo(ctx, repo.writer(), input)
		return err
	})
	return event, err
}

func (s *Service) AppendTo(ctx context.Context, db *gorm.DB, input EventInput) (Event, error) {
	return s.appendTo(ctx, db, input)
}

func (s *Service) appendTo(ctx context.Context, db *gorm.DB, input EventInput) (Event, error) {
	if s == nil || s.repo == nil || db == nil || validate(input) != nil {
		return Event{}, ErrInvalidEvent
	}
	detail, err := json.Marshal(input.Detail)
	if err != nil {
		return Event{}, fmt.Errorf("encode audit detail: %w", err)
	}
	if string(detail) == "null" {
		detail = []byte("{}")
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	row, err := s.repo.appendTo(ctx, db, input, string(detail), now)
	if err != nil {
		return Event{}, err
	}
	return eventFromRow(row), nil
}

func (s *Service) List(ctx context.Context, filter Filter) ([]Event, int64, error) {
	if validateFilter(filter, true) != nil {
		return nil, 0, ErrInvalidFilter
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.list(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, eventFromRow(row))
	}
	return events, total, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id guid.ID) (Event, error) {
	repo, err := s.pinnedRepository()
	if err != nil {
		return Event{}, err
	}
	row, err := repo.get(ctx, tenantID, id)
	if err != nil {
		return Event{}, err
	}
	return eventFromRow(row), nil
}

func (s *Service) Verify(ctx context.Context, tenantID guid.ID) (Integrity, error) {
	repo, err := s.pinnedRepository()
	if err != nil {
		return Integrity{}, err
	}
	result, err := s.verify(ctx, repo, tenantID)
	if err != nil {
		return Integrity{}, err
	}
	if s.anchor != nil {
		status, anchorErr := s.verifyAnchors(ctx, repo, tenantID)
		if anchorErr != nil {
			return Integrity{}, anchorErr
		}
		result.Anchor = status
	}
	return result, nil
}

func (s *Service) verify(ctx context.Context, repo *Repository, tenantID guid.ID) (Integrity, error) {
	head, found, err := repo.readHead(ctx, tenantID)
	if err != nil {
		return Integrity{}, err
	}
	if !found {
		return Integrity{TenantID: tenantID, Valid: true}, nil
	}
	return s.verifyHead(ctx, repo, head)
}

func (s *Service) verifyHead(ctx context.Context, repo *Repository, head model.Head) (Integrity, error) {
	result, err := s.verifyPrefix(ctx, repo, head.TenantID, head.Sequence)
	if err != nil {
		return Integrity{}, err
	}
	result.Valid = result.Valid && result.HeadHash == head.EventHash
	result.HeadHash = head.EventHash
	return result, nil
}

func (s *Service) verifyPrefix(ctx context.Context, repo *Repository, tenantID guid.ID, maxSequence int64) (Integrity, error) {
	result := Integrity{TenantID: tenantID, Valid: true, HeadSequence: maxSequence}
	previous := ""
	seen := int64(0)
	for offset := 0; ; {
		rows, err := repo.readPrefix(ctx, tenantID, maxSequence, offset, exportBatchSize)
		if err != nil {
			return Integrity{}, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen++
			if row.Sequence != seen || normalizeStoredHash(row.PreviousHash) != previous || !validEventHash(row) {
				result.Valid = false
				return result, nil
			}
			previous = normalizeStoredHash(row.EventHash)
		}
		offset += len(rows)
		if len(rows) < exportBatchSize {
			break
		}
	}
	result.VerifiedEvents = seen
	result.HeadHash = previous
	result.Valid = maxSequence == seen
	return result, nil
}

func (s *Service) PrepareExport(ctx context.Context, filter Filter) (ExportSnapshot, error) {
	filter.Offset, filter.Limit = 0, 0
	if validateFilter(filter, false) != nil {
		return ExportSnapshot{}, ErrInvalidFilter
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return ExportSnapshot{}, err
	}
	return s.prepareExport(ctx, repo, filter)
}

func (s *Service) prepareExport(ctx context.Context, repo *Repository, filter Filter) (ExportSnapshot, error) {
	integrity, err := s.verify(ctx, repo, filter.TenantID)
	if err != nil {
		return ExportSnapshot{}, err
	}
	if !integrity.Valid {
		return ExportSnapshot{}, ErrIntegrity
	}
	return ExportSnapshot{Filter: filter, Integrity: integrity, GeneratedAt: s.now().UTC()}, nil
}

// RequestExport records the request and freezes the resulting verified head in
// one transaction before the response begins streaming.
func (s *Service) RequestExport(ctx context.Context, filter Filter, principalID guid.ID) (ExportSnapshot, error) {
	filter.Offset, filter.Limit = 0, 0
	if validateFilter(filter, false) != nil {
		return ExportSnapshot{}, ErrInvalidFilter
	}
	var snapshot ExportSnapshot
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		before, err := s.prepareExport(ctx, repo, filter)
		if err != nil || !before.Integrity.Valid {
			return err
		}
		if _, err := s.appendTo(ctx, repo.writer(), EventInput{
			TenantID: filter.TenantID, PrincipalID: principalID,
			EventType: "audit_export_requested", TargetType: "audit_event", Outcome: "success",
			Detail: map[string]any{"filter": exportCriteriaOf(filter)},
		}); err != nil {
			return err
		}
		snapshot, err = s.prepareExport(ctx, repo, filter)
		return err
	})
	return snapshot, err
}

func (s *Service) WriteExport(ctx context.Context, snapshot ExportSnapshot, writer io.Writer) error {
	if writer == nil || validateFilter(snapshot.Filter, false) != nil || !snapshot.Integrity.Valid || snapshot.Integrity.TenantID != snapshot.Filter.TenantID ||
		snapshot.AfterSequence < 0 || snapshot.AfterSequence > snapshot.Integrity.HeadSequence {
		return ErrInvalidFilter
	}
	manifest := exportManifest{
		Type: "manifest", Schema: "audit-export.v1", TenantID: snapshot.Filter.TenantID,
		GeneratedAt: snapshot.GeneratedAt.UTC(), HeadSequence: snapshot.Integrity.HeadSequence,
		HeadHash: snapshot.Integrity.HeadHash, VerifiedEvents: snapshot.Integrity.VerifiedEvents,
		Filter:        exportCriteriaOf(snapshot.Filter),
		AfterSequence: snapshot.AfterSequence, PreviousHash: snapshot.PreviousHash,
	}
	if err := writeJSONLine(writer, manifest); err != nil {
		return fmt.Errorf("write audit export manifest: %w", err)
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return err
	}
	contentHash := sha256.New()
	cursor := snapshot.AfterSequence
	var exported int64
	for cursor < snapshot.Integrity.HeadSequence {
		rows, readErr := repo.readExport(ctx, snapshot.Filter, cursor, snapshot.Integrity.HeadSequence, exportBatchSize)
		if readErr != nil {
			return readErr
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if row.Sequence <= cursor || !validEventHash(row) {
				return ErrIntegrity
			}
			line, marshalErr := json.Marshal(exportEvent{Type: "event", Event: eventFromRow(row)})
			if marshalErr != nil {
				return fmt.Errorf("encode audit export event: %w", marshalErr)
			}
			if writeErr := writeLine(writer, line); writeErr != nil {
				return fmt.Errorf("write audit export event: %w", writeErr)
			}
			_, _ = contentHash.Write(line)
			_, _ = contentHash.Write([]byte{'\n'})
			cursor = row.Sequence
			exported++
		}
	}
	complete := exportComplete{Type: "complete", ExportedEvents: exported, ContentSHA256: hex.EncodeToString(contentHash.Sum(nil))}
	if err := writeJSONLine(writer, complete); err != nil {
		return fmt.Errorf("write audit export completion: %w", err)
	}
	return nil
}

func (s *Service) pinnedRepository() (*Repository, error) {
	if s == nil || s.repo == nil {
		return nil, errRepositoryUnavailable
	}
	return s.repo.pinWriter()
}

func validateFilter(filter Filter, paginated bool) error {
	if len(filter.EventType) > 64 || len(filter.TargetType) > 64 {
		return ErrInvalidFilter
	}
	if filter.Outcome != "" && filter.Outcome != "success" && filter.Outcome != "denied" && filter.Outcome != "failure" {
		return ErrInvalidFilter
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return ErrInvalidFilter
	}
	if paginated && (filter.Offset < 0 || filter.Limit < 1 || filter.Limit > 200) {
		return ErrInvalidFilter
	}
	return nil
}

func exportCriteriaOf(filter Filter) exportCriteria {
	criteria := exportCriteria{PrincipalID: filter.PrincipalID, EventType: filter.EventType, Outcome: filter.Outcome, TargetType: filter.TargetType, TargetID: filter.TargetID}
	if !filter.From.IsZero() {
		criteria.From = filter.From.UTC().Format(time.RFC3339Nano)
	}
	if !filter.To.IsZero() {
		criteria.To = filter.To.UTC().Format(time.RFC3339Nano)
	}
	return criteria
}

func writeJSONLine(writer io.Writer, value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeLine(writer, line)
}

func writeLine(writer io.Writer, line []byte) error {
	written, err := writer.Write(append(line, '\n'))
	if err != nil {
		return err
	}
	if written != len(line)+1 {
		return io.ErrShortWrite
	}
	return nil
}

func validate(input EventInput) error {
	if strings.TrimSpace(input.EventType) == "" || len(input.EventType) > 64 ||
		(input.Outcome != "success" && input.Outcome != "denied" && input.Outcome != "failure") {
		return ErrInvalidEvent
	}
	if len(input.TargetType) > 64 || len(input.IPAddress) > 64 || len(input.UserAgent) > 512 {
		return ErrInvalidEvent
	}
	return nil
}

func hash(row model.Event) string {
	row.PreviousHash = normalizeStoredHash(row.PreviousHash)
	return hashStored(row)
}

func validEventHash(row model.Event) bool {
	if row.EventHash == hash(row) {
		return true
	}
	return row.PreviousHash != normalizeStoredHash(row.PreviousHash) && row.EventHash == hashStored(row)
}

func hashStored(row model.Event) string {
	payload, _ := json.Marshal(struct {
		ID, TenantID, PrincipalID, EventType, TargetType, TargetID, Outcome, IPAddress, UserAgent, Detail string
		CreatedAt, Sequence                                                                               int64
		PreviousHash                                                                                      string
	}{
		row.ID.String(), row.TenantID.String(), optionalID(row.PrincipalID).String(), row.EventType,
		row.TargetType, optionalID(row.TargetID).String(), row.Outcome, row.IPAddress, row.UserAgent,
		row.Detail, row.CreatedAt.UTC().UnixMilli(), row.Sequence, row.PreviousHash,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func eventFromRow(row model.Event) Event {
	return Event{
		ID: row.ID, TenantID: row.TenantID, PrincipalID: optionalID(row.PrincipalID), EventType: row.EventType,
		TargetType: row.TargetType, TargetID: optionalID(row.TargetID), Outcome: row.Outcome,
		IPAddress: row.IPAddress, UserAgent: row.UserAgent, Detail: json.RawMessage(row.Detail),
		Sequence: row.Sequence, PreviousHash: normalizeStoredHash(row.PreviousHash), EventHash: normalizeStoredHash(row.EventHash), CreatedAt: row.CreatedAt.UTC(),
	}
}

func normalizeStoredHash(value string) string { return strings.TrimRight(value, " ") }

func optionalID(id *guid.ID) guid.ID {
	if id == nil {
		return 0
	}
	return *id
}

func idPointer(id guid.ID) *guid.ID {
	if id.Zero() {
		return nil
	}
	return &id
}
