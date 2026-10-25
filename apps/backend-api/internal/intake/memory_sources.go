package intake

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// MemorySourceText exposes original bytes only to the trusted memory provenance
// adapter. The caller first checks current execution, Agent and project authority.
type MemorySourceText struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error)
}

func (m *Module) provideMemorySourceText(s *module.Services) error {
	return module.Provide[MemorySourceText](s, MemorySourceText{ReadOn: m.memorySourceTextOn})
}

func (m *Module) memorySourceTextOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (string, error) {
	if m == nil || m.names.Records.IsZero() {
		return "", ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 {
		return "", ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	r, err := q.WithContext(ctx).Select(q.RawContent, q.RawContentHash).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrInvalidArgument
	}
	if err != nil {
		return "", err
	}
	if r.RawContent == nil {
		return "", ErrUnavailable
	}
	return *r.RawContent, nil
}
