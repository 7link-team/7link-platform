package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCredentialRotationRequestAcknowledgementAndRetirementStorageFailures(t *testing.T) {
	for _, command := range []string{"request", "ack", "retire"} {
		for _, operation := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := rotationHarness(t)
				var delivery CredentialDelivery
				if command != "request" {
					_, e := f.requestRotation()
					require.NoError(t, e)
					if command == "ack" {
						out, e := f.pullDelivery()
						require.NoError(t, e)
						require.Len(t, out, 1)
						delivery = out[0]
					}
				}
				rq := query.BindRotations(f.db, f.m.names)
				cq := query.BindCredentials(f.db, f.m.names)
				before, e := rq.WithContext(f.ctx).Order(rq.ID).Find()
				require.NoError(t, e)
				credentials, e := cq.WithContext(f.ctx).Order(cq.ID).Find()
				require.NoError(t, e)
				faultDB, writerErr := f.m.writer()
				require.NoError(t, writerErr)
				count := 0
				injected := errors.New("credential lifecycle storage unavailable")
				hook := func(tx *gorm.DB) {
					if !strings.Contains(tx.Statement.Table, "fleet_") {
						return
					}
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, faultDB.Callback().Query().Before("gorm:query").Register("rotation_storage_failure", hook))
				case "create":
					require.NoError(t, faultDB.Callback().Create().Before("gorm:create").Register("rotation_storage_failure", hook))
				case "update":
					require.NoError(t, faultDB.Callback().Update().Before("gorm:update").Register("rotation_storage_failure", hook))
				}
				var err error
				switch command {
				case "request":
					_, err = f.requestRotation()
				case "ack":
					_, err = f.ackDelivery(delivery)
				case "retire":
					err = f.m.retireCredential(f.ctx, 7, 1, 303, time.Now().UTC().Add(25*time.Hour))
				}
				require.NoError(t, faultDB.Callback().Query().Remove("rotation_storage_failure"))
				require.NoError(t, faultDB.Callback().Create().Remove("rotation_storage_failure"))
				require.NoError(t, faultDB.Callback().Update().Remove("rotation_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := rq.WithContext(f.ctx).Order(rq.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				afterCredentials, e := cq.WithContext(f.ctx).Order(cq.ID).Find()
				require.NoError(t, e)
				require.Equal(t, credentials, afterCredentials)
				return count
			}
			count := exercise(t, 0)
			for point := 1; point <= count; point++ {
				t.Run(fmt.Sprintf("%s/%s/%d", command, operation, point), func(t *testing.T) { exercise(t, point) })
			}
		}
	}
}

type rotationFixture struct {
	*inspectionFixture
	credentials                  []identity.ServiceAccountCredential
	issued, revoked              int
	issueAfter                   func()
	issueErr, revokeErr, listErr error
}

