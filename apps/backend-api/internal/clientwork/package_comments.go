package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func (m *Module) bindPackageComments(s *module.Services) error {
	var err error
	if m.packageCommentContacts, err = module.Resolve[delivery.ClientCommentContacts](s); err != nil {
		return err
	}
	if m.packageComments, err = module.Resolve[execution.ClientArtifactComments](s); err != nil {
		return err
	}
	c, err := module.Resolve[execution.ClientCommentAuthorityConnector](s)
	if err != nil {
		return err
	}
	return c.Bind(execution.ClientCommentAuthority{ItemOn: m.clientCommentItemOn, OpenOn: m.openClientCommentItemOn})
}

func (m *Module) clientCommentItemOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, item string, write bool) (execution.ClientDeliveryItem, error) {
	var out execution.ClientDeliveryItem
	if tenant != authn.TenantIDFromContext(ctx) {
		return out, ErrNotFound
	}
	ext, err := m.authorize(ctx, tx, project, "delivery_read", true)
	if err != nil {
		return out, err
	}
	if write && !ext {
		return out, ErrForbidden
	}
	row, err := m.packageOn(ctx, tx, project, id, ext)
	if err != nil {
		return out, err
	}
	if row.State == "DRAFT" {
		return out, ErrNotFound
	}
	if write && row.State != "PUBLISHED" {
		return out, ErrConflict
	}
	var manifest packageManifest
	if json.Unmarshal([]byte(row.Manifest), &manifest) != nil {
		return out, ErrUnavailable
	}
	items := packageItems(row, manifest.Content)
	if len(items) != len(manifest.Refs) {
		return out, ErrUnavailable
	}
	for i, v := range items {
		if v.ItemID == item {
			out = execution.ClientDeliveryItem{WorkItemID: manifest.Refs[i].WorkItemID, EvidenceID: manifest.Refs[i].EvidenceID, VersionID: v.Artifact.VersionID, SHA256: v.Artifact.SHA256}
			if ext {
				if m.packageCommentContacts.ContactOn == nil {
					return execution.ClientDeliveryItem{}, ErrUnavailable
				}
				out.ContactID, err = m.packageCommentContacts.ContactOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx))
				if err != nil {
					return execution.ClientDeliveryItem{}, err
				}
			}
			return out, nil
		}
	}
	return out, ErrNotFound
}

type packageCommentListInput struct {
	ListInput
	ID       string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ItemHash string `path:"item_hash" pattern:"^[a-f0-9]{64}$" maxLength:"64"`
}
type packageCommentCreateInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ItemHash string `path:"item_hash" pattern:"^[a-f0-9]{64}$" maxLength:"64"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     execution.ClientThreadCreate
}

func (m *Module) registerPackageCommentsHTTP(api huma.API) {
	path := entityBase + "/delivery-packages/{id}/items/{item_hash}/comments"
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "channel", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_comment.list", Method: "GET", Path: path, Summary: "Read CLIENT threads for a frozen delivery item", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.packageCommentsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_comment.create", Method: "POST", Path: path, DefaultStatus: 201, Summary: "Annotate the actual fixed file delivered to the current client", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.createPackageCommentHTTP)
}

func (m *Module) packageCommentsHTTP(ctx context.Context, in *packageCommentListInput) (*respx.Body[Page[execution.ClientThreadView]], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return readDomain(ctx, m, &in.ListInput, "package-comments:"+id.String()+":"+in.ItemHash, "delivery_read", true, func(tx *gorm.DB, _ bool, last guid.ID, n int) ([]execution.ClientThreadView, []guid.ID, error) {
		if m.packageComments.ListOn == nil {
			return nil, nil, ErrUnavailable
		}
		project, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, nil, ErrInvalid
		}
		rows, err := m.packageComments.ListOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, in.ItemHash, last, n)
		if err != nil {
			return nil, nil, err
		}
		ids := make([]guid.ID, len(rows))
		for i, row := range rows {
			ids[i] = row.ThreadID
		}
		return rows, ids, nil
	})
}

func (m *Module) createPackageCommentHTTP(ctx context.Context, in *packageCommentCreateInput) (*CommandOutput, error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, project, "clientwork.package_comment.create", "POST", entityBase+"/delivery-packages/"+id.String()+"/items/"+in.ItemHash+"/comments", in.Key, 201, in, "delivery_read", true, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.packageComments.CreateOn == nil {
			return Receipt{}, ErrUnavailable
		}
		out, err := m.packageComments.CreateOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, in.ItemHash, in.Body)
		if err != nil {
			return Receipt{}, err
		}
		if err = m.event(ctx, tx, project, out.ThreadID, "annotation_created", "channel_read", 1); err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: out.ThreadID, Version: 1, State: "OPEN"}, nil
	})
}

func (m *Module) freezeRejectionAnchorsOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, contact guid.ID, outcomes []PackageItemDecision) error {
	for i := range outcomes {
		item := &outcomes[i]
		// Clients may supply only thread references; snapshots are source-owned.
		if len(item.Anchors) > 0 || item.AcceptanceItemID != 0 {
			return ErrInvalid
		}
		if len(item.CommentThreadIDs) > 20 {
			return &execution.ClientThreadError{Reason: "NOT_THIS_ITEM"}
		}
		if len(item.CommentThreadIDs) > 0 && item.Result != "REJECTED" {
			return &execution.ClientThreadError{Reason: "NOT_REJECTED"}
		}
		seen := map[guid.ID]bool{}
		ids := make([]guid.ID, 0, len(item.CommentThreadIDs))
		for _, id := range item.CommentThreadIDs {
			if id <= 0 {
				return ErrInvalid
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		item.CommentThreadIDs = ids
		if len(ids) == 0 {
			continue
		}
		if m.packageComments.ConvertOn == nil {
			return ErrUnavailable
		}
		target, err := m.nextID()
		if err != nil {
			return err
		}
		item.AcceptanceItemID = target
		for _, id := range ids {
			anchor, err := m.packageComments.ConvertOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, item.ItemID, id, target, contact)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &execution.ClientThreadError{Reason: "NOT_THIS_ITEM"}
			}
			if err != nil {
				return err
			}
			item.Anchors = append(item.Anchors, anchor)
		}
	}
	return nil
}

func validPackageItemID(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}

func (m *Module) openClientCommentItemOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, item string) (files.PinnedResourceRead, error) {
	var out files.PinnedResourceRead
	source, err := m.clientCommentItemOn(ctx, tx, tenant, project, id, item, true)
	if err != nil {
		return out, err
	}
	if m.files.OpenOn == nil {
		return out, ErrUnavailable
	}
	row, err := m.packageOn(ctx, tx, project, id, true)
	if err != nil {
		return out, err
	}
	var manifest packageManifest
	if json.Unmarshal([]byte(row.Manifest), &manifest) != nil {
		return out, ErrUnavailable
	}
	for _, pin := range manifest.Bindings {
		if pin.VersionID == source.VersionID {
			return m.files.OpenOn(ctx, tx, pin.BindingID)
		}
	}
	return out, ErrNotFound
}
