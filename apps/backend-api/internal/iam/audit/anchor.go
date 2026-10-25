package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	anchorSchema   = "audit-anchor.v1"
	anchorPrefix   = "audit/anchors"
	anchorLockMode = minio.Compliance
)

var (
	ErrAnchorDisabled      = errors.New("audit anchoring is not enabled")
	ErrAnchorEmpty         = errors.New("audit chain has no events to anchor")
	ErrAnchorMisconfigured = errors.New("audit anchoring requires endpoint, bucket, access key, secret key, and retention days")
	ErrAnchorAlreadyExists = errors.New("audit anchor already exists")
	ErrAnchorUnavailable   = errors.New("audit anchor store unavailable")
	ErrRootSigningDisabled = errors.New("audit root signing is not enabled")
	ErrInvalidSigningKey   = errors.New("audit root signing key must be a base64-encoded 32-byte Ed25519 seed")
)

type Anchor struct {
	Schema             string    `json:"schema"`
	TenantID           guid.ID   `json:"tenant_id"`
	HeadSequence       int64     `json:"head_sequence"`
	HeadHash           string    `json:"head_hash"`
	AnchoredAt         time.Time `json:"anchored_at"`
	PreviousAnchorHash string    `json:"previous_anchor_hash,omitempty"`
	AnchorHash         string    `json:"anchor_hash"`
	SigningKeyID       string    `json:"signing_key_id,omitempty"`
	RootPublicKey      string    `json:"root_public_key,omitempty"`
	RootSignature      string    `json:"root_signature,omitempty"`
}

type AnchorStatus struct {
	Enabled        bool      `json:"enabled"`
	SigningEnabled bool      `json:"signing_enabled"`
	Sequence       int64     `json:"sequence,omitempty"`
	Hash           string    `json:"hash,omitempty"`
	AnchoredAt     time.Time `json:"anchored_at,omitempty"`
	Signed         bool      `json:"signed"`
	SignatureValid bool      `json:"signature_valid,omitempty"`
	Valid          bool      `json:"valid"`
}

type AnchorStore struct {
	client    *minio.Client
	telemetry *s3Telemetry
	bucket    string
	prefix    string
	region    string
	retention time.Duration
	ensureMu  sync.Mutex
	ready     bool
}

func NewAnchorStore(cfg AnchorConfig) (*AnchorStore, error) {
	if !cfg.valid() {
		return nil, ErrAnchorMisconfigured
	}
	address, secure, err := endpointAddress(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	telemetry, err := newS3Telemetry(secure)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(address, &minio.Options{
		Transport: telemetry,
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("build anchor client: %w", err)
	}
	return &AnchorStore{
		client: client, telemetry: telemetry, bucket: cfg.Bucket, prefix: anchorPrefix, region: cfg.Region,
		retention: time.Duration(cfg.RetentionDays) * 24 * time.Hour,
	}, nil
}

func endpointAddress(endpoint string) (string, bool, error) {
	lower := strings.ToLower(endpoint)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return endpoint, false, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "", false, fmt.Errorf("invalid anchor endpoint %q", endpoint)
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func anchorStoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrAnchorUnavailable, err.Error())
}

func (s *AnchorStore) ensureBucket(ctx context.Context) error {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if s.ready {
		return nil
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check anchor bucket: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region, ObjectLocking: true}); err != nil {
			// Another replica may have created the bucket after our HEAD.
			code := minio.ToErrorResponse(err).Code
			if code != "BucketAlreadyOwnedByYou" && code != "BucketAlreadyExists" {
				return fmt.Errorf("create anchor bucket %q: %w", s.bucket, err)
			}
		}
	}
	if err := s.probeObjectLock(ctx); err != nil {
		return err
	}
	s.ready = true
	return nil
}

func (s *AnchorStore) probeObjectLock(ctx context.Context) error {
	key := s.prefix + "/_lock-probe"
	opts := minio.PutObjectOptions{Mode: minio.Governance, RetainUntilDate: time.Now().UTC().Add(time.Minute)}
	if _, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(nil), 0, opts); err != nil {
		return fmt.Errorf("anchor bucket %q must support object locking: %w", s.bucket, err)
	}
	_ = s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{GovernanceBypass: true})
	return nil
}

func (s *AnchorStore) objectKey(tenantID guid.ID, sequence int64) string {
	return s.prefix + "/" + tenantID.String() + "/" + strconv.FormatInt(sequence, 10) + ".json"
}

