package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type CredentialRotationView struct {
	ID           guid.ID   `json:"id"`
	State        string    `json:"state" enum:"PENDING_DELIVERY,ACTIVE,FAILED"`
	ReasonCode   string    `json:"reason_code"`
	Attempts     int       `json:"attempts"`
	CredentialID *guid.ID  `json:"credential_id"`
	RetireOldAt  time.Time `json:"retire_old_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type CredentialLifecycleView struct {
	ID          guid.ID    `json:"id"`
	State       string     `json:"state" enum:"PENDING_DELIVERY,ACTIVE,RETIRING,REVOKED"`
	ReasonCode  string     `json:"reason_code"`
	RetireAt    *time.Time `json:"retire_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}
type CredentialDelivery struct {
	RotationID   guid.ID   `json:"rotation_id"`
	CredentialID guid.ID   `json:"credential_id"`
	ClientID     guid.ID   `json:"client_id"`
	ClientSecret string    `json:"client_secret" readOnly:"true" doc:"Shown once in this delivery response; persist securely before acknowledging."`
	RetireOldAt  time.Time `json:"retire_old_at"`
}

func rotationView(r *model.CredentialRotation) CredentialRotationView {
	return CredentialRotationView{r.ID, r.State, r.ReasonCode, r.Attempts, r.CredentialID, r.RetireOldAt, r.CreatedAt, r.UpdatedAt}
}
func (m *Module) rotationAvailable() bool {
	return m != nil && m.ids != nil && m.enrollmentLease != nil && m.enrollmentIAM.IssueCredential != nil && m.enrollmentIAM.ListCredentials != nil && m.enrollmentIAM.RevokeCredential != nil
}

// IAM operations commit independently. The renewing machine lease serializes the
// saga, while each fleet transaction fences the lease before committing facts.
func (m *Module) rotationRun(ctx context.Context, tenant, machine guid.ID, fn func(context.Context, *gorm.DB, *dlock.Lock) error) error {
	if tenant <= 0 || machine <= 0 {
		return ErrInvalidArgument
	}
	if !m.rotationAvailable() {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	lock, err := m.enrollmentLease(work, "fleet:rotation:"+tenant.String()+":"+machine.String(), time.Minute, func(string, error) { cancel() })
	if err != nil {
		return err
	}
	defer func() {
		if e := lock.Unlock(context.WithoutCancel(ctx)); e != nil {
			slog.WarnContext(ctx, "fleet rotation lease release failed", "machine_id", machine, "error", e)
		}
	}()
	return fn(work, db, lock)
}
func (m *Module) rotationEventOn(ctx context.Context, tx *gorm.DB, r *model.CredentialRotation) error {
	return m.machineEventOn(ctx, tx, r.TenantID, r.MachineID, "machine.credential_changed", 0, map[string]any{"rotation_id": r.ID, "credential_id": r.CredentialID, "state": r.State, "phase": r.Phase, "attempts": r.Attempts, "reason_code": r.ReasonCode, "retire_old_at": r.RetireOldAt})
}
func (m *Module) saveRotationOn(ctx context.Context, tx *gorm.DB, r *model.CredentialRotation) error {
	r.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindRotations(tx, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.TenantID.Eq(r.TenantID)).Updates(map[string]any{"state": r.State, "phase": r.Phase, "reason_code": r.ReasonCode, "active_key": r.ActiveKey, "attempts": r.Attempts, "credential_id": r.CredentialID, "issued_at": r.IssuedAt, "updated_at": r.UpdatedAt})
	if err != nil {
		return err
	}
	return m.rotationEventOn(ctx, tx, r)
}
func (m *Module) RequestCredentialRotation(ctx context.Context, tenant, machine guid.ID) (CredentialRotationView, error) {
	var out CredentialRotationView
	err := m.rotationRun(ctx, tenant, machine, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		var principal guid.ID
		guard := func(tx *gorm.DB) error {
			if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
				return e
			}
			row, e := m.machineOn(ctx, tx, tenant, machine, true)
			if e != nil {
				return e
			}
			if row.State == "REVOKED" {
				return ErrMachineRevoked
			}
			if principal > 0 && principal != row.PrincipalID {
				return ErrVersionConflict
			}
			principal = row.PrincipalID
			if e = lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			q := query.BindRotations(tx, m.names)
			n, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ActiveKey.IsNotNull()).Count()
			if e != nil {
				return e
			}
			if n > 0 {
				return ErrVersionConflict
			}
			return nil
		}
		if e := db.WithContext(ctx).Transaction(guard); e != nil {
			return e
		}
		credentials, e := m.enrollmentIAM.ListCredentials(ctx, tenant, principal)
		if e != nil {
			return e
		}
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := guard(tx); e != nil {
				return e
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			retire := now.Add(24 * time.Hour)
			q := query.BindCredentials(tx, m.names)
			for _, c := range credentials {
				if c.ServiceAccountID != principal || c.ID <= 0 {
					return ErrUnavailable
				}
				if c.RevokedAt != nil || c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
					continue
				}
				prior, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(c.ID)).First()
				if errors.Is(e, gorm.ErrRecordNotFound) {
					e = q.WithContext(ctx).Create(&model.MachineCredential{ID: c.ID, TenantID: tenant, MachineID: machine, State: "RETIRING", RetireAt: &retire, CreatedAt: now, UpdatedAt: now})
				} else if e == nil && prior.State == "ACTIVE" {
					_, e = q.WithContext(ctx).Where(q.ID.Eq(c.ID), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": "RETIRING", "retire_at": retire, "updated_at": now})
				}
				if e != nil {
					return e
				}
			}
			id, e := m.ids.Next()
			if e != nil {
				return e
			}
			if id <= 0 {
				return ErrUnavailable
			}
			active := 1
			r := &model.CredentialRotation{ID: guid.ID(id), TenantID: tenant, MachineID: machine, PrincipalID: principal, RequestedBy: authn.PrincipalIDFromContext(ctx), State: "PENDING_DELIVERY", Phase: "QUEUED", ActiveKey: &active, RetireOldAt: retire, CreatedAt: now, UpdatedAt: now}
			if e = query.BindRotations(tx, m.names).WithContext(ctx).Create(r); e != nil {
				return e
			}
			if e = m.rotationEventOn(humanMachineContext(ctx), tx, r); e != nil {
				return e
			}
			out = rotationView(r)
			return nil
		})
	})
	return out, err
}
func rotationName(r *model.CredentialRotation) string {
	return fmt.Sprintf("runner-rotation-%s-%d", r.ID.String(), r.Attempts)
}
func rotationHumanContext(ctx context.Context, r *model.CredentialRotation) context.Context {
	return humanMachineContext(authn.WithClaims(ctx, &authn.Claims{TenantID: r.TenantID, PrincipalID: r.RequestedBy, SubjectType: authn.SubjectTypePrincipal}))
}

