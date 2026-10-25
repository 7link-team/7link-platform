package governance

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
)

// InboxKinds is the complete spec08 搂6.10/6.10b catalog. A missing source is
// unavailable, never evidence that its queue is empty.
func InboxKinds() []string {
	return strings.Fields(`WORK_ITEM_REVIEW INTERACTION HUMAN_VERIFICATION CLARIFICATION_REPLY
DELIVERY_CONFIRM ESCALATION_ALERT MEMORY_RULE_APPROVAL DISPATCH_PROPOSAL INTAKE_TRIAGE
EXECUTION_ATTENTION ENSEMBLE_APPROVAL ENSEMBLE_SELECTION AGENT_SPAWN_APPROVAL
PLAN_CONFIRMATION REPORT_CONFIRMATION REPORT_BLOCKED REPORT_OVERDUE
ESCALATION_INTERNAL_EXHAUSTED REQUIREMENT_CONFLICT ANDON EXCEPTION_DECISION
PLAN_APPROVAL QUALITY_ESCAPE REVIEW_SHEET WORKLOG_APPROVAL PREVIEW_SHARE_APPROVAL`)
}

// InboxKey is the shared keyset order: priority descending, due time ascending
// (null last), created time ascending, then kind and immutable source ID.
type InboxKey struct {
	Priority  string     `json:"priority" enum:"NORMAL,HIGH,CRITICAL"`
	DueAt     *time.Time `json:"due_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Kind      string     `json:"kind"`
	ID        guid.ID    `json:"id"`
}

type InboxItem struct {
	InboxKey
	ProjectID guid.ID `json:"project_id,omitempty"`
	Title     string  `json:"title"`
	Reason    string  `json:"reason,omitempty"`
	// ObjectType and ObjectID let the web client construct an existing canonical
	// project route, instead of executing an arbitrary source-provided URL.
	ObjectType        string  `json:"object_type" enum:"work_item,execution,intake,plan,report,memory,agent,review_sheet,worklog,project,quality_escape,spawn_request,dispatch_proposal,tolerance_exception,preview_share,escalation_incident,review_plan"`
	ObjectID          guid.ID `json:"object_id"`
	SourcePrincipalID guid.ID `json:"source_principal_id,omitempty"`
	SourceRevision    string  `json:"source_revision,omitempty"`
	SourceScope       string  `json:"source_scope,omitempty" enum:"MEMBER,PROJECT,TEAM,AGENT"`
	TenantID          guid.ID `json:"-"`
	RecipientID       guid.ID `json:"-"`
	Visibility        string  `json:"-"`
}

type InboxRequest struct {
	// Transaction is supplied only by an acknowledgement command so the source
	// rechecks recipient and revision under the same authority locks.
	Transaction *gorm.DB
	TenantID    guid.ID
	PrincipalID guid.ID
	ProjectID   guid.ID
	Kind        string
	Limit       int
	After       *InboxKey
}

// InboxSource must filter by current IAM membership, resource visibility and
// decision eligibility BEFORE applying Limit. It returns at most Limit items
// after After in InboxKey order. Ordinary internal QA failures are excluded.
// Sources read current domain state; they never infer approvals from events.
type InboxSource struct {
	Kind string
	Read func(context.Context, InboxRequest) ([]InboxItem, error)
}

type InboxSources interface{ Register(InboxSource) error }

type inboxRegistry struct {
	mu      sync.RWMutex
	sources map[string]InboxSource
}

func (r *inboxRegistry) Register(source InboxSource) error {
	if r == nil {
		return ErrUnavailable
	}
	if !slices.Contains(InboxKinds(), source.Kind) || source.Read == nil {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sources == nil {
		r.sources = make(map[string]InboxSource)
	}
	if _, exists := r.sources[source.Kind]; exists {
		return ErrInvalidArgument
	}
	r.sources[source.Kind] = source
	return nil
}

type InboxCapability struct {
	Kind   string `json:"kind"`
	Status string `json:"status" enum:"AVAILABLE,UNAVAILABLE"`
}

type InboxPage struct {
	Items        []InboxItem       `json:"items"`
	NextCursor   string            `json:"next_cursor,omitempty"`
	HasMore      bool              `json:"has_more"`
	Capabilities []InboxCapability `json:"capabilities"`
}

type inboxCursor struct {
	Scope string   `json:"scope"`
	Key   InboxKey `json:"key"`
}

func inboxScope(req InboxRequest) string {
	value, _ := json.Marshal([]string{req.TenantID.String(), req.PrincipalID.String(), req.ProjectID.String(), req.Kind})
	hash := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func priorityRank(priority string) int {
	switch priority {
	case "CRITICAL":
		return 3
	case "HIGH":
		return 2
	case "NORMAL":
		return 1
	}
	return 0
}

// CompareInboxKeys is also used by providers to apply exactly the cursor order.
func CompareInboxKeys(a, b InboxKey) int {
	if x, y := priorityRank(a.Priority), priorityRank(b.Priority); x != y {
		return y - x
	}
	if a.DueAt == nil && b.DueAt != nil {
		return 1
	}
	if a.DueAt != nil && b.DueAt == nil {
		return -1
	}
	if a.DueAt != nil && b.DueAt != nil {
		if c := a.DueAt.Compare(*b.DueAt); c != 0 {
			return c
		}
	}
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	if c := strings.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}

func (r *inboxRegistry) read(ctx context.Context, req InboxRequest, cursor string) (InboxPage, error) {
	page := InboxPage{Items: []InboxItem{}, Capabilities: []InboxCapability{}}
	if r == nil {
		return page, ErrUnavailable
	}
	if req.TenantID <= 0 || req.PrincipalID <= 0 || req.ProjectID < 0 || req.After != nil || req.Limit < 1 || req.Limit > 100 || (req.Kind != "" && !slices.Contains(InboxKinds(), req.Kind)) {
		return page, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if cursor != "" {
		if len(cursor) > 2048 {
			return page, ErrInvalidArgument
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		var decoded inboxCursor
		if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.Scope != inboxScope(req) || !validInboxKey(decoded.Key) {
			return page, ErrInvalidArgument
		}
		req.After = &decoded.Key
	}
	r.mu.RLock()
	sources := make(map[string]InboxSource, len(r.sources))
	for kind, source := range r.sources {
		sources[kind] = source
	}
	r.mu.RUnlock()
	limit := req.Limit
	for _, kind := range InboxKinds() {
		if req.Kind != "" && req.Kind != kind {
			continue
		}
		source, ok := sources[kind]
		capability := InboxCapability{Kind: kind, Status: "UNAVAILABLE"}
		if ok {
			request := req
			if req.After != nil {
				key := *req.After
				if key.DueAt != nil {
					due := *key.DueAt
					key.DueAt = &due
				}
				request.After = &key
			}
			request.Kind, request.Limit = kind, limit+1
			items, err := source.Read(ctx, request)
			if err != nil && !errors.Is(err, ErrUnavailable) {
				return InboxPage{}, err
			}
			if err == nil {
				capability.Status = "AVAILABLE"
				if len(items) > limit+1 {
					return InboxPage{}, ErrInvalidArgument
				}
				for i, item := range items {
					if !validInboxKey(item.InboxKey) || item.Kind != kind || item.TenantID != req.TenantID || item.RecipientID != req.PrincipalID || item.ObjectID <= 0 || item.Title == "" || item.Visibility == "INTERNAL" || (req.ProjectID != 0 && item.ProjectID != req.ProjectID) || (req.After != nil && CompareInboxKeys(item.InboxKey, *req.After) <= 0) || (i > 0 && CompareInboxKeys(items[i-1].InboxKey, item.InboxKey) >= 0) {
						return InboxPage{}, ErrInvalidArgument
					}
				}
				page.Items = append(page.Items, items...)
			}
		}
		page.Capabilities = append(page.Capabilities, capability)
	}
	if err := ctx.Err(); err != nil {
		return InboxPage{}, err
	}
	slices.SortFunc(page.Items, func(a, b InboxItem) int { return CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
		encoded, err := json.Marshal(inboxCursor{Scope: inboxScope(req), Key: page.Items[limit-1].InboxKey})
		if err != nil {
			return InboxPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

func validInboxKey(key InboxKey) bool {
	return key.ID > 0 && priorityRank(key.Priority) > 0 && !key.CreatedAt.IsZero() && slices.Contains(InboxKinds(), key.Kind) && (key.DueAt == nil || !key.DueAt.IsZero())
}

// ExecutionAttentionInbox consolidates current execution and work failures.
type ExecutionAttentionInbox func(context.Context, InboxRequest) ([]InboxItem, error)
