package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
)

// ClientPackageConfirmationSource reads the active package aggregate. lock=true
// validates its existing publication prerequisites; false reads current identity
// and revision without locking source rows before notification recipient locks.
// This is not evidence of spec07 bundle construction or a READY Delivery.
type ClientPackageConfirmationSource func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ClientPackageConfirmationFact, bool, error)
type ClientPackageConfirmationFact struct {
	ProjectID, PackageID guid.ID
	Version              int
	Title                string
	Draft                bool
}

type DeliveryConfirmationFact struct {
	ID            guid.ID   `json:"id"`
	ProjectID     guid.ID   `json:"project_id"`
	PackageID     guid.ID   `json:"package_id"`
	RequestedBy   guid.ID   `json:"requested_by"`
	SourceVersion int       `json:"source_version"`
	Title         string    `json:"title"`
	RequestedAt   time.Time `json:"requested_at"`
}
type DeliveryConfirmationFacts struct {
	PendingOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]DeliveryConfirmationFact, error)
	ReadOn     func(context.Context, *gorm.DB, guid.ID, guid.ID) (DeliveryConfirmationFact, bool, error)
	EligibleOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}

func (m *Module) provideDeliveryConfirmations(s *module.Services) error {
	return module.Provide[DeliveryConfirmationFacts](s, DeliveryConfirmationFacts{PendingOn: m.pendingDeliveryConfirmationsOn, ReadOn: m.deliveryConfirmationOn, EligibleOn: m.deliveryConfirmationEligibleOn})
}
func (m *Module) injectDeliveryConfirmations(s *module.Services) error {
	if source, err := module.Resolve[ClientPackageConfirmationSource](s); err == nil {
		m.clientPackageConfirmation = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.clientPackageConfirmation == nil {
		return nil
	}
	sources, err := module.Resolve[governance.InboxSources](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "DELIVERY_CONFIRM", Read: m.deliveryConfirmationInbox})
}

func (m *Module) confirmationSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, lock bool) (ClientPackageConfirmationFact, error) {
	if m.clientPackageConfirmation == nil {
		return ClientPackageConfirmationFact{}, ErrUnavailable
	}
	f, found, err := m.clientPackageConfirmation(ctx, tx, tenant, project, id, lock)
	if err != nil {
		return f, err
	}
	if !found || f.ProjectID != project || f.PackageID != id {
		return f, ErrProjectNotFound
	}
	return f, nil
}

func (m *Module) requestDeliveryConfirmationOn(ctx context.Context, tx *gorm.DB, tenant, project, pkg, actor guid.ID, version int) (DeliveryConfirmationFact, error) {
	var out DeliveryConfirmationFact
	if pkg <= 0 || version < 1 || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrInvalidArgument
	}
	if _, _, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "delivery_compose"); err != nil {
		return out, err
	}
	source, err := m.confirmationSourceOn(ctx, tx, tenant, project, pkg, true)
	if err != nil {
		return out, err
	}
	if source.Version != version {
		return out, ErrWorkVersion
	}
	if !source.Draft {
		return out, ErrResultState
	}
	q := query.BindDeliveryConfirmations(tx, m.service.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.PackageID.Eq(pkg), q.SourceVersion.Eq(version), q.WithdrawnAt.IsNull()).First()
	if err == nil {
		if prior.WithdrawnAt != nil {
			return out, ErrResultState
		}
		return confirmationFact(*prior, source.Title), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	activeKey := "ACTIVE"
	row := model.DeliveryConfirmation{ID: guid.ID(id), TenantID: tenant, ProjectID: project, PackageID: pkg, SourceVersion: version, ActiveKey: &activeKey, RequestedBy: actor, RequestedAt: time.Now().UTC().Truncate(time.Millisecond), Version: 1}
	if err = q.WithContext(ctx).Create(&row); err != nil {
		return out, err
	}
	if err = m.deliveryConfirmationEventOn(ctx, tx, row, actor, "delivery.confirmation_requested"); err != nil {
		return out, err
	}
	return confirmationFact(row, source.Title), nil
}

func (m *Module) withdrawDeliveryConfirmationOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID) (DeliveryConfirmationFact, error) {
	var out DeliveryConfirmationFact
	if id <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrInvalidArgument
	}
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "delivery_compose")
	if err != nil {
		return out, err
	}
	if kind != "HUMAN" {
		return out, ErrWorkForbidden
	}
	q := query.BindDeliveryConfirmations(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrProjectNotFound
	}
	if err != nil {
		return out, err
	}
	source, err := m.confirmationSourceOn(ctx, tx, tenant, project, row.PackageID, false)
	if err != nil {
		return out, err
	}
	if row.WithdrawnAt != nil {
		return confirmationFact(*row, source.Title), nil
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	row.WithdrawnAt, row.WithdrawnBy, row.Version = &at, &actor, row.Version+1
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"active_key": nil, "withdrawn_at": at, "withdrawn_by": actor, "version": row.Version}); err != nil {
		return out, err
	}
	if err = m.deliveryConfirmationEventOn(ctx, tx, *row, actor, "delivery.withdrawn"); err != nil {
		return out, err
	}
	return confirmationFact(*row, source.Title), nil
}

