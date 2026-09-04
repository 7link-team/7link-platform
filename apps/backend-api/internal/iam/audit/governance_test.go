package audit

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetentionPolicyLifecycleAndTenantIsolation(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	tenantA, tenantB := guid.ID(201), guid.ID(202)
	policy, err := h.service.GetRetentionPolicy(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, defaultMinRetentionDays, policy.MinDays)
	assert.Equal(t, defaultArchiveAfterDays, policy.ArchiveAfterDays)

	policy, err = h.service.SetRetentionPolicy(t.Context(), tenantA, 90, 180)
	require.NoError(t, err)
	assert.Equal(t, RetentionPolicy{
		TenantID: tenantA, MinDays: 90, ArchiveAfterDays: 180, UpdatedAt: auditTestNow,
	}, policy)
	stored, err := h.service.GetRetentionPolicy(t.Context(), tenantA)
	require.NoError(t, err)
	assert.Equal(t, policy, stored)
	foreign, err := h.service.GetRetentionPolicy(t.Context(), tenantB)
	require.NoError(t, err)
	assert.Equal(t, defaultMinRetentionDays, foreign.MinDays)
	assert.Equal(t, int64(1), h.count(t, "iam_audit_events", tenantA))
	assert.Zero(t, h.count(t, "iam_audit_events", tenantB))

	updated, err := h.service.SetRetentionPolicy(t.Context(), tenantA, 30, 365)
	require.NoError(t, err)
	assert.Equal(t, 30, updated.MinDays)
	assert.Equal(t, 365, updated.ArchiveAfterDays)
	assert.Equal(t, int64(2), h.count(t, "iam_audit_events", tenantA))
}

func TestRetentionValidationAndAuditRollback(t *testing.T) {
	h := newAuditHarness(t, "")
	for _, test := range []struct {
		tenantID             guid.ID
		minDays, archiveDays int
	}{
		{0, 1, 1},
		{1, 0, 10},
		{1, maxRetentionDays + 1, maxRetentionDays + 1},
		{1, 365, 30},
		{1, 1, maxRetentionDays + 1},
	} {
		_, err := h.service.SetRetentionPolicy(t.Context(), test.tenantID, test.minDays, test.archiveDays)
		assert.ErrorIs(t, err, ErrRetentionInvalid)
	}
	_, err := h.service.GetRetentionPolicy(t.Context(), 0)
	assert.ErrorIs(t, err, ErrRetentionInvalid)
	_, err = h.service.Governance(t.Context(), 0)
	assert.ErrorIs(t, err, ErrRetentionInvalid)
	var unavailable *Service
	_, err = unavailable.SetRetentionPolicy(t.Context(), 1, 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	failing := NewService(h.runtime, auditFailingIDs{})
	failing.now = h.service.now
	_, err = failing.SetRetentionPolicy(t.Context(), 301, 30, 60)
	assert.ErrorContains(t, err, "ID source down")
	assert.Zero(t, h.count(t, "iam_audit_retention_policies", 301))
}

func TestGovernanceReportsRetentionIntegrityAndArchiveCounts(t *testing.T) {
	h := newAuditHarness(t, "")
	tenantID := guid.ID(401)
	then := auditTestNow.AddDate(0, 0, -120)
	h.service.now = func() time.Time { return then }
	for range 3 {
		_, err := h.service.Append(t.Context(), EventInput{
			TenantID: tenantID, EventType: "created", Outcome: "success",
		})
		require.NoError(t, err)
	}
	require.NoError(t, func() error {
		_, err := h.service.SetRetentionPolicy(t.Context(), tenantID, 30, 90)
		return err
	}())
	h.service.now = func() time.Time { return auditTestNow }

	report, err := h.service.Governance(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, RetentionPolicy{TenantID: tenantID, MinDays: 30, ArchiveAfterDays: 90, UpdatedAt: then}, report.Policy)
	assert.Equal(t, int64(4), report.TotalEvents)
	assert.Equal(t, int64(4), report.ArchiveReady)
	assert.Zero(t, report.Anchored)
	assert.Nil(t, report.Anchors)
	assert.True(t, report.Integrity.Valid)
	assert.False(t, report.Integrity.Anchor.Enabled)
}

func TestRootSignerRoundTripAndTamperDetection(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	signer, err := NewRootSigner(base64.StdEncoding.EncodeToString(seed))
	require.NoError(t, err)
	anchor := Anchor{
		Schema: anchorSchema, TenantID: 501, HeadSequence: 7,
		HeadHash: strings.Repeat("a", 64), AnchoredAt: auditTestNow,
	}
	anchor.AnchorHash = computeAnchorHash(anchor)
	require.NoError(t, signer.Sign(&anchor))
	assert.NotEmpty(t, anchor.SigningKeyID)
	assert.NotEmpty(t, anchor.RootPublicKey)
	assert.NotEmpty(t, anchor.RootSignature)
	assert.True(t, VerifyRootSignature(anchor))

	for name, mutate := range map[string]func(*Anchor){
		"hash":       func(value *Anchor) { value.AnchorHash = strings.Repeat("0", 64) },
		"signature":  func(value *Anchor) { value.RootSignature = "invalid" },
		"public key": func(value *Anchor) { value.RootPublicKey = "invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := anchor
			mutate(&changed)
			assert.False(t, VerifyRootSignature(changed))
		})
	}
	unsigned := anchor
	unsigned.RootSignature = ""
	assert.False(t, VerifyRootSignature(unsigned))
	assert.ErrorIs(t, (*RootSigner)(nil).Sign(&anchor), ErrInvalidSigningKey)
	assert.ErrorIs(t, (&RootSigner{}).Sign(&anchor), ErrInvalidSigningKey)
	assert.ErrorIs(t, signer.Sign(nil), ErrInvalidSigningKey)
	invalidHash := anchor
	invalidHash.AnchorHash = "bad"
	assert.Error(t, signer.Sign(&invalidHash))
}

