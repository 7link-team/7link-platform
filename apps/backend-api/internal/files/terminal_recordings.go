package files

import (
	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"bytes"
	"context"
	"fmt"
	"gorm.io/gorm"
	"io"
	"time"
)

// TerminalRecordings persists immutable chunks in the configured files backend.
// No terminal content is spooled to an API-server temporary directory.
type TerminalRecordings struct {
	module    *Module
	authorize func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error
	retention time.Duration
}
type TerminalRecordingChunk struct{ prepared *PreparedAccountObject }

// ConfigureRetention is bound during module injection, before any recording
// can be created. Zero selects the specification's180-day deployment default.
func (p *TerminalRecordings) ConfigureRetention(days int) error {
	if p == nil || days < 0 || days > int((time.Duration(1<<63-1))/(24*time.Hour)) {
		return fmt.Errorf("files: invalid terminal recording retention days")
	}
	if days == 0 {
		days = 180
	}
	p.retention = time.Duration(days) * 24 * time.Hour
	return nil
}

func (p *TerminalRecordings) retentionDuration() time.Duration {
	if p.retention == 0 {
		return 180 * 24 * time.Hour
	}
	return p.retention
}

func (m *Module) provideTerminalRecordings(s *module.Services) error {
	return module.Provide[*TerminalRecordings](s, &TerminalRecordings{module: m})
}
func (p *TerminalRecordings) Bind(authorize func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error) error {
	if p == nil || p.module == nil || authorize == nil || p.authorize != nil {
		return ErrUnsupported
	}
	if err := p.module.bindings.RegisterPurpose(ResourcePurpose{Name: "terminal_recording", ResourceKind: "terminal_recording", OwnerScopes: []string{"entity"}, VersionMode: "pinned", AllowExpiry: true, ValidateResource: authorize}); err != nil {
		return err
	}
	p.authorize = authorize
	return nil
}
func (p *TerminalRecordings) Available() bool {
	return p != nil && p.module != nil && p.module.accounts != nil && p.module.driver != nil && p.authorize != nil
}
func (p *TerminalRecordings) StageChunk(ctx context.Context, actor guid.ID, data []byte) (*TerminalRecordingChunk, error) {
	if !p.Available() || actor <= 0 || len(data) == 0 || len(data) > 1<<20 {
		return nil, ErrUnsupported
	}
	m := p.module
	id, err := m.nextFileID()
	if err != nil {
		return nil, err
	}
	prepared, err := m.accounts.Stage(ctx, m.runtime.Database().Write(), id, actor, "terminal/chunks/"+id.String(), bytes.NewReader(data), int64(len(data)), time.Now().Add(180*24*time.Hour))
	if err != nil {
		return nil, err
	}
	return &TerminalRecordingChunk{prepared: prepared}, nil
}

// StageAppendChunks replaces only a partial durable tail. Completed 1 MiB
// chunks remain immutable; each accepted append still reaches protected storage.
// The caller attaches the replacement and releases the old reference in the
// same transaction as its recording metadata update.
func (p *TerminalRecordings) StageAppendChunks(ctx context.Context, actor, tail guid.ID, data []byte) ([]*TerminalRecordingChunk, bool, error) {
	const chunkSize = 1 << 20
	if !p.Available() || actor <= 0 || len(data) == 0 || len(data) > chunkSize {
		return nil, false, ErrUnsupported
	}
	combined := data
	replaceTail := false
	if tail > 0 {
		q := query.Bind(p.module.runtime.Database().Write(), p.module.bindings.prefix)
		count, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ObjectID.Eq(tail), q.AccountReference.PrincipalID.Eq(actor), q.AccountReference.Purpose.Eq("terminal_recording"), q.AccountReference.State.Eq("active"), q.AccountReference.ExpiresAt.Gt(time.Now().UnixMilli())).Count()
		if err != nil {
			return nil, false, err
		}
		if count != 1 {
			return nil, false, ErrNotFound
		}
		object, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(tail), q.Object.State.Eq("verified")).First()
		if err != nil {
			return nil, false, err
		}
		if object.Size <= 0 || object.Size > chunkSize {
			return nil, false, ErrUnsupported
		}
		if object.Size < chunkSize {
			reader, err := p.module.driver.Open(ctx, object.StorageKey)
			if err != nil {
				return nil, false, err
			}
			previous, readErr := io.ReadAll(io.LimitReader(reader, chunkSize+1))
			closeErr := reader.Close()
			if readErr != nil {
				return nil, false, readErr
			}
			if closeErr != nil {
				return nil, false, closeErr
			}
			if int64(len(previous)) != object.Size {
				return nil, false, ErrUnsupported
			}
			combined = append(previous, data...)
			replaceTail = true
		}
	}
	var chunks []*TerminalRecordingChunk
	for len(combined) > 0 {
		size := min(len(combined), chunkSize)
		chunk, err := p.StageChunk(ctx, actor, combined[:size])
		if err != nil {
			return nil, false, err
		}
		chunks = append(chunks, chunk)
		combined = combined[size:]
	}
	return chunks, replaceTail, nil
}

func (p *TerminalRecordings) AttachChunkOn(ctx context.Context, tx *gorm.DB, chunk *TerminalRecordingChunk, actor guid.ID) (guid.ID, error) {
	if !p.Available() || chunk == nil || chunk.prepared == nil {
		return 0, ErrUnsupported
	}
	obj, err := p.module.accounts.attachFileObject(ctx, tx, chunk.prepared, actor)
	if err != nil {
		return 0, err
	}
	ref, err := p.module.nextFileID()
	if err != nil {
		return 0, err
	}
	expiry := time.Now().Add(p.retentionDuration()).UnixMilli()
	q := query.Bind(tx, p.module.bindings.prefix)
	err = q.AccountReference.WithContext(ctx).Create(&model.AccountReference{ID: ref, PrincipalID: actor, ObjectID: obj.ID, Purpose: "terminal_recording", State: "active", CreatedAt: time.Now().UnixMilli(), ExpiresAt: &expiry})
	return obj.ID, err
}