func rotationHarness(t *testing.T) *rotationFixture {
	t.Helper()
	f := &rotationFixture{inspectionFixture: inspectionHarness(t), credentials: []identity.ServiceAccountCredential{{ID: 303, ServiceAccountID: 101, Name: "enrollment", CreatedAt: time.Now().UTC()}}}
	f.m.enrollmentIAM.ListCredentials = func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) {
		return f.credentials, f.listErr
	}
	f.m.enrollmentIAM.IssueCredential = func(_ context.Context, tenant, principal guid.ID, name string, scopes []string, _ *time.Time) (identity.ServiceAccountCredentialSecret, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(101), principal)
		require.Equal(t, []string{"openid"}, scopes)
		f.issued++
		c := identity.ServiceAccountCredential{ID: guid.ID(400 + f.issued), ServiceAccountID: principal, Name: name, CreatedAt: time.Now().UTC()}
		f.credentials = append(f.credentials, c)
		if f.issueAfter != nil {
			f.issueAfter()
		}
		return identity.ServiceAccountCredentialSecret{Credential: c, Secret: fmt.Sprintf("private-rotation-secret-%d", f.issued)}, f.issueErr
	}
	f.m.enrollmentIAM.RevokeCredential = func(_ context.Context, tenant, principal, id guid.ID) error {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(101), principal)
		if f.revokeErr != nil {
			return f.revokeErr
		}
		for i := range f.credentials {
			if f.credentials[i].ID == id && f.credentials[i].RevokedAt == nil {
				now := time.Now().UTC()
				f.credentials[i].RevokedAt = &now
				f.revoked++
			}
		}
		return nil
	}
	return f
}
func (f *rotationFixture) requestRotation() (CredentialRotationView, error) {
	return f.m.RequestCredentialRotation(f.ctx, 7, 1)
}
func (f *rotationFixture) pullDelivery() ([]CredentialDelivery, error) {
	return f.m.PullCredentialDelivery(f.ctx, 7, 1, f.session.ID, f.session.ConnEpoch)
}
func (f *rotationFixture) ackDelivery(d CredentialDelivery) (CredentialRotationView, error) {
	return f.m.AckCredentialDelivery(f.ctx, 7, 1, d.RotationID, d.CredentialID, f.session.ID, f.session.ConnEpoch)
}
func (f *rotationFixture) rotation(t *testing.T, id guid.ID) *model.CredentialRotation {
	t.Helper()
	q := query.BindRotations(f.db, f.m.names)
	r, e := q.WithContext(f.ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	return r
}
func (f *rotationFixture) lifecycle(t *testing.T, id guid.ID) *model.MachineCredential {
	t.Helper()
	q := query.BindCredentials(f.db, f.m.names)
	r, e := q.WithContext(f.ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	return r
}
func (f *rotationFixture) ageDelivery(t *testing.T, id guid.ID) {
	t.Helper()
	q := query.BindRotations(f.db, f.m.names)
	_, e := q.WithContext(f.ctx).Where(q.ID.Eq(id)).Update(q.IssuedAt, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, e)
}

func TestCredentialRotationOneTimeDeliveryAckAndRetirement(t *testing.T) {
	f := rotationHarness(t)
	// Neither rows nor transactional audit records may contain reusable secrets.
	original := f.m.audit
	f.m.audit = func(ctx context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private-rotation-secret")
		return original(ctx, tx, e)
	}
	r, e := f.requestRotation()
	require.NoError(t, e)
	require.Equal(t, "PENDING_DELIVERY", r.State)
	require.Equal(t, 24*time.Hour, r.RetireOldAt.Sub(r.CreatedAt))
	require.Equal(t, "RETIRING", f.lifecycle(t, 303).State)
	_, e = f.requestRotation()
	require.ErrorIs(t, e, ErrVersionConflict)
	d, e := f.pullDelivery()
	require.NoError(t, e)
	require.Len(t, d, 1)
	require.Equal(t, d[0].CredentialID, d[0].ClientID, "IAM client_credentials selects credential.id, while authenticated token subject is the account principal")
	require.Equal(t, "private-rotation-secret-1", d[0].ClientSecret)
	require.Equal(t, "PENDING_DELIVERY", f.lifecycle(t, d[0].CredentialID).State)
	replay, e := f.pullDelivery()
	require.NoError(t, e)
	require.Empty(t, replay)
	require.Equal(t, 1, f.issued)
	_, e = f.m.AckCredentialDelivery(f.ctx, 7, 1, r.ID, 999, f.session.ID, 1)
	require.ErrorIs(t, e, ErrVersionConflict)
	active, e := f.ackDelivery(d[0])
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", active.State)
	require.Nil(t, f.rotation(t, r.ID).ActiveKey)
	require.NotNil(t, f.lifecycle(t, d[0].CredentialID).DeliveredAt)
	again, e := f.ackDelivery(d[0])
	require.NoError(t, e)
	require.Equal(t, active, again)
	metadata, e := f.m.machineCredentials(f.ctx, 1)
	require.NoError(t, e)
	require.True(t, metadata.RotationAvailable)
	require.Len(t, metadata.Lifecycle, 2)
	require.Equal(t, "ACTIVE", metadata.Rotation.State)
	encoded, e := json.Marshal([]any{f.rotation(t, r.ID), f.lifecycle(t, d[0].CredentialID), metadata})
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "private-rotation-secret")
	// A second request never extends a previously retiring enrollment key.
	r2, e := f.requestRotation()
	require.NoError(t, e)
	require.Equal(t, r.RetireOldAt, *f.lifecycle(t, 303).RetireAt)
	require.Equal(t, r2.RetireOldAt, *f.lifecycle(t, d[0].CredentialID).RetireAt)
	require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, r.RetireOldAt.Add(-time.Millisecond)))
	require.Zero(t, f.revoked)
	require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, r.RetireOldAt))
	require.Equal(t, "REVOKED", f.lifecycle(t, 303).State)
	require.NotNil(t, f.credentials[0].RevokedAt)
}
func TestCredentialRotationTimeoutRetriesFiveAndCleansWithoutHuman(t *testing.T) {
	f := rotationHarness(t)
	r, e := f.requestRotation()
	require.NoError(t, e)
	for attempt := 1; attempt <= 5; attempt++ {
		d, e := f.pullDelivery()
		require.NoError(t, e)
		require.Len(t, d, 1)
		require.Equal(t, attempt, f.rotation(t, r.ID).Attempts)
		f.ageDelivery(t, r.ID)
		_, e = f.ackDelivery(d[0])
		require.ErrorIs(t, e, ErrVersionConflict)
		// Cleanup must remain possible after the requesting Human loses authority.
		prior := f.m.principals.ActiveHumanByID
		f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, nil
		}
		require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, time.Now().UTC()))
		f.m.principals.ActiveHumanByID = prior
		require.Equal(t, "REVOKED", f.lifecycle(t, d[0].CredentialID).State)
		require.NotNil(t, f.credentials[attempt].RevokedAt)
		_, e = f.ackDelivery(d[0])
		require.ErrorIs(t, e, ErrVersionConflict)
	}
	row := f.rotation(t, r.ID)
	require.Equal(t, "FAILED", row.State)
	require.Equal(t, "DELIVERY_FAILED", row.ReasonCode)
	require.Nil(t, row.ActiveKey)
	require.Equal(t, 5, f.issued)
	require.Equal(t, 5, f.revoked)
	d, e := f.pullDelivery()
	require.NoError(t, e)
	require.Empty(t, d)
	require.Equal(t, 5, f.issued)
	require.Nil(t, f.credentials[0].RevokedAt, "old credential retains its original 24h grace")
}
func TestCredentialRotationCurrentAuthorityAndIndependentIAMFailures(t *testing.T) {
	for _, fault := range []string{"authority", "session", "event", "issue", "revoke", "list", "empty_secret", "wrong_account"} {
		t.Run(fault, func(t *testing.T) {
			f := rotationHarness(t)
			r, e := f.requestRotation()
			require.NoError(t, e)
			switch fault {
			case "authority":
				f.issueAfter = func() {
					f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, nil
					}
				}
			case "session":
				f.issueAfter = func() {
					q := query.BindMachines(f.db, f.m.names)
					_, e := q.WithContext(f.ctx).Where(q.ID.Eq(guid.ID(1))).Update(q.ConnEpoch, 2)
					require.NoError(t, e)
				}
			case "event":
				f.issueAfter = func() { f.publisher.fail = true }
			case "issue":
				f.issueErr = errors.New("IAM failed after commit")
			case "revoke":
				f.issueErr = errors.New("IAM failed after commit")
				f.revokeErr = errors.New("IAM revoke unavailable")
			case "list":
				f.issueAfter = func() { f.listErr = errors.New("IAM list unavailable") }
				f.issueErr = errors.New("IAM failed after commit")
			case "empty_secret", "wrong_account":
				issue := f.m.enrollmentIAM.IssueCredential
				f.m.enrollmentIAM.IssueCredential = func(ctx context.Context, a, b guid.ID, n string, s []string, x *time.Time) (identity.ServiceAccountCredentialSecret, error) {
					v, e := issue(ctx, a, b, n, s, x)
					if fault == "empty_secret" {
						v.Secret = ""
					} else {
						v.Credential.ServiceAccountID = 999
					}
					return v, e
				}
			}
			d, e := f.pullDelivery()
			require.Error(t, e)
			require.Empty(t, d)
			require.Equal(t, 1, f.issued)
			f.publisher.fail = false
			f.revokeErr = nil
			f.listErr = nil
			// Recovery uses persisted ISSUING/COMPENSATING and deterministic IAM name;
			// it works even though no credential ID reached a committed fleet row.
			require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, time.Now().UTC().Add(2*time.Minute)))
			require.NotNil(t, f.credentials[1].RevokedAt)
			require.Equal(t, 1, f.revoked)
			require.NotEqual(t, "DELIVERED", f.rotation(t, r.ID).Phase)
			if fault == "authority" {
				require.Equal(t, "FAILED", f.rotation(t, r.ID).State)
			}
		})
	}
}
func TestCredentialRotationFencesCurrentSessionAndAllowsReconnectAck(t *testing.T) {
	f := rotationHarness(t)
	_, e := f.requestRotation()
	require.NoError(t, e)
	d, e := f.pullDelivery()
	require.NoError(t, e)
	_, e = f.m.PullCredentialDelivery(f.ctx, 7, 1, f.session.ID, 2)
	require.ErrorIs(t, e, ErrSessionStale)
	_, e = f.m.AckCredentialDelivery(f.ctx, 8, 1, d[0].RotationID, d[0].CredentialID, f.session.ID, 1)
	require.Error(t, e)
	other := authn.WithClaims(f.ctx, &authn.Claims{TenantID: 7, PrincipalID: 102})
	_, e = f.m.AckCredentialDelivery(other, 7, 1, d[0].RotationID, d[0].CredentialID, f.session.ID, 1)
	require.ErrorIs(t, e, ErrMachineForbidden)
	q := query.BindMachines(f.db, f.m.names)
	_, e = q.WithContext(f.ctx).Where(q.ID.Eq(guid.ID(1))).Update(q.LastSeenAt, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, e)
	sq := query.BindSessions(f.db, f.m.names)
	_, e = sq.WithContext(f.ctx).Where(sq.ID.Eq(f.session.ID)).Update(sq.LastSeenAt, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, e)
	old := f.session
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		f.session, e = f.m.OpenSessionOn(f.ctx, tx, 7, 1, "reconnected-after-save")
		return e
	}))
	_, e = f.m.AckCredentialDelivery(f.ctx, 7, 1, d[0].RotationID, d[0].CredentialID, old.ID, old.ConnEpoch)
	require.ErrorIs(t, e, ErrSessionStale)
	active, e := f.ackDelivery(d[0])
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", active.State)
}
func TestCredentialRotationHTTPWireNoStoreAndMachineBinding(t *testing.T) {
	f := rotationHarness(t)
	router := fleetBusinessRouter(t, f.m)
	r := fleetHTTPRequest(router, http.MethodPost, "/machines/1/credential-rotations", "", nil)
	require.Equal(t, 201, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "client_secret")
	var response struct {
		Data CredentialRotationView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &response))
	headers := map[string]string{"Authorization": "Bearer runner"}
	body := fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d}`, f.session.ID.String(), f.session.ConnEpoch)
	r = fleetHTTPRequest(router, http.MethodPost, "/machines/1/credential-deliveries", body, headers)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	require.Contains(t, r.Body.String(), "private-rotation-secret-1")
	var deliveries struct {
		Data []CredentialDelivery `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &deliveries))
	require.Len(t, deliveries.Data, 1)
	r = fleetHTTPRequest(router, http.MethodPost, "/machines/1/credential-deliveries", body, headers)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "private-rotation-secret")
	d := deliveries.Data[0]
	ack := fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d,"credential_id":%q}`, f.session.ID.String(), f.session.ConnEpoch, d.CredentialID.String())
	r = fleetHTTPRequest(router, http.MethodPut, "/machines/1/credential-deliveries/"+response.Data.ID.String()+"/ack", ack, headers)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"state":"ACTIVE"`)
	r = fleetHTTPRequest(router, http.MethodPost, "/machines/999/credential-deliveries", body, headers)
	require.Equal(t, 404, r.Code, r.Body.String())
	f.m.enrollmentIAM.IssueCredential = nil
	r = fleetHTTPRequest(router, http.MethodPost, "/machines/1/credential-deliveries", body, headers)
	require.Equal(t, 503, r.Code, r.Body.String())
}

