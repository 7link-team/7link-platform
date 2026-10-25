package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/mail"
	"slices"
	"strings"
)

// IntakeChannelSettings represents durable intake policy, not an ingress health check.
type IntakeChannelSettings struct {
	CallbackSubscriptionID *guid.ID  `json:"callback_subscription_id,omitempty" doc:"API only: active signed intake_metadata webhook subscription in this tenant; callbacks currently cover all tenant Intake metadata"`
	TriageAgentID          *guid.ID  `json:"triage_agent_id,omitempty"`
	NotifyPrincipalIDs     []guid.ID `json:"notify_principal_ids,omitempty" maxItems:"50" uniqueItems:"true"`
	Enabled                bool      `json:"enabled"`
	DisplayName            string    `json:"display_name" maxLength:"200"`
	SupportEmail           string    `json:"support_email" maxLength:"254"`
	Language               string    `json:"language" enum:"en-US,zh-CN,ms-MY"`
	DailyLimit             int       `json:"daily_limit" minimum:"1" maximum:"10000"`
	ReceiptEnabled         bool      `json:"receipt_enabled"`
	ReminderHours          int       `json:"reminder_hours" minimum:"1" maximum:"8760"`
	TimeoutHours           int       `json:"timeout_hours" minimum:"1" maximum:"8760"`
}
type IntakeChannelView struct {
	ID                   guid.ID               `json:"id"`
	Kind                 string                `json:"kind" enum:"PORTAL,EMAIL,API"`
	Version              int                   `json:"version"`
	Settings             IntakeChannelSettings `json:"settings"`
	Availability         string                `json:"availability" enum:"AVAILABLE,UNAVAILABLE" doc:"Only API intake with enabled policy and a receiving credential is available; email and portal remain unavailable."`
	CredentialConfigured bool                  `json:"credential_configured"`
}
type IntakeChannelPath struct {
	WorkProjectPath
	Kind string `path:"kind" enum:"PORTAL,EMAIL,API"`
}
type intakeChannelSave struct {
	IntakeChannelPath
	WriteHeaders
	Body IntakeChannelSettings
}

