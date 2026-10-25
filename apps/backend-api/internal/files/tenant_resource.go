package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"slices"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// TenantPurpose is registered by the resource owner during initialization.
// Authorize must lock and validate current membership and resource authority on
// the supplied transaction, including resourceID=0 for staging. It receives
// stage, write, read or release; rebind checks both the old and new resource.
type TenantPurpose struct {
	Name, Module, Directory, ResourceKind string
	MaxSize                               int64
	AllowedMIME                           []string
	AllowExpiry                           bool
	Authorize                             func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
}
type TenantResourceFiles struct {
	module   *Module
	mu       sync.RWMutex
	purposes map[string]TenantPurpose
}
type PreparedTenantObject struct {
	tenant, actor guid.ID
	purpose       string
	object        *PreparedAccountObject
}
type TenantResourceInput struct {
	TenantID, ResourceID              guid.ID
	Purpose, ResourceKind, Name, MIME string
	ExpiresAt                         *time.Time
	Prepared                          *PreparedTenantObject
}

func (m *Module) provideTenantResourceFiles(s *module.Services) error {
	return module.Provide[*TenantResourceFiles](s, &TenantResourceFiles{module: m, purposes: make(map[string]TenantPurpose)})
}
func (p *TenantResourceFiles) RegisterPurpose(v TenantPurpose) error {
	if p == nil || p.module == nil || p.module.bindings == nil || !resourceBindingName.MatchString(v.Name) || !resourceBindingName.MatchString(v.Module) || !resourceBindingName.MatchString(v.ResourceKind) || v.Authorize == nil || v.MaxSize < 1 || v.MaxSize > 100<<20 || len(v.AllowedMIME) == 0 {
		return errResourcePurpose
	}
	if v.Directory == "" {
		v.Directory = ".system"
	}
	if _, _, err := PortableName(v.Directory); err != nil {
		return errResourcePurpose
	}
	for _, s := range v.AllowedMIME {
		typ, _, err := mime.ParseMediaType(s)
		if err != nil || typ != s || strings.Contains(s, "*") {
			return errResourcePurpose
		}
	}
	v.AllowedMIME = slices.Clone(v.AllowedMIME)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.purposes[v.Name]; ok {
		return errResourcePurpose
	}
	err := p.module.bindings.RegisterPurpose(ResourcePurpose{Name: v.Name, ResourceKind: v.ResourceKind, OwnerScopes: []string{ownerTenant}, VersionMode: "pinned", AllowExpiry: v.AllowExpiry, ValidateResource: func(ctx context.Context, tx *gorm.DB, id guid.ID, owner ResourceOwner) error {
		return v.Authorize(ctx, tx, owner.ID, id, "write")
	}})
	if err != nil {
		return err
	}
	p.purposes[v.Name] = v
	return nil
}
func (p *TenantResourceFiles) purpose(name string) (TenantPurpose, error) {
	if p == nil {
		return TenantPurpose{}, errOperationUnavailable
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.purposes[name]
	if !ok {
		return v, errResourcePurpose
	}
	return v, nil
}
func (p *TenantResourceFiles) ready(tx *gorm.DB) error {
	if p == nil || p.module == nil || p.module.accounts == nil || p.module.bindings == nil || p.module.appender == nil || p.module.ids == nil {
		return errOperationUnavailable
	}
	if tx == nil || tx.Statement == nil {
		return ErrAccountTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	return nil
}
func (p *TenantResourceFiles) StageOn(ctx context.Context, db *gorm.DB, tenant guid.ID, purpose string, r io.Reader, size int64, deadline time.Time) (*PreparedTenantObject, error) {
	v, err := p.purpose(purpose)
	if err != nil {
		return nil, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if db == nil || tenant <= 0 || actor <= 0 || r == nil || size < 0 || size > v.MaxSize {
		return nil, errOperationInput
	}
	if _, active := db.Statement.ConnPool.(gorm.TxCommitter); active {
		return nil, ErrAccountTransaction
	}
	if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := p.ready(tx); e != nil {
			return e
		}
		return v.Authorize(ctx, tx, tenant, 0, "stage")
	}); err != nil {
		return nil, err
	}
	id, err := p.module.nextFileID()
	if err != nil {
		return nil, err
	}
	obj, err := p.module.accounts.Stage(ctx, db, id, actor, "tenant/"+tenant.String()+"/"+id.String(), r, size, deadline)
	if err != nil {
		return nil, err
	}
	// Rejected MIME remains durably staged for the existing expiry collector.
	if !slices.Contains(v.AllowedMIME, obj.object.MIME) {
		return nil, errResourcePurpose
	}
	return &PreparedTenantObject{tenant: tenant, actor: actor, purpose: purpose, object: obj}, nil
}
func (p *TenantResourceFiles) CreateOn(ctx context.Context, tx *gorm.DB, in TenantResourceInput, now time.Time) (PinnedResource, error) {
	if err := p.ready(tx); err != nil {
		return PinnedResource{}, err
	}
	v, err := p.purpose(in.Purpose)
	if err != nil {
		return PinnedResource{}, err
	}
	prep := in.Prepared
	if prep == nil || prep.object == nil || prep.tenant != in.TenantID || prep.purpose != in.Purpose || prep.actor != authn.PrincipalIDFromContext(ctx) || in.ResourceKind != v.ResourceKind || in.ResourceID <= 0 || in.MIME != prep.object.object.MIME || !slices.Contains(v.AllowedMIME, in.MIME) || (!v.AllowExpiry && in.ExpiresAt != nil) || (in.ExpiresAt != nil && !in.ExpiresAt.After(now)) {
		return PinnedResource{}, errResourceBinding
	}
	if _, _, err = PortableName(in.Name); err != nil {
		return PinnedResource{}, err
	}
	if err = v.Authorize(ctx, tx, in.TenantID, in.ResourceID, "write"); err != nil {
		return PinnedResource{}, err
	}
	root, err := p.module.provisionOrganizationRoot(ctx, tx, core.FileRootProvisionInput{TenantID: in.TenantID, Scope: ownerTenant, OwnerID: in.TenantID}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	q := query.Bind(tx, tree.prefix)
	parent, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(root)).Take()
	if err != nil {
		return PinnedResource{}, err
	}
	for _, name := range []string{v.Directory, v.Module, now.UTC().Format("2006"), now.UTC().Format("01")} {
		child, e := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(parent.SpaceID), q.Node.ParentID.Eq(parent.ID), q.Node.NameKey.Eq(strings.ToLower(name)), q.Node.State.Eq("active")).Take()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			id, e2 := p.module.nextFileID()
			if e2 != nil {
				return PinnedResource{}, e2
			}
			child, e = tree.createNodeOn(ctx, tx, parent.SpaceID, id, parent.ID, "directory", name, now)
			if e == nil {
				e = p.protectDirectory(ctx, tx, child, in.TenantID, now)
			}
		}
		if e != nil {
			return PinnedResource{}, e
		}
		if child.Kind != "directory" {
			return PinnedResource{}, errHierarchy
		}
		protected, e := q.NodeACL.WithContext(ctx).Where(q.NodeACL.NodeID.Eq(child.ID), q.NodeACL.SubjectKind.Eq("owner"), q.NodeACL.SubjectID.Eq(in.TenantID), q.NodeACL.Effect.Eq("deny"), q.NodeACL.Action.Eq("create")).Count()
		if e != nil {
			return PinnedResource{}, e
		}
		if protected != 1 {
			return PinnedResource{}, errOperationForbidden
		}
		parent = child
	}
	fileID, err := p.module.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	versionID, err := p.module.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	bindingID, err := p.module.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	file, err := tree.createNodeOn(ctx, tx, parent.SpaceID, fileID, parent.ID, "file", fmt.Sprintf("%s-%s", fileID, in.Name), now)
	if err != nil {
		return PinnedResource{}, err
	}
	obj, err := p.module.accounts.attachFileObject(ctx, tx, prep.object, prep.actor)
	if err != nil {
		return PinnedResource{}, err
	}
	if err = tree.appendVersion(ctx, tx, model.Version{ID: versionID, SpaceID: parent.SpaceID, NodeID: fileID, Number: 1, ObjectID: obj.ID, Ext: file.Ext, DeclaredMIME: in.MIME, CreatedBy: prep.actor}, file.Version, now); err != nil {
		return PinnedResource{}, err
	}
	file, err = q.Node.WithContext(ctx).Where(q.Node.ID.Eq(fileID)).Take()
	if err != nil {
		return PinnedResource{}, err
	}
	b, err := p.module.bindings.Bind(ctx, tx, ResourceBindingInput{ID: bindingID, SpaceID: file.SpaceID, FileID: file.ID, ExpectedFileVersion: file.Version, VersionID: &versionID, Purpose: in.Purpose, ResourceKind: in.ResourceKind, ResourceID: in.ResourceID, Slot: "file-" + fileID.String(), ExpiresAt: in.ExpiresAt, ActorID: prep.actor}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	if err = p.audit(ctx, tx, in.TenantID, "tenant_file_created", b); err != nil {
		return PinnedResource{}, err
	}
	return PinnedResource{BindingID: b.ID, SpaceID: b.SpaceID, FileID: b.FileID, VersionID: versionID, ResourceID: b.ResourceID, Purpose: b.Purpose, ResourceKind: b.ResourceKind, Name: file.Name, MIME: obj.MIME, Size: obj.Size, SHA256: obj.SHA256}, nil
}

