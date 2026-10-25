package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrEnrollmentSlowDown = errors.New("fleet: enrollment polling too frequently")
	ErrEnrollmentPending  = errors.New("fleet: enrollment authorization pending")
	ErrEnrollmentConsumed = errors.New("fleet: enrollment credentials already delivered")
	ErrEnrollmentFailed   = errors.New("fleet: enrollment failed; restart device authorization")
)

type EnrollmentStart struct {
	TenantID guid.ID          `json:"tenant_id"`
	ClientID guid.ID          `json:"client_id"`
	Name     string           `json:"name" minLength:"1" maxLength:"100"`
	OS       string           `json:"os" enum:"WINDOWS,MACOS,LINUX"`
	Facts    *EnrollmentFacts `json:"facts,omitempty"`
}

// EnrollmentFacts are device-reported observations for Human comparison only.
// They never authorize runtime routing or replace authenticated inventory.
type EnrollmentFacts struct {
	Hostname string              `json:"hostname" minLength:"1" maxLength:"255"`
	Arch     string              `json:"arch" minLength:"1" maxLength:"32"`
	Runtimes []EnrollmentRuntime `json:"runtimes" maxItems:"2"`
}
type EnrollmentRuntime struct {
	Kind      string `json:"kind" enum:"CODEX,CLAUDE_CODE"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty" maxLength:"64"`
	Error     string `json:"error,omitempty" enum:",NOT_FOUND,TIMEOUT,VERSION_FAILED"`
}

func validateEnrollmentFacts(f *EnrollmentFacts) bool {
	if f == nil {
		return true
	}
	if !boundedFact(f.Hostname, 255) || !boundedFact(f.Arch, 32) || f.Runtimes == nil || len(f.Runtimes) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, r := range f.Runtimes {
		if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, r.Kind) || seen[r.Kind] {
			return false
		}
		seen[r.Kind] = true
		if r.Available {
			if !boundedFact(r.Version, 64) || r.Error != "" {
				return false
			}
		} else if r.Version != "" || !slices.Contains([]string{"NOT_FOUND", "TIMEOUT", "VERSION_FAILED"}, r.Error) {
			return false
		}
	}
	return true
}

type EnrollmentStarted struct {
	ID     guid.ID                   `json:"id"`
	Device oauth.DeviceAuthorization `json:"device"`
}
type EnrollmentCredential struct {
	MachineID  guid.ID                                 `json:"machine_id"`
	Credential identity.ServiceAccountCredentialSecret `json:"credential"`
}

func enrollmentHash(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }
func userCodeHash(value string) string {
	return enrollmentHash(strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(value)), "-", ""))
}