func channelView(r *model.IntakeChannel) (IntakeChannelView, error) {
	v := IntakeChannelView{ID: r.ID, Kind: r.Kind, Version: r.Version, Availability: "UNAVAILABLE"}
	e := json.Unmarshal([]byte(r.Document), &v.Settings)
	v.CredentialConfigured = r.CredentialHash != nil && len(*r.CredentialHash) == 64
	if r.Kind == "API" && v.Settings.Enabled && v.CredentialConfigured {
		v.Availability = "AVAILABLE"
	}
	return v, e
}
func validateIntakeSettings(in IntakeChannelSettings) error {
	if len(in.NotifyPrincipalIDs) > 50 {
		return ErrInvalid
	}
	seen := map[guid.ID]bool{}
	for _, id := range in.NotifyPrincipalIDs {
		if id <= 0 || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	if in.TriageAgentID != nil && *in.TriageAgentID <= 0 {
		return ErrInvalid
	}
	if !textValid(in.DisplayName, 200) || strings.TrimSpace(in.DisplayName) == "" || !textValid(in.SupportEmail, 254) || !slices.Contains([]string{"en-US", "zh-CN", "ms-MY"}, in.Language) || in.DailyLimit < 1 || in.DailyLimit > 10000 || in.ReminderHours < 1 || in.TimeoutHours <= in.ReminderHours || in.TimeoutHours > 8760 {
		return ErrInvalid
	}
	if in.SupportEmail != "" {
		v, e := mail.ParseAddress(in.SupportEmail)
		if e != nil || v.Address != in.SupportEmail {
			return ErrInvalid
		}
	}
	return nil
}
func (m *Module) saveIntakeChannelOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, version int, in IntakeChannelSettings) (IntakeChannelView, error) {
	var out IntakeChannelView
	if e := m.intakeTriageAgentOn(ctx, tx, tenant, in); e != nil {
		return out, e
	}
	if e := m.projectAuthorityOn(ctx, tx, tenant, project, "intake_channel_manage"); e != nil {
		return out, e
	}
	if !slices.Contains([]string{"PORTAL", "EMAIL", "API"}, kind) {
		return out, ErrInvalid
	}
	if e := validateIntakeSettings(in); e != nil {
		return out, e
	}
	if e := m.validateIntakeCallbackOn(ctx, tx, tenant, kind, in.CallbackSubscriptionID); e != nil {
		return out, e
	}
	for _, id := range in.NotifyPrincipalIDs {
		if e := m.intakeNotifyEligibleOn(ctx, tx, tenant, project, id); e != nil {
			return out, e
		}
	}
	q := query.BindIntakeChannels(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.Eq(kind)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		if version != 0 {
			return out, ErrConflict
		}
		id, err := m.nextID()
		if err != nil {
			return out, err
		}
		r = &model.IntakeChannel{ID: id, TenantID: tenant, ProjectID: project, Kind: kind, Version: 0}
	} else if e != nil {
		return out, e
	} else if r.Version != version {
		return out, ErrConflict
	}
	data, e := json.Marshal(in)
	if e != nil {
		return out, e
	}
	r.Document = string(data)
	r.Version++
	r.UpdatedAt = m.now()
	r.UpdatedBy = authn.PrincipalIDFromContext(ctx)
	if e = q.WithContext(ctx).Save(r); e != nil {
		return out, e
	}
	if e = m.audit(ctx, tx, audit.NewDomainEvent(ctx, tenant, "intake.channel_configured", "intake_channel", r.ID)); e != nil {
		return out, e
	}
	view, e := channelView(r)
	if e != nil {
		return out, e
	}
	_, e = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "intake.channel_configured", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx), AuthoritySource: "permission:intake_channel_manage"}, Subject: events.Subject{Kind: "intake_channel", ID: r.ID}, SubjectVersion: int64(r.Version), Data: map[string]any{"kind": kind, "enabled": in.Enabled, "availability": view.Availability}})
	if e != nil {
		return out, e
	}
	return channelView(r)
}
func (m *Module) intakeTriageAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in IntakeChannelSettings) error {
	if in.TriageAgentID == nil {
		return nil
	}
	if *in.TriageAgentID <= 0 {
		return ErrInvalid
	}
	if m.projects.ValidateTriageAgentOn == nil {
		return ErrUnavailable
	}
	if err := m.projects.ValidateTriageAgentOn(ctx, tx, tenant, *in.TriageAgentID); err != nil {
		if errors.Is(err, delivery.ErrUnavailable) {
			return ErrUnavailable
		}
		if errors.Is(err, delivery.ErrWorkForbidden) {
			return ErrForbidden
		}
		return err
	}
	return nil
}
func (m *Module) registerIntakeChannelsHTTP(api huma.API) error {
	if err := m.registerIntakeReceivingHTTP(api); err != nil {
		return err
	}
	base := "/projects/{project_id}/intake-channels"
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"intakes"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("intake.channel.list", "GET", base, "Read durable intake channel policy and transport availability"), authz.Guard{Resource: "intake", Verb: "read", FilterData: true}, m.listIntakeChannelsHTTP)
	authz.Register(m.registrar, api, op("intake.channel.replace", "PUT", base+"/{kind}", "Save versioned intake channel policy; only credentialed API receiving is supported"), authz.Guard{Resource: "intake_channel", Verb: "manage", FilterData: true}, m.saveIntakeChannelHTTP)
	return authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+base+"/{kind}")
}
func (m *Module) listIntakeChannelsHTTP(ctx context.Context, in *WorkProjectPath) (*respx.Body[[]IntakeChannelView], error) {
	tenant, project, e := workIDs(*in)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := []IntakeChannelView{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.projectAuthorityOn(ctx, tx, tenant, project, "intake_read"); err != nil {
			return err
		}
		q := query.BindIntakeChannels(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Order(q.Kind).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			v, err := channelView(r)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) saveIntakeChannelHTTP(ctx context.Context, in *intakeChannelSave) (*commandOutput[IntakeChannelView], error) {
	tenant, project, e := workIDs(in.WorkProjectPath)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	version := 0
	if in.IfMatch != "" {
		version, e = versionHeader(in.IfMatch)
		if e != nil {
			return nil, httpError(ctx, e)
		}
	}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "intake.channel.replace:"+in.Kind+":"+project.String(), in.Body, 200, func(tx *gorm.DB) error {
		if e := m.intakeTriageAgentOn(ctx, tx, tenant, in.Body); e != nil {
			return e
		}
		return m.projectAuthorityOn(ctx, tx, tenant, project, "intake_channel_manage")
	}, func(tx *gorm.DB) (IntakeChannelView, int, error) {
		v, e := m.saveIntakeChannelOn(ctx, tx, tenant, project, in.Kind, version, in.Body)
		return v, v.Version, e
	})
}