// Lock principals in stable order before business locks. The requesting Human's
// authority is checked again after IAM issues, before any secret can be exposed.
func (m *Module) rotationRunnerGuard(ctx context.Context, tx *gorm.DB, lock *dlock.Lock, r *model.CredentialRotation, session guid.ID, epoch int64, checkHuman bool) error {
	if checkHuman {
		if m.principals.Lock == nil {
			return ErrUnavailable
		}
		ids := []guid.ID{r.RequestedBy, authn.PrincipalIDFromContext(ctx)}
		slices.Sort(ids)
		for _, id := range slices.Compact(ids) {
			if e := m.principals.Lock(ctx, tx, id); e != nil {
				return e
			}
		}
		if e := m.controlAuthorityOn(rotationHumanContext(ctx, r), tx, r.TenantID, "machine_manage"); e != nil {
			return e
		}
	}
	if e := m.HeartbeatOn(ctx, tx, r.TenantID, r.MachineID, session, epoch); e != nil {
		return e
	}
	return lock.FenceOn(ctx, tx)
}
func (m *Module) activeRotation(ctx context.Context, db *gorm.DB, tenant, machine guid.ID) (*model.CredentialRotation, error) {
	q := query.BindRotations(db, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ActiveKey.IsNotNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return r, e
}

// compensateRotation revokes by deterministic attempt name even when the process
// died after IAM committed but before recording the returned credential ID.
// It deliberately uses service authority, so revoking the requesting Human does
// not strand a valid, unacknowledged credential.
func (m *Module) compensateRotation(ctx context.Context, db *gorm.DB, lock *dlock.Lock, r *model.CredentialRotation, terminal bool) error {
	commit := func(fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, e := m.machineOn(ctx, tx, r.TenantID, r.MachineID, true); e != nil {
				return e
			}
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			return fn(tx)
		})
	}
	if e := commit(func(tx *gorm.DB) error {
		r.Phase = "COMPENSATING"
		if terminal {
			r.ReasonCode = "AUTHORITY_LOST"
		}
		return m.saveRotationOn(ctx, tx, r)
	}); e != nil {
		return e
	}
	rows, e := m.enrollmentIAM.ListCredentials(ctx, r.TenantID, r.PrincipalID)
	if e != nil {
		return e
	}
	for _, c := range rows {
		if c.ServiceAccountID != r.PrincipalID {
			return ErrUnavailable
		}
		if c.Name != rotationName(r) && (r.CredentialID == nil || c.ID != *r.CredentialID) {
			continue
		}
		if c.RevokedAt == nil {
			if e = m.enrollmentIAM.RevokeCredential(ctx, r.TenantID, r.PrincipalID, c.ID); e != nil {
				return e
			}
		}
	}
	return commit(func(tx *gorm.DB) error {
		q := query.BindCredentials(tx, m.names)
		if r.CredentialID != nil {
			if _, e := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.MachineID.Eq(r.MachineID), q.ID.Eq(*r.CredentialID)).Updates(map[string]any{"state": "REVOKED", "reason_code": "DELIVERY_UNACKNOWLEDGED", "updated_at": time.Now().UTC()}); e != nil {
				return e
			}
		}
		if terminal || r.ReasonCode == "AUTHORITY_LOST" || r.Attempts >= 5 {
			r.State, r.Phase, r.ActiveKey = "FAILED", "DONE", nil
			if r.ReasonCode == "" {
				r.ReasonCode = "DELIVERY_FAILED"
			}
		} else {
			r.Phase = "QUEUED"
			r.CredentialID = nil
			r.IssuedAt = nil
		}
		return m.saveRotationOn(ctx, tx, r)
	})
}
func (m *Module) PullCredentialDelivery(ctx context.Context, tenant, machine, session guid.ID, epoch int64) ([]CredentialDelivery, error) {
	out := []CredentialDelivery{}
	err := m.rotationRun(ctx, tenant, machine, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		// Even an empty poll authenticates current IAM and the current connection.
		if e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); e != nil {
				return e
			}
			return lock.FenceOn(ctx, tx)
		}); e != nil {
			return e
		}
		r, e := m.activeRotation(ctx, db, tenant, machine)
		if e != nil || r == nil {
			return e
		}
		if r.Phase == "ISSUING" || r.Phase == "COMPENSATING" || r.Phase == "DELIVERED" && (r.IssuedAt == nil || !r.IssuedAt.Add(time.Minute).After(time.Now().UTC())) {
			if e = m.compensateRotation(ctx, db, lock, r, false); e != nil {
				return e
			}
			if r.ActiveKey == nil {
				return nil
			}
		}
		if r.Phase == "DELIVERED" {
			return nil
		}
		if r.Phase != "QUEUED" {
			return ErrVersionConflict
		}
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.rotationRunnerGuard(ctx, tx, lock, r, session, epoch, true); e != nil {
				return e
			}
			r.Phase = "ISSUING"
			r.Attempts++
			return m.saveRotationOn(ctx, tx, r)
		})
		if e != nil {
			if errors.Is(e, ErrMachineForbidden) || errors.Is(e, ErrMachineRevoked) {
				return errors.Join(e, m.compensateRotation(context.WithoutCancel(ctx), db, lock, r, true))
			}
			return e
		}
		secret, e := m.enrollmentIAM.IssueCredential(rotationHumanContext(ctx, r), tenant, r.PrincipalID, rotationName(r), []string{"openid"}, nil)
		if e != nil {
			return errors.Join(e, m.compensateRotation(context.WithoutCancel(ctx), db, lock, r, false))
		}
		if secret.Credential.ID <= 0 || secret.Credential.ServiceAccountID != r.PrincipalID || secret.Secret == "" {
			return errors.Join(ErrUnavailable, m.compensateRotation(context.WithoutCancel(ctx), db, lock, r, false))
		}
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.rotationRunnerGuard(ctx, tx, lock, r, session, epoch, true); e != nil {
				return e
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			id := secret.Credential.ID
			c := &model.MachineCredential{ID: id, TenantID: tenant, MachineID: machine, RotationID: &r.ID, State: "PENDING_DELIVERY", CreatedAt: now, UpdatedAt: now}
			if e := query.BindCredentials(tx, m.names).WithContext(ctx).Create(c); e != nil {
				return e
			}
			r.Phase, r.CredentialID, r.IssuedAt = "DELIVERED", &id, &now
			return m.saveRotationOn(ctx, tx, r)
		})
		if e != nil {
			return errors.Join(e, m.compensateRotation(context.WithoutCancel(ctx), db, lock, r, errors.Is(e, ErrMachineForbidden) || errors.Is(e, ErrMachineRevoked)))
		}
		// IAM's client_credentials lookup selects credential.id, not principal_id.
		out = append(out, CredentialDelivery{r.ID, secret.Credential.ID, secret.Credential.ID, secret.Secret, r.RetireOldAt})
		return nil
	})
	return out, err
}
func (m *Module) AckCredentialDelivery(ctx context.Context, tenant, machine, id, credential, session guid.ID, epoch int64) (CredentialRotationView, error) {
	var out CredentialRotationView
	if id <= 0 || credential <= 0 {
		return out, ErrInvalidArgument
	}
	err := m.rotationRun(ctx, tenant, machine, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); e != nil {
				return e
			}
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			q := query.BindRotations(tx, m.names)
			r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrMachineNotFound
			}
			if e != nil {
				return e
			}
			if r.CredentialID == nil || *r.CredentialID != credential {
				return ErrVersionConflict
			}
			if r.State == "ACTIVE" {
				out = rotationView(r)
				return nil
			}
			if r.State != "PENDING_DELIVERY" || r.Phase != "DELIVERED" || r.IssuedAt == nil || !r.IssuedAt.Add(time.Minute).After(time.Now().UTC()) {
				return ErrVersionConflict
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			c := query.BindCredentials(tx, m.names)
			result, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.MachineID.Eq(machine), c.ID.Eq(credential), c.State.Eq("PENDING_DELIVERY")).Updates(map[string]any{"state": "ACTIVE", "delivered_at": now, "updated_at": now})
			if e != nil {
				return e
			}
			if result.RowsAffected != 1 {
				return ErrVersionConflict
			}
			r.State, r.Phase, r.ActiveKey = "ACTIVE", "DONE", nil
			if e = m.saveRotationOn(ctx, tx, r); e != nil {
				return e
			}
			out = rotationView(r)
			return nil
		})
	})
	return out, err
}