func TestCredentialRotationRequestPreservesAuthorityAndRollsBackEffects(t *testing.T) {
	for _, fault := range []string{"tenant", "human", "machine", "revoked", "list", "owner", "outbox", "insert", "expired"} {
		t.Run(fault, func(t *testing.T) {
			f := rotationHarness(t)
			ctx := f.ctx
			machine := guid.ID(1)
			switch fault {
			case "tenant":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 101})
			case "human":
				f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "machine":
				machine = 999
			case "revoked":
				q := query.BindMachines(f.db, f.m.names)
				_, e := q.WithContext(ctx).Where(q.ID.Eq(machine)).Updates(map[string]any{"state": "REVOKED", "name_key": nil})
				require.NoError(t, e)
			case "list":
				f.listErr = errors.New("IAM metadata unavailable")
			case "owner":
				f.credentials[0].ServiceAccountID = 999
			case "outbox":
				f.publisher.fail = true
			case "insert":
				writer, e := f.m.writer()
				require.NoError(t, e)
				require.NoError(t, writer.Callback().Create().Before("gorm:create").Register("fail_rotation", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Rotations.String() {
						tx.AddError(errors.New("rotation persistence failed"))
					}
				}))
			case "expired":
				past := time.Now().UTC().Add(-time.Hour)
				f.credentials[0].ExpiresAt = &past
			}
			_, e := f.m.RequestCredentialRotation(ctx, 7, machine)
			if fault == "expired" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			q := query.BindCredentials(f.db, f.m.names)
			n, e := q.WithContext(f.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, n, "invalid authority or failed transaction cannot retire old keys")
			require.Zero(t, f.issued)
			require.Zero(t, f.revoked)
		})
	}
}
func TestCredentialRotationLeaseLossFencesSecretAndDurablyRecovers(t *testing.T) {
	for _, stage := range []string{"acquire", "before_transaction", "after_iam"} {
		t.Run(stage, func(t *testing.T) {
			f := rotationHarness(t)
			r, e := f.requestRotation()
			require.NoError(t, e)
			acquire := f.m.enrollmentLease
			steal := func() {
				require.NoError(t, f.db.Table("dir_dlocks").Where("name = ?", "fleet:rotation:7:1").Update("owner", "replacement-worker").Error)
			}
			if stage == "after_iam" {
				f.issueAfter = steal
			} else {
				f.m.enrollmentLease = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
					if stage == "acquire" {
						return nil, dlock.ErrNotAcquired
					}
					lock, e := acquire(ctx, name, ttl, onLost)
					if e == nil {
						steal()
					}
					return lock, e
				}
			}
			d, e := f.pullDelivery()
			require.ErrorIs(t, e, dlock.ErrNotAcquired)
			require.Empty(t, d)
			if stage != "after_iam" {
				require.Zero(t, f.issued)
				return
			}
			require.Equal(t, "ISSUING", f.rotation(t, r.ID).Phase, "lost fleet fence leaves durable ambiguous IAM attempt, never a deliverable secret")
			require.NoError(t, f.db.Table("dir_dlocks").Where("name = ?", "fleet:rotation:7:1").Update("expires_at", int64(0)).Error)
			f.issueAfter = nil
			require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, time.Now().UTC()))
			require.NotNil(t, f.credentials[1].RevokedAt)
			require.Equal(t, "QUEUED", f.rotation(t, r.ID).Phase)
		})
	}
}
func TestCredentialRotationAckOutboxFailureAndRetirementRetry(t *testing.T) {
	f := rotationHarness(t)
	r, e := f.requestRotation()
	require.NoError(t, e)
	d, e := f.pullDelivery()
	require.NoError(t, e)
	f.publisher.fail = true
	_, e = f.ackDelivery(d[0])
	require.Error(t, e)
	require.Equal(t, "PENDING_DELIVERY", f.lifecycle(t, d[0].CredentialID).State)
	require.Equal(t, "DELIVERED", f.rotation(t, r.ID).Phase)
	f.publisher.fail = false
	_, e = f.ackDelivery(d[0])
	require.NoError(t, e)
	f.revokeErr = errors.New("IAM revoke temporarily unavailable")
	require.Error(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, r.RetireOldAt))
	require.Equal(t, "RETIRING", f.lifecycle(t, 303).State)
	require.Nil(t, f.credentials[0].RevokedAt)
	f.revokeErr = nil
	f.publisher.fail = true
	require.Error(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, r.RetireOldAt))
	require.Equal(t, "RETIRING", f.lifecycle(t, 303).State)
	require.NotNil(t, f.credentials[0].RevokedAt, "independent IAM commit survives fleet outbox rollback")
	f.publisher.fail = false
	require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, r.RetireOldAt))
	require.Equal(t, "REVOKED", f.lifecycle(t, 303).State)
	require.Equal(t, 1, f.revoked, "retry must be idempotent at IAM")
}
func TestCredentialRotationUnavailableAndMalformedRequests(t *testing.T) {
	f := rotationHarness(t)
	_, e := (*Module)(nil).RequestCredentialRotation(f.ctx, 7, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = f.m.RequestCredentialRotation(f.ctx, 0, 1)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = f.m.AckCredentialDelivery(f.ctx, 7, 1, 0, 0, f.session.ID, 1)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = f.m.AckCredentialDelivery(f.ctx, 7, 1, 999, 888, f.session.ID, 1)
	require.ErrorIs(t, e, ErrMachineNotFound)
	prior := f.m.runtime
	f.m.runtime = nil
	_, e = f.requestRotation()
	require.ErrorIs(t, e, ErrUnavailable)
	f.m.runtime = prior
	router := fleetBusinessRouter(t, f.m)
	for _, tc := range []struct {
		method, path, body string
		headers            map[string]string
		status             int
	}{
		{http.MethodPost, "/machines/9999999999999999999/credential-rotations", "", nil, 422},
		{http.MethodPost, "/machines/9999999999999999999/credential-deliveries", `{"session_id":"1","conn_epoch":1}`, nil, 422},
		{http.MethodPut, "/machines/1/credential-deliveries/9999999999999999999/ack", `{"session_id":"1","conn_epoch":1,"credential_id":"2"}`, nil, 422},
		{http.MethodPut, "/machines/9999999999999999999/credential-deliveries/1/ack", `{"session_id":"1","conn_epoch":1,"credential_id":"2"}`, nil, 422},
		{http.MethodPost, "/machines/1/credential-deliveries", `{"session_id":"1","conn_epoch":1}`, map[string]string{"Authorization": "Bearer untrusted"}, 401},
		{http.MethodPut, "/machines/1/credential-deliveries/1/ack", `{"session_id":"1","conn_epoch":1,"credential_id":"2"}`, map[string]string{"Authorization": "Bearer runner"}, 409},
	} {
		response := fleetHTTPRequest(router, tc.method, tc.path, tc.body, tc.headers)
		require.Equal(t, tc.status, response.Code, response.Body.String())
	}
	for _, tc := range []struct{ path, method string }{
		{"/machines/1/credential-rotations", "POST"}, {"/machines/1/credential-deliveries", "POST"}, {"/machines/1/credential-deliveries/1/ack", "PUT"},
	} {
		response := fleetHTTPRequest(router, http.MethodOptions, tc.path+"?method="+tc.method, "", nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "operation_id")
	}
	q := query.BindRotations(f.db, f.m.names)
	n, e := q.WithContext(f.ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n, "OPTIONS and malformed requests cannot start a saga")
}

func TestCredentialRotationRejectsRevokedHumanBeforeIssuance(t *testing.T) {
	f := rotationHarness(t)
	r, e := f.requestRotation()
	require.NoError(t, e)
	f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	d, e := f.pullDelivery()
	require.ErrorIs(t, e, ErrMachineForbidden)
	require.Empty(t, d)
	require.Zero(t, f.issued)
	row := f.rotation(t, r.ID)
	require.Equal(t, "FAILED", row.State)
	require.Equal(t, "AUTHORITY_LOST", row.ReasonCode)
	require.Nil(t, row.ActiveKey)
}

func TestCredentialRotationPollingRecoversAnUnacknowledgedDelivery(t *testing.T) {
	f := rotationHarness(t)
	r, e := f.requestRotation()
	require.NoError(t, e)
	first, e := f.pullDelivery()
	require.NoError(t, e)
	require.Len(t, first, 1)
	f.ageDelivery(t, r.ID)
	second, e := f.pullDelivery()
	require.NoError(t, e)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].CredentialID, second[0].CredentialID)
	require.Equal(t, 2, f.issued)
	require.NotNil(t, f.credentials[1].RevokedAt)
	require.Equal(t, "REVOKED", f.lifecycle(t, first[0].CredentialID).State)
	_, e = f.ackDelivery(first[0])
	require.ErrorIs(t, e, ErrVersionConflict)
	active, e := f.ackDelivery(second[0])
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", active.State)
}

