package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/minio/minio-go/v7"
)

const (
	erasureObligationSchema  = "iam-erasure-obligation.v1"
	erasureObligationPrefix  = "audit/erasures"
	maxErasureObligationSize = 4096
)

var (
	ErrErasureLedgerDisabled    = errors.New("external erasure ledger is not enabled")
	ErrErasureLedgerInvalid     = errors.New("invalid erasure obligation")
	ErrErasureLedgerIntegrity   = errors.New("erasure ledger integrity check failed")
	ErrErasureLedgerUnavailable = errors.New("external erasure ledger unavailable")
)

// ErasureObligation is deliberately free of PII. Once its immutable object is
// verified, the platform owes this erasure even if the following database
// commit fails or an older backup later replaces the database.
type ErasureObligation struct {
	Schema           string    `json:"schema"`
	PrincipalID      guid.ID   `json:"principal_id"`
	LifecycleVersion int64     `json:"lifecycle_version"`
	RequestedBy      guid.ID   `json:"requested_by,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
}

type VerifiedErasureObligation struct {
	ErasureObligation
	ObjectKey   string    `json:"object_key"`
	VersionID   string    `json:"version_id"`
	SHA256      string    `json:"sha256"`
	RetainUntil time.Time `json:"retain_until"`
}

// ErasureRecorder is the narrow capability identity consumes. Audit owns the
// external storage protocol; identity only supplies the obligation it accepted.
type ErasureRecorder func(context.Context, ErasureObligation) error

func NewErasureObligation(ctx context.Context, principalID guid.ID, lifecycleVersion int64, recordedAt time.Time) ErasureObligation {
	return ErasureObligation{
		Schema: erasureObligationSchema, PrincipalID: principalID, LifecycleVersion: lifecycleVersion,
		RequestedBy: authn.PrincipalIDFromContext(ctx), RecordedAt: recordedAt.UTC().Truncate(time.Millisecond),
	}
}

type ErasureLedger struct {
	store *AnchorStore
}

func NewErasureLedger(store *AnchorStore) *ErasureLedger {
	return &ErasureLedger{store: store}
}

func validateErasureLedgerConfig(config ErasureLedgerConfig) error {
	if config.Timeout < time.Second || config.Timeout > time.Minute {
		return errors.New("audit erasure ledger timeout must be between one second and one minute")
	}
	return nil
}

func (ledger *ErasureLedger) Check(ctx context.Context) error {
	if ledger == nil || ledger.store == nil {
		return ErrErasureLedgerDisabled
	}
	if err := ledger.store.ensureBucket(ctx); err != nil {
		return erasureLedgerStoreError(err)
	}
	return nil
}

func (ledger *ErasureLedger) Put(ctx context.Context, obligation ErasureObligation) error {
	if ledger == nil || ledger.store == nil {
		return ErrErasureLedgerDisabled
	}
	if err := validateErasureObligation(obligation); err != nil {
		return err
	}
	if err := ledger.Check(ctx); err != nil {
		return err
	}
	key := erasureObligationKey(obligation.PrincipalID, obligation.LifecycleVersion)
	if _, exists, err := ledger.findExisting(ctx, key); err != nil {
		return err
	} else if exists {
		return nil
	}
	payload, err := json.Marshal(obligation)
	if err != nil {
		return fmt.Errorf("encode erasure obligation: %w", err)
	}
	digest := sha256.Sum256(payload)
	retainUntil := time.Now().UTC().Add(ledger.store.retention).Truncate(time.Second).Add(time.Second)
	opts := minio.PutObjectOptions{
		ContentType: "application/json", Mode: minio.Compliance, RetainUntilDate: retainUntil,
	}
	opts.SetMatchETagExcept("*")
	upload, putErr := ledger.store.client.PutObject(
		ctx, ledger.store.bucket, key, bytes.NewReader(payload), int64(len(payload)), opts,
	)
	if putErr != nil {
		if _, exists, findErr := ledger.findExisting(ctx, key); findErr != nil {
			return findErr
		} else if exists {
			return nil
		}
		return erasureLedgerStoreError(fmt.Errorf("put erasure obligation: %w", putErr))
	}
	if upload.VersionID == "" || upload.VersionID == "null" {
		return ErrErasureLedgerIntegrity
	}
	_, err = ledger.verify(ctx, key, upload.VersionID, hex.EncodeToString(digest[:]), retainUntil)
	return err
}

// findExisting searches object versions so a delete marker cannot make a
// retry publish a second obligation for the same lifecycle version.
func (ledger *ErasureLedger) findExisting(
	ctx context.Context,
	key string,
) (VerifiedErasureObligation, bool, error) {
	var result VerifiedErasureObligation
	found := false
	for object := range ledger.store.client.ListObjects(ctx, ledger.store.bucket, minio.ListObjectsOptions{
		Prefix: key, Recursive: true, WithVersions: true,
	}) {
		if object.Err != nil {
			return VerifiedErasureObligation{}, false,
				erasureLedgerStoreError(fmt.Errorf("find erasure obligation: %w", object.Err))
		}
		if object.Key != key || object.IsDeleteMarker {
			continue
		}
		if found {
			return VerifiedErasureObligation{}, false, ErrErasureLedgerIntegrity
		}
		verified, err := ledger.verify(ctx, key, object.VersionID, "", time.Time{})
		if err != nil {
			return VerifiedErasureObligation{}, false, err
		}
		result, found = verified, true
	}
	return result, found, nil
}

func (ledger *ErasureLedger) List(ctx context.Context) ([]VerifiedErasureObligation, error) {
	if ledger == nil || ledger.store == nil {
		return nil, ErrErasureLedgerDisabled
	}
	result := make([]VerifiedErasureObligation, 0)
	seen := make(map[string]struct{})
	for object := range ledger.store.client.ListObjects(ctx, ledger.store.bucket, minio.ListObjectsOptions{
		Prefix: erasureObligationPrefix + "/", Recursive: true, WithVersions: true,
	}) {
		if object.Err != nil {
			return nil, erasureLedgerStoreError(fmt.Errorf("list erasure obligations: %w", object.Err))
		}
		if object.IsDeleteMarker {
			continue
		}
		if _, exists := seen[object.Key]; exists {
			return nil, ErrErasureLedgerIntegrity
		}
		seen[object.Key] = struct{}{}
		verified, err := ledger.verify(ctx, object.Key, object.VersionID, "", time.Time{})
		if err != nil {
			return nil, err
		}
		result = append(result, verified)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PrincipalID == result[j].PrincipalID {
			return result[i].LifecycleVersion < result[j].LifecycleVersion
		}
		return result[i].PrincipalID < result[j].PrincipalID
	})
	return result, nil
}

// ListErasureObligations constructs the recovery-side reader from the same
// configuration used by the running audit module. It deliberately does not
// initialize the application or connect to its database.
func ListErasureObligations(ctx context.Context, config Config) ([]VerifiedErasureObligation, error) {
	if !config.ErasureLedger.Enabled {
		return nil, ErrErasureLedgerDisabled
	}
	if err := validateErasureLedgerConfig(config.ErasureLedger); err != nil {
		return nil, err
	}
	store, err := NewAnchorStore(config.Anchor)
	if err != nil {
		return nil, fmt.Errorf("audit erasure ledger configuration: %w", err)
	}
	listCtx, cancel := context.WithTimeout(ctx, config.ErasureLedger.Timeout)
	defer cancel()
	return NewErasureLedger(store).List(listCtx)
}

func (ledger *ErasureLedger) verify(
	ctx context.Context,
	key, versionID, expectedDigest string,
	minimumRetention time.Time,
) (VerifiedErasureObligation, error) {
	if versionID == "" || versionID == "null" {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	info, err := ledger.store.client.StatObject(ctx, ledger.store.bucket, key, minio.StatObjectOptions{VersionID: versionID})
	if err != nil {
		return VerifiedErasureObligation{}, erasureLedgerStoreError(err)
	}
	if info.VersionID != versionID || info.Size <= 0 || info.Size > maxErasureObligationSize {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	object, err := ledger.store.client.GetObject(ctx, ledger.store.bucket, key, minio.GetObjectOptions{VersionID: versionID})
	if err != nil {
		return VerifiedErasureObligation{}, erasureLedgerStoreError(err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(object, maxErasureObligationSize+1))
	closeErr := object.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return VerifiedErasureObligation{}, erasureLedgerStoreError(err)
	}
	if int64(len(payload)) != info.Size {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	digest := sha256.Sum256(payload)
	digestText := hex.EncodeToString(digest[:])
	if expectedDigest != "" && digestText != expectedDigest {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	var obligation ErasureObligation
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&obligation); err != nil {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	if err := validateErasureObligation(obligation); err != nil || erasureObligationKey(obligation.PrincipalID, obligation.LifecycleVersion) != key {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	mode, until, err := ledger.store.client.GetObjectRetention(ctx, ledger.store.bucket, key, versionID)
	if err != nil {
		return VerifiedErasureObligation{}, erasureLedgerStoreError(err)
	}
	if mode == nil || *mode != minio.Compliance || until == nil || until.Before(minimumRetention) {
		return VerifiedErasureObligation{}, ErrErasureLedgerIntegrity
	}
	return VerifiedErasureObligation{
		ErasureObligation: obligation, ObjectKey: key, VersionID: versionID,
		SHA256: digestText, RetainUntil: until.UTC(),
	}, nil
}

func validateErasureObligation(obligation ErasureObligation) error {
	if obligation.Schema != erasureObligationSchema || obligation.PrincipalID.Zero() || obligation.LifecycleVersion < 1 ||
		obligation.RecordedAt.IsZero() || obligation.RecordedAt.Location() != time.UTC {
		return ErrErasureLedgerInvalid
	}
	return nil
}

func erasureObligationKey(principalID guid.ID, lifecycleVersion int64) string {
	return erasureObligationPrefix + "/" + principalID.String() + "/" + strconv.FormatInt(lifecycleVersion, 10) + ".json"
}

func erasureLedgerStoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrErasureLedgerUnavailable, err.Error())
}