func (m *Module) StartEnrollment(ctx context.Context, in EnrollmentStart) (EnrollmentStarted, error) {
	if in.TenantID <= 0 || in.ClientID <= 0 || !boundedFact(in.Name, 100) || !slices.Contains([]string{"WINDOWS", "MACOS", "LINUX"}, in.OS) || !validateEnrollmentFacts(in.Facts) {
		return EnrollmentStarted{}, ErrInvalidArgument
	}
	if m == nil || m.ids == nil || !m.enrollmentIAM.Bound() {
		return EnrollmentStarted{}, ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return EnrollmentStarted{}, err
	}
	device, err := m.enrollmentIAM.StartDevice(ctx, in.ClientID.String(), "")
	if err != nil {
		return EnrollmentStarted{}, err
	}
	// The public client belongs to IAM's tenant; never trust a device-supplied tenant.
	request, err := m.enrollmentIAM.LookupDevice(ctx, in.TenantID, device.UserCode)
	if err != nil {
		return EnrollmentStarted{}, err
	}
	if request.ClientID != in.ClientID || device.ExpiresIn <= 0 {
		return EnrollmentStarted{}, ErrInvalidArgument
	}
	// IAM's generic device grant can mint the approving Human's token. Never
	// expose its bearer proof to a machine: return a one-way, domain-separated
	// enrollment proof and discard the original. Only fleet consumes this proof;
	// the inaccessible IAM grant expires without ever issuing a Human token.
	device.DeviceCode = enrollmentHash("fleet:machine-enrollment:v1:" + device.DeviceCode)
	id, err := m.ids.Next()
	if err != nil {
		return EnrollmentStarted{}, err
	}
	if id <= 0 {
		return EnrollmentStarted{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	expiry := now.Add(time.Duration(device.ExpiresIn) * time.Second)
	if request.ExpiresAt.Before(expiry) {
		expiry = request.ExpiresAt
	}
	if expiry.After(now.Add(300 * time.Second)) {
		expiry = now.Add(300 * time.Second)
		device.ExpiresIn = 300
	}
	if device.Interval < 1 || device.Interval > 300 {
		device.Interval = 5
	}
	row := &model.Enrollment{PollIntervalSeconds: int64(device.Interval), ID: guid.ID(id), TenantID: in.TenantID, ClientID: in.ClientID, DeviceHash: enrollmentHash(device.DeviceCode), UserHash: userCodeHash(device.UserCode), Name: in.Name, OS: in.OS, State: "PENDING", ExpiresAt: expiry, CreatedAt: now, UpdatedAt: now}
	if in.Facts != nil {
		raw, e := json.Marshal(in.Facts)
		if e != nil {
			return EnrollmentStarted{}, e
		}
		value := string(raw)
		row.FactsJSON = &value
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := query.BindEnrollments(tx, m.names).WithContext(ctx).Create(row); e != nil {
			return e
		}
		return m.enrollmentEventOn(ctx, tx, row.TenantID, row.ID, "PENDING")
	})
	// A failed fleet insert leaves only an expiring IAM grant, no account or credential.
	if err != nil {
		return EnrollmentStarted{}, err
	}
	return EnrollmentStarted{ID: row.ID, Device: device}, nil
}

func (m *Module) enrollmentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.Enrollment, error) {
	if err := fleetTransaction(tx, tenant, id); err != nil {
		return nil, err
	}
	q := query.BindEnrollments(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	return row, err
}

// enrollmentRun serializes a saga without keeping a SQL transaction open while
// IAM commits. Every fleet commit fences the lease. A lost lease cancels IAM work.
func (m *Module) enrollmentRun(ctx context.Context, id guid.ID, fn func(context.Context, *gorm.DB, *dlock.Lock) error) error {
	if m == nil || m.enrollmentLease == nil || !m.enrollmentIAM.Bound() {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	lock, err := m.enrollmentLease(work, "fleet:enrollment:"+id.String(), time.Minute, func(string, error) { cancel() })
	if err != nil {
		return err
	}
	defer func() {
		if e := lock.Unlock(context.WithoutCancel(ctx)); e != nil {
			slog.WarnContext(ctx, "fleet enrollment lease release failed", "enrollment_id", id, "error", e)
		}
	}()
	return fn(work, db, lock)
}

func (m *Module) enrollmentUpdate(ctx context.Context, db *gorm.DB, lock *dlock.Lock, tenant, id guid.ID, values map[string]any) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lock.FenceOn(ctx, tx); err != nil {
			return err
		}
		q := query.BindEnrollments(tx, m.names)
		values["updated_at"] = time.Now().UTC().Truncate(time.Millisecond)
		result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(values)
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrMachineNotFound
		}
		current, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return err
		}
		state := current.State
		return m.enrollmentEventOn(ctx, tx, tenant, id, state)
	})
}

func (m *Module) enrollmentEventOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, state string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.enrollment.state_changed", "machine_enrollment", id)
	event.Detail["state"] = state
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	kind, authority := "SYSTEM", "SYSTEM:fleet"
	if actor > 0 {
		kind, authority = "HUMAN", "permission:machine_manage"
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "machine.enrollment_changed", Subject: events.Subject{Kind: "machine_enrollment", ID: id}, SubjectVersion: 1, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Data: map[string]any{"state": state}})
	return err
}

