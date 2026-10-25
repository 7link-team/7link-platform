package budget

import (
	"7link.com/platform/internal/budget/model"
	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"time"
)

var (
	ErrInvalidArgument = errors.New("budget: invalid argument")
	ErrUnavailable     = errors.New("budget: unavailable")
	ErrExceeded        = errors.New("budget: cap exceeded")
	ErrPriceUnknown    = errors.New("budget: price unknown for capped project")
	ErrConflict        = errors.New("budget: reservation replay conflicts")
)

type Fact struct {
	CapMicros                   *int64
	SpentMicros, ReservedMicros int64
	UnknownReserved             bool
}

// Provider is internal: callers must authorize their domain command, lock
// principals before projects, and roll back the transaction on any error.
type Provider struct {
	FactOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (Fact, error)
	ReserveOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, int64, bool) error
	SettleOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, int64, bool) error
}

func validTransaction(tx *gorm.DB, tenant, project guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	return nil
}

func (m *Module) FactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (Fact, error) {
	if err := validTransaction(tx, tenant, project); err != nil {
		return Fact{}, err
	}
	if m == nil || m.projects.LockOn == nil || m.names.Reservations.IsZero() {
		return Fact{}, ErrUnavailable
	}
	p, err := m.projects.LockOn(ctx, tx, tenant, project)
	if err != nil {
		return Fact{}, err
	}
	f := Fact{CapMicros: p.CapMicros, SpentMicros: p.SpentMicros}
	if f.SpentMicros < 0 || (f.CapMicros != nil && *f.CapMicros < 0) {
		return Fact{}, ErrInvalidArgument
	}
	q := query.BindReservations(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("RESERVED")).Clauses(clause.Locking{Strength: "UPDATE"}).Find()
	if err != nil {
		return Fact{}, err
	}
	for _, r := range rows {
		if !r.PriceKnown {
			f.UnknownReserved = true
		}
		if r.ReservedMicros < 0 || r.ReservedMicros > math.MaxInt64-f.ReservedMicros {
			return Fact{}, ErrInvalidArgument
		}
		f.ReservedMicros += r.ReservedMicros
	}
	return f, nil
}

func sourceValid(source string, id guid.ID, amount int64, known bool) bool {
	return (source == "EXECUTION" || source == "GATEWAY") && id > 0 && amount >= 0 && (known || amount == 0)
}

func (m *Module) reservationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, source string, id guid.ID) (*model.Reservation, error) {
	q := query.BindReservations(tx, m.names)
	return q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Source.Eq(source), q.SourceID.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
}

func (m *Module) ReserveOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, source string, id guid.ID, amount int64, known bool) error {
	if !sourceValid(source, id, amount, known) {
		return ErrInvalidArgument
	}
	f, err := m.FactOn(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	r, err := m.reservationOn(ctx, tx, tenant, source, id)
	if err == nil {
		if r.ProjectID != project || r.ReservedMicros != amount || r.PriceKnown != known {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if f.CapMicros != nil {
		if !known || f.UnknownReserved {
			return ErrPriceUnknown
		}
		ceiling := *f.CapMicros
		if f.SpentMicros > ceiling || f.ReservedMicros > ceiling-f.SpentMicros || amount > ceiling-f.SpentMicros-f.ReservedMicros {
			return ErrExceeded
		}
	}
	if amount > math.MaxInt64-f.ReservedMicros {
		return ErrInvalidArgument
	}
	if m.ids == nil {
		return ErrUnavailable
	}
	next, err := m.ids.Next()
	if err != nil {
		return err
	}
	if next <= 0 {
		return ErrUnavailable
	}
	r = &model.Reservation{ID: guid.ID(next), TenantID: tenant, ProjectID: project, Source: source, SourceID: id, ReservedMicros: amount, PriceKnown: known, State: "RESERVED", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = query.BindReservations(tx, m.names).WithContext(ctx).Create(r); err != nil {
		return err
	}
	return m.eventOn(ctx, tx, r, "budget.reserved")
}

// SettleOn records actual usage even above the estimate or project cap: money
// already consumed cannot be discarded. Unknown usage stays reserved until a
// known cost fact arrives; a terminal execution state alone is not such a fact.
func (m *Module) SettleOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, source string, id guid.ID, actual int64, known bool) error {
	if !sourceValid(source, id, actual, known) {
		return ErrInvalidArgument
	}
	if !known {
		return ErrPriceUnknown
	}
	if _, err := m.FactOn(ctx, tx, tenant, project); err != nil {
		return err
	}
	r, err := m.reservationOn(ctx, tx, tenant, source, id)
	if err != nil {
		return err
	}
	if r.ProjectID != project {
		return ErrConflict
	}
	if r.State == "SETTLED" {
		if (r.ActualMicros == nil) != (!known) || (known && *r.ActualMicros != actual) {
			return ErrConflict
		}
		return nil
	}
	if m.projects.SpendOn == nil {
		return ErrUnavailable
	}
	if err = m.projects.SpendOn(ctx, tx, tenant, project, actual); err != nil {
		return err
	}
	r.ActualMicros = &actual
	r.State = "SETTLED"
	now := time.Now().UTC().Truncate(time.Millisecond)
	r.SettledAt = &now
	q := query.BindReservations(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID)).UpdateSimple(q.State.Value(r.State), q.ActualMicros.Value(actual), q.SettledAt.Value(now)); err != nil {
		return err
	}
	return m.eventOn(ctx, tx, r, "budget.settled")
}

func (m *Module) eventOn(ctx context.Context, tx *gorm.DB, r *model.Reservation, kind string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, r.TenantID, kind, "budget_reservation", r.ID)
	e.Detail["source"] = r.Source
	e.Detail["source_id"] = r.SourceID.String()
	e.Detail["reserved_micros"] = r.ReservedMicros
	e.Detail["actual_micros"] = r.ActualMicros
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	actor := events.Actor{Kind: "SYSTEM", AuthoritySource: "budget:domain_command"}
	version := int64(1)
	if r.State == "SETTLED" {
		version = 2
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: kind, Subject: events.Subject{Kind: "budget_reservation", ID: r.ID}, SubjectVersion: version, Actor: actor, Data: e.Detail})
	return err
}
