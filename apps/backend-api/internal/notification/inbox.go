package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"maps"
	"strings"
	"text/template"
	"time"
)

type NotificationView struct {
	ID         guid.ID    `json:"id"`
	TenantID   guid.ID    `json:"tenant_id"`
	Type       string     `json:"type"`
	Severity   string     `json:"severity" enum:"INFO,ACTION,URGENT"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	LinkPath   string     `json:"link_path"`
	MergeCount int        `json:"merge_count"`
	ReadAt     *time.Time `json:"read_at"`
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
}
type NotificationSummary struct {
	Unread         int             `json:"unread"`
	ActionRequired int             `json:"action_required"`
	Urgent         int             `json:"urgent"`
	ByTenant       []TenantSummary `json:"by_tenant"`
}
type TenantSummary struct {
	TenantID guid.ID `json:"tenant_id"`
	Unread   int     `json:"unread"`
}

// Older persisted outage notices used subject_id for the machine itself.
// Normalize only that historical template; new episodes carry machine_id.
func notificationTemplateParams(key string, params map[string]string) map[string]string {
	if !strings.HasPrefix(key, "notification.machine_offline") || params["machine_id"] != "" || params["subject_id"] == "" {
		return params
	}
	out := maps.Clone(params)
	out["machine_id"] = params["subject_id"]
	if out["source_count"] == "" {
		out["source_count"] = "1"
	}
	return out
}

func render(ctx context.Context, key string, params map[string]string) (string, error) {
	params = notificationTemplateParams(key, params)
	text := i18n.TContext(ctx, key)
	if text == key {
		return "", ErrInvalid
	}
	t, err := template.New(key).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, params)
	return b.String(), err
}
func view(ctx context.Context, r *model.Notification) (NotificationView, error) {
	var params map[string]string
	if err := json.Unmarshal([]byte(r.Params), &params); err != nil {
		return NotificationView{}, err
	}
	key := r.TemplateKey
	if key == "" {
		key = "notification.event"
	}
	title, err := render(ctx, key+"_title", params)
	if err != nil {
		return NotificationView{}, err
	}
	body, err := render(ctx, key+"_body", params)
	if err != nil {
		return NotificationView{}, err
	}
	return NotificationView{r.ID, r.TenantID, r.Type, r.Severity, title, body, r.LinkPath, r.MergeCount, r.ReadAt, r.ArchivedAt, r.CreatedAt}, nil
}

// inboxRows pages through invisible records as well, so revocations never
// produce short false-empty pages or inflate the caller's count.
func (m *Module) inboxRows(ctx context.Context, tx *gorm.DB, principal, tenant, before guid.ID, read string, limit int) ([]*model.Notification, error) {
	if principal <= 0 || tenant < 0 || before < 0 || limit < 1 {
		return nil, ErrInvalid
	}
	out := []*model.Notification{}
	q := query.BindNotifications(tx, m.names)
	for len(out) < limit {
		scope := q.WithContext(ctx).Where(q.RecipientPrincipalID.Eq(principal), q.ArchivedAt.IsNull())
		if tenant > 0 {
			scope = scope.Where(q.TenantID.Eq(tenant))
		}
		if before > 0 {
			scope = scope.Where(q.ID.Lt(before))
		}
		if read == "unread" {
			scope = scope.Where(q.ReadAt.IsNull())
		} else if read == "read" {
			scope = scope.Where(q.ReadAt.IsNotNull())
		}
		rows, err := scope.Order(q.ID.Desc()).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			before = r.ID
			yes, err := m.visibleOn(ctx, tx, r, principal)
			if err != nil {
				return nil, err
			}
			if yes {
				out = append(out, r)
				if len(out) == limit {
					return out, nil
				}
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return out, nil
}
func (m *Module) summaryOn(ctx context.Context, tx *gorm.DB, principal, tenant guid.ID) (NotificationSummary, error) {
	out := NotificationSummary{ByTenant: []TenantSummary{}}
	var before guid.ID
	byTenant := map[guid.ID]int{}
	for {
		rows, err := m.inboxRows(ctx, tx, principal, tenant, before, "unread", 100)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			before = r.ID
			out.Unread++
			byTenant[r.TenantID]++
			if r.Severity == string(Action) {
				out.ActionRequired++
			}
			if r.Severity == string(Urgent) {
				out.Urgent++
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	for id, count := range byTenant {
		out.ByTenant = append(out.ByTenant, TenantSummary{id, count})
	}
	return out, nil
}
func (m *Module) patchOn(ctx context.Context, tx *gorm.DB, principal, id guid.ID, read, archived *bool) (NotificationView, error) {
	if principal <= 0 || id <= 0 || read == nil && archived == nil {
		return NotificationView{}, ErrInvalid
	}
	if err := m.principal.LockActive(ctx, tx, principal); err != nil {
		return NotificationView{}, err
	}
	q := query.BindNotifications(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.RecipientPrincipalID.Eq(principal)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotificationView{}, ErrNotFound
	}
	if err != nil {
		return NotificationView{}, err
	}
	visible, err := m.visibleOn(ctx, tx, r, principal)
	if err != nil {
		return NotificationView{}, err
	}
	if !visible {
		return NotificationView{}, ErrNotFound
	}
	now := m.now()
	updates := map[string]any{"updated_at": now}
	if read != nil {
		r.ReadAt = nil
		if *read {
			r.ReadAt = &now
		}
		updates["read_at"] = r.ReadAt
		updates["dedupe_slot"] = nil
	}
	if archived != nil {
		r.ArchivedAt = nil
		if *archived {
			r.ArchivedAt = &now
		}
		updates["archived_at"] = r.ArchivedAt
		updates["dedupe_slot"] = nil
	}
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(id), q.RecipientPrincipalID.Eq(principal)).Updates(updates); err != nil {
		return NotificationView{}, err
	}
	m.changedOn(ctx, r)
	return view(ctx, r)
}
func (m *Module) markReadOn(ctx context.Context, tx *gorm.DB, principal, tenant, upTo guid.ID) (int, error) {
	if tenant < 0 || principal <= 0 || upTo <= 0 {
		return 0, ErrInvalid
	}
	if err := m.principal.LockActive(ctx, tx, principal); err != nil {
		return 0, err
	}
	marked := 0
	var before guid.ID
	for {
		rows, err := m.inboxRows(ctx, tx, principal, tenant, before, "unread", 100)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			before = r.ID
			if r.ID > upTo || tenant == 0 && r.TenantID != 0 {
				continue
			}
			yes := true
			if _, err = m.patchOn(ctx, tx, principal, r.ID, &yes, nil); err != nil {
				return 0, err
			}
			marked++
		}
		if len(rows) < 100 {
			break
		}
	}
	return marked, nil
}
