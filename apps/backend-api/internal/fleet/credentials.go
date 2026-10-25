package fleet

import (
	"context"
	"net/http"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type MachineCredentials struct {
	Items               []identity.ServiceAccountCredential `json:"items"`
	Lifecycle           []CredentialLifecycleView           `json:"lifecycle"`
	Rotation            *CredentialRotationView             `json:"rotation"`
	RotationAvailable   bool                                `json:"rotation_available"`
	RevocationAvailable bool                                `json:"revocation_available"`
}

// IAM owns credential metadata and its own transactions. Recheck current fleet
// authority after the independent IAM read before exposing even secret-free facts.
func (m *Module) machineCredentials(ctx context.Context, id guid.ID) (MachineCredentials, error) {
	out := MachineCredentials{Items: []identity.ServiceAccountCredential{}}
	if m == nil || m.enrollmentIAM.ListCredentials == nil {
		return out, ErrUnavailable
	}
	db, e := m.writer()
	if e != nil {
		return out, e
	}
	tenant := authn.TenantIDFromContext(ctx)
	principal := guid.ID(0)
	authorize := func(tx *gorm.DB) error {
		if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
			return err
		}
		row, err := m.machineOn(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if principal > 0 && principal != row.PrincipalID {
			return ErrVersionConflict
		}
		principal = row.PrincipalID
		return nil
	}
	readLifecycle := func(tx *gorm.DB) error {
		if err := authorize(tx); err != nil {
			return err
		}
		q := query.BindCredentials(tx, m.names)
		credentials, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(id)).Order(q.ID).Find()
		if err != nil {
			return err
		}
		out.Lifecycle = []CredentialLifecycleView{}
		for _, c := range credentials {
			out.Lifecycle = append(out.Lifecycle, CredentialLifecycleView{c.ID, c.State, c.ReasonCode, c.RetireAt, c.DeliveredAt})
		}
		r := query.BindRotations(tx, m.names)
		rotations, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.MachineID.Eq(id)).Order(r.ID.Desc()).Limit(1).Find()
		if err != nil {
			return err
		}
		if len(rotations) > 0 {
			v := rotationView(rotations[0])
			out.Rotation = &v
		}
		return nil
	}
	if e = db.WithContext(ctx).Transaction(authorize); e != nil {
		return out, e
	}
	rows, e := m.enrollmentIAM.ListCredentials(ctx, tenant, principal)
	if e != nil {
		return out, e
	}
	if e = db.WithContext(ctx).Transaction(readLifecycle); e != nil {
		return out, e
	}
	for _, row := range rows {
		if row.ServiceAccountID != principal {
			return out, ErrUnavailable
		}
	}
	if rows != nil {
		out.Items = rows
	}
	out.RotationAvailable = m.rotationAvailable()
	out.RevocationAvailable = m.revokeProof != nil && m.revokeAccount != nil && m.revokeBrowser.ValidateCSRF != nil
	return out, nil
}
func (m *Module) registerCredentialsHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.credentials.list", Method: http.MethodGet, Path: "/machines/{machine_id}/credentials", Summary: "Read IAM machine credential metadata and lifecycle availability", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, func(ctx context.Context, in *MachineInput) (*respx.Body[MachineCredentials], error) {
		id, e := guid.RequireID(in.MachineID)
		if e != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
		out, e := m.machineCredentials(ctx, id)
		if e != nil {
			return nil, fleetHTTPError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
}
