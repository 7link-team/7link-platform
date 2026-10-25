package clientwork

import (
	"context"
	"errors"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type IntakeThreadLink struct {
	ChannelID     guid.ID `json:"channel_id"`
	RootMessageID guid.ID `json:"root_message_id"`
	RecipientID   guid.ID `json:"recipient_id"`
}

type IntakeThreadView struct {
	IntakeID guid.ID `json:"intake_id"`
	IntakeThreadLink
	Transport string `json:"transport" enum:"CLIENT_SHARED"`
}

type IntakeThreadMessage struct {
	Body        string   `json:"body" minLength:"1" maxLength:"2000"`
	WorkItemID  *guid.ID `json:"work_item_id,omitempty"`
	WorkVersion int      `json:"work_version,omitempty"`
}

const intakeThreadPath = entityBase + "/intakes/{id}/conversation"

func (m *Module) registerIntakeThreadHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.client_merge", Method: "GET", Path: entityBase + "/intakes/{id}/client-status", Summary: "Read original-contact merge status without exposing another contact", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.clientMergeHTTP)
	guard := authz.Guard{Resource: "scope", Verb: "change"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.forward_reply", Method: "POST", Path: entityBase + "/intakes/{id}/forwarded-replies", Summary: "Forward an actual merged-source client reply into its current target thread", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 428, 500, 503}}, authz.EntityPath, owner, guard, m.forwardMergedReplyHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.conversation.link", Method: "POST", Path: intakeThreadPath, Summary: "Link the actual source contact and matching client-shared message root", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.linkIntakeThreadHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.conversation.get", Method: "GET", Path: intakeThreadPath, Summary: "Read the original client-shared source thread under current authority", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getIntakeThreadHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.conversation.reply", Method: "POST", Path: intakeThreadPath + "/messages", Summary: "Post an actual client-shared reply without claiming email or webhook delivery", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.replyIntakeThreadHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.conversation.messages", Method: "GET", Path: intakeThreadPath + "/messages", Summary: "Read actual source-thread messages under current source contact and channel authority", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.listIntakeThreadMessagesHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if err := authz.RegisterRequestContract(api, intakeThreadPath); err != nil {
		return err
	}
	if err := authz.RegisterRequestContract(api, entityBase+"/intakes/{id}/forwarded-replies"); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, intakeThreadPath+"/messages")
}

func (m *Module) listIntakeThreadMessagesHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[collab.CursorPage[collab.MessageView]], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeConversation.ReadOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out collab.CursorPage[collab.MessageView]
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
			return err
		}
		row, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		hash, err := m.validateIntakeThreadOn(ctx, tx, p, id, threadLink(row))
		if err != nil {
			return err
		}
		out, err = m.intakeConversation.ReadOn(ctx, tx, row.TenantID, p, row.ChannelID, row.RootMessageID, row.RecipientID, hash, in.ReplyCursor)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) validateIntakeThreadOn(ctx context.Context, tx *gorm.DB, project, source guid.ID, in IntakeThreadLink) (string, error) {
	if in.ChannelID <= 0 || in.RootMessageID <= 0 || in.RecipientID <= 0 {
		return "", ErrInvalid
	}
	if m.intakeSources.SourceOn == nil || m.intakeSources.RecipientOn == nil || m.intakeConversation.CheckOn == nil || m.clients == nil {
		return "", ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	if err := m.intakeSources.RecipientOn(ctx, tx, tenant, project, source, in.RecipientID); err != nil {
		return "", err
	}
	client, err := m.clients.IsClientOn(ctx, tx, tenant, project, in.RecipientID)
	if err != nil {
		return "", err
	}
	if !client {
		return "", ErrNotFound
	}
	row, err := m.intakeSources.SourceOn(ctx, tx, tenant, project, source)
	if err != nil {
		return "", err
	}
	if err := m.intakeConversation.CheckOn(ctx, tx, tenant, project, in.ChannelID, in.RootMessageID, in.RecipientID, row.RawContentHash); err != nil {
		return "", err
	}
	return row.RawContentHash, nil
}

func (m *Module) linkIntakeThreadHTTP(ctx context.Context, in *EditInput[IntakeThreadLink]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	check := func(tx *gorm.DB) error { _, err := m.validateIntakeThreadOn(ctx, tx, p, id, in.Body); return err }
	return m.command(ctx, p, "clientwork.intake.conversation.link", "POST", intakeThreadPath, in.Key, 201, in, "scope_change", false, check, func(tx *gorm.DB) (Receipt, error) {
		tenant := authn.TenantIDFromContext(ctx)
		source, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, id)
		if err != nil {
			return Receipt{}, err
		}
		if source.Version != v {
			return Receipt{}, ErrConflict
		}
		q := query.BindIntakeThread(tx, m.names)
		_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.IntakeID.Eq(id)).First()
		if err == nil {
			return Receipt{}, ErrConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return Receipt{}, err
		}
		row := &model.IntakeThread{TenantID: tenant, ProjectID: p, IntakeID: id, ChannelID: in.Body.ChannelID, RootMessageID: in.Body.RootMessageID, RecipientID: in.Body.RecipientID, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
		if err := q.WithContext(ctx).Create(row); err != nil {
			return Receipt{}, err
		}
		if err := m.event(ctx, tx, p, id, "intake_thread_linked", "scope_change", 1); err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: id, Version: 1, State: "LINKED"}, nil
	})
}

