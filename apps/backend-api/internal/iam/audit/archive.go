package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/minio/minio-go/v7"
)

const (
	archivePrefix        = "audit/archives"
	archiveSinglePutSize = 16 << 20
)

var (
	ErrArchiveInvalid     = errors.New("invalid audit archive")
	ErrArchiveUnavailable = errors.New("audit archive store unavailable")
	ErrArchiveIntegrity   = errors.New("audit archive object integrity or retention check failed")
)

// ArchiveObject identifies the verified, immutable version, not the mutable
// latest-version alias at Key. SHA256 covers the entire NDJSON export, including
// its manifest and completion record.
type ArchiveObject struct {
	Bucket      string    `json:"bucket"`
	Key         string    `json:"key"`
	VersionID   string    `json:"version_id"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	RetainUntil time.Time `json:"retain_until"`
}

// writeArchiveSnapshot executes the object-storage half of a durable archive
// job. Its snapshot, ID and requested retention deadline must survive retries.
// The export is streamed twice to avoid temporary files containing audit PII:
// first to freeze its digest/length, then to upload the exact same bytes.
func (s *Service) writeArchiveSnapshot(ctx context.Context, store *AnchorStore, snapshot ExportSnapshot, id guid.ID, retainUntil time.Time) (ArchiveObject, error) {
	if store == nil || id <= 0 || snapshot.Filter.TenantID < 0 || snapshot.Integrity.HeadSequence <= 0 ||
		!snapshot.Integrity.Valid || snapshot.Filter.TenantID != snapshot.Integrity.TenantID ||
		snapshot.GeneratedAt.IsZero() || retainUntil.IsZero() || snapshot.Filter != (Filter{TenantID: snapshot.Filter.TenantID}) {
		return ArchiveObject{}, ErrArchiveInvalid
	}
	repo, err := s.pinnedRepository()
	if err != nil {
		return ArchiveObject{}, err
	}
	integrity, err := s.verifyPrefix(ctx, repo, snapshot.Filter.TenantID, snapshot.Integrity.HeadSequence)
	if err != nil {
		return ArchiveObject{}, err
	}
	if !integrity.Valid || integrity.HeadHash != snapshot.Integrity.HeadHash || integrity.VerifiedEvents != snapshot.Integrity.VerifiedEvents {
		return ArchiveObject{}, ErrIntegrity
	}
	if snapshot.AfterSequence < 0 || snapshot.AfterSequence >= snapshot.Integrity.HeadSequence {
		return ArchiveObject{}, ErrArchiveInvalid
	}
	if snapshot.AfterSequence > 0 {
		rows, readErr := repo.readExport(ctx, Filter{TenantID: snapshot.Filter.TenantID}, snapshot.AfterSequence-1, snapshot.AfterSequence, 1)
		if readErr != nil {
			return ArchiveObject{}, readErr
		}
		if len(rows) != 1 || normalizeStoredHash(rows[0].EventHash) != snapshot.PreviousHash {
			return ArchiveObject{}, ErrIntegrity
		}
	} else if snapshot.PreviousHash != "" {
		return ArchiveObject{}, ErrArchiveInvalid
	}
	write := func(writer io.Writer) error { return s.WriteExport(ctx, snapshot, writer) }
	digest := sha256.New()
	count := &archiveCountingWriter{writer: digest}
	if err := write(count); err != nil {
		return ArchiveObject{}, err
	}
	object := ArchiveObject{
		Bucket: store.bucket,
		Key:    archivePrefix + "/" + snapshot.Filter.TenantID.String() + "/" + id.String() + ".ndjson",
		SHA256: hex.EncodeToString(digest.Sum(nil)), Size: count.size,
		RetainUntil: retainUntil.UTC(),
	}
	return store.putArchive(ctx, object, write)
}

type archiveCountingWriter struct {
	writer io.Writer
	size   int64
}

func (w *archiveCountingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.size += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}

func archiveStoreError(err error) error {
	return fmt.Errorf("%w: %w", ErrArchiveUnavailable, err)
}

func (s *AnchorStore) putArchive(ctx context.Context, expected ArchiveObject, write func(io.Writer) error) (ArchiveObject, error) {
	if err := s.ensureBucket(ctx); err != nil {
		return ArchiveObject{}, archiveStoreError(err)
	}
	// A retry after a crash or a lost upload response reuses the existing version
	// only after independently validating its content and retention.
	info, err := s.client.StatObject(ctx, s.bucket, expected.Key, minio.StatObjectOptions{})
	if err == nil {
		expected.VersionID = info.VersionID
		return s.verifyArchive(ctx, expected)
	}
	if minio.ToErrorResponse(err).Code != minio.NoSuchKey {
		return ArchiveObject{}, archiveStoreError(err)
	}
	until := time.Now().UTC().Add(s.retention).Truncate(time.Second)
	if expected.RetainUntil.After(until) {
		until = expected.RetainUntil.UTC().Truncate(time.Second).Add(time.Second)
	}
	opts := minio.PutObjectOptions{
		ContentType: "application/x-ndjson", Mode: minio.Compliance, RetainUntilDate: until,
	}
	opts.SetMatchETagExcept("*")
	reader, writer := io.Pipe()
	stop := context.AfterFunc(ctx, func() {
		_ = writer.CloseWithError(ctx.Err())
		_ = reader.CloseWithError(ctx.Err())
	})
	defer stop()
	done := make(chan error, 1)
	go func() {
		digest := sha256.New()
		count := &archiveCountingWriter{writer: io.MultiWriter(writer, digest)}
		writeErr := write(count)
		if writeErr == nil && (count.size != expected.Size || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256) {
			writeErr = ErrArchiveIntegrity
		}
		_ = writer.CloseWithError(writeErr)
		done <- writeErr
	}()
	upload, uploadErr := s.uploadArchive(ctx, expected.Key, reader, expected.Size, opts)
	_ = reader.CloseWithError(uploadErr)
	writeErr := <-done
	if ctx.Err() != nil {
		return ArchiveObject{}, archiveStoreError(ctx.Err())
	}
	if uploadErr != nil {
		code := minio.ToErrorResponse(uploadErr).Code
		if code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
			info, statErr := s.client.StatObject(ctx, s.bucket, expected.Key, minio.StatObjectOptions{})
			if statErr != nil {
				return ArchiveObject{}, archiveStoreError(statErr)
			}
			expected.VersionID = info.VersionID
			return s.verifyArchive(ctx, expected)
		}
		return ArchiveObject{}, archiveStoreError(uploadErr)
	}
	if writeErr != nil {
		return ArchiveObject{}, writeErr
	}
	expected.VersionID = upload.VersionID
	return s.verifyArchive(ctx, expected)
}

func (s *AnchorStore) uploadArchive(ctx context.Context, key string, reader io.Reader, size int64, opts minio.PutObjectOptions) (info minio.UploadInfo, err error) {
	if size <= archiveSinglePutSize {
		opts.PartSize = archiveSinglePutSize
		return s.client.PutObject(ctx, s.bucket, key, reader, size, opts)
	}
	// minio-go v7.2.1 drops conditional headers in its high-level multipart
	// completion. Core preserves them, while streaming parts avoids PII staging.
	count, partSize, lastSize, err := minio.OptimalPartInfo(size, 0)
	if err != nil {
		return info, err
	}
	core := minio.Core{Client: s.client}
	uploadID, err := core.NewMultipartUpload(ctx, s.bucket, key, opts)
	if err != nil {
		return info, err
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			abortErr := core.AbortMultipartUpload(cleanup, s.bucket, key, uploadID)
			if abortErr != nil && minio.ToErrorResponse(abortErr).Code != "NoSuchUpload" {
				err = errors.Join(err, fmt.Errorf("abort archive multipart upload: %w", abortErr))
			}
		}
	}()
	parts := make([]minio.CompletePart, 0, count)
	for number := 1; number <= count; number++ {
		length := partSize
		if number == count {
			length = lastSize
		}
		partReader := &io.LimitedReader{R: reader, N: length}
		part, partErr := core.PutObjectPart(ctx, s.bucket, key, uploadID, number, partReader, length, minio.PutObjectPartOptions{})
		if partErr != nil {
			return info, partErr
		}
		if partReader.N != 0 {
			return info, io.ErrUnexpectedEOF
		}
		parts = append(parts, minio.CompletePart{ETag: part.ETag, PartNumber: number})
	}
	// Await the producer's digest/length check before publishing any version.
	var extra [1]byte
	n, readErr := io.ReadFull(reader, extra[:])
	if n != 0 {
		return info, ErrArchiveIntegrity
	}
	if readErr != io.EOF {
		return info, readErr
	}
	// Retention and content type belong to initiation, not completion.
	completion := minio.PutObjectOptions{}
	completion.SetMatchETagExcept("*")
	info, err = core.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, parts, completion)
	info.Size = size
	return info, err
}

func (s *AnchorStore) verifyArchive(ctx context.Context, expected ArchiveObject) (ArchiveObject, error) {
	if expected.Bucket != s.bucket || expected.VersionID == "" || expected.VersionID == "null" {
		return ArchiveObject{}, ErrArchiveIntegrity
	}
	info, err := s.client.StatObject(ctx, s.bucket, expected.Key, minio.StatObjectOptions{VersionID: expected.VersionID})
	if err != nil {
		return ArchiveObject{}, archiveStoreError(err)
	}
	if info.VersionID != expected.VersionID || info.Size != expected.Size {
		return ArchiveObject{}, ErrArchiveIntegrity
	}
	object, err := s.client.GetObject(ctx, s.bucket, expected.Key, minio.GetObjectOptions{VersionID: expected.VersionID})
	if err != nil {
		return ArchiveObject{}, archiveStoreError(err)
	}
	digest := sha256.New()
	size, readErr := io.Copy(digest, io.LimitReader(object, expected.Size+1))
	closeErr := object.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return ArchiveObject{}, archiveStoreError(err)
	}
	if size != expected.Size || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
		return ArchiveObject{}, ErrArchiveIntegrity
	}
	mode, until, err := s.client.GetObjectRetention(ctx, s.bucket, expected.Key, expected.VersionID)
	if err != nil {
		return ArchiveObject{}, archiveStoreError(err)
	}
	if mode == nil || *mode != minio.Compliance || until == nil || until.Before(expected.RetainUntil) {
		return ArchiveObject{}, ErrArchiveIntegrity
	}
	expected.RetainUntil = until.UTC()
	return expected, nil
}