// Bounded scans resume each machine saga using trusted service authority.
func (m *Module) expireCredentialRotations(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindRotations(db, m.names)
	cursor := guid.ID(0)
	for {
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ActiveKey.IsNotNull(), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row.ID
			if row.Phase == "QUEUED" {
				continue
			}
			if row.Phase == "DELIVERED" && row.IssuedAt != nil && row.IssuedAt.Add(time.Minute).After(now) {
				continue
			}
			if e = m.rotationRun(ctx, tenant, row.MachineID, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
				r, e := m.activeRotation(ctx, db, tenant, row.MachineID)
				if e != nil || r == nil {
					return e
				}
				if r.Phase == "QUEUED" || r.Phase == "DELIVERED" && r.IssuedAt != nil && r.IssuedAt.Add(time.Minute).After(now) {
					return nil
				}
				return m.compensateRotation(ctx, db, lock, r, false)
			}); e != nil {
				return e
			}
		}
	}
	c := query.BindCredentials(db, m.names)
	cursor = 0
	for {
		rows, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.State.Eq("RETIRING"), c.RetireAt.Lte(now), c.ID.Gt(cursor)).Order(c.ID).Limit(100).Find()
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			cursor = row.ID
			if e = m.retireCredential(ctx, tenant, row.MachineID, row.ID, now); e != nil {
				return e
			}
		}
	}
}
func (m *Module) retireCredential(ctx context.Context, tenant, machine, id guid.ID, now time.Time) error {
	return m.rotationRun(ctx, tenant, machine, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		var principal guid.ID
		guard := func(tx *gorm.DB) error {
			r, e := m.machineOn(ctx, tx, tenant, machine, true)
			if e != nil {
				return e
			}
			principal = r.PrincipalID
			return lock.FenceOn(ctx, tx)
		}
		if e := db.WithContext(ctx).Transaction(guard); e != nil {
			return e
		}
		if e := m.enrollmentIAM.RevokeCredential(ctx, tenant, principal, id); e != nil {
			return e
		}
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := guard(tx); e != nil {
				return e
			}
			q := query.BindCredentials(tx, m.names)
			_, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id), q.State.Eq("RETIRING"), q.RetireAt.Lte(now)).Updates(map[string]any{"state": "REVOKED", "reason_code": "ROTATED", "updated_at": now})
			if e != nil {
				return e
			}
			return m.machineEventOn(ctx, tx, tenant, machine, "machine.credential_changed", 0, map[string]any{"credential_id": id, "state": "REVOKED", "reason_code": "ROTATED"})
		})
	})
}