func (m *Module) intakeThreadOn(ctx context.Context, tx *gorm.DB, project, source guid.ID) (*model.IntakeThread, error) {
	q := query.BindIntakeThread(tx, m.names)
	return q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.IntakeID.Eq(source)).First()
}

func threadLink(row *model.IntakeThread) IntakeThreadLink {
	return IntakeThreadLink{ChannelID: row.ChannelID, RootMessageID: row.RootMessageID, RecipientID: row.RecipientID}
}

// A missing link preserves in-app availability. A linked source must publish
// through its current real channel or roll back, never claim a dropped send.
func (m *Module) postLinkedIntakeMessageOn(ctx context.Context, tx *gorm.DB, project, source guid.ID, body string) error {
	row, err := m.intakeThreadOn(ctx, tx, project, source)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	hash, err := m.validateIntakeThreadOn(ctx, tx, project, source, threadLink(row))
	if err != nil {
		return err
	}
	if m.intakeConversation.ReplyOn == nil {
		return ErrUnavailable
	}
	_, err = m.intakeConversation.ReplyOn(ctx, tx, row.TenantID, project, row.ChannelID, row.RootMessageID, row.RecipientID, hash, body)
	return err
}

func (m *Module) getIntakeThreadHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[IntakeThreadView], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out IntakeThreadView
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
			return err
		}
		row, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if _, err = m.validateIntakeThreadOn(ctx, tx, p, id, threadLink(row)); err != nil {
			return err
		}
		out = IntakeThreadView{IntakeID: id, IntakeThreadLink: threadLink(row), Transport: "CLIENT_SHARED"}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) replyIntakeThreadHTTP(ctx context.Context, in *EditInput[IntakeThreadMessage]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if (in.Body.WorkItemID == nil && in.Body.WorkVersion != 0) || (in.Body.WorkItemID != nil && (*in.Body.WorkItemID <= 0 || in.Body.WorkVersion < 1)) {
		return nil, httpError(ctx, ErrInvalid)
	}
	var boundOwner guid.ID
	check := func(tx *gorm.DB) error {
		row, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		_, err = m.validateIntakeThreadOn(ctx, tx, p, id, threadLink(row))
		if err != nil || in.Body.WorkItemID == nil {
			return err
		}
		source, err := m.intakeSources.SourceOn(ctx, tx, row.TenantID, p, id)
		if err != nil {
			return err
		}
		if source.RequirementID == nil {
			return ErrConflict
		}
		if m.intakeWorkOwner == nil || m.intakeConversation.BindWorkOn == nil {
			return ErrUnavailable
		}
		boundOwner, err = m.intakeWorkOwner(ctx, tx, row.TenantID, p, *source.RequirementID, *in.Body.WorkItemID, in.Body.WorkVersion)
		if err != nil {
			return err
		}
		if boundOwner != authn.PrincipalIDFromContext(ctx) {
			return ErrForbidden
		}
		return nil
	}
	return m.command(ctx, p, "clientwork.intake.conversation.reply", "POST", intakeThreadPath+"/messages", in.Key, 201, in, "scope_change", false, check, func(tx *gorm.DB) (Receipt, error) {
		if !validText(in.Body.Body, 2000) {
			return Receipt{}, ErrInvalid
		}
		row, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return Receipt{}, err
		}
		source, err := m.intakeSources.SourceOn(ctx, tx, row.TenantID, p, id)
		if err != nil {
			return Receipt{}, err
		}
		if source.Version != v {
			return Receipt{}, ErrConflict
		}
		if m.intakeConversation.ReplyOn == nil {
			return Receipt{}, ErrUnavailable
		}
		message, err := m.intakeConversation.ReplyOn(ctx, tx, row.TenantID, p, row.ChannelID, row.RootMessageID, row.RecipientID, source.RawContentHash, in.Body.Body)
		if err != nil {
			return Receipt{}, err
		}
		if in.Body.WorkItemID != nil {
			if err = m.bindIntakeReplyWorkOn(ctx, tx, row.TenantID, p, id, message, *in.Body.WorkItemID, boundOwner); err != nil {
				return Receipt{}, err
			}
		}
		if err = m.event(ctx, tx, p, id, "intake_thread_replied", "scope_change", 1); err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: message, Version: 1, State: "POSTED"}, nil
	}, func(tx *gorm.DB) error {
		return m.lockIntakeReplyWorkOn(ctx, tx, p, in.Body.WorkItemID, in.Body.WorkVersion)
	})
}

