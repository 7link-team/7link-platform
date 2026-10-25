package control

import (
	"context"
	"errors"
	"os"
	"time"
)

type credentialDelivery struct {
	RotationID   string    `json:"rotation_id"`
	CredentialID string    `json:"credential_id"`
	ClientID     string    `json:"client_id"`
	Secret       string    `json:"client_secret"`
	RetireOldAt  time.Time `json:"retire_old_at"`
}

// The previous credential survives an uncertain ACK and the server's pending
// delivery timeout. This file receives the same OS protection as identity.json.
type pendingRotation struct {
	Previous Profile            `json:"previous"`
	Delivery credentialDelivery `json:"delivery"`
}

func pendingCredential(store *Store) (*pendingRotation, error) {
	var pending *pendingRotation
	if err := store.Read("rotation.json", &pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if pending != nil && (!validID(pending.Delivery.RotationID) || !validID(pending.Delivery.CredentialID) || pending.Delivery.ClientID != pending.Delivery.CredentialID || pending.Delivery.Secret == "" || !validID(pending.Previous.ClientID) || pending.Previous.Secret == "") {
		return nil, errors.New("invalid pending credential rotation")
	}
	return pending, nil
}

func authenticateProfile(ctx context.Context, store *Store, c *Client, p Profile) (Profile, error) {
	err := c.Authenticate(ctx, p)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 401 {
		return p, err
	}
	pending, e := pendingCredential(store)
	if e != nil {
		return p, e
	}
	if pending == nil {
		return p, err
	}
	if pending.Previous.MachineID != p.MachineID || pending.Previous.TenantID != p.TenantID || pending.Previous.Server != p.Server {
		return p, errors.New("pending credential identity mismatch")
	}
	if e = c.Authenticate(ctx, pending.Previous); e != nil {
		return p, e
	}
	return pending.Previous, nil
}

func rotateCredential(ctx context.Context, store *Store, c *Client, p *Profile, f fence) error {
	pending, err := pendingCredential(store)
	if err != nil {
		return err
	}
	if pending != nil && (pending.Previous.MachineID != p.MachineID || pending.Previous.TenantID != p.TenantID || pending.Previous.Server != p.Server) {
		return errors.New("pending credential identity mismatch")
	}
	if pending == nil {
		var deliveries []credentialDelivery
		if err = c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/credential-deliveries", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &deliveries, true); err != nil {
			return err
		}
		if len(deliveries) > 1 {
			return errors.New("unsupported credential delivery batch")
		}
		if len(deliveries) == 0 {
			return nil
		}
		d := deliveries[0]
		if !validID(d.RotationID) || !validID(d.CredentialID) || d.ClientID != d.CredentialID || d.Secret == "" || d.RetireOldAt.IsZero() {
			return errors.New("invalid credential delivery")
		}
		pending = &pendingRotation{Previous: *p, Delivery: d}
		if err = store.Write("rotation.json", pending); err != nil {
			return errors.New("cannot persist pending credential; acknowledgement withheld")
		}
	}
	next := pending.Previous
	next.ClientID = pending.Delivery.ClientID
	next.Secret = pending.Delivery.Secret
	if err = store.SaveProfile(next); err != nil {
		return errors.New("cannot persist rotated identity; acknowledgement withheld")
	}
	err = c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/credential-deliveries/"+pending.Delivery.RotationID+"/ack", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch, "credential_id": pending.Delivery.CredentialID}, nil, true)
	var api *APIError
	if errors.As(err, &api) && api.Status == 409 {
		// A timed-out delivery was revoked. Keep the prior identity and allow the
		// next poll to obtain the server's fresh delivery attempt.
		if e := store.SaveProfile(pending.Previous); e != nil {
			return e
		}
		*p = pending.Previous
		return store.Write("rotation.json", nil)
	}
	if err != nil {
		return err
	}
	*p = next
	return store.Write("rotation.json", nil)
}