// Seal streams committed chunks into a single asciicast file and pins its immutable version.
func (p *TerminalRecordings) Seal(ctx context.Context, tenant, project, session, actor guid.ID, chunks []guid.ID) (*PreparedExecutionArtifact, error) {
	if !p.Available() || len(chunks) == 0 {
		return nil, ErrUnsupported
	}
	m := p.module
	db := m.runtime.Database().Write()
	keys := []string{}
	var size int64
	for _, id := range chunks {
		q := query.Bind(db, m.bindings.prefix)
		count, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ObjectID.Eq(id), q.AccountReference.PrincipalID.Eq(actor), q.AccountReference.Purpose.Eq("terminal_recording"), q.AccountReference.State.Eq("active"), q.AccountReference.ExpiresAt.Gt(time.Now().UnixMilli())).Count()
		if err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, ErrNotFound
		}
		obj, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(id), q.Object.State.Eq("verified")).First()
		if err != nil {
			return nil, err
		}
		keys = append(keys, obj.StorageKey)
		size += obj.Size
	}
	reader := &terminalChunkReader{ctx: ctx, driver: m.driver, keys: keys}
	defer func() { _ = reader.Close() }()
	id, err := m.nextFileID()
	if err != nil {
		return nil, err
	}
	object, err := m.accounts.Stage(ctx, db, id, actor, "terminal/sealed/"+id.String(), reader, size, time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	return &PreparedExecutionArtifact{authority: ExecutionArtifactAuthority{TenantID: tenant, EntityID: project, ExecutionID: session}, purpose: "terminal_recording", name: fmt.Sprintf("terminal-%s.cast", session), slot: "recording", object: object, actor: actor}, nil
}
func (p *TerminalRecordings) AttachSealedOn(ctx context.Context, tx *gorm.DB, prepared *PreparedExecutionArtifact, session guid.ID) (PinnedResource, error) {
	if !p.Available() {
		return PinnedResource{}, ErrUnsupported
	}
	a := &ExecutionArtifactFiles{module: p.module, authorize: func(ctx context.Context, tx *gorm.DB, _ ExecutionArtifactAuthority, _ string, id guid.ID, owner ResourceOwner) error {
		if id == 0 {
			id = session
		}
		return p.authorize(ctx, tx, id, owner)
	}}
	pin, err := a.AttachOn(ctx, tx, prepared, session)
	if err != nil {
		return PinnedResource{}, err
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	if _, err = q.Node.WithContext(ctx).Where(q.Node.ID.Eq(pin.FileID)).Update(q.Node.ManagementMode, "resource"); err != nil {
		return PinnedResource{}, err
	}
	expiry := time.Now().UTC().Add(p.retentionDuration()).UnixMilli()
	_, err = q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).Update(q.ResourceBinding.ExpiresAt, expiry)
	return pin, err
}

// Only one immutable chunk is open at a time, regardless of recording duration.
type terminalChunkReader struct {
	ctx     context.Context
	driver  Driver
	keys    []string
	current io.ReadCloser
}

func (r *terminalChunkReader) Read(dst []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.keys) == 0 {
				return 0, io.EOF
			}
			var err error
			r.current, err = r.driver.Open(r.ctx, r.keys[0])
			if err != nil {
				return 0, err
			}
			r.keys = r.keys[1:]
		}
		n, err := r.current.Read(dst)
		if err == io.EOF {
			closeErr := r.current.Close()
			r.current = nil
			if closeErr != nil {
				return n, closeErr
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func (r *terminalChunkReader) Close() error {
	if r.current != nil {
		err := r.current.Close()
		r.current = nil
		return err
	}
	return nil
}
func (p *TerminalRecordings) ReleaseChunksOn(ctx context.Context, tx *gorm.DB, actor guid.ID, chunks []guid.ID) error {
	q := query.Bind(tx, p.module.bindings.prefix)
	for _, id := range chunks {
		if _, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ObjectID.Eq(id), q.AccountReference.PrincipalID.Eq(actor), q.AccountReference.Purpose.Eq("terminal_recording"), q.AccountReference.State.Eq("active")).Update(q.AccountReference.State, "inactive"); err != nil {
			return err
		}
	}
	return nil
}

// Prevent a new resource binding from laundering audit-only terminal bytes.
func (m *Module) authorizeTerminalRepinOn(ctx context.Context, tx *gorm.DB, space, file guid.ID) error {
	q := query.Bind(tx, m.bindings.prefix)
	bindings, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.SpaceID.Eq(space), q.ResourceBinding.FileID.Eq(file), q.ResourceBinding.Purpose.Eq("terminal_recording")).Find()
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	owner, err := (directoryTree{prefix: m.bindings.prefix}).resourceOwner(ctx, tx, space, file)
	if err != nil {
		return err
	}
	purpose, ok := m.bindings.purpose("terminal_recording")
	if !ok || purpose.ValidateResource == nil {
		return errOperationForbidden
	}
	for _, binding := range bindings {
		if binding.State != "active" || binding.ExpiresAt != nil && *binding.ExpiresAt <= time.Now().UnixMilli() {
			return errOperationForbidden
		}
		if err = purpose.ValidateResource(ctx, tx, binding.ResourceID, owner); err != nil {
			return err
		}
	}
	return nil
}