func TestCredentialRotationWatchdogTraversesPagesAndSeparatesTenants(t *testing.T) {
	f := rotationHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	// Ambiguous IAM issuance is recoverable without an in-memory credential ID,
	// without runner IAM authority, and without a live requesting Human.
	f.m.enrollmentIAM.ListCredentials = func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) { return nil, nil }
	for id := guid.ID(100); id < 202; id++ {
		tenant := guid.ID(7)
		if id == 201 {
			tenant = 8
		}
		key := "machine-" + id.String()
		require.NoError(t, query.BindMachines(f.db, f.m.names).WithContext(f.ctx).Create(&model.Machine{ID: id, TenantID: tenant, PrincipalID: id + 1000, Name: key, NameKey: &key, State: "OFFLINE", OS: "LINUX", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}))
		active := 1
		require.NoError(t, query.BindRotations(f.db, f.m.names).WithContext(f.ctx).Create(&model.CredentialRotation{ID: id, TenantID: tenant, MachineID: id, PrincipalID: id + 1000, RequestedBy: 101, State: "PENDING_DELIVERY", Phase: "ISSUING", Attempts: 5, ActiveKey: &active, RetireOldAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}))
	}
	require.NoError(t, f.m.expireCredentialRotations(context.Background(), f.db, 7, now))
	q := query.BindRotations(f.db, f.m.names)
	n, e := q.WithContext(f.ctx).Where(q.TenantID.Eq(guid.ID(7)), q.State.Eq("FAILED")).Count()
	require.NoError(t, e)
	require.Equal(t, int64(101), n)
	require.Equal(t, "PENDING_DELIVERY", f.rotation(t, 201).State)
	require.Zero(t, f.issued)
}