func confirmationFact(row model.DeliveryConfirmation, title string) DeliveryConfirmationFact {
	return DeliveryConfirmationFact{ID: row.ID, ProjectID: row.ProjectID, PackageID: row.PackageID, RequestedBy: row.RequestedBy, SourceVersion: row.SourceVersion, Title: title, RequestedAt: row.RequestedAt}
}
func (m *Module) deliveryConfirmationEventOn(ctx context.Context, tx *gorm.DB, row model.DeliveryConfirmation, actor guid.ID, typ string) error {
	kind, err := m.resultActorKindOn(ctx, tx, actor, "")
	if err != nil {
		return err
	}
	detail := map[string]any{"package_id": row.PackageID.String(), "source_version": row.SourceVersion, "requested_by": row.RequestedBy.String(), "source_kind": "client_package", "authority_source": "permission:delivery_compose", "actor_kind": kind}
	e := audit.NewDomainEvent(ctx, row.TenantID, typ, "delivery_confirmation", row.ID)
	for key, value := range detail {
		e.Detail[key] = value
	}
	if err = m.service.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: typ, Subject: events.Subject{Kind: "delivery_confirmation", ID: row.ID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:delivery_compose"}, Data: detail})
	return err
}

func (m *Module) deliveryConfirmationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (DeliveryConfirmationFact, bool, error) {
	var out DeliveryConfirmationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	if m.clientPackageConfirmation == nil {
		return out, false, ErrUnavailable
	}
	q := query.BindDeliveryConfirmations(tx, m.service.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.WithdrawnAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(row.ProjectID), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if project.Stage == "CLOSED" || project.Stage == "CANCELLED" { //nolint:misspell // Frozen protocol state.
		return out, false, nil
	}
	source, err := m.confirmationSourceOn(ctx, tx, tenant, row.ProjectID, row.PackageID, false)
	if errors.Is(err, ErrProjectNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if !source.Draft || source.Version != row.SourceVersion {
		return out, false, nil
	}
	return confirmationFact(*row, source.Title), true, nil
}
func (m *Module) pendingDeliveryConfirmationsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]DeliveryConfirmationFact, error) {
	if tx == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	if m.clientPackageConfirmation == nil {
		return nil, ErrUnavailable
	}
	q := query.BindDeliveryConfirmations(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WithdrawnAt.IsNull())
	if project > 0 {
		base = base.Where(q.ProjectID.Eq(project))
	}
	rows, err := base.Order(q.RequestedAt, q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []DeliveryConfirmationFact{}
	for _, row := range rows {
		f, found, err := m.deliveryConfirmationOn(ctx, tx, tenant, row.ID)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, f)
		}
	}
	return out, nil
}
func (m *Module) deliveryConfirmationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID) (bool, error) {
	f, found, err := m.deliveryConfirmationOn(ctx, tx, tenant, id)
	if err != nil || !found {
		return false, err
	}
	if actor <= 0 || m.principals.ActiveHumanByID == nil || m.members == nil || m.workPolicy == nil {
		return false, ErrUnavailable
	}
	h, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil || !active || h.ID != actor {
		return false, err
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil || !active {
		return false, err
	}
	c, err := m.workPolicy(ctx, tx, tenant, f.ProjectID, "delivery_send", actor)
	if err != nil {
		return false, err
	}
	if !c.AllowsEntityRows(f.ProjectID) {
		return false, nil
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(f.ProjectID), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.AllowAll || slices.Contains(c.ResourceIDs, f.ProjectID.String()) || (row.CreatedBy != nil && slices.Contains(c.OwnerIDs, row.CreatedBy.String())), nil
}
func (m *Module) deliveryConfirmationInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	if req.Kind != "DELIVERY_CONFIRM" || req.TenantID <= 0 || authn.TenantIDFromContext(ctx) != req.TenantID || authn.PrincipalIDFromContext(ctx) != req.PrincipalID || req.Limit < 1 || req.Limit > 101 {
		return nil, ErrWorkForbidden
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	out := []governance.InboxItem{}
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := m.pendingDeliveryConfirmationsOn(ctx, tx, req.TenantID, req.ProjectID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			ok, err := m.deliveryConfirmationEligibleOn(ctx, tx, req.TenantID, r.ID, req.PrincipalID)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Kind: "DELIVERY_CONFIRM", ID: r.ID, Priority: "NORMAL", CreatedAt: r.RequestedAt}, ProjectID: r.ProjectID, Title: r.Title, ObjectType: "project", ObjectID: r.ProjectID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
