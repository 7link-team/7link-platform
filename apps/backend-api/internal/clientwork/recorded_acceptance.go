package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strings"
)

const acceptanceRecordPurpose = "acceptance_record"
const recordedAcceptancePath = entityBase + "/delivery-packages/{id}/recorded-receipts"

type RecordedAcceptanceBody struct {
	PackageDecision
	EvidenceFileID guid.ID `json:"evidence_file_id"`
	ReauthProof    string  `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type RecordedAcceptanceInput struct {
	EditInput[RecordedAcceptanceBody]
	Cookie string `header:"Cookie" hidden:"true"`
}
type recordedAcceptance struct {
	RecordedBy guid.ID
	Evidence   AnswerAttachment
}

func (m *Module) injectRecordedAcceptance(s *module.Services, bindings *files.ResourceBindings) error {
	var err error
	if m.releaseDrafters, err = module.Resolve[delivery.ReleaseNoteDrafters](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.acceptanceProof, err = module.Resolve[authn.ReauthenticationConsumer](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.recordedContact, err = module.Resolve[delivery.RecordedAcceptanceContact](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	return bindings.RegisterPurpose(files.ResourcePurpose{Name: acceptanceRecordPurpose, ResourceKind: "client_package", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.acceptanceRecordResourceOn})
}

func (m *Module) registerRecordedAcceptanceHTTP(api huma.API) {
	iamauthn.RegisterAcceptanceReauthentication(api)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_acceptance.evidence", Method: "GET", Path: entityBase + "/delivery-packages/{id}/receipt-evidence", Summary: "Download exact pinned offline acceptance evidence", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "delivery", Verb: "read"}, m.recordedEvidenceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_acceptance.record", Method: "POST", Path: recordedAcceptancePath, Summary: "Record a client's offline item acceptance with pinned evidence and fresh Human confirmation", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "delivery", Verb: "send"}, m.recordedAcceptanceHTTP)
}

func (m *Module) recordedAcceptanceHTTP(ctx context.Context, in *RecordedAcceptanceInput) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	// Lock the real client principal before the project fence; rediscover the
	// persisted recipient inside the transaction, never trust request identity.
	var recipient guid.ID
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	err = m.writer().WithContext(ctx).Table(m.names.DeliveryPackage.String()).Where("tenant_id=? AND project_id=? AND id=?", authn.TenantIDFromContext(ctx), p, id).Select("recipient_id").Scan(&recipient).Error
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if recipient <= 0 {
		return nil, httpError(ctx, ErrNotFound)
	}
	return m.command(ctx, p, "clientwork.package_acceptance.record", "POST", recordedAcceptancePath, in.Key, 200, in, "delivery_send", false, func(tx *gorm.DB) error {
		_, _, err := m.recordedAcceptanceClientOn(ctx, tx, p, id, recipient)
		return err
	}, func(tx *gorm.DB) (Receipt, error) {
		return m.recordedAcceptanceOn(ctx, tx, p, id, v, in.Cookie, in.Body, recipient)
	}, func(tx *gorm.DB) error {
		return m.lockRecordedParticipantsOn(ctx, tx, recipient)
	})
}

func (m *Module) lockRecordedParticipantsOn(ctx context.Context, tx *gorm.DB, recipient guid.ID) error {
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	ids := []guid.ID{actor, recipient}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	for i, id := range ids {
		if i == 1 && id == ids[0] {
			continue
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) recordedAcceptanceClientOn(ctx context.Context, tx *gorm.DB, p, id, recipient guid.ID) (guid.ID, guid.ID, error) {
	if _, err := m.authorize(ctx, tx, p, "delivery_send", false); err != nil {
		return 0, 0, err
	}
	row, err := m.packageOn(ctx, tx, p, id, false)
	if err != nil {
		return 0, 0, err
	}
	if row.RecipientID != recipient {
		return 0, 0, ErrConflict
	}
	if m.recordedContact == nil || m.clients == nil {
		return 0, 0, ErrUnavailable
	}
	active, err := m.clients.IsClientOn(ctx, tx, row.TenantID, p, recipient)
	if err != nil {
		return 0, 0, err
	}
	if !active {
		return 0, 0, ErrForbidden
	}
	contact, err := m.recordedContact(ctx, tx, row.TenantID, p, recipient)
	return contact, recipient, err
}
func (m *Module) recordedAcceptanceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, cookie string, in RecordedAcceptanceBody, recipient guid.ID) (Receipt, error) {
	if !in.Confirmed || !validText(in.SignerName, 200) || !validText(in.Reason, 2000) || in.EvidenceFileID <= 0 || len(in.Items) == 0 {
		return Receipt{}, ErrInvalid
	}
	contact, actor, err := m.recordedAcceptanceClientOn(ctx, tx, p, id, recipient)
	if err != nil {
		return Receipt{}, err
	}
	if m.acceptanceProof == nil || m.answerFiles == nil {
		return Receipt{}, ErrUnavailable
	}
	if err = m.acceptanceProof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, in.ReauthProof, acceptanceRecordPurpose, "package:"+id.String()); err != nil {
		return Receipt{}, err
	}
	candidate, err := m.answerFiles.InspectOn(ctx, tx, in.EvidenceFileID)
	if err != nil {
		return Receipt{}, err
	}
	if candidate.FileID != in.EvidenceFileID || candidate.NodeVersion < 1 || candidate.VersionID <= 0 || len(candidate.SHA256) != 64 || candidate.Size <= 0 || candidate.Size > answerAttachmentLimit || !permittedAnswerAttachment(candidate.Name, candidate.MIME) {
		return Receipt{}, ErrInvalid
	}
	bid, err := m.nextID()
	if err != nil {
		return Receipt{}, err
	}
	pin, err := m.answerFiles.PinOn(ctx, tx, files.ResourceBindingInput{ID: bid, SpaceID: candidate.SpaceID, FileID: candidate.FileID, ExpectedFileVersion: candidate.NodeVersion, VersionID: &candidate.VersionID, Purpose: acceptanceRecordPurpose, ResourceKind: "client_package", ResourceID: id, Slot: "acceptance_evidence", ActorID: authn.PrincipalIDFromContext(ctx)})
	if err != nil {
		return Receipt{}, err
	}
	if pin.FileID != candidate.FileID || pin.SpaceID != candidate.SpaceID || pin.VersionID != candidate.VersionID || pin.SHA256 != candidate.SHA256 || pin.Name != candidate.Name || pin.MIME != candidate.MIME || pin.Size != candidate.Size || pin.BindingID <= 0 {
		return Receipt{}, ErrConflict
	}
	record := &recordedAcceptance{RecordedBy: authn.PrincipalIDFromContext(ctx), Evidence: AnswerAttachment{ID: pin.BindingID, FileID: pin.FileID, VersionID: pin.VersionID, Name: pin.Name, MIME: pin.MIME, Size: pin.Size, SHA256: pin.SHA256}}
	return m.decidePackageForOn(ctx, tx, p, id, v, in.PackageDecision, contact, actor, record)
}

// Keep the online canonical bytes unchanged. Offline signatures add evidence
// identity and recorder in lexical key order; proof/session values never persist.
func recordedReceiptSignature(signature PackageSignature, record recordedAcceptance) (PackageSignature, error) {
	if record.RecordedBy <= 0 || record.Evidence.ID <= 0 || len(record.Evidence.SHA256) != 64 {
		return PackageSignature{}, ErrInvalid
	}
	raw := strings.Replace(signature.ReceiptJSON, `,"signer_name":`, `,"record_evidence_sha256":`+receiptJSONString(record.Evidence.SHA256)+`,"record_evidence_version_id":`+receiptJSONString(record.Evidence.VersionID.String())+`,"recorded_by":`+receiptJSONString(record.RecordedBy.String())+`,"signer_name":`, 1)
	raw = strings.Replace(raw, `"source":"PORTAL"`, `"source":"RECORDED"`, 1)
	if !json.Valid([]byte(raw)) {
		return PackageSignature{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(raw))
	signature.ReceiptJSON = raw
	signature.ReceiptSHA256 = hex.EncodeToString(digest[:])
	signature.Source = "RECORDED"
	signature.RecordedBy = &record.RecordedBy
	signature.Evidence = &record.Evidence
	return signature, nil
}

func (m *Module) acceptanceRecordResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	ext, err := m.authorize(ctx, tx, p, "delivery_read", true)
	if err != nil {
		return err
	}
	_, err = m.packageOn(ctx, tx, p, id, ext)
	return err
}
func (m *Module) recordedEvidenceHTTP(ctx context.Context, in *ObjectInput) (*huma.StreamResponse, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.answerFiles == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var read files.PinnedResourceRead
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
		if e != nil {
			return e
		}
		if _, e = m.packageOn(ctx, tx, p, id, ext); e != nil {
			return e
		}
		q := query.BindPackageReceipt(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.PackageID.Eq(id)).Take()
		if e != nil {
			return e
		}
		if r.EvidenceBindingID == nil || r.EvidenceJSON == nil || r.Source != "RECORDED" {
			return ErrNotFound
		}
		var evidence AnswerAttachment
		if e = json.Unmarshal([]byte(*r.EvidenceJSON), &evidence); e != nil {
			return e
		}
		read, e = m.answerFiles.OpenOn(ctx, tx, *r.EvidenceBindingID)
		if e != nil {
			return e
		}
		if read.Reader == nil || read.BindingID != evidence.ID || read.ResourceID != id || read.Purpose != acceptanceRecordPurpose || read.ResourceKind != "client_package" || read.FileID != evidence.FileID || read.VersionID != evidence.VersionID || read.SHA256 != evidence.SHA256 || read.Size != evidence.Size || read.Name != evidence.Name || read.MIME != evidence.MIME {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		if read.Reader != nil {
			_ = read.Reader.Close()
		}
		return nil, httpError(ctx, err)
	}
	return pinnedContentResponse(read, false), nil
}
