package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type Severity string

const (
	Info   Severity = "INFO"
	Action Severity = "ACTION"
	Urgent Severity = "URGENT"
)

type Notification struct {
	Channels                        []ChannelName
	TenantID, ProjectID             guid.ID
	Recipients                      []guid.ID
	Type                            string
	Severity                        Severity
	TemplateKey                     string
	Params                          map[string]string
	LinkPath, DedupeKey, Permission string
}

// Notifier always persists the in-app channel in the caller's transaction.
// External transport is a distinct capability and is never claimed delivered.
type Notifier interface {
	Notify(context.Context, *gorm.DB, Notification) ([]guid.ID, error)
	SendEmail(context.Context, *gorm.DB, EmailMessage) (guid.ID, error)
}

var typePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

func validTransaction(tx *gorm.DB) bool {
	if tx == nil || tx.Statement == nil {
		return false
	}
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}
func validLink(path string) bool {
	if path == "" {
		return true
	}
	u, err := url.Parse(path)
	return err == nil && len(path) <= 512 && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.ContainsAny(path, "\\\r\n") && !u.IsAbs() && u.Host == ""
}
func (m *Module) Notify(ctx context.Context, tx *gorm.DB, n Notification) ([]guid.ID, error) {
	if !validTransaction(tx) || n.TenantID < 0 || n.ProjectID < 0 || len(n.Recipients) < 1 || len(n.Recipients) > 200 || !typePattern.MatchString(n.Type) || len(n.Type) > 96 || len(n.DedupeKey) > 160 || len(n.TemplateKey) > 160 || !validLink(n.LinkPath) {
		return nil, ErrInvalid
	}
	if n.TenantID == 0 && n.Permission != "system_job_manage" && n.Permission != "system_event_manage" {
		return nil, ErrInvalid
	}
	if n.Severity != Info && n.Severity != Action && n.Severity != Urgent {
		return nil, ErrInvalid
	}
	if m == nil || m.ids == nil || m.principal.LockActive == nil || m.principal.ActiveHumanByID == nil || m.members == nil {
		return nil, ErrUnavailable
	}
	if m.config.Email.Enabled {
		if m.admission == nil || m.guard == nil {
			return nil, ErrUnavailable
		}
		if err := m.admission(ctx, tx, n.TenantID); err != nil {
			return nil, err
		}
		if err := m.guard(ctx, tx, "notification:rate"); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(n.Params)
	if err != nil || len(data) > 8192 {
		return nil, ErrInvalid
	}
	recipients := slices.Clone(n.Recipients)
	slices.Sort(recipients)
	recipients = slices.Compact(recipients)
	if recipients[0] <= 0 {
		return nil, ErrInvalid
	}
	out := []guid.ID{}
	for _, recipient := range recipients {
		if err = m.principal.LockActive(ctx, tx, recipient); err != nil {
			return nil, err
		}
		_, active, err := m.principal.ActiveHumanByID(ctx, tx, recipient)
		if err != nil {
			return nil, err
		}
		if !active {
			continue
		}
		member, err := m.recipientScopeOn(ctx, tx, n.TenantID, recipient, n.Permission)
		if err != nil {
			return nil, err
		}
		if !member {
			continue
		}
		now := m.now()
		q := query.BindNotifications(tx, m.names)
		var slot *string
		if n.DedupeKey != "" {
			slot = &n.DedupeKey
			old, err := q.WithContext(ctx).Where(q.TenantID.Eq(n.TenantID), q.RecipientPrincipalID.Eq(recipient), q.DedupeSlot.Eq(n.DedupeKey)).First()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if old != nil {
				if mergeWindow(n.Type) == 0 {
					out = append(out, old.ID)
					continue
				}
				if old.ReadAt == nil && old.ArchivedAt == nil && old.CreatedAt.After(now.Add(-mergeWindow(n.Type))) {
					_, err = q.WithContext(ctx).Where(q.ID.Eq(old.ID)).Updates(map[string]any{"params": string(data), "merge_count": old.MergeCount + 1, "updated_at": now, "last_merged_at": now})
					if err != nil {
						return nil, err
					}
					m.changedOn(ctx, old)
					out = append(out, old.ID)
					continue
				}
				if _, err = q.WithContext(ctx).Where(q.ID.Eq(old.ID)).Update(q.DedupeSlot, nil); err != nil {
					return nil, err
				}
			}
		}
		id, err := m.ids.Next()
		if err != nil {
			return nil, err
		}
		if id <= 0 {
			return nil, ErrUnavailable
		}
		row := model.Notification{ID: guid.ID(id), TenantID: n.TenantID, ProjectID: n.ProjectID, RecipientPrincipalID: recipient, Type: n.Type, Severity: string(n.Severity), TemplateKey: n.TemplateKey, Params: string(data), LinkPath: n.LinkPath, Permission: n.Permission, DedupeKey: slot, DedupeSlot: slot, MergeCount: 1, CreatedAt: now, UpdatedAt: now, LastMergedAt: now}
		if err = q.WithContext(ctx).Create(&row); err != nil {
			return nil, err
		}
		if err = m.notificationEmailOn(ctx, tx, &row, n.Channels); err != nil {
			return nil, err
		}
		m.changedOn(ctx, &row)
		out = append(out, row.ID)
	}
	return out, nil
}

type inboxChangesKey struct{}
type inboxChanges map[string]bool

func collectInboxChanges(ctx context.Context) (context.Context, inboxChanges) {
	changes := inboxChanges{}
	return context.WithValue(ctx, inboxChangesKey{}, changes), changes
}

// changedOn records only a content-free invalidation. The projector and HTTP
// mutations flush their collection after committing their owning transaction.
func (m *Module) changedOn(ctx context.Context, r *model.Notification) {
	if changes, ok := ctx.Value(inboxChangesKey{}).(inboxChanges); ok {
		changes["tenant/"+r.TenantID.String()+"/inbox/"+r.RecipientPrincipalID.String()] = true
	}
}

func (m *Module) broadcastInboxChanges(ctx context.Context, changes inboxChanges) {
	if m.broadcast == nil {
		return
	}
	for topic := range changes {
		// A missed realtime hint never reverses an already committed inbox
		// change; the client also reconciles on reconnect and by polling.
		_ = m.broadcast(ctx, topic, realtime.Message{Type: "notification.created", Payload: map[string]any{}})
	}
}

// visibleOn is shared by list, summary and mutations. Permission revocation
// removes both content and counts; no stored notification grants authority.
func (m *Module) visibleOn(ctx context.Context, tx *gorm.DB, r *model.Notification, principal guid.ID) (bool, error) {
	if principal <= 0 || r.RecipientPrincipalID != principal {
		return false, nil
	}
	_, human, err := m.principal.ActiveHumanByID(ctx, tx, principal)
	if err != nil || !human {
		return false, err
	}
	member, err := m.recipientScopeOn(ctx, tx, r.TenantID, principal, r.Permission)
	if err != nil || !member {
		return false, err
	}
	if r.TenantID == 0 {
		return member, nil
	}
	if r.Type == "dispatch.confirmation_required" {
		return m.collaborationAudienceOn(ctx, tx, r, principal)
	}
	// Source eligibility evaluates the actual project's owner-scoped send grant;
	// a generic entity-ID filter cannot evaluate that source ownership.
	if r.Type == "delivery.confirmation_requested" {
		return m.deliveryConfirmationAudienceOn(ctx, tx, r, principal)
	}
	if (r.Type == "machine.offline" && r.Permission == "") || r.Type == "machine.recovered" {
		return m.machineAudienceOn(ctx, tx, r, principal)
	}
	if r.Type == "fleet.agent_error_persisting" {
		if m.activity.SnapshotOn == nil {
			return false, ErrUnavailable
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(r.Params), &params); err != nil {
			return false, err
		}
		agent, err := guid.RequireID(params["subject_id"])
		if err != nil {
			return false, ErrInvalid
		}
		current, exists, err := m.activity.SnapshotOn(ctx, tx, r.TenantID, agent)
		return exists && current.ResponsiblePrincipalID == principal, err
	}
	if r.Type == "channel.mentioned" {
		if m.mentions.VisibleOn == nil {
			return false, ErrUnavailable
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(r.Params), &params); err != nil {
			return false, err
		}
		message, err := guid.RequireID(params["subject_id"])
		if err != nil {
			return false, ErrInvalid
		}
		return m.mentions.VisibleOn(ctx, tx, r.TenantID, message, principal)
	}
	if r.Type == "governance.delegation_ended" || r.Type == "governance.delegation_expiring" {
		return m.sourceAudienceOn(ctx, tx, r, principal)
	}
	if r.Permission == "" {
		if r.Type == "worklog.approval_pending" {
			return m.worklogAudienceOn(ctx, tx, r, principal)
		}
		return true, nil
	}
	eligible, err := m.sourceAudienceOn(ctx, tx, r, principal)
	if err != nil || !eligible {
		return false, err
	}
	if r.ProjectID > 0 {
		if r.Permission == "project_read" {
			if m.projectAuthority == nil {
				return false, ErrUnavailable
			}
			return m.projectAuthority(ctx, tx, r.TenantID, r.ProjectID, principal)
		}
		c, err := m.entityPermission(ctx, tx, r.TenantID, r.ProjectID, r.Permission, principal)
		return c.AllowsEntityRows(r.ProjectID) && (c.AllowAll || slices.Contains(c.ResourceIDs, r.ProjectID.String())), err
	}
	c, err := m.tenantPermission(ctx, tx, r.TenantID, r.Permission, principal)
	return c.AllowAll, err
}

func (m *Module) recipientScopeOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, permission string) (bool, error) {
	if tenant > 0 {
		return m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	}
	if tenant < 0 || m.platformPermission == nil {
		return false, ErrUnavailable
	}
	if permission == "system_job_manage" || permission == "system_event_manage" {
		return m.platformPermission(ctx, tx, permission, principal)
	}
	for _, code := range []string{"system_job_manage", "system_event_manage"} {
		ok, err := m.platformPermission(ctx, tx, code, principal)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
