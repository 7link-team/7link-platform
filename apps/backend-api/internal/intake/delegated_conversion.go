package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DelegatedNewAuthority evaluates current authority against the persisted card,
// including its real author. It is bound by the canonical project owner.
type DelegatedNewAuthority func(context.Context, *gorm.DB, guid.ID, guid.ID, TriageSource) (string, error)
type DelegatedNewConnector struct {
	Bind func(DelegatedNewAuthority) error
}
type DelegatedNewSources struct {
	ReadOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (TriageSource, string, error)
	ConvertOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID) error
}

func (m *Module) provideDelegatedNew(s *module.Services) error {
	if err := module.Provide[DelegatedNewConnector](s, DelegatedNewConnector{Bind: func(check DelegatedNewAuthority) error {
		if check == nil || m.delegatedNewAuthority != nil {
			return ErrInvalidArgument
		}
		m.delegatedNewAuthority = check
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[DelegatedNewSources](s, DelegatedNewSources{ReadOn: m.readDelegatedNewOn, ConvertOn: m.convertDelegatedNewOn})
}

func (m *Module) readDelegatedNewOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (TriageSource, string, error) {
	var empty TriageSource
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project || authn.PrincipalIDFromContext(ctx) <= 0 {
		return empty, "", ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, "", ErrInvalidArgument
	}
	if m.delegatedNewAuthority == nil {
		return empty, "", ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return empty, "", err
	}
	// CONVERTED is needed for current-authority checks before idempotent replay.
	if row.Kind != "NEW" || (row.Status != "TRIAGED" && (row.Status != "CONVERTED" || row.ConversionMode == nil || *row.ConversionMode != "NEW_REQUIREMENT")) || row.OpenRoundID != nil {
		return empty, "", ErrConflict
	}
	card, err := m.readTriageSourceOn(ctx, tx, tenant, row)
	if err != nil {
		return empty, "", err
	}
	source, err := m.delegatedNewAuthority(ctx, tx, tenant, project, card)
	if err != nil {
		return empty, "", err
	}
	if source == "" {
		return empty, "", ErrForbidden
	}
	// The first read is a hint. Authority acquires principal/project fences
	// before the source lock; require the identical source under those fences.
	if _, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(row.Version), q.LatestCardRevision.Eq(row.LatestCardRevision), q.Status.Eq(row.Status), q.OpenRoundID.IsNull()).Take(); err != nil {
		return empty, "", err
	}
	return card, source, nil
}

func (m *Module) convertDelegatedNewOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected, revision int, hash string, requirement guid.ID) error {
	card, authority, err := m.readDelegatedNewOn(ctx, tx, tenant, project, id)
	if err != nil {
		return err
	}
	if card.Version != expected || card.Revision != revision || card.Hash != hash {
		return ErrConflict
	}
	return m.writeSourceTargetOn(ctx, tx, tenant, project, id, expected, revision, hash, project, requirement, "NEW_REQUIREMENT", "AGENT", authority)
}
