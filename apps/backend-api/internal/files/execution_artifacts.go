package files

import (
	"context"
	"fmt"
	"io"
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

// ExecutionArtifactAuthority is checked by execution against its current runner
// and owning project, never against a client-provided file owner.
type ExecutionArtifactAuthority struct {
	VerificationTaskID                                    guid.ID
	TenantID, EntityID, ExecutionID, MachineID, SessionID guid.ID
	ConnEpoch, DispatchEpoch, Seq                         int64
}
type ExecutionArtifactAuthorizer func(context.Context, *gorm.DB, ExecutionArtifactAuthority, string, guid.ID, ResourceOwner) error
type ExecutionArtifactFiles struct {
	module    *Module
	authorize ExecutionArtifactAuthorizer
}
type PreparedExecutionArtifact struct {
	authority           ExecutionArtifactAuthority
	purpose, name, slot string
	object              *PreparedAccountObject
	actor               guid.ID
}
type executionReferenceKey struct{}
type ExecutionArtifactReferences interface {
	PinReferenceOn(context.Context, *gorm.DB, guid.ID, guid.ID, ResourceBindingInput) (PinnedResource, error)
}

func (p pinnedResources) PinReferenceOn(ctx context.Context, tx *gorm.DB, sourceBindingID, executionID guid.ID, target ResourceBindingInput) (PinnedResource, error) {
	if p.module == nil || p.module.executionArtifacts == nil {
		return PinnedResource{}, errOperationUnavailable
	}
	return p.module.executionArtifacts.PinReferenceOn(ctx, tx, sourceBindingID, executionID, target)
}

func ExecutionReferenceAuthorized(ctx context.Context, executionID, entityID guid.ID) bool {
	a, ok := ctx.Value(executionReferenceKey{}).(ExecutionArtifactAuthority)
	return ok && a.ExecutionID == executionID && a.EntityID == entityID
}

func (m *Module) provideExecutionArtifactFiles(s *module.Services) error {
	m.executionArtifacts = &ExecutionArtifactFiles{module: m}
	return module.Provide[*ExecutionArtifactFiles](s, m.executionArtifacts)
}
func (p *ExecutionArtifactFiles) BindAuthorization(authorize ExecutionArtifactAuthorizer) error {
	if p == nil || p.module == nil || p.module.bindings == nil || authorize == nil || p.authorize != nil {
		return errOperationUnavailable
	}
	for _, name := range []string{"execution_evidence", "execution_log", "execution_context", "execution_artifact"} {
		purpose := name
		if err := p.module.bindings.RegisterPurpose(ResourcePurpose{Name: purpose, ResourceKind: purpose, OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, tx *gorm.DB, id guid.ID, owner ResourceOwner) error {
			a, _ := ctx.Value(executionReferenceKey{}).(ExecutionArtifactAuthority)
			return authorize(ctx, tx, a, purpose, id, owner)
		}}); err != nil {
			return err
		}
	}
	p.authorize = authorize
	return nil
}
func (p *ExecutionArtifactFiles) ready(tx *gorm.DB) error {
	if p == nil || p.module == nil || p.module.runtime == nil || p.module.accounts == nil || p.module.bindings == nil || p.module.ids == nil || p.module.appender == nil || p.authorize == nil {
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

// Stage writes bytes outside the receipt transaction and keeps the durable
// preparation record for cleanup if the later receipt is rejected or rolled back.
func (p *ExecutionArtifactFiles) Stage(ctx context.Context, a ExecutionArtifactAuthority, purpose, name, slot string, source io.Reader, size int64) (*PreparedExecutionArtifact, error) {
	if p == nil || p.module == nil || p.module.runtime == nil || p.module.runtime.Database() == nil || p.authorize == nil || p.module.accounts == nil {
		return nil, errOperationUnavailable
	}
	if _, _, err := PortableName(name); err != nil {
		return nil, errOperationInput
	}
	maxSize := int64(65536)
	if a.VerificationTaskID > 0 && purpose == "execution_evidence" {
		maxSize = 4 << 20
	}
	if !resourceBindingName.MatchString(slot) || size < 0 || size > maxSize || source == nil {
		return nil, errOperationInput
	}
	if purpose != "execution_log" && purpose != "execution_evidence" && purpose != "execution_context" && purpose != "execution_artifact" {
		return nil, errResourcePurpose
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 {
		return nil, errOperationForbidden
	}
	db := p.module.runtime.Database().Write()
	if db == nil {
		return nil, errOperationUnavailable
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := p.ready(tx); err != nil {
			return err
		}
		return p.authorize(ctx, tx, a, purpose, 0, ResourceOwner{Scope: "entity", ID: a.EntityID})
	}); err != nil {
		return nil, err
	}
	id, err := p.module.nextFileID()
	if err != nil {
		return nil, err
	}
	object, err := p.module.accounts.Stage(ctx, db, id, actor, "execution/"+id.String(), source, size, time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	return &PreparedExecutionArtifact{authority: a, purpose: purpose, name: name, slot: slot, object: object, actor: actor}, nil
}

func (p *ExecutionArtifactFiles) AttachOn(ctx context.Context, tx *gorm.DB, prepared *PreparedExecutionArtifact, resourceID guid.ID) (PinnedResource, error) {
	if err := p.ready(tx); err != nil {
		return PinnedResource{}, err
	}
	if prepared == nil || prepared.object == nil || resourceID <= 0 || authn.PrincipalIDFromContext(ctx) != prepared.actor {
		return PinnedResource{}, errOperationForbidden
	}
	a := prepared.authority
	ctx = context.WithValue(ctx, executionReferenceKey{}, a)
	if err := p.authorize(ctx, tx, a, prepared.purpose, resourceID, ResourceOwner{Scope: "entity", ID: a.EntityID}); err != nil {
		return PinnedResource{}, err
	}
	now := time.Now().UTC()
	rootID, err := p.module.provisionOrganizationRoot(ctx, tx, core.FileRootProvisionInput{TenantID: a.TenantID, Scope: "entity", OwnerID: a.EntityID}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	q := query.Bind(tx, tree.prefix)
	root, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(rootID)).First()
	if err != nil {
		return PinnedResource{}, err
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
	file, err := tree.createNodeOn(ctx, tx, root.SpaceID, fileID, root.ID, "file", fmt.Sprintf("%s-%s", fileID, prepared.name), now)
	if err != nil {
		return PinnedResource{}, err
	}
	object, err := p.module.accounts.attachFileObject(ctx, tx, prepared.object, prepared.actor)
	if err != nil {
		return PinnedResource{}, err
	}
	if err = tree.appendVersion(ctx, tx, model.Version{ID: versionID, SpaceID: root.SpaceID, NodeID: fileID, Number: 1, ObjectID: object.ID, Ext: file.Ext, DeclaredMIME: object.MIME, CreatedBy: prepared.actor}, file.Version, now); err != nil {
		return PinnedResource{}, err
	}
	file, err = q.Node.WithContext(ctx).Where(q.Node.ID.Eq(fileID)).First()
	if err != nil {
		return PinnedResource{}, err
	}
	binding, err := p.module.bindings.Bind(ctx, tx, ResourceBindingInput{ID: bindingID, SpaceID: root.SpaceID, FileID: fileID, ExpectedFileVersion: file.Version, VersionID: &versionID, Purpose: prepared.purpose, ResourceKind: prepared.purpose, ResourceID: resourceID, Slot: prepared.slot, ActorID: prepared.actor}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	event := audit.NewDomainEvent(ctx, a.TenantID, "execution_file_ingested", "file_node", fileID)
	event.Detail["execution_id"], event.Detail["binding_id"], event.Detail["system_write"] = a.ExecutionID.String(), binding.ID.String(), true
	if err = p.module.appender(ctx, tx, event); err != nil {
		return PinnedResource{}, err
	}
	return PinnedResource{BindingID: binding.ID, SpaceID: root.SpaceID, FileID: fileID, VersionID: versionID, ResourceID: resourceID, Purpose: binding.Purpose, ResourceKind: binding.ResourceKind, Name: file.Name, MIME: object.MIME, SHA256: object.SHA256, Size: object.Size}, nil
}

// WithReferencesOn confines delegated result pinning to a freshly authorized
// execution completion call; its context marker cannot be supplied over HTTP.
func (p *ExecutionArtifactFiles) WithReferencesOn(ctx context.Context, tx *gorm.DB, a ExecutionArtifactAuthority, run func(context.Context) error) error {
	if err := p.ready(tx); err != nil {
		return err
	}
	if run == nil {
		return errOperationInput
	}
	if err := p.authorize(ctx, tx, a, "execution_evidence", 0, ResourceOwner{Scope: "entity", ID: a.EntityID}); err != nil {
		return err
	}
	return run(context.WithValue(ctx, executionReferenceKey{}, a))
}
func (p *ExecutionArtifactFiles) PinReferenceOn(ctx context.Context, tx *gorm.DB, sourceBindingID, executionID guid.ID, target ResourceBindingInput) (PinnedResource, error) {
	if err := p.ready(tx); err != nil {
		return PinnedResource{}, err
	}
	a, ok := ctx.Value(executionReferenceKey{}).(ExecutionArtifactAuthority)
	if !ok || a.ExecutionID != executionID || target.ActorID != authn.PrincipalIDFromContext(ctx) || target.Purpose != "delivery.result_evidence" || target.ResourceKind != "delivery_result" || target.ResourceID != executionID {
		return PinnedResource{}, errOperationForbidden
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	binding, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(sourceBindingID), q.ResourceBinding.Purpose.Eq("execution_evidence"), q.ResourceBinding.State.Eq("active")).First()
	if err != nil {
		return PinnedResource{}, err
	}
	if binding.VersionID == nil || target.VersionID == nil || binding.SpaceID != target.SpaceID || binding.FileID != target.FileID || *binding.VersionID != *target.VersionID {
		return PinnedResource{}, errOperationForbidden
	}
	owner, err := (directoryTree{prefix: p.module.bindings.prefix}).resourceOwner(ctx, tx, binding.SpaceID, binding.FileID)
	if err != nil {
		return PinnedResource{}, err
	}
	if err = p.authorize(ctx, tx, a, "execution_evidence", binding.ResourceID, owner); err != nil {
		return PinnedResource{}, err
	}
	pin, _, err := (pinnedResources{module: p.module}).content(ctx, tx, binding.SpaceID, binding.FileID, *binding.VersionID)
	if err != nil {
		return PinnedResource{}, err
	}
	// The current node CAS is server-derived; the source pins its immutable version.
	node, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(binding.FileID), q.Node.SpaceID.Eq(binding.SpaceID)).First()
	if err != nil {
		return PinnedResource{}, err
	}
	target.ExpectedFileVersion = node.Version
	attached, err := p.module.bindings.Bind(ctx, tx, target, time.Now())
	if err != nil {
		return PinnedResource{}, err
	}
	pin.BindingID, pin.Purpose, pin.ResourceKind, pin.ResourceID = attached.ID, attached.Purpose, attached.ResourceKind, attached.ResourceID
	return pin, nil
}
