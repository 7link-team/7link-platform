package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMemoryQuota = errors.Join(ErrConflict, errors.New("knowledge: memory quota exhausted"))
var ErrMemorySecret = errors.Join(ErrInvalid, errors.New("knowledge: memory contains a suspected secret"))
var ErrMemoryProvenance = errors.Join(ErrInvalid, errors.New("knowledge: invalid memory execution provenance"))

// VerifyOn must read the actual execution in the caller transaction, checking
// Agent ownership, exact project, terminal state and an end time within 24h.
// OriginalText is read from the execution's actual intake source, never supplied
// by the memory writer. An absent adapter fails closed for execution-origin writes.
type MemoryExecutionEvidence struct {
	VerifyOn func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID, guid.ID, time.Time) (string, error)
}
type MemoryExecutionEvidenceConnector struct {
	Bind func(MemoryExecutionEvidence) error
}
type memoryEvidenceBinding struct {
	sync.RWMutex
	evidence MemoryExecutionEvidence
}

func (m *Module) provideMemoryEvidence(s *module.Services) error {
	return module.Provide[MemoryExecutionEvidenceConnector](s, MemoryExecutionEvidenceConnector{Bind: func(e MemoryExecutionEvidence) error {
		if e.VerifyOn == nil {
			return ErrUnavailable
		}
		m.memory.Lock()
		defer m.memory.Unlock()
		if m.memory.evidence.VerifyOn != nil {
			return ErrConflict
		}
		m.memory.evidence = e
		return nil
	}})
}
func memoryFlags(raw string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}
func memoryLiveSlot(state string) *int {
	if state == "ACTIVE" || state == "PENDING_REVIEW" {
		v := 1
		return &v
	}
	return nil
}

var memorySecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`), regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`), regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36}\b`), regexp.MustCompile(`(?i)(password|passwd|secret|token)\s*[:=]\s*\S{6,}`),
}
var memoryOverride = regexp.MustCompile(`(?i)(ignore|disregard|override|bypass|skip)\s+(all\s+|previous\s+|the\s+)?(instructions?|rules?|tests?|verification|review)|\x{5ffd}\x{7565}(\x{4e4b}\x{524d}|\x{4ee5}\x{4e0a}|\x{6240}\x{6709})?.{0,6}(\x{6307}\x{4ee4}|\x{89c4}\x{5219}|\x{8981}\x{6c42})|(\x{8df3}\x{8fc7}|\x{4e0d}\x{8981}|\x{65e0}\x{9700}|\x{7981}\x{7528}).{0,6}(\x{6d4b}\x{8bd5}|\x{9a8c}\x{8bc1}|\x{8bc4}\x{5ba1}|\x{5ba1}\x{67e5}|\x{5ba1}\x{6279})|always\s+(approve|merge|pass)|(\x{603b}\x{662f}|\x{4e00}\x{5f8b}|\x{76f4}\x{63a5}).{0,4}(\x{901a}\x{8fc7}|\x{5408}\x{5e76}|\x{6279}\x{51c6})`)
var memoryAuthority = regexp.MustCompile(`(?i)\b(i am|as)\s+(the\s+)?(owner|admin|lead)\b|(\x{6211}\x{662f}|\x{4f5c}\x{4e3a}).{0,4}(\x{8d1f}\x{8d23}\x{4eba}|\x{7ba1}\x{7406}\x{5458}|\x{6240}\x{6709}\x{8005})|(\x{6388}\x{6743}|\x{6743}\x{9650}).{0,6}(\x{5df2}|\x{88ab})?(\x{6388}\x{4e88}|\x{6279}\x{51c6})`)

func memoryRisks(text, original string) []string {
	out := []string{}
	if memoryOverride.MatchString(text) {
		out = append(out, "IMPERATIVE_OVERRIDE")
	}
	if memoryAuthority.MatchString(text) {
		out = append(out, "AUTHORITY_CLAIM")
	}
	// Exact contiguous rune comparison avoids byte-based thresholds for CJK.
	runes := []rune(text)
	if len([]rune(original)) >= 60 {
		for i := 0; i+60 <= len(runes); i++ {
			if strings.Contains(original, string(runes[i:i+60])) {
				out = append(out, "EXTERNAL_TEXT")
				break
			}
		}
	}
	return out
}
func (m *Module) validateMemoryWriteOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in *EntryInput, actorKind string) error {
	if in.Scope.Scope != "AGENT" || in.Kind == "DOCUMENT" {
		if in.SourceExecutionID != nil || in.TTLDays != 0 {
			return ErrInvalid
		}
		return nil
	}
	if utf8.RuneCountInString(in.Title) > 120 || utf8.RuneCountInString(in.Body) < 10 || utf8.RuneCountInString(in.Body) > 1000 {
		return ErrInvalid
	}
	if in.TTLDays == 0 {
		in.TTLDays = 180
	}
	if in.TTLDays < 7 || in.TTLDays > 730 {
		return ErrInvalid
	}
	for _, pattern := range memorySecretPatterns {
		if pattern.MatchString(in.Title + "\n" + in.Body) {
			return ErrMemorySecret
		}
	}
	if actorKind == "AGENT" && in.SourceExecutionID == nil && !in.edited {
		return ErrMemoryProvenance
	}
	original := ""
	if in.SourceExecutionID != nil {
		if *in.SourceExecutionID <= 0 {
			return ErrMemoryProvenance
		}
		m.memory.RLock()
		verify := m.memory.evidence.VerifyOn
		m.memory.RUnlock()
		if verify == nil {
			return ErrUnavailable
		}
		var err error
		verifyAt := m.now()
		if in.edited && !in.proofAt.IsZero() {
			verifyAt = in.proofAt
		}
		original, err = verify(ctx, tx, tenant, *in.AgentPrincipalID, in.ProjectID, *in.SourceExecutionID, verifyAt)
		if err != nil {
			return err
		}
	}
	in.riskFlags = memoryRisks(in.Title+"\n"+in.Body, original)
	if len(in.riskFlags) > 0 {
		in.Kind = "RULE"
	}
	// The Agent principal is the quota fence for Human and Agent writers alike.
	if _, active, err := m.principals.LockActiveRow(ctx, tx, *in.AgentPrincipalID); err != nil {
		return err
	} else if !active {
		return ErrForbidden
	}
	q := query.BindEntries(tx, m.names)
	base := func() query.IEntryDo {
		return q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(*in.AgentPrincipalID))
	}
	// Locking bounded reads see current rows even after an earlier MySQL RR read.
	count := func(read query.IEntryDo, limit int) (int, error) {
		rows, err := read.Clauses(clause.Locking{Strength: "UPDATE"}).Select(q.ID).Limit(limit).Find()
		return len(rows), err
	}
	midnight := m.now().UTC().Truncate(24 * time.Hour)
	daily, err := count(base().Where(q.CreatedBy.Eq(*in.AgentPrincipalID), q.CreatedAt.Gte(midnight)), 20)
	if err != nil {
		return err
	}
	rules, err := count(base().Where(q.CreatedBy.Eq(*in.AgentPrincipalID), q.CreatedAt.Gte(midnight), q.Kind.Eq("RULE")), 5)
	if err != nil {
		return err
	}
	pending, err := count(base().Where(q.State.Eq("PENDING_REVIEW")), 20)
	if err != nil {
		return err
	}
	experience, err := count(base().Where(q.State.Eq("ACTIVE"), q.Kind.Eq("EXPERIENCE")), 500)
	if err != nil {
		return err
	}
	activeRules, err := count(base().Where(q.State.Eq("ACTIVE"), q.Kind.Eq("RULE")), 100)
	if err != nil {
		return err
	}
	settings, err := m.settingsValueOn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	willBePending := !in.promoted && (in.Kind == "RULE" || settings.MemoryApproval == "ALL_MEMORIES")
	if actorKind == "AGENT" && (daily >= 20 || in.Kind == "RULE" && rules >= 5) || willBePending && pending >= 20 || !willBePending && (in.Kind == "RULE" && activeRules >= 100 || in.Kind == "EXPERIENCE" && experience >= 500) {
		return ErrMemoryQuota
	}
	return nil
}
func (m *Module) initializeMemory(row *model.Entry, in EntryInput, actorKind string) {
	row.Origin = "HUMAN"
	row.RiskFlags = "[]"
	if row.Scope != "AGENT" || row.Kind == "DOCUMENT" {
		return
	}
	row.TTLDays = in.TTLDays
	row.SourceExecutionID = in.SourceExecutionID
	row.LiveSlot = memoryLiveSlot(row.State)
	if in.SourceExecutionID != nil {
		row.Origin = "EXECUTION"
	}
	if in.edited && in.inheritedOrigin != "" {
		row.Origin = in.inheritedOrigin
	}
	flags := in.riskFlags
	if flags == nil {
		flags = []string{}
	}
	b, _ := json.Marshal(flags)
	row.RiskFlags = string(b)
	if row.Kind == "EXPERIENCE" {
		expiry := row.CreatedAt.AddDate(0, 0, row.TTLDays)
		row.ExpiresAt = &expiry
	}
}

func (m *Module) existingMemoryOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in EntryInput) (*model.Entry, bool, error) {
	if in.Scope.Scope != "AGENT" || in.Kind == "DOCUMENT" {
		return nil, false, nil
	}
	if _, active, err := m.principals.LockActiveRow(ctx, tx, *in.AgentPrincipalID); err != nil {
		return nil, false, err
	} else if !active {
		return nil, false, ErrForbidden
	}
	q := query.BindEntries(tx, m.names)
	hash := sha256.Sum256([]byte(in.Title + "\n" + in.Body))
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq("AGENT"), q.AgentPrincipalID.Eq(*in.AgentPrincipalID), q.ContentHash.Eq(hex.EncodeToString(hash[:])), q.State.In("ACTIVE", "PENDING_REVIEW"))
	if in.ProjectID == nil {
		r = r.Where(q.ProjectID.IsNull())
	} else {
		r = r.Where(q.ProjectID.Eq(*in.ProjectID))
	}
	row, err := r.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return row, err == nil, err
}

func (m *Module) memoryApprovalQuotaOn(ctx context.Context, tx *gorm.DB, row *model.Entry) error {
	if row.Scope != "AGENT" || row.AgentPrincipalID == nil {
		return nil
	}
	if _, _, err := m.principals.LockActiveRow(ctx, tx, *row.AgentPrincipalID); err != nil {
		return err
	}
	q := query.BindEntries(tx, m.names)
	memoryLimit := 500
	if row.Kind == "RULE" {
		memoryLimit = 100
	}
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select(q.ID).Where(q.TenantID.Eq(row.TenantID), q.AgentPrincipalID.Eq(*row.AgentPrincipalID), q.Kind.Eq(row.Kind), q.State.Eq("ACTIVE")).Limit(memoryLimit).Find()
	if err != nil {
		return err
	}
	if len(rows) >= memoryLimit {
		return ErrMemoryQuota
	}
	return nil
}

// Denials are committed separately from the rolled-back command, after current
// source authority is rechecked. Only a classifier name is retained, never text.
func (m *Module) recordMemoryDenied(ctx context.Context, tenant guid.ID, body any, cause error) error {
	if !errors.Is(cause, ErrMemorySecret) && !errors.Is(cause, ErrMemoryQuota) {
		return nil
	}
	in, ok := body.(EntryInput)
	edit, editing := body.(MemoryMutation)
	if (!ok || in.Scope.Scope != "AGENT") && !editing {
		return nil
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		subject := guid.ID(0)
		if editing {
			row, _, e := m.memoryEntryOn(ctx, tx, tenant, edit.memoryID, "knowledge_write", false)
			if e != nil {
				return e
			}
			if row.Scope != "AGENT" {
				return nil
			}
			in.Scope = Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}
			subject = row.ID
		}
		if _, e := m.authorize(ctx, tx, tenant, in.Scope, "knowledge_write", false); e != nil {
			return e
		}
		if m.audit == nil {
			return ErrUnavailable
		}
		rule := "MEMORY_QUOTA"
		if errors.Is(cause, ErrMemorySecret) {
			rule = "SUSPECTED_SECRET"
		}
		event := audit.NewDomainEvent(ctx, tenant, "knowledge.memory_write_denied", "knowledge_entry", subject)
		event.Detail["authority_source"] = "IAM:knowledge_write"
		event.Detail["evidence_refs"] = []string{}
		event.Detail["rule_name"] = rule
		return m.audit(ctx, tx, event)
	})
}

type memoryApprovalVisibilityKey struct{}

func memoryApprovalContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, memoryApprovalVisibilityKey{}, true)
}
func memoryApprovalVisible(ctx context.Context) bool {
	visible, _ := ctx.Value(memoryApprovalVisibilityKey{}).(bool)
	return visible
}

// Synchronous expiry prevents the scheduled retention cadence from creating a
// window where an expired EXPERIENCE can enter a new runtime snapshot.
func (m *Module) memoryActive(row *model.Entry) bool {
	return row.State == "ACTIVE" && (row.Kind != "EXPERIENCE" || row.ExpiresAt == nil || row.ExpiresAt.After(m.now()))
}