// binding locks the same space fence used by binding expiry and checks current
// resource authority. Tenant identity is derived from the verified file root.
func (p *TenantResourceFiles) binding(ctx context.Context, tx *gorm.DB, id guid.ID, action string, now time.Time) (*model.ResourceBinding, guid.ID, error) {
	if e := p.ready(tx); e != nil {
		return nil, 0, e
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	b, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(id)).Take()
	if err != nil {
		return nil, 0, err
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	if _, _, err = tree.lockOperationSpace(ctx, tx, b.SpaceID); err != nil {
		return nil, 0, err
	}
	b, err = q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(id), q.ResourceBinding.State.Eq("active")).Take()
	if err != nil {
		return nil, 0, err
	}
	if b.ExpiresAt != nil && *b.ExpiresAt <= now.UnixMilli() {
		return nil, 0, ErrNotFound
	}
	owner, err := tree.resourceOwner(ctx, tx, b.SpaceID, b.FileID)
	if err != nil {
		return nil, 0, err
	}
	if owner.Scope != ownerTenant {
		return nil, 0, errOperationForbidden
	}
	v, err := p.purpose(b.Purpose)
	if err != nil {
		return nil, 0, err
	}
	if v.ResourceKind != b.ResourceKind {
		return nil, 0, errResourcePurpose
	}
	if authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, 0, errOperationForbidden
	}
	if err = v.Authorize(ctx, tx, owner.ID, b.ResourceID, action); err != nil {
		return nil, 0, err
	}
	return b, owner.ID, nil
}
func (p *TenantResourceFiles) Rebind(ctx context.Context, tx *gorm.DB, id guid.ID, kind string, resource guid.ID, expected int64, now time.Time) error {
	b, tenant, err := p.binding(ctx, tx, id, "write", now)
	if err != nil {
		return err
	}
	if b.Version != expected {
		return errResourceBindingConflict
	}
	v, err := p.purpose(b.Purpose)
	if err != nil {
		return err
	}
	if kind != v.ResourceKind || resource <= 0 {
		return errResourcePurpose
	}
	if err = v.Authorize(ctx, tx, tenant, resource, "write"); err != nil {
		return err
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	result, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(id), q.ResourceBinding.Version.Eq(expected), q.ResourceBinding.State.Eq("active")).UpdateSimple(q.ResourceBinding.ResourceID.Value(resource), q.ResourceBinding.Version.Add(1), q.ResourceBinding.UpdatedAt.Value(now.UnixMilli()))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return errResourceBindingConflict
	}
	if _, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(b.SpaceID)).UpdateSimple(q.Space.Version.Add(1)); err != nil {
		return err
	}
	b.ResourceID = resource
	return p.audit(ctx, tx, tenant, "tenant_file_rebound", b)
}
func (p *TenantResourceFiles) Release(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) error {
	b, tenant, err := p.binding(ctx, tx, id, "release", now)
	if err != nil {
		return err
	}
	if err = p.module.bindings.Unbind(ctx, tx, b.SpaceID, b.ID, b.Version, now); err != nil {
		return err
	}
	return p.audit(ctx, tx, tenant, "tenant_file_released", b)
}
func (p *TenantResourceFiles) Open(ctx context.Context, db *gorm.DB, tenant, node guid.ID) (PinnedResourceRead, error) {
	var out PinnedResourceRead
	if db == nil || tenant <= 0 || node <= 0 {
		return out, errOperationInput
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := p.ready(tx); err != nil {
			return err
		}
		q := query.Bind(tx, p.module.bindings.prefix)
		b, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.FileID.Eq(node), q.ResourceBinding.State.Eq("active")).Take()
		if err != nil {
			return err
		}
		b, actual, err := p.binding(ctx, tx, b.ID, "read", time.Now())
		if err != nil {
			return err
		}
		if actual != tenant || b.VersionID == nil {
			return errOperationForbidden
		}
		info, obj, err := (pinnedResources{module: p.module}).content(ctx, tx, b.SpaceID, b.FileID, *b.VersionID)
		if err != nil {
			return err
		}
		info.BindingID, info.ResourceID, info.Purpose, info.ResourceKind = b.ID, b.ResourceID, b.Purpose, b.ResourceKind
		if err = p.audit(ctx, tx, tenant, "tenant_file_read", b); err != nil {
			return err
		}
		reader, err := openStoredObject(ctx, p.module.driver, obj)
		if err != nil {
			return err
		}
		out = PinnedResourceRead{PinnedResource: info, Reader: reader}
		return nil
	})
	if err != nil && out.Reader != nil {
		_ = out.Reader.Close()
	}
	return out, err
}
func (p *TenantResourceFiles) audit(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, b *model.ResourceBinding) error {
	event := audit.NewDomainEvent(ctx, tenant, kind, "file_node", b.FileID)
	event.Detail["binding_id"], event.Detail["purpose"], event.Detail["resource_id"], event.Detail["system_write"] = b.ID.String(), b.Purpose, b.ResourceID.String(), kind != "tenant_file_read"
	return p.module.appender(ctx, tx, event)
}

