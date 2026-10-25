package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type InboxCounts struct {
	Total        int               `json:"total"`
	ByKind       map[string]int    `json:"by_kind"`
	ByPriority   map[string]int    `json:"by_priority"`
	Revision     string            `json:"revision"`
	ObservedAt   time.Time         `json:"observed_at"`
	Capabilities []InboxCapability `json:"capabilities"`
}

func inboxRevision(item InboxItem) string {
	raw, _ := json.Marshal([]string{item.Kind, item.ID.String(), item.SourceRevision, item.CreatedAt.UTC().Format(time.RFC3339Nano)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Module) acknowledgedInbox(ctx context.Context, item InboxItem) (bool, error) {
	if item.Kind != "CLARIFICATION_REPLY" {
		return false, nil
	}
	db, err := m.writer()
	if err != nil {
		return false, err
	}
	q := query.BindInboxAcknowledgements(db, m.seed.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(item.TenantID), q.PrincipalID.Eq(item.RecipientID), q.Kind.Eq(item.Kind), q.SourceID.Eq(item.ID), q.SourceRevision.Eq(inboxRevision(item))).Count()
	return n > 0, err
}

// readInbox keeps paging the authorized sources after suppressed acknowledgements;
// suppression cannot turn a full source page into an apparent end of queue.
func (m *Module) readInbox(ctx context.Context, req InboxRequest, cursor string, includeAcknowledged bool) (InboxPage, error) {
	out := InboxPage{Items: []InboxItem{}}
	if m == nil || req.Limit < 1 || req.Limit > 100 {
		return out, ErrInvalidArgument
	}
	sourceReq := req
	sourceReq.Limit = 100
	for {
		page, err := m.inbox.read(ctx, sourceReq, cursor)
		if err != nil {
			return InboxPage{}, err
		}
		out.Capabilities = page.Capabilities
		for _, item := range page.Items {
			ack := false
			if !includeAcknowledged {
				ack, err = m.acknowledgedInbox(ctx, item)
				if err != nil {
					return InboxPage{}, err
				}
			}
			if !ack {
				out.Items = append(out.Items, item)
			}
			if len(out.Items) > req.Limit {
				out.Items = out.Items[:req.Limit]
				out.HasMore = true
				raw, _ := json.Marshal(inboxCursor{Scope: inboxScope(req), Key: out.Items[req.Limit-1].InboxKey})
				out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
				return out, nil
			}
		}
		if !page.HasMore {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

func (m *Module) inboxItem(ctx context.Context, tenant guid.ID, kind string, id guid.ID, includeAcknowledged bool) (InboxItem, error) {
	return m.inboxItemOn(ctx, nil, tenant, kind, id, includeAcknowledged)
}

func (m *Module) inboxItemOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, id guid.ID, includeAcknowledged bool) (InboxItem, error) {
	if tenant != authn.TenantIDFromContext(ctx) || authn.PrincipalIDFromContext(ctx) <= 0 {
		return InboxItem{}, ErrForbidden
	}
	if id <= 0 || kind == "" {
		return InboxItem{}, ErrInvalidArgument
	}
	req := InboxRequest{Transaction: tx, TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), Kind: kind, Limit: 100}
	cursor := ""
	for {
		page, err := m.readInbox(ctx, req, cursor, includeAcknowledged)
		if err != nil {
			return InboxItem{}, err
		}
		for _, item := range page.Items {
			if item.ID == id {
				return item, nil
			}
		}
		if !page.HasMore {
			return InboxItem{}, ErrNotFound
		}
		cursor = page.NextCursor
	}
}

func (m *Module) inboxCounts(ctx context.Context, req InboxRequest) (InboxCounts, error) {
	out := InboxCounts{ByKind: map[string]int{}, ByPriority: map[string]int{}, ObservedAt: time.Now().UTC()}
	req.Limit = 100
	cursor := ""
	hash := sha256.New()
	for {
		page, err := m.readInbox(ctx, req, cursor, false)
		if err != nil {
			return InboxCounts{}, err
		}
		out.Capabilities = page.Capabilities
		for _, c := range page.Capabilities {
			if c.Status == "AVAILABLE" {
				if _, ok := out.ByKind[c.Kind]; !ok {
					out.ByKind[c.Kind] = 0
				}
			}
		}
		for _, item := range page.Items {
			out.Total++
			out.ByKind[item.Kind]++
			out.ByPriority[item.Priority]++
			hash.Write([]byte(inboxRevision(item)))
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	out.Revision = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}

func (m *Module) acknowledgeInboxOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, id guid.ID, revision string) (InboxItem, error) {
	if tx == nil || tx.Statement == nil {
		return InboxItem{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return InboxItem{}, ErrInvalidArgument
	}
	if kind != "CLARIFICATION_REPLY" && kind != "ESCALATION_ALERT" {
		return InboxItem{}, ErrInvalidArgument
	}
	if tenant != authn.TenantIDFromContext(ctx) || authn.PrincipalIDFromContext(ctx) <= 0 {
		return InboxItem{}, ErrForbidden
	}
	if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil {
		return InboxItem{}, ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, authn.PrincipalIDFromContext(ctx)); err != nil {
		return InboxItem{}, err
	}
	if _, active, err := m.principals.ActiveHumanByID(ctx, tx, authn.PrincipalIDFromContext(ctx)); err != nil {
		return InboxItem{}, err
	} else if !active {
		return InboxItem{}, ErrForbidden
	}
	if kind == "ESCALATION_ALERT" {
		if err := m.authorizeHumanOn(ctx, tx, tenant, "policy_read"); err != nil {
			return InboxItem{}, err
		}
		q := query.BindEscalationIncidents(tx, m.seed.names)
		row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return InboxItem{}, ErrNotFound
		}
		if err != nil {
			return InboxItem{}, err
		}
		allowed, err := m.canReadIncidentProject(ctx, tx, tenant, row.ProjectID)
		if err != nil {
			return InboxItem{}, err
		}
		if !allowed {
			return InboxItem{}, ErrNotFound
		}
		allowed, err = m.escalationInboxRecipientOn(ctx, tx, tenant, row.ProjectID, authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return InboxItem{}, err
		}
		if !allowed {
			return InboxItem{}, ErrNotFound
		}
		if row.State != "OPEN" && row.State != "ACKNOWLEDGED" {
			return InboxItem{}, ErrNotFound
		}
		item := InboxItem{InboxKey: InboxKey{Kind: kind, ID: id, Priority: "HIGH", CreatedAt: row.CreatedAt}, ProjectID: row.ProjectID, Title: row.Kind, Reason: row.Reason, ObjectType: "escalation_incident", ObjectID: id, TenantID: tenant, RecipientID: authn.PrincipalIDFromContext(ctx), Visibility: "HUMAN"}
		if revision != inboxRevision(item) {
			return InboxItem{}, ErrVersion
		}
		_, err = m.incidentDecisionOn(ctx, tx, tenant, id, row.Version, "ACKNOWLEDGED", "")
		return item, err
	}
	item, err := m.inboxItemOn(ctx, tx, tenant, kind, id, true)
	if err != nil {
		return InboxItem{}, err
	}
	if revision != inboxRevision(item) {
		return InboxItem{}, ErrVersion
	}

	q := query.BindInboxAcknowledgements(tx, m.seed.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(item.RecipientID), q.Kind.Eq(kind), q.SourceID.Eq(id), q.SourceRevision.Eq(revision)).Take()
	if err == nil && old != nil {
		return item, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return InboxItem{}, err
	}
	next, err := m.seed.ids.Next()
	if err != nil {
		return InboxItem{}, err
	}
	if next <= 0 {
		return InboxItem{}, ErrUnavailable
	}
	row := &model.InboxAcknowledgement{ID: guid.ID(next), TenantID: tenant, PrincipalID: item.RecipientID, Kind: kind, SourceID: id, SourceRevision: revision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return InboxItem{}, err
	}
	err = m.governanceEventOn(ctx, tx, tenant, item.ProjectID, row.ID, 1, "governance.inbox_acknowledged", "inbox_item", map[string]any{"kind": kind, "source_id": id.String(), "source_revision": revision})
	return item, err
}
