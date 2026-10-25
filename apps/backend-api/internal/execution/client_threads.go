package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClientDeliveryItem is resolved by the delivery owner under current IAM in the
// caller's transaction. It is never accepted directly from a transport input.
type ClientDeliveryItem struct {
	WorkItemID, EvidenceID, VersionID, ContactID guid.ID
	SHA256                                       string
}

type ClientCommentAuthority struct {
	OpenOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error)
	ItemOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, bool) (ClientDeliveryItem, error)
}

type ClientCommentAuthorityConnector struct {
	Bind func(ClientCommentAuthority) error
}

type ClientThreadCreate struct {
	AnchorKind      string                `json:"anchor_kind,omitempty" enum:"CODE_LINES,TABLE_ROW,HTML_REGION,TEXT_PARAGRAPH,MEDIA_TIME"`
	ViewerKind      string                `json:"viewer_kind,omitempty" enum:"CODE,DOCUMENT,WEB_DEVICE,REPORT"`
	RowIndex        *int                  `json:"row_index,omitempty" minimum:"0"`
	RowSHA256       string                `json:"row_sha256,omitempty" pattern:"^[a-f0-9]{64}$"`
	ParagraphIndex  *int                  `json:"paragraph_index,omitempty" minimum:"0"`
	ParagraphSHA256 string                `json:"paragraph_sha256,omitempty" pattern:"^[a-f0-9]{64}$"`
	TextQuote       string                `json:"text_quote,omitempty" maxLength:"500"`
	Selector        string                `json:"selector,omitempty" maxLength:"512"`
	Rect            *ClientAnchorRect     `json:"rect,omitempty"`
	Viewport        *ClientAnchorViewport `json:"viewport,omitempty"`
	Scroll          *ClientAnchorPoint    `json:"scroll,omitempty"`
	StartLine       int                   `json:"start_line,omitempty" minimum:"1"`
	EndLine         int                   `json:"end_line,omitempty" minimum:"1"`
	Body            string                `json:"body" minLength:"1" maxLength:"2000"`
}

type ClientThreadAnchor struct {
	Anchor        json.RawMessage                 `json:"anchor,omitempty"`
	ThreadID      guid.ID                         `json:"thread_id"`
	AnchorKind    string                          `json:"anchor_kind"`
	AnchorExcerpt string                          `json:"anchor_excerpt"`
	VersionRef    string                          `json:"version_ref"`
	ArtifactRef   delivery.ArtifactAnchorSnapshot `json:"artifact_ref"`
}

type ClientThreadView struct {
	ClientThreadAnchor
	Body              string   `json:"body"`
	ConvertedTargetID *guid.ID `json:"converted_target_id,omitempty"`
}

type ClientThreadError struct {
	Reason   string
	TargetID *guid.ID
}

func (e *ClientThreadError) Error() string {
	return "execution: invalid client rejection comment: " + e.Reason
}

type ClientArtifactComments struct {
	ListOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID, int) ([]ClientThreadView, error)
	CreateOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, ClientThreadCreate) (ClientThreadView, error)
	GetOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID) (ClientThreadView, error)
	ConvertOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, guid.ID, guid.ID, guid.ID) (ClientThreadAnchor, error)
}

type clientThreadServices struct{ authority ClientCommentAuthority }

