package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestReceiptJSONStringMatchesCanonicalControlCharacterVectors(t *testing.T) {
	// RFC 8785 preserves printable Unicode and uses lowercase hex only for
	// controls without a dedicated JSON escape. These bytes are the contract.
	for _, vector := range []struct{ input, want string }{
		{"\"\\\b\t\n\f\r", `"\"\\\b\t\n\f\r"`},
		{"\x00\x01\x0b\x0e\x1f", `"\u0000\u0001\u000b\u000e\u001f"`},
		{"<>& /\u2028\u2029", "\"<>& /\u2028\u2029\""},
	} {
		encoded := receiptJSONString(vector.input)
		require.Equal(t, vector.want, encoded)
		var decoded string
		require.NoError(t, json.Unmarshal([]byte(encoded), &decoded))
		require.Equal(t, vector.input, decoded)
	}
}

func TestPackageSignatureCanonicalUnicodeSortedItemsAndTamperEvidence(t *testing.T) {
	at := time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC)
	items := []PackageItemDecision{{ItemID: strings.Repeat("b", 64), Result: "REJECTED", FeedbackCategory: "OTHER", Comment: "Line\n\u2028<unsafe>"}, {ItemID: strings.Repeat("a", 64), Result: "ACCEPTED"}}
	sign, err := packageReceiptSignature(100, 4, strings.Repeat("c", 64), "陈明 😀", "Checked", items, at)
	require.NoError(t, err)
	reversed, err := packageReceiptSignature(100, 4, strings.Repeat("c", 64), "陈明 😀", "Checked", []PackageItemDecision{items[1], items[0]}, at)
	require.NoError(t, err)
	require.Equal(t, sign, reversed)
	require.Contains(t, sign.ReceiptJSON, "\u2028<unsafe>")
	require.NotContains(t, sign.ReceiptJSON, `\u2028`)
	require.NotContains(t, sign.ReceiptJSON, `\u003c`)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(sign.ReceiptJSON), &parsed))
	require.Equal(t, "100", parsed["delivery_id"])
	digest := sha256.Sum256([]byte(sign.ReceiptJSON))
	require.Equal(t, hex.EncodeToString(digest[:]), sign.ReceiptSHA256)
	for _, change := range []struct {
		name, manifest string
		at             time.Time
	}{{"Another signer", strings.Repeat("c", 64), at}, {"陈明 😀", strings.Repeat("d", 64), at}, {"陈明 😀", strings.Repeat("c", 64), at.Add(time.Millisecond)}} {
		changed, err := packageReceiptSignature(100, 4, change.manifest, change.name, "Checked", items, change.at)
		require.NoError(t, err)
		require.NotEqual(t, sign.ReceiptSHA256, changed.ReceiptSHA256)
	}
	_, err = packageReceiptSignature(100, 4, "hash", " ", "Checked", items, at)
	require.Error(t, err)
}
func TestPackageSigningRequiresExplicitConfirmationCurrentContactAndRechecksReplay(t *testing.T) {
	m, db, _, _ := harness(t)
	created, err := m.create_packageHTTP(testContext(t, 2), &CreateInput[PackageBody]{EntityID: "3", Key: "signed-package", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, err)
	id := created.Body.Data.ID.String()
	_, err = publishBuiltPackageHTTP(t, m, testContext(t, 2), &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: id, Key: "signed-publish", IfMatch: `"1"`})
	require.NoError(t, err)
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	in := &EditInput[PackageDecision]{EntityID: "3", ID: id, Key: "signed-receipt", IfMatch: `"2"`, Body: PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Verified"}}
	for i, mutate := range []func(*PackageDecision){func(b *PackageDecision) { b.Confirmed = false }, func(b *PackageDecision) { b.SignerName = " " }, func(b *PackageDecision) { b.ContentHash = strings.Repeat("f", 64) }} {
		invalid := *in
		invalid.Key = string(rune('a' + i))
		mutate(&invalid.Body)
		_, err = m.package_acceptanceHTTP(testContext(t, 4), &invalid)
		require.Error(t, err)
	}
	allowed := false
	m.clientSigners.ContactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) {
		if !allowed {
			return 0, errors.New("contact can_accept revoked")
		}
		return 4, nil
	}
	_, err = m.package_acceptanceHTTP(testContext(t, 4), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Count(&count).Error)
	require.Zero(t, count)
	allowed = true
	_, err = m.package_acceptanceHTTP(testContext(t, 4), in)
	require.NoError(t, err)
	view, err = m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	require.NotNil(t, view.Body.Data.Signature)
	require.Len(t, view.Body.Data.Signature.ReceiptSHA256, 64)
	replay, err := m.package_acceptanceHTTP(testContext(t, 4), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	allowed = false
	_, err = m.package_acceptanceHTTP(testContext(t, 4), in)
	require.Error(t, err)
}
