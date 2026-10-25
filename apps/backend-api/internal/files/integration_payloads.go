package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// IntegrationPayloads keeps raw signed ingress in tracked, expiring Files
// objects. The integration owner must authenticate before staging and authorize
// the exact connection on every attach/read; no key or public URL is exposed.
type IntegrationPayloads struct {
	module    *Module
	authorize func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}

type PreparedIntegrationPayload struct {
	object                    *PreparedAccountObject
	tenant, connection, owner guid.ID
}

func (m *Module) providePayloads(s *module.Services) error {
	return module.Provide[*IntegrationPayloads](s, &IntegrationPayloads{module: m})
}

func (p *IntegrationPayloads) Bind(authorize func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
	if p == nil || p.module == nil || p.authorize != nil || authorize == nil {
		return ErrUnsupported
	}
	p.authorize = authorize
	return nil
}

func (p *IntegrationPayloads) ready() bool {
	return p != nil && p.module != nil && p.module.accounts != nil && p.module.driver != nil && p.authorize != nil
}

func (p *IntegrationPayloads) Stage(ctx context.Context, tenant, connection, owner guid.ID, data []byte) (*PreparedIntegrationPayload, error) {
	if !p.ready() || tenant <= 0 || connection <= 0 || owner <= 0 || len(data) == 0 || len(data) > 1<<20 {
		return nil, ErrUnsupported
	}
	db := p.module.runtime.Database().Write()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return p.authorize(ctx, tx, tenant, connection, owner) }); err != nil {
		return nil, err
	}
	id, err := p.module.nextFileID()
	if err != nil {
		return nil, err
	}
	obj, err := p.module.accounts.Stage(ctx, db, id, owner, fmt.Sprintf("integration/ingress/%s/%s/%s", tenant, connection, id), bytes.NewReader(data), int64(len(data)), time.Now().UTC().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	return &PreparedIntegrationPayload{object: obj, tenant: tenant, connection: connection, owner: owner}, nil
}

func (p *IntegrationPayloads) AttachOn(ctx context.Context, tx *gorm.DB, prepared *PreparedIntegrationPayload) (guid.ID, error) {
	if !p.ready() || tx == nil || tx.Statement == nil || prepared == nil || prepared.object == nil {
		return 0, ErrUnsupported
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrAccountTransaction
	}
	if err := p.authorize(ctx, tx, prepared.tenant, prepared.connection, prepared.owner); err != nil {
		return 0, err
	}
	obj, err := p.module.accounts.attachFileObject(ctx, tx, prepared.object, prepared.owner)
	if err != nil {
		return 0, err
	}
	id, err := p.module.nextFileID()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	expiry := now.Add(90 * 24 * time.Hour).UnixMilli()
	q := query.Bind(tx, p.module.bindings.prefix)
	err = q.AccountReference.WithContext(ctx).Create(&model.AccountReference{ID: id, PrincipalID: prepared.owner, ObjectID: obj.ID, Purpose: "integration_ingress", State: "active", CreatedAt: now.UnixMilli(), ExpiresAt: &expiry})
	return obj.ID, err
}

func (p *IntegrationPayloads) OpenOn(ctx context.Context, tx *gorm.DB, tenant, connection, owner, object guid.ID) (io.ReadCloser, error) {
	if !p.ready() || tx == nil || tx.Statement == nil || object <= 0 {
		return nil, ErrUnsupported
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrAccountTransaction
	}
	if err := p.authorize(ctx, tx, tenant, connection, owner); err != nil {
		return nil, err
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	count, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.ObjectID.Eq(object), q.AccountReference.PrincipalID.Eq(owner), q.AccountReference.Purpose.Eq("integration_ingress"), q.AccountReference.State.Eq("active"), q.AccountReference.ExpiresAt.Gt(time.Now().UnixMilli())).Count()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrNotFound
	}
	obj, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(object), q.Object.State.Eq("verified")).First()
	if err != nil {
		return nil, err
	}
	// Bind the object to its connection as well as its retention owner.
	if obj.StorageKey != fmt.Sprintf("integration/ingress/%s/%s/%s", tenant, connection, object) {
		return nil, ErrPinnedForbidden
	}
	return openStoredObject(ctx, p.module.driver, obj)
}