func (m *Module) clientMergeHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.ClientMergeProjection], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.ClientMergeOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.ClientMergeProjection
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, err := m.authorize(ctx, tx, p, "channel_read", true)
		if err != nil {
			return err
		}
		if !external {
			return ErrNotFound
		}
		out, err = m.intakeSources.ClientMergeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type MergedReplyBody struct {
	MessageID   guid.ID  `json:"message_id"`
	WorkItemID  *guid.ID `json:"work_item_id,omitempty"`
	WorkVersion int      `json:"work_version,omitempty"`
}

func (m *Module) forwardMergedReplyHTTP(ctx context.Context, in *EditInput[MergedReplyBody]) (*CommandOutput, error) {
	p, id, version, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil || in.Body.MessageID <= 0 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.intakeSources.MergeRouteOn == nil || m.intakeConversation.ForwardOn == nil || m.intakeSources.SourceOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	if (in.Body.WorkItemID == nil && in.Body.WorkVersion != 0) || (in.Body.WorkItemID != nil && (*in.Body.WorkItemID <= 0 || in.Body.WorkVersion < 1)) {
		return nil, httpError(ctx, ErrInvalid)
	}
	var boundOwner guid.ID
	var route intake.MergeRoute
	check := func(tx *gorm.DB) error {
		var err error
		route, err = m.intakeSources.MergeRouteOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		if err != nil {
			return err
		}
		if route.SourceID == route.TargetID {
			return ErrConflict
		}
		if in.Body.WorkItemID != nil {
			if route.TargetRequirementID == nil {
				return ErrConflict
			}
			if m.intakeWorkOwner == nil || m.intakeConversation.BindWorkOn == nil {
				return ErrUnavailable
			}
			boundOwner, err = m.intakeWorkOwner(ctx, tx, authn.TenantIDFromContext(ctx), p, *route.TargetRequirementID, *in.Body.WorkItemID, in.Body.WorkVersion)
			if err != nil {
				return err
			}
		}
		for _, source := range []guid.ID{id, route.TargetID} {
			row, err := m.intakeThreadOn(ctx, tx, p, source)
			if err != nil {
				return err
			}
			if _, err = m.validateIntakeThreadOn(ctx, tx, p, source, threadLink(row)); err != nil {
				return err
			}
		}
		return nil
	}
	return m.command(ctx, p, "clientwork.intake.forward_reply", "POST", entityBase+"/intakes/"+id.String()+"/forwarded-replies", in.Key, 201, in, "scope_change", false, check, func(tx *gorm.DB) (Receipt, error) {
		tenant := authn.TenantIDFromContext(ctx)
		source, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, id)
		if err != nil {
			return Receipt{}, err
		}
		if source.Version != version {
			return Receipt{}, ErrConflict
		}
		target, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, route.TargetID)
		if err != nil {
			return Receipt{}, err
		}
		from, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return Receipt{}, err
		}
		to, err := m.intakeThreadOn(ctx, tx, p, route.TargetID)
		if err != nil {
			return Receipt{}, err
		}
		message, err := m.intakeConversation.ForwardOn(ctx, tx, tenant, p, collab.IntakeReplyForward{SourceChannelID: from.ChannelID, SourceRootID: from.RootMessageID, SourceRecipientID: from.RecipientID, MessageID: in.Body.MessageID, SourceHash: source.RawContentHash, SourceRef: route.SourceRef, TargetChannelID: to.ChannelID, TargetRootID: to.RootMessageID, TargetRecipientID: to.RecipientID, TargetHash: target.RawContentHash})
		if err != nil {
			return Receipt{}, err
		}
		if in.Body.WorkItemID != nil {
			if err = m.bindIntakeReplyWorkOn(ctx, tx, tenant, p, id, message, *in.Body.WorkItemID, boundOwner); err != nil {
				return Receipt{}, err
			}
		}
		if err = m.event(ctx, tx, p, id, "intake_thread_replied", "scope_change", source.Version); err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: message, Version: 1, State: "POSTED"}, nil
	}, func(tx *gorm.DB) error {
		return m.lockIntakeReplyWorkOn(ctx, tx, p, in.Body.WorkItemID, in.Body.WorkVersion)
	})
}

func (m *Module) lockIntakeReplyWorkOn(ctx context.Context, tx *gorm.DB, project guid.ID, work *guid.ID, version int) error {
	if work == nil {
		return nil
	}
	if m.intakeWorkFence == nil {
		return ErrUnavailable
	}
	return m.intakeWorkFence(ctx, tx, authn.TenantIDFromContext(ctx), project, *work, version)
}

func (m *Module) bindIntakeReplyWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, source, message, work, owner guid.ID) error {
	if err := m.intakeConversation.BindWorkOn(ctx, tx, tenant, project, message, work); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, tenant, "clientwork.intake_thread_work_bound", Name, source)
	event.Detail["message_id"], event.Detail["work_item_id"], event.Detail["accountable_principal_id"] = message.String(), work.String(), owner.String()
	return m.audit(ctx, tx, event)
}
