package notification

import (
	activityi18n "7link.com/platform/internal/notification/i18n"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func templateLifecycleType(typ string) bool {
	switch typ {
	case "verifier.calibration_drifted", "provider.unavailable", "eval.below_target", "rubric.divergence_detected", "verifier.tool_unavailable":
		return true
	}
	return false
}
func (m *Module) injectTemplateLifecycle(s *module.Services) error {
	facts, err := module.Resolve[templates.LifecycleFacts](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if facts.CurrentOn == nil {
		return ErrInvalid
	}
	m.templateLifecycle = facts
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_notification_template_lifecycle_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_notification_template_lifecycle_description", Name: "notification.template_lifecycle", EventTypes: []string{"verifier.calibration_changed", "provider.state_changed", "eval.run_completed", "rubric.divergence_detected", "verifier.tool_availability_changed"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.projectTemplateLifecycleOn(eventCtx, tx, events.RecordedEvent{ID: e.EventID, TenantID: e.TenantID, ProjectID: e.ProjectID, Type: e.EventType, SubjectID: e.Subject.ID, SubjectKind: e.Subject.Kind}); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}})
}

// Event payloads are wakeups only. Source-owned immutable receipts determine
// conditions and counts; the current source and IAM decide visibility again.
func (m *Module) projectTemplateLifecycleOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "template_lifecycle" {
		return ErrInvalid
	}
	if m.templateLifecycle.CurrentOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	f, found, err := m.templateLifecycle.CurrentOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found {
		return err
	}
	if f.ID != e.SubjectID || f.EventType != e.Type || f.ProjectID != e.ProjectID {
		return ErrInvalid
	}
	if !templateLifecycleType(f.NoticeType) {
		return nil
	}
	if err = m.projectTemplateLifecycleRecipientsOn(ctx, tx, e, f); err != nil {
		return err
	}
	if f.NoticeType == "provider.unavailable" && f.ProjectID == 0 {
		for _, project := range f.AffectedProjectIDs {
			affected := f
			affected.ProjectID = project
			if err = m.projectTemplateLifecycleRecipientsOn(ctx, tx, e, affected); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Module) projectTemplateLifecycleRecipientsOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, f templates.LifecycleView) error {
	params := map[string]string{"subject_id": f.ID.String()}
	for k, v := range f.Params {
		params[k] = v
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	permission := "template_manage"
	if f.ProjectID > 0 {
		permission = "project_read"
	}
	link := "/admin/" + e.TenantID.String() + "/templates/" + f.TemplateVersionID.String() + "/workflow"
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			row := &model.Notification{TenantID: e.TenantID, ProjectID: f.ProjectID, RecipientPrincipalID: id, Type: f.NoticeType, Permission: permission, Params: string(raw), LinkPath: link}
			yes, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if m.guard == nil {
				return ErrUnavailable
			}
			if err = m.principal.LockActive(ctx, tx, id); err != nil {
				return err
			}
			if err = m.guard(ctx, tx, "notification:template:"+e.TenantID.String()+":"+id.String()); err != nil {
				return err
			}
			dedupe := "template:" + f.ObjectKey + ":" + f.ID.String()
			channels := []ChannelName{}
			switch f.NoticeType {
			case "verifier.calibration_drifted":
				channels = []ChannelName{Email}
				condition := sha256.Sum256([]byte(f.Params["verifier"]))
				prefix := "template-calibration:" + hex.EncodeToString(condition[:]) + ":"
				dedupe = prefix + f.ID.String()
				q := query.BindNotifications(tx, m.names)
				n, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.RecipientPrincipalID.Eq(id), q.Type.Eq(f.NoticeType), q.DedupeKey.Like(prefix+"%"), q.CreatedAt.Gt(m.now().Add(-7*24*time.Hour))).Count()
				if err != nil {
					return err
				}
				if n > 0 {
					continue
				}
			case "provider.unavailable":
				channels = []ChannelName{WebPush}
			case "verifier.tool_unavailable":
				hash := sha256.Sum256([]byte(f.Reference))
				dedupe = "template-tool:" + hex.EncodeToString(hash[:]) + ":" + m.now().UTC().Format("2006-01-02")
			case "rubric.divergence_detected":
				hash := sha256.Sum256([]byte(f.ProjectID.String() + ":" + f.Reference))
				dedupe = "template-rubric:" + hex.EncodeToString(hash[:])
			}
			_, err = m.Notify(ctx, tx, Notification{Channels: channels, TenantID: e.TenantID, ProjectID: f.ProjectID, Recipients: []guid.ID{id}, Type: f.NoticeType, Severity: Action, Permission: permission, TemplateKey: "notification." + strings.ReplaceAll(f.NoticeType, ".", "_"), Params: params, LinkPath: link, DedupeKey: dedupe})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}

func (m *Module) templateLifecycleAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.templateLifecycle.CurrentOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	f, found, err := m.templateLifecycle.CurrentOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || f.ID != id || f.NoticeType != row.Type {
		return false, err
	}
	if f.ProjectID != row.ProjectID && !(row.Type == "provider.unavailable" && f.ProjectID == 0 && slices.Contains(f.AffectedProjectIDs, row.ProjectID)) {
		return false, nil
	}
	switch row.Type {
	case "eval.below_target":
		return f.InitiatorID == principal, nil
	case "rubric.divergence_detected":
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	case "verifier.calibration_drifted":
		if f.QualityOwnerID > 0 && f.QualityOwnerID == principal {
			return true, nil
		}
	case "provider.unavailable":
		if f.ProjectID == 0 && row.ProjectID > 0 {
			return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
		}
		if row.ProjectID > 0 {
			yes, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
			if err != nil || yes {
				return yes, err
			}
		}
	case "verifier.tool_unavailable":
	default:
		return false, ErrInvalid
	}
	return m.templateTeamOwnerOn(ctx, tx, row.TenantID, principal)
}
func (m *Module) templateTeamOwnerOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (bool, error) {
	if m.roles.RoleIDOn == nil || m.explain == nil {
		return false, ErrUnavailable
	}
	role, err := m.roles.RoleIDOn(ctx, tx, tenant, "team_owner")
	if errors.Is(err, governance.ErrRoleNotSeeded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: principal, Permission: "template_manage"})
	if err != nil || !e.Allowed {
		return false, err
	}
	for _, match := range e.Matches {
		if match.Effect == "allow" && match.RoleID == role.String() {
			return true, nil
		}
	}
	return false, nil
}