// ConfirmEnrollment reuses IAM's RFC8628 decision, then creates only a service
// account. The device obtains its one-time credential through the device grant.
func (m *Module) ConfirmEnrollment(ctx context.Context, tenant, id guid.ID, code string, approve bool) error {
	return m.DecideEnrollment(ctx, tenant, id, code, approve, "")
}

// DecideEnrollment preserves the existing saga; an optional name is frozen
// before IAM approval and must match on retries after the pending state.
func (m *Module) DecideEnrollment(ctx context.Context, tenant, id guid.ID, code string, approve bool, name string) error {
	if name != "" && (!boundedFact(name, 100) || strings.TrimSpace(name) == "") {
		return ErrInvalidArgument
	}
	return m.enrollmentRun(ctx, id, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		var row *model.Enrollment
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
				return e
			}
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			var e error
			row, e = m.enrollmentOn(ctx, tx, tenant, id)
			if e != nil {
				return e
			}
			if row.UserHash != userCodeHash(code) {
				return ErrMachineNotFound
			}
			if name != "" && name != row.Name && row.State != "PENDING" {
				return ErrVersionConflict
			}
			if row.State == "APPROVED" || row.State == "COMPLETE" {
				return nil
			}
			if !slices.Contains([]string{"PENDING", "PROVISIONING", "APPROVING", "COMPENSATING"}, row.State) {
				return ErrEnrollmentFailed
			}
			if !row.ExpiresAt.After(time.Now().UTC()) && row.State == "PENDING" {
				return ErrEnrollmentFailed
			}
			if approve && row.State == "PENDING" {
				chosen := row.Name
				if name != "" {
					chosen = name
				}
				machines := query.BindMachines(tx, m.names)
				count, e := machines.WithContext(ctx).Where(machines.TenantID.Eq(tenant), machines.NameKey.Eq(strings.ToLower(chosen)), machines.State.Neq("REVOKED")).Count()
				if e != nil {
					return e
				}
				if count > 0 {
					return ErrInvalidArgument
				}
				row.Name = chosen
			}
			return nil
		})
		if err != nil {
			return err
		}
		if row.State == "APPROVED" || row.State == "COMPLETE" {
			if !approve {
				return ErrEnrollmentFailed
			}
			return nil
		}
		if row.State == "APPROVING" {
			// A crash may have happened either side of IAM's decision. The grant
			// cannot prove which caller decided it, so terminate explicitly.
			return errors.Join(ErrEnrollmentFailed, m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "FAILED"}))
		}
		if row.State == "COMPENSATING" || !row.ExpiresAt.After(time.Now().UTC()) {
			return errors.Join(ErrEnrollmentFailed, m.compensateEnrollment(ctx, db, lock, row))
		}
		if row.State == "PENDING" {
			request, err := m.enrollmentIAM.LookupDevice(ctx, tenant, code)
			if err != nil {
				return err
			}
			if request.ClientID != row.ClientID {
				return ErrMachineForbidden
			}
			actor := authn.PrincipalIDFromContext(ctx)
			if err = m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "APPROVING", "approved_by": actor, "name": row.Name}); err != nil {
				return err
			}
			claims, ok := authn.FromContext(ctx)
			if !ok {
				return ErrMachineForbidden
			}
			if err = m.enrollmentIAM.DecideDevice(ctx, tenant, code, actor, approve, claims.AuthTime, claims.ACR, strings.Join(claims.AMR, " ")); err != nil {
				return errors.Join(err, m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "FAILED"}))
			}
			if !approve {
				return m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "DENIED"})
			}
			if err = m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "PROVISIONING"}); err != nil {
				return err
			}
		} else if !approve {
			return ErrEnrollmentFailed
		}
		login := "machine-" + id.String()
		account, found, err := m.enrollmentIAM.FindAccount(ctx, tenant, login)
		if err != nil {
			return err
		}
		if !found {
			account, err = m.enrollmentIAM.CreateAccount(ctx, tenant, login, row.Name, "Runner machine", nil)
			if err != nil {
				return err
			}
		}
		row.PrincipalID = &account.ID
		if err = m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"principal_id": account.ID}); err != nil {
			return err
		}
		// Recheck authority after all independent IAM transactions, before exposure.
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
				return e
			}
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			nameKey := strings.ToLower(row.Name)
			machine := &model.Machine{ID: id, TenantID: tenant, PrincipalID: account.ID, Name: row.Name, NameKey: &nameKey, State: "OFFLINE", OS: row.OS, SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
			if e := query.BindMachines(tx, m.names).WithContext(ctx).Create(machine); e != nil {
				return e
			}
			q := query.BindEnrollments(tx, m.names)
			if _, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": "APPROVED", "machine_id": id, "updated_at": now}); e != nil {
				return e
			}
			if e := m.enrollmentEventOn(ctx, tx, tenant, id, "APPROVED"); e != nil {
				return e
			}
			return m.machineEventOn(humanMachineContext(ctx), tx, tenant, id, "machine.state_changed", 1, map[string]any{"state": "OFFLINE", "reason": "ENROLLED"})
		})
		if err == nil {
			return nil
		}
		// Compensation is durable and retried explicitly; never hide an orphan account.
		stateErr := m.enrollmentUpdate(context.WithoutCancel(ctx), db, lock, tenant, id, map[string]any{"state": "COMPENSATING"})
		cleanup := m.enrollmentIAM.DeleteAccount(context.WithoutCancel(ctx), tenant, account.ID, account.Version)
		if cleanup == nil {
			cleanup = m.enrollmentUpdate(context.WithoutCancel(ctx), db, lock, tenant, id, map[string]any{"state": "FAILED"})
		}
		return errors.Join(err, stateErr, cleanup)
	})
}