func TestRotationDeliveryEveryStorageFailureReturnsNoPrivateCredential(t *testing.T) {
	for _, kind := range []string{"query", "create", "update"} {
		total := 0
		run := func(t *testing.T, at int) {
			f := rotationHarness(t)
			_, e := f.requestRotation()
			require.NoError(t, e)
			injected := errors.New("rotation delivery storage unavailable")
			faultDB, writerErr := f.m.writer()
			require.NoError(t, writerErr)
			count := 0
			hook := func(tx *gorm.DB) {
				if !strings.Contains(tx.Statement.Table, "fleet_") {
					return
				}
				count++
				if at > 0 && count == at {
					tx.AddError(injected)
				}
			}
			switch kind {
			case "query":
				require.NoError(t, faultDB.Callback().Query().Before("gorm:query").Register("rotation_delivery_failure", hook))
			case "create":
				require.NoError(t, faultDB.Callback().Create().Before("gorm:create").Register("rotation_delivery_failure", hook))
			case "update":
				require.NoError(t, faultDB.Callback().Update().Before("gorm:update").Register("rotation_delivery_failure", hook))
			}
			deliveries, err := f.pullDelivery()
			require.NoError(t, faultDB.Callback().Query().Remove("rotation_delivery_failure"))
			require.NoError(t, faultDB.Callback().Create().Remove("rotation_delivery_failure"))
			require.NoError(t, faultDB.Callback().Update().Remove("rotation_delivery_failure"))
			if at == 0 {
				require.NoError(t, err)
				require.Len(t, deliveries, 1)
				require.NotEmpty(t, deliveries[0].ClientSecret)
				require.Positive(t, count)
				total = count
				return
			}
			require.ErrorIs(t, err, injected)
			require.Empty(t, deliveries)
			rotations, e := query.BindRotations(f.db, f.m.names).WithContext(f.ctx).Find()
			require.NoError(t, e)
			require.Len(t, rotations, 1)
			require.Equal(t, "PENDING_DELIVERY", rotations[0].State)
			require.LessOrEqual(t, f.issued, 1)
		}
		t.Run(kind+"/success", func(t *testing.T) { run(t, 0) })
		for at := 1; at <= total; at++ {
			t.Run(fmt.Sprintf("%s/%d", kind, at), func(t *testing.T) { run(t, at) })
		}
	}
}