func (s *AnchorStore) Put(ctx context.Context, anchor Anchor) error {
	if err := s.ensureBucket(ctx); err != nil {
		return err
	}
	key := s.objectKey(anchor.TenantID, anchor.HeadSequence)
	if _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
		return ErrAnchorAlreadyExists
	} else if minio.ToErrorResponse(err).Code != minio.NoSuchKey {
		return fmt.Errorf("stat anchor object: %w", err)
	}
	payload, err := json.Marshal(anchor)
	if err != nil {
		return fmt.Errorf("encode anchor: %w", err)
	}
	opts := minio.PutObjectOptions{
		ContentType: "application/json", Mode: anchorLockMode,
		RetainUntilDate: time.Now().UTC().Add(s.retention),
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(payload), int64(len(payload)), opts); err != nil {
		return fmt.Errorf("put anchor object: %w", err)
	}
	return nil
}

func (s *AnchorStore) List(ctx context.Context, tenantID guid.ID) ([]Anchor, error) {
	if err := s.ensureBucket(ctx); err != nil {
		return nil, err
	}
	var anchors []Anchor
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix: s.prefix + "/" + tenantID.String() + "/", Recursive: true,
	}) {
		if object.Err != nil {
			return nil, fmt.Errorf("list anchor objects: %w", object.Err)
		}
		anchor, ok, err := readAnchorObject(ctx, s.client, s.bucket, object.Key)
		if err != nil {
			return nil, err
		}
		if ok {
			anchors = append(anchors, anchor)
		}
	}
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].HeadSequence < anchors[j].HeadSequence })
	return anchors, nil
}

func readAnchorObject(ctx context.Context, client *minio.Client, bucket, key string) (Anchor, bool, error) {
	name := key[strings.LastIndexByte(key, '/')+1:]
	if !strings.HasSuffix(name, ".json") {
		return Anchor{}, false, nil
	}
	sequence, ok := parseAnchorSequence(name)
	if !ok {
		return Anchor{}, false, nil
	}
	object, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return Anchor{}, false, fmt.Errorf("read anchor object %q: %w", key, err)
	}
	payload, err := io.ReadAll(object)
	closeErr := object.Close()
	if err != nil {
		return Anchor{}, false, fmt.Errorf("read anchor object %q: %w", key, err)
	}
	if closeErr != nil {
		return Anchor{}, false, fmt.Errorf("close anchor object %q: %w", key, closeErr)
	}
	var anchor Anchor
	if err := json.Unmarshal(payload, &anchor); err != nil {
		return Anchor{}, false, fmt.Errorf("decode anchor object %q: %w", key, err)
	}
	if anchor.Schema != anchorSchema || anchor.HeadSequence != sequence {
		return Anchor{}, false, nil
	}
	return anchor, true, nil
}

func parseAnchorSequence(name string) (int64, bool) {
	sequence, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
	return sequence, err == nil
}

func computeAnchorHash(anchor Anchor) string {
	payload, _ := json.Marshal(struct {
		Schema, TenantID, HeadHash, PreviousAnchorHash string
		HeadSequence                                   int64
		AnchoredAt                                     time.Time
	}{anchor.Schema, anchor.TenantID.String(), anchor.HeadHash, anchor.PreviousAnchorHash, anchor.HeadSequence, anchor.AnchoredAt.UTC()})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type RootSigner struct {
	privateKey ed25519.PrivateKey
	keyID      string
}

func NewRootSigner(seedBase64 string) (*RootSigner, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedBase64))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, ErrInvalidSigningKey
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := ed25519.PublicKey(privateKey[ed25519.SeedSize:])
	digest := sha256.Sum256(publicKey)
	return &RootSigner{privateKey: privateKey, keyID: hex.EncodeToString(digest[:8])}, nil
}

func (s *RootSigner) Sign(anchor *Anchor) error {
	if s == nil || anchor == nil || len(s.privateKey) != ed25519.PrivateKeySize {
		return ErrInvalidSigningKey
	}
	digest, err := hex.DecodeString(anchor.AnchorHash)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("audit anchor hash is not a valid SHA-256 digest")
	}
	anchor.SigningKeyID = s.keyID
	publicKey := ed25519.PublicKey(s.privateKey[ed25519.SeedSize:])
	anchor.RootPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	anchor.RootSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(s.privateKey, digest))
	return nil
}

