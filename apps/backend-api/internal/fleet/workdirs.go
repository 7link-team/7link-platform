package fleet

import (
	"context"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// RepositoryAuthorizer locks current project/repository facts after the caller's
// IAM authority fence and before its machine lock. It never accesses fleet tables.
type RepositoryAuthorizer func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
type RepositoryConnector struct {
	Bind func(RepositoryAuthorizer) error
}

func (m *Module) bindRepository(p RepositoryAuthorizer) error {
	if m == nil || p == nil || m.repository != nil {
		return ErrInvalidArgument
	}
	m.repository = p
	return nil
}

var windowsRoot = regexp.MustCompile(`^[A-Za-z]:/`)

// Normalize both machine OS path syntaxes without applying the server's OS rules.
func workdirPath(raw, osKind string) (string, error) {
	if !boundedFact(raw, 1024) || strings.TrimSpace(raw) != raw {
		return "", ErrInvalidArgument
	}
	value := raw
	if osKind == "WINDOWS" {
		value = strings.ReplaceAll(value, `\`, "/")
		if !windowsRoot.MatchString(value) {
			return "", ErrInvalidArgument
		}
	} else if !strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return "", ErrInvalidArgument
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", ErrInvalidArgument
		}
	}
	value = path.Clean(value)
	if osKind == "WINDOWS" && len(value) == 2 {
		value += "/"
	}
	return value, nil
}

// RegisterWorkdirOn preserves the machine/project/repository identity. Editing
// and archiving use the separate versioned lifecycle command.
func (m *Module) RegisterWorkdirOn(ctx context.Context, tx *gorm.DB, tenant, machine, project, repository guid.ID, raw string) (MachineWorkdirView, error) {
	if e := fleetTransaction(tx, tenant, machine); e != nil {
		return MachineWorkdirView{}, e
	}
	if project <= 0 || repository <= 0 {
		return MachineWorkdirView{}, ErrInvalidArgument
	}
	if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); e != nil {
		return MachineWorkdirView{}, e
	}
	if m.repository == nil {
		return MachineWorkdirView{}, ErrUnavailable
	}
	if e := m.repository(ctx, tx, tenant, project, repository); e != nil {
		return MachineWorkdirView{}, e
	}
	machineRow, e := m.machineOn(ctx, tx, tenant, machine, true)
	if e != nil {
		return MachineWorkdirView{}, e
	}
	if machineRow.State == "REVOKED" {
		return MachineWorkdirView{}, ErrMachineRevoked
	}
	normalized, e := workdirPath(raw, machineRow.OS)
	if e != nil {
		return MachineWorkdirView{}, e
	}
	keyPath := normalized
	if machineRow.OS == "WINDOWS" || machineRow.OS == "MACOS" {
		keyPath = strings.ToLower(keyPath)
	}
	key := enrollmentHash(keyPath)
	q := query.BindWorkdirs(tx, m.names)
	old, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ProjectID.Eq(project), q.RepositoryID.Eq(repository)).First()
	if e == nil {
		if old.PathKey != key || old.ArchivedAt != nil {
			return MachineWorkdirView{}, ErrVersionConflict
		}
		return workdirView(old), nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return MachineWorkdirView{}, e
	}
	count, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.PathKey.Eq(key)).Count()
	if e != nil {
		return MachineWorkdirView{}, e
	}
	if count > 0 {
		return MachineWorkdirView{}, ErrVersionConflict
	}
	next, e := m.ids.Next()
	if e != nil {
		return MachineWorkdirView{}, e
	}
	if next <= 0 {
		return MachineWorkdirView{}, ErrUnavailable
	}
	row := &model.Workdir{Version: 1, ID: guid.ID(next), TenantID: tenant, MachineID: machine, ProjectID: project, RepositoryID: repository, Path: normalized, PathKey: key, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if e = q.WithContext(ctx).Create(row); e != nil {
		return MachineWorkdirView{}, e
	}
	if e = m.machineEventOn(humanMachineContext(ctx), tx, tenant, machine, "machine.inventory_updated", machineRow.Version, map[string]any{"workdir_id": row.ID, "project_id": project, "repository_id": repository}); e != nil {
		return MachineWorkdirView{}, e
	}
	return workdirView(row), nil
}

type workdirInput struct {
	MachineInput
	Body struct {
		ProjectID    guid.ID `json:"project_id"`
		RepositoryID guid.ID `json:"repository_id"`
		Path         string  `json:"path" minLength:"1" maxLength:"1024"`
	}
}

func (m *Module) registerWorkdirHTTP(api huma.API) error {
	if err := m.registerWorkdirLifecycleHTTP(api); err != nil {
		return err
	}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.workdir.register", Method: http.MethodPost, Path: "/machines/{machine_id}/workdirs", Summary: "Register an authorized project repository workspace", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, func(ctx context.Context, in *workdirInput) (*respx.Body[MachineWorkdirView], error) {
		id, e := guid.RequireID(in.MachineID)
		if e != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
		db, e := m.writer()
		if e != nil {
			return nil, fleetHTTPError(ctx, e)
		}
		var out MachineWorkdirView
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.RegisterWorkdirOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.ProjectID, in.Body.RepositoryID, in.Body.Path)
			return err
		})
		if e != nil {
			return nil, fleetHTTPError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
	return authz.RegisterRequestContract(api, "/machines/{machine_id}/workdirs")
}
