package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
	"strings"
	"time"
)

type PackageSignature struct {
	Source        string            `json:"source,omitempty" enum:"PORTAL,RECORDED"`
	RecordedBy    *guid.ID          `json:"recorded_by,omitempty"`
	Evidence      *AnswerAttachment `json:"evidence,omitempty"`
	SignerName    string            `json:"signer_name"`
	ReceiptSHA256 string            `json:"receipt_sha256"`
	ReceiptJSON   string            `json:"receipt_json"`
}

func (m *Module) clientSignerOn(ctx context.Context, tx *gorm.DB, project guid.ID) (guid.ID, error) {
	if m.clientSigners.ContactOn == nil {
		return 0, ErrUnavailable
	}
	return m.clientSigners.ContactOn(ctx, tx, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx))
}

// receiptJSONString follows JCS string escaping: printable Unicode is retained
// (including U+2028/U+2029), with only controls, quote and backslash escaped.
func receiptJSONString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte("0123456789abcdef"[byte(r)>>4])
				b.WriteByte("0123456789abcdef"[byte(r)&15])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
func packageReceiptSignature(id, contact guid.ID, manifest, name, comment string, items []PackageItemDecision, received time.Time) (PackageSignature, error) {
	if id <= 0 || contact <= 0 || !validText(name, 200) || !validText(comment, 2000) || received.IsZero() {
		return PackageSignature{}, ErrInvalid
	}
	ordered := slices.Clone(items)
	slices.SortFunc(ordered, func(a, b PackageItemDecision) int { return strings.Compare(a.ItemID, b.ItemID) })
	var b strings.Builder
	b.WriteString(`{"delivery_id":` + receiptJSONString(id.String()) + `,"intake_contact_id":` + receiptJSONString(contact.String()) + `,"items":[`)
	for i, item := range ordered {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"comment":` + receiptJSONString(item.Comment) + `,"delivery_item_id":` + receiptJSONString(item.ItemID) + `,"feedback_category":` + receiptJSONString(item.FeedbackCategory) + `,"result":` + receiptJSONString(item.Result) + `}`)
	}
	b.WriteString(`],"manifest_sha256_ack":` + receiptJSONString(manifest) + `,"overall_comment":` + receiptJSONString(comment) + `,"received_at":` + receiptJSONString(received.UTC().Format(time.RFC3339Nano)) + `,"signer_name":` + receiptJSONString(name) + `,"source":"PORTAL"}`)
	raw := b.String()
	if !json.Valid([]byte(raw)) {
		return PackageSignature{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(raw))
	return PackageSignature{SignerName: name, ReceiptSHA256: hex.EncodeToString(digest[:]), ReceiptJSON: raw}, nil
}