// Organization roots never grant owner subjects to people. These owner-deny
// rows are a server-owned write fence, enforced before ordinary IAM/ACL grants.
func (p *TenantResourceFiles) protectDirectory(ctx context.Context, tx *gorm.DB, node *model.Node, tenant guid.ID, now time.Time) error {
	q := query.Bind(tx, p.module.bindings.prefix)
	for _, action := range []string{"create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"} {
		id, err := p.module.nextFileID()
		if err != nil {
			return err
		}
		if err = q.NodeACL.WithContext(ctx).Create(&model.NodeACL{ID: id, SpaceID: node.SpaceID, NodeID: node.ID, SubjectKind: "owner", SubjectID: tenant, Effect: "deny", Action: action, Applies: "self_and_descendants", CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now.UnixMilli()}); err != nil {
			return err
		}
	}
	return nil
}
func tenantSystemDirectoryDenied(p storedNodePermissions, action string) bool {
	if p.space == nil || p.space.TenantID == nil || p.root.scope != ownerTenant {
		return false
	}
	for _, layer := range p.layers {
		for _, rule := range layer.rules {
			if rule.subject.kind == "owner" && rule.subject.id == *p.space.TenantID && rule.effect == "deny" && rule.applies == "self_and_descendants" && slices.Contains(rule.actions, action) {
				return true
			}
		}
	}
	return false
}
