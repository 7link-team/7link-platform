package yunxiao

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IntakeCredentialView struct {
	Channel    IntakeChannelView `json:"channel"`
	Credential string            `json:"credential,omitempty" doc:"Shown once on first execution; never persisted in plaintext. A replay omits it. Rotate again if lost."`
}
type intakeCredentialInput struct {
	WorkProjectPath
	WriteHeaders
	Body struct{}
}
type intakeReceivingInput struct {
	WorkProjectPath
	WriteHeaders
	Credential string `header:"X-Intake-Key" required:"true" minLength:"64" maxLength:"64"`
	Body       intake.APIReceiptInput
}

func credentialDigest(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}
func (m *Module) intakeAPIChannelOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (*model.IntakeChannel, error) {
	q := query.BindIntakeChannels(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.Eq("API")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUnavailable
	}
	return row, err
}

// authorizeIntakeAPIOn discovers participant identities without locking policy.
// Like policy replacement, it locks triage participants before project/IAM and
// channel fences. The current locking reread rejects stale discovery, including
// a concurrent credential rotation or triage binding change.
func (m *Module) authorizeIntakeAPIOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) (*model.IntakeChannel, error) {
	if tenant <= 0 || project <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	q := query.BindIntakeChannels(tx, m.names)
	observed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.Eq("API")).Take()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		observed = nil
	}
	if observed != nil {
		view, e := channelView(observed)
		if e != nil {
			return nil, e
		}
		if e = m.intakeTriageAgentOn(ctx, tx, tenant, view.Settings); e != nil {
			return nil, e
		}
	}
	if err = m.projectAuthorityOn(ctx, tx, tenant, project, permission); err != nil {
		return nil, err
	}
	row, err := m.intakeAPIChannelOn(ctx, tx, tenant, project)
	if err != nil {
		return nil, err
	}
	if observed == nil || observed.ID != row.ID || observed.Version != row.Version {
		return nil, ErrConflict
	}
	return row, nil
}
func (m *Module) authorizeIntakeReceivingOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, secret string) error {
	row, err := m.authorizeIntakeAPIOn(ctx, tx, tenant, project, "intake_submit")
	if err != nil {
		return err
	}
	view, err := channelView(row)
	if err != nil {
		return err
	}
	if !view.Settings.Enabled {
		return ErrForbidden
	}
	if row.CredentialHash == nil || len(secret) != 64 || subtle.ConstantTimeCompare([]byte(*row.CredentialHash), []byte(credentialDigest(secret))) != 1 {
		return ErrForbidden
	}
	return nil
}
func (m *Module) rotateIntakeCredentialHTTP(ctx context.Context, in *intakeCredentialInput) (*commandOutput[IntakeCredentialView], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	version, err := versionHeader(in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return nil, httpError(ctx, err)
	}
	secret := hex.EncodeToString(raw[:])
	out, err := commandAuthorized(m, ctx, tenant, in.WriteHeaders, "intake.credential.rotate:"+project.String(), in.Body, 200, func(tx *gorm.DB) error {
		_, err := m.authorizeIntakeAPIOn(ctx, tx, tenant, project, "intake_channel_manage")
		return err
	}, func(tx *gorm.DB) (IntakeCredentialView, int, error) {
		var result IntakeCredentialView
		row, e := m.intakeAPIChannelOn(ctx, tx, tenant, project)
		if e != nil {
			return result, 0, e
		}
		if row.Version != version {
			return result, 0, ErrConflict
		}
		hash := credentialDigest(secret)
		row.CredentialHash = &hash
		// Reuse the policy command's version, audit and outbox transaction. It loads
		// the row afresh, so save the hash before applying its unchanged policy.
		if e = query.BindIntakeChannels(tx, m.names).WithContext(ctx).Save(row); e != nil {
			return result, 0, e
		}
		current, e := channelView(row)
		if e != nil {
			return result, 0, e
		}
		result.Channel, e = m.saveIntakeChannelOn(ctx, tx, tenant, project, "API", version, current.Settings)
		return result, result.Channel.Version, e
	})
	if err == nil && out.Replayed != "true" {
		out.Body.Data.Credential = secret
	}
	return out, err
}
func (m *Module) receiveIntakeHTTP(ctx context.Context, in *intakeReceivingInput) (*commandOutput[intake.IntakeLedgerEntry], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	// Include only a digest in request identity; neither the replay result nor
	// an audit/outbox payload contains the receiving credential.
	body := struct {
		Input            intake.APIReceiptInput
		CredentialDigest string
	}{in.Body, credentialDigest(in.Credential)}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "intake.receive:"+project.String(), body, 201, func(tx *gorm.DB) error { return m.authorizeIntakeReceivingOn(ctx, tx, tenant, project, in.Credential) }, func(tx *gorm.DB) (intake.IntakeLedgerEntry, int, error) {
		var result intake.IntakeLedgerEntry
		if m.intakeReceiving.ReceiveOn == nil {
			return result, 0, ErrUnavailable
		}
		row, e := m.intakeAPIChannelOn(ctx, tx, tenant, project)
		if e != nil {
			return result, 0, e
		}
		view, e := channelView(row)
		if e != nil {
			return result, 0, e
		}
		day := m.now().UTC().Format("2006-01-02")
		if row.ReceivingDay == nil || *row.ReceivingDay != day {
			row.ReceivingDay = &day
			row.ReceivedCount = 0
		}
		if row.ReceivedCount >= view.Settings.DailyLimit {
			return result, 0, ErrConflict
		}
		result, e = m.intakeReceiving.ReceiveOn(ctx, tx, tenant, project, row.ID, in.Body)
		if errors.Is(e, intake.ErrInvalidArgument) {
			e = ErrInvalid
		}
		if errors.Is(e, intake.ErrForbidden) {
			e = ErrForbidden
		}
		if errors.Is(e, intake.ErrUnavailable) {
			e = ErrUnavailable
		}
		if e != nil {
			return result, 0, e
		}
		row.ReceivedCount++
		e = query.BindIntakeChannels(tx, m.names).WithContext(ctx).Save(row)
		return result, result.Version, e
	})
}
func (m *Module) registerIntakeReceivingHTTP(api huma.API) error {
	base := "/projects/{project_id}/intake-channels/API"
	op := huma.Operation{OperationID: "intake.credential.rotate", Method: "POST", Path: base + "/credential", Summary: "Generate and rotate a hashed API intake receiving credential", Tags: []string{"intakes"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	authz.Register(m.registrar, api, op, authz.Guard{Resource: "intake_channel", Verb: "manage", FilterData: true}, m.rotateIntakeCredentialHTTP)
	op.OperationID = "intake.receive"
	op.Path = base + "/submissions"
	op.Summary = "Receive an authenticated intake source using current project rights and channel credential"
	op.DefaultStatus = 201
	authz.Register(m.registrar, api, op, authz.Guard{Resource: "intake", Verb: "submit", FilterData: true}, m.receiveIntakeHTTP)
	for _, suffix := range []string{"/credential", "/submissions"} {
		path := "/tenants/{tenant_id}" + base + suffix
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
		// The channel selector is case-sensitive domain data. Keep the published
		// URL while conforming its generated metadata ID to the API convention.
		op := api.OpenAPI().Paths[path].Options
		op.OperationID = strings.ToLower(op.OperationID)
	}
	return nil
}