func TestAnchorConfigurationAndCanonicalHelpers(t *testing.T) {
	valid := AnchorConfig{
		Endpoint: "http://127.0.0.1:9000", Bucket: "anchors",
		AccessKey: "access", SecretKey: "secret", RetentionDays: 30,
	}
	assert.True(t, valid.valid())
	for _, mutate := range []func(*AnchorConfig){
		func(config *AnchorConfig) { config.Endpoint = "" },
		func(config *AnchorConfig) { config.Bucket = "" },
		func(config *AnchorConfig) { config.AccessKey = "" },
		func(config *AnchorConfig) { config.SecretKey = "" },
		func(config *AnchorConfig) { config.RetentionDays = 0 },
	} {
		changed := valid
		mutate(&changed)
		assert.False(t, changed.valid())
		_, err := NewAnchorStore(changed)
		assert.ErrorIs(t, err, ErrAnchorMisconfigured)
	}

	address, secure, err := endpointAddress(valid.Endpoint)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9000", address)
	assert.False(t, secure)
	address, secure, err = endpointAddress("https://objects.example")
	require.NoError(t, err)
	assert.Equal(t, "objects.example", address)
	assert.True(t, secure)
	address, secure, err = endpointAddress("objects.example:9000")
	require.NoError(t, err)
	assert.Equal(t, "objects.example:9000", address)
	assert.False(t, secure)
	_, _, err = endpointAddress("http://[")
	assert.Error(t, err)

	store, err := NewAnchorStore(valid)
	require.NoError(t, err)
	assert.Equal(t, anchorPrefix+"/601/7.json", store.objectKey(601, 7))
	assert.Equal(t, int64(7), func() int64 { value, _ := parseAnchorSequence("7.json"); return value }())
	_, ok := parseAnchorSequence("bad.json")
	assert.False(t, ok)
	assert.NoError(t, anchorStoreError(nil))
	assert.ErrorIs(t, anchorStoreError(errors.New("offline")), ErrAnchorUnavailable)

	base := Anchor{
		Schema: anchorSchema, TenantID: 601, HeadSequence: 7,
		HeadHash: strings.Repeat("b", 64), AnchoredAt: auditTestNow,
	}
	first := computeAnchorHash(base)
	base.AnchoredAt = base.AnchoredAt.In(time.FixedZone("offset", 3600))
	assert.Equal(t, first, computeAnchorHash(base))
}

func TestRootSignerRejectsInvalidSeeds(t *testing.T) {
	for _, seed := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize-1))} {
		_, err := NewRootSigner(seed)
		assert.ErrorIs(t, err, ErrInvalidSigningKey)
	}
}