// ExchangeEnrollment consumes fleet's derived proof against a durable IAM-backed
// approval, revalidates the approving Human and issues a machine credential once.
func (m *Module) ExchangeEnrollment(ctx context.Context, tenant, id guid.ID, deviceCode string) (EnrollmentCredential, error) {
	var result EnrollmentCredential
	err := m.enrollmentRun(ctx, id, func(ctx context.Context, db *gorm.DB, lock *dlock.Lock) error {
		var row *model.Enrollment
		var throttled bool
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			var e error
			row, e = m.enrollmentOn(ctx, tx, tenant, id)
			if e != nil {
				return e
			}
			if row.DeviceHash != enrollmentHash(deviceCode) {
				return ErrMachineNotFound
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			// Terminal receipts and interrupted issuance retain their recovery semantics.
			if !row.ExpiresAt.After(now) || !slices.Contains([]string{"PENDING", "APPROVING", "APPROVED"}, row.State) {
				return nil
			}
			interval := row.PollIntervalSeconds
			if row.LastPolledAt != nil && now.Before(row.LastPolledAt.Add(time.Duration(interval)*time.Second)) {
				throttled = true
				interval = min(interval+5, 300)
			}
			q := query.BindEnrollments(tx, m.names)
			_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"last_polled_at": now, "poll_interval_seconds": interval})
			return e
		})
		if err != nil {
			return err
		}
		if throttled {
			return ErrEnrollmentSlowDown
		}
		if row.State == "COMPLETE" {
			return ErrEnrollmentConsumed
		}
		if row.State == "ISSUING" {
			return errors.Join(ErrEnrollmentFailed, m.cleanupEnrollmentCredential(ctx, db, lock, row))
		}
		if !row.ExpiresAt.After(time.Now().UTC()) {
			return ErrEnrollmentFailed
		}
		if row.State == "PENDING" || row.State == "APPROVING" {
			return ErrEnrollmentPending
		}
		if row.State != "APPROVED" || row.PrincipalID == nil || row.MachineID == nil || row.ApprovedBy == nil {
			return ErrEnrollmentFailed
		}
		// A replay after an ambiguous issuance must clean up instead of minting a
		// second secret. No approving Human access token is created along this path.
		if err = m.enrollmentUpdate(ctx, db, lock, tenant, id, map[string]any{"state": "ISSUING"}); err != nil {
			return err
		}
		actorCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: *row.ApprovedBy, SubjectType: authn.SubjectTypePrincipal})
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.controlAuthorityOn(actorCtx, tx, tenant, "machine_manage") })
		if err != nil {
			return err
		}
		credential, err := m.enrollmentIAM.IssueCredential(actorCtx, tenant, *row.PrincipalID, "runner-"+id.String(), []string{"openid"}, nil)
		if err != nil {
			return err
		}
		// Credential material remains memory-only. A commit failure revokes it.
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.controlAuthorityOn(actorCtx, tx, tenant, "machine_manage"); e != nil {
				return e
			}
			if e := lock.FenceOn(ctx, tx); e != nil {
				return e
			}
			machine, e := m.machineOn(ctx, tx, tenant, *row.MachineID, true)
			if e != nil {
				return e
			}
			if machine.State == "REVOKED" {
				return ErrMachineRevoked
			}
			q := query.BindEnrollments(tx, m.names)
			if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.State.Eq("ISSUING")).Updates(map[string]any{"state": "COMPLETE", "credential_id": credential.Credential.ID, "updated_at": time.Now().UTC().Truncate(time.Millisecond)}); e != nil {
				return e
			}
			return m.enrollmentEventOn(actorCtx, tx, tenant, id, "COMPLETE")
		})
		if err != nil {
			return errors.Join(err, m.enrollmentIAM.RevokeCredential(context.WithoutCancel(actorCtx), tenant, *row.PrincipalID, credential.Credential.ID))
		}
		result = EnrollmentCredential{MachineID: *row.MachineID, Credential: credential}
		return nil
	})
	return result, err
}

