package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WorkdirLifecycleConnector binds the Execution owner's current-read guard.
// Fleet holds the machine lock, also required by execution creation/recovery.
type WorkdirLifecycleConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)) error
}

func (m *Module) bindWorkdirQuiet(f func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)) error {
	if m == nil || f == nil || m.workdirQuiet != nil {
		return ErrInvalidArgument
	}
	m.workdirQuiet = f
	return nil
}

func workdirView(row *model.Workdir) MachineWorkdirView {
	return MachineWorkdirView{ID: row.ID, ProjectID: row.ProjectID, RepositoryID: row.RepositoryID, Path: row.Path, Version: row.Version, ArchivedAt: row.ArchivedAt}
}

type WorkdirChange struct {
	Path     string `json:"path" minLength:"1" maxLength:"1024"`
	Archived bool   `json:"archived"`
}
type workdirChangeInput struct {
	MachineInput
	WorkdirID      string `path:"workdir_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch        string `header:"If-Match" required:"true"`
	Body           WorkdirChange
}

// Current authority is checked even when returning an idempotent receipt.
func (m *Module) workdirChangeAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, machine, id guid.ID) (*model.Workdir, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return nil, err
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); err != nil {
		return nil, err
	}
	q := query.BindWorkdirs(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	if err != nil {
		return nil, err
	}
	if m.repository == nil {
		return nil, ErrUnavailable
	}
	if err = m.repository(ctx, tx, tenant, row.ProjectID, row.RepositoryID); err != nil {
		return nil, err
	}
	machineRow, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return nil, err
	}
	if machineRow.State == "REVOKED" {
		return nil, ErrMachineRevoked
	}
	// A locking read sees the latest committed version under MySQL RR as well.
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
}

func (m *Module) changeWorkdirOn(ctx context.Context, tx *gorm.DB, tenant, machine, id guid.ID, version int, in WorkdirChange) (MachineWorkdirView, error) {
	row, err := m.workdirChangeAuthorityOn(ctx, tx, tenant, machine, id)
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if version < 1 || row.Version != version {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	machineRow, err := m.machineOn(ctx, tx, tenant, machine, false)
	if err != nil {
		return MachineWorkdirView{}, err
	}
	normalized, err := workdirPath(in.Path, machineRow.OS)
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if m.workdirQuiet == nil {
		return MachineWorkdirView{}, ErrUnavailable
	}
	quiet, err := m.workdirQuiet(ctx, tx, tenant, machine, id)
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if !quiet {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	// Never change the path of an inspection already queued/running on a runner.
	inspections := query.BindInspections(tx, m.names)
	active, err := inspections.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(inspections.TenantID.Eq(tenant), inspections.MachineID.Eq(machine), inspections.ScopeKey.Eq(id.String()), inspections.State.In("QUEUED", "RUNNING")).Limit(1).Find()
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if len(active) > 0 {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	keyPath := normalized
	if machineRow.OS == "WINDOWS" || machineRow.OS == "MACOS" {
		keyPath = strings.ToLower(keyPath)
	}
	key := enrollmentHash(keyPath)
	q := query.BindWorkdirs(tx, m.names)
	collisions, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.PathKey.Eq(key), q.ID.Neq(id)).Count()
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if collisions > 0 {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	var archived *time.Time
	if in.Archived {
		now := time.Now().UTC().Truncate(time.Millisecond)
		archived = &now
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"path": normalized, "path_key": key, "archived_at": archived, "version": version + 1})
	if err != nil {
		return MachineWorkdirView{}, err
	}
	if result.RowsAffected != 1 {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	row.Path, row.PathKey, row.ArchivedAt, row.Version = normalized, key, archived, version+1
	if err = m.machineEventOn(humanMachineContext(ctx), tx, tenant, machine, "machine.inventory_updated", machineRow.Version, map[string]any{"workdir_id": id, "workdir_version": row.Version, "project_id": row.ProjectID, "repository_id": row.RepositoryID, "archived": in.Archived}); err != nil {
		return MachineWorkdirView{}, err
	}
	return workdirView(row), nil
}

func (m *Module) registerWorkdirLifecycleHTTP(api huma.API) error {
	const route = "/machines/{machine_id}/workdirs/{workdir_id}"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.workdir.update", Method: http.MethodPut, Path: route, Summary: "Edit archive or restore an idle repository workspace", Tags: []string{"machines"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.changeWorkdirHTTP)
	return authz.RegisterRequestContract(api, route)
}

func (m *Module) changeWorkdirHTTP(ctx context.Context, in *workdirChangeInput) (*respx.Body[MachineWorkdirView], error) {
	machine, err := guid.RequireID(in.MachineID)
	id, idErr := guid.RequireID(in.WorkdirID)
	version, vErr := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || idErr != nil || vErr != nil || version < 1 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	var out MachineWorkdirView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.workdirChangeAuthorityOn(ctx, tx, tenant, machine, id); e != nil {
			return e
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		digest, e := idempotency.HashRequest(http.MethodPut, "machine.workdir.update", map[string]string{"machine_id": machine.String(), "workdir_id": id.String(), "version": strconv.Itoa(version)}, in.Body)
		if e != nil {
			return e
		}
		result, _, e := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "machine.workdir.update:" + id.String(), Value: in.IdempotencyKey, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			value, e := m.changeWorkdirOn(ctx, tx, tenant, machine, id, version, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := json.Marshal(value)
			return idempotency.Result{StatusCode: 200, Body: body}, e
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(result.Body, &out)
	})
	if errors.Is(err, idempotency.ErrKeyReused) || errors.Is(err, idempotency.ErrInProgress) {
		err = ErrVersionConflict
	}
	if errors.Is(err, idempotency.ErrInvalidKey) {
		err = ErrInvalidArgument
	}
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