func (m *Module) provideClientThreads(s *module.Services) error {
	if err := module.Provide[ClientCommentAuthorityConnector](s, ClientCommentAuthorityConnector{Bind: func(a ClientCommentAuthority) error {
		if a.ItemOn == nil || a.OpenOn == nil || m.clientThreads.authority.ItemOn != nil {
			return ErrUnavailable
		}
		m.clientThreads.authority = a
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[ClientArtifactComments](s, ClientArtifactComments{ListOn: m.listClientThreadsOn, CreateOn: m.createClientThreadOn, GetOn: m.getClientThreadOn, ConvertOn: m.convertClientThreadOn})
}

func (m *Module) listClientThreadsOn(ctx context.Context, tx *gorm.DB, tenant, project, deliveryID guid.ID, item string, after guid.ID, limit int) ([]ClientThreadView, error) {
	if _, err := m.clientThreadItemOn(ctx, tx, tenant, project, deliveryID, item, false); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalidArgument
	}
	q := query.BindClientCommentLinks(tx, m.names)
	links, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.DeliveryID.Eq(deliveryID), q.ItemID.Eq(item), q.ThreadID.Gt(after)).Order(q.ThreadID).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	out := make([]ClientThreadView, 0, len(links))
	for _, link := range links {
		v, err := m.getClientThreadOn(ctx, tx, tenant, project, deliveryID, item, link.ThreadID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (m *Module) clientThreadItemOn(ctx context.Context, tx *gorm.DB, tenant, project, deliveryID guid.ID, item string, write bool) (ClientDeliveryItem, error) {
	if err := transaction(tx, tenant, deliveryID); err != nil {
		return ClientDeliveryItem{}, err
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 || project <= 0 || len(item) != 64 || strings.Trim(item, "0123456789abcdef") != "" {
		return ClientDeliveryItem{}, ErrInvalidArgument
	}
	if m.clientThreads.authority.ItemOn == nil {
		return ClientDeliveryItem{}, ErrUnavailable
	}
	return m.clientThreads.authority.ItemOn(ctx, tx, tenant, project, deliveryID, item, write)
}

func (m *Module) createClientThreadOn(ctx context.Context, tx *gorm.DB, tenant, project, deliveryID guid.ID, item string, in ClientThreadCreate) (ClientThreadView, error) {
	var out ClientThreadView
	source, err := m.clientThreadItemOn(ctx, tx, tenant, project, deliveryID, item, true)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Body) == "" || len([]rune(in.Body)) > 2000 || source.ContactID <= 0 {
		return out, ErrInvalidArgument
	}
	if m.clientThreads.authority.OpenOn == nil {
		return out, ErrUnavailable
	}
	pin, err := m.clientThreads.authority.OpenOn(ctx, tx, tenant, project, deliveryID, item)
	if err != nil {
		if pin.Reader != nil {
			err = errors.Join(err, pin.Reader.Close())
		}
		return out, err
	}
	anchor, err := clientArtifactAnchor(pin, source, in)
	if err != nil {
		return out, err
	}
	id, err := m.nextID()
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(anchor)
	if err != nil {
		return out, err
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	link := model.ClientCommentLink{ThreadID: id, TenantID: tenant, ProjectID: project, DeliveryID: deliveryID, ItemID: item, ContactID: source.ContactID, AnchorSnapshot: string(raw), FirstBody: in.Body, CreatedBy: actor, CreatedAt: at}
	if err = query.BindClientCommentLinks(tx, m.names).WithContext(ctx).Create(&link); err != nil {
		return out, err
	}
	out = ClientThreadView{ClientThreadAnchor: clientThreadAnchor(id, anchor.ArtifactAnchorSnapshot), Body: in.Body}
	out.Anchor = anchor.Anchor
	return out, nil
}

func clientThreadAnchor(id guid.ID, a delivery.ArtifactAnchorSnapshot) ClientThreadAnchor {
	return ClientThreadAnchor{ThreadID: id, AnchorKind: a.Kind, AnchorExcerpt: a.Excerpt, VersionRef: a.VersionRef, ArtifactRef: a}
}

func (m *Module) getClientThreadOn(ctx context.Context, tx *gorm.DB, tenant, project, deliveryID guid.ID, item string, id guid.ID) (ClientThreadView, error) {
	var out ClientThreadView
	if id <= 0 {
		return out, ErrInvalidArgument
	}
	if _, err := m.clientThreadItemOn(ctx, tx, tenant, project, deliveryID, item, false); err != nil {
		return out, err
	}
	q := query.BindClientCommentLinks(tx, m.names)
	link, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ThreadID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		q := query.BindArtifactCommentThreads(tx, m.names)
		thread, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if e == nil && thread.Visibility != "CLIENT" {
			return out, &ClientThreadError{Reason: "NOT_CLIENT_THREAD"}
		}
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return out, e
		}
		return out, &ClientThreadError{Reason: "NOT_THIS_ITEM"}
	}
	if err != nil {
		return out, err
	}
	if link.ProjectID != project || link.DeliveryID != deliveryID || link.ItemID != item {
		return out, &ClientThreadError{Reason: "NOT_THIS_ITEM"}
	}

	var anchor clientAnchorSnapshot
	if json.Unmarshal([]byte(link.AnchorSnapshot), &anchor) != nil {
		return out, ErrUnavailable
	}
	out = ClientThreadView{ClientThreadAnchor: clientThreadAnchor(id, anchor.ArtifactAnchorSnapshot), Body: link.FirstBody, ConvertedTargetID: link.TargetID}
	out.Anchor = anchor.Anchor
	return out, nil
}

func (m *Module) convertClientThreadOn(ctx context.Context, tx *gorm.DB, tenant, project, deliveryID guid.ID, item string, id, target, contact guid.ID) (ClientThreadAnchor, error) {
	var out ClientThreadAnchor
	if target <= 0 || contact <= 0 {
		return out, ErrInvalidArgument
	}
	source, err := m.clientThreadItemOn(ctx, tx, tenant, project, deliveryID, item, false)
	if err != nil {
		return out, err
	}
	if source.ContactID > 0 && source.ContactID != contact {
		return out, ErrForbidden
	}
	view, err := m.getClientThreadOn(ctx, tx, tenant, project, deliveryID, item, id)
	if err != nil {
		return out, err
	}
	q := query.BindClientCommentLinks(tx, m.names)
	link, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ThreadID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	if link.TargetID != nil {
		return out, &ClientThreadError{Reason: "ALREADY_CONVERTED", TargetID: link.TargetID}
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ThreadID.Eq(id), q.TargetID.IsNull()).UpdateSimple(q.TargetKind.Value("REJECTION_ITEM"), q.TargetID.Value(target), q.ConvertedAt.Value(at), q.ConvertedBy.Value(contact))
	if err != nil {
		return out, err
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	return view.ClientThreadAnchor, nil
}