// An interrupted issuance has no recoverable secret. Revoke every credential
// issued for this enrollment before allowing the saga to become terminal.
func (m *Module) cleanupEnrollmentCredential(ctx context.Context, db *gorm.DB, lock *dlock.Lock, row *model.Enrollment) error {
	if row.PrincipalID == nil {
		return ErrEnrollmentFailed
	}
	credentials, err := m.enrollmentIAM.ListCredentials(ctx, row.TenantID, *row.PrincipalID)
	if err != nil {
		return err
	}
	for _, credential := range credentials {
		if credential.Name == "runner-"+row.ID.String() && credential.RevokedAt == nil {
			if err = m.enrollmentIAM.RevokeCredential(ctx, row.TenantID, *row.PrincipalID, credential.ID); err != nil {
				return err
			}
		}
	}
	return m.enrollmentUpdate(ctx, db, lock, row.TenantID, row.ID, map[string]any{"state": "FAILED"})
}

func (m *Module) compensateEnrollment(ctx context.Context, db *gorm.DB, lock *dlock.Lock, row *model.Enrollment) error {
	if row.MachineID != nil {
		return ErrEnrollmentFailed
	}
	if err := m.enrollmentUpdate(ctx, db, lock, row.TenantID, row.ID, map[string]any{"state": "COMPENSATING"}); err != nil {
		return err
	}
	account, found, err := m.enrollmentIAM.FindAccount(ctx, row.TenantID, "machine-"+row.ID.String())
	if err != nil {
		return err
	}
	if found {
		if err = m.enrollmentIAM.DeleteAccount(ctx, row.TenantID, account.ID, account.Version); err != nil {
			return err
		}
	}
	return m.enrollmentUpdate(ctx, db, lock, row.TenantID, row.ID, map[string]any{"state": "FAILED"})
}