func TestRotationRequestRejectsInvalidCurrentCredentialOwnerAndIdentifiers(t *testing.T) {
	for _, fault := range []string{"list", "foreign_owner", "zero_credential", "identifier", "zero_identifier", "missing_lease", "lease", "writer"} {
		t.Run(fault, func(t *testing.T) {
			f := rotationHarness(t)
			injected := errors.New("credential authority unavailable")
			switch fault {
			case "list":
				f.listErr = injected
			case "foreign_owner":
				f.credentials[0].ServiceAccountID = 999
			case "zero_credential":
				f.credentials[0].ID = 0
			case "identifier":
				f.m.ids = failedFleetIDs{err: injected}
			case "zero_identifier":
				f.m.ids = failedFleetIDs{}
			case "missing_lease":
				f.m.enrollmentLease = nil
			case "lease":
				f.m.enrollmentLease = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
					return nil, injected
				}
			case "writer":
				f.m.runtime = nil
			}
			_, err := f.requestRotation()
			require.Error(t, err)
			rotations, e := query.BindRotations(f.db, f.m.names).WithContext(f.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, rotations)
			credentials, e := query.BindCredentials(f.db, f.m.names).WithContext(f.ctx).Count()
			require.NoError(t, e)
			require.Zero(t, credentials)
			require.Zero(t, f.issued)
			require.Zero(t, f.revoked)
		})
	}
}