func VerifyRootSignature(anchor Anchor) bool {
	if anchor.RootSignature == "" || anchor.RootPublicKey == "" {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(anchor.RootSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	publicKey, err := base64.StdEncoding.DecodeString(anchor.RootPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	digest, err := hex.DecodeString(anchor.AnchorHash)
	if err != nil || len(digest) != sha256.Size {
		return false
	}
	return ed25519.Verify(publicKey, digest, signature)
}

func (s *Service) Anchor(ctx context.Context, tenantID guid.ID) (Anchor, error) {
	if s == nil || s.anchor == nil {
		return Anchor{}, ErrAnchorDisabled
	}
	if tenantID.Zero() {
		return Anchor{}, ErrInvalidFilter
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return Anchor{}, err
	}
	head, found, err := repo.readHead(ctx, tenantID)
	if err != nil {
		return Anchor{}, err
	}
	if !found {
		return Anchor{}, ErrAnchorEmpty
	}
	integrity, err := s.verifyHead(ctx, repo, head)
	if err != nil {
		return Anchor{}, err
	}
	if !integrity.Valid {
		return Anchor{}, ErrIntegrity
	}
	anchors, err := s.anchor.List(ctx, tenantID)
	if err != nil {
		return Anchor{}, anchorStoreError(err)
	}
	var latest *Anchor
	if len(anchors) > 0 {
		latest = &anchors[len(anchors)-1]
	}
	switch {
	case latest != nil && latest.HeadSequence > head.Sequence:
		return Anchor{}, ErrIntegrity
	case latest != nil && latest.HeadSequence == head.Sequence:
		return *latest, nil
	}
	anchor := Anchor{
		Schema: anchorSchema, TenantID: tenantID, HeadSequence: head.Sequence,
		HeadHash: head.EventHash, AnchoredAt: s.now().UTC(),
	}
	if latest != nil {
		anchor.PreviousAnchorHash = latest.AnchorHash
	}
	anchor.AnchorHash = computeAnchorHash(anchor)
	if s.signer != nil {
		if err := s.signer.Sign(&anchor); err != nil {
			return Anchor{}, fmt.Errorf("sign audit root: %w", err)
		}
	}
	if err := s.anchor.Put(ctx, anchor); err != nil {
		if errors.Is(err, ErrAnchorAlreadyExists) {
			anchors, listErr := s.anchor.List(ctx, tenantID)
			if listErr != nil {
				return Anchor{}, anchorStoreError(listErr)
			}
			if len(anchors) > 0 {
				return anchors[len(anchors)-1], nil
			}
		}
		return Anchor{}, anchorStoreError(err)
	}
	return anchor, nil
}

func (s *Service) SignRoot(ctx context.Context, tenantID guid.ID) (Anchor, error) {
	if s == nil || s.anchor == nil {
		return Anchor{}, ErrAnchorDisabled
	}
	if s.signer == nil {
		return Anchor{}, ErrRootSigningDisabled
	}
	anchor, err := s.Anchor(ctx, tenantID)
	if err != nil {
		return Anchor{}, err
	}
	if anchor.RootSignature == "" {
		return Anchor{}, ErrRootSigningDisabled
	}
	return anchor, nil
}

func (s *Service) verifyAnchors(ctx context.Context, repo *Repository, tenantID guid.ID) (AnchorStatus, error) {
	anchors, err := s.anchor.List(ctx, tenantID)
	if err != nil {
		return AnchorStatus{}, anchorStoreError(err)
	}
	status := AnchorStatus{Enabled: true, SigningEnabled: s.signer != nil, Valid: true}
	if len(anchors) == 0 {
		return status, nil
	}
	previous := ""
	for _, anchor := range anchors {
		if anchor.TenantID != tenantID || anchor.PreviousAnchorHash != previous || anchor.AnchorHash != computeAnchorHash(anchor) {
			status.Valid = false
			return status, nil
		}
		if anchor.RootSignature != "" {
			status.Signed = true
			if !VerifyRootSignature(anchor) {
				status.Valid = false
				return status, nil
			}
			status.SignatureValid = true
		}
		previous = anchor.AnchorHash
	}
	latest := anchors[len(anchors)-1]
	status.Sequence, status.Hash, status.AnchoredAt = latest.HeadSequence, latest.AnchorHash, latest.AnchoredAt
	chain, err := s.verifyPrefix(ctx, repo, tenantID, latest.HeadSequence)
	if err != nil {
		return AnchorStatus{}, err
	}
	if !chain.Valid || chain.HeadHash != latest.HeadHash {
		status.Valid = false
	}
	return status, nil
}
