package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// S3 stores immutable service-generated keys. The bucket must be provisioned
// by the operator; constructing a driver never changes bucket policy.
type S3 struct {
	core   *minio.Core
	bucket string
	prefix string
	sse    encrypt.ServerSide
}

func NewS3(config DriverConfig) (*S3, error) {
	endpoint, err := url.Parse(config["endpoint"])
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("files: S3 endpoint must be an HTTP(S) origin")
	}
	if err := s3utils.CheckValidBucketName(config["bucket"]); err != nil {
		return nil, errors.New("files: invalid S3 bucket")
	}
	prefix := config["prefix"]
	if prefix != "" && !validKey(prefix) {
		return nil, ErrInvalidKey
	}
	access, secret := config["access_key"], config["secret_key"]
	for _, credential := range []struct {
		file  string
		value *string
	}{{config["access_key_file"], &access}, {config["secret_key_file"], &secret}} {
		if credential.file != "" {
			if *credential.value != "" {
				return nil, errors.New("files: specify a credential or its file, not both")
			}
			data, err := os.ReadFile(credential.file)
			if err != nil {
				return nil, errors.New("files: cannot read S3 credential file")
			}
			*credential.value = strings.TrimSpace(string(data))
		}
	}
	if access == "" || secret == "" {
		return nil, errors.New("files: S3 credentials are required")
	}
	var sse encrypt.ServerSide
	switch config["sse"] {
	case "", "none":
	case "AES256":
		sse = encrypt.NewSSE()
	case "aws:kms":
		if config["sse_kms_key_id"] == "" {
			return nil, errors.New("files: S3 KMS key ID is required")
		}
		sse, err = encrypt.NewSSEKMS(config["sse_kms_key_id"], nil)
		if err != nil {
			return nil, errors.New("files: invalid S3 KMS configuration")
		}
	default:
		return nil, errors.New("files: invalid S3 encryption mode")
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("files: default HTTP transport is not configurable")
	}
	transport := defaultTransport.Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	core, err := minio.NewCore(endpoint.Host, &minio.Options{Secure: endpoint.Scheme == "https", Region: config["region"], Creds: credentials.NewStaticV4(access, secret, ""), Transport: transport, MaxRetries: 3, TrailingHeaders: true})
	if err != nil {
		return nil, errors.New("files: invalid S3 configuration")
	}
	return &S3{core: core, bucket: config["bucket"], prefix: prefix, sse: sse}, nil
}

func (*S3) Capabilities() Capabilities {
	return Capabilities{RangeRead: true, Multipart: true, ServerCopy: true}
}
func (s *S3) key(key string) (string, error) {
	if !validKey(key) {
		return "", ErrInvalidKey
	}
	if s.prefix != "" {
		return s.prefix + "/" + key, nil
	}
	return key, nil
}
func (s *S3) uri(key string) string { return "s3://" + s.bucket + "/" + key }
func s3Error(err error) error {
	if err == nil {
		return nil
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject":
		return ErrNotFound
	case "PreconditionFailed", "ConditionalRequestConflict":
		return ErrExists
	case "IncompleteBody", "EntityTooSmall", "XAmzContentSHA256Mismatch", "BadDigest":
		return ErrLength
	}
	return err
}
func (s *S3) options() minio.PutObjectOptions {
	options := minio.PutObjectOptions{DisableMultipart: true, ServerSideEncryption: s.sse, Checksum: minio.ChecksumSHA256}
	options.SetMatchETagExcept("*")
	return options
}

// exactReader refuses overlong input before its last byte can be published.
// It retains one byte, never the whole object. The SDK also rejects short input.
type exactReader struct {
	source    io.Reader
	remaining int64
	checked   bool
}

func (r *exactReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		if r.checked {
			return 0, io.EOF
		}
		r.checked = true
		var extra [1]byte
		n, err := io.ReadFull(r.source, extra[:])
		if n != 0 {
			return 0, ErrLength
		}
		if err == io.EOF {
			return 0, io.EOF
		}
		return 0, err
	}
	// Hold back the final byte until EOF is confirmed, so a length mismatch
	// cannot successfully commit a truncated single PUT or part.
	if r.remaining == 1 {
		var last [2]byte
		n, err := io.ReadFull(r.source, last[:])
		if n != 1 || err != io.ErrUnexpectedEOF {
			return 0, ErrLength
		}
		p[0] = last[0]
		r.remaining = 0
		r.checked = true
		return 1, nil
	}
	if int64(len(p)) >= r.remaining {
		p = p[:r.remaining-1]
	}
	n, err := r.source.Read(p)
	r.remaining -= int64(n)
	if err == io.EOF {
		return n, ErrLength
	}
	return n, err
}

func (s *S3) Put(ctx context.Context, key string, reader io.Reader, size int64) (string, error) {
	object, err := s.key(key)
	if err != nil {
		return "", err
	}
	if reader == nil || size < 0 {
		return "", ErrLength
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	exact := &exactReader{source: contextReader{ctx, reader}, remaining: size}
	if size == 0 {
		var b [1]byte
		n, e := io.ReadFull(exact.source, b[:])
		if n != 0 || e != io.EOF {
			return "", ErrLength
		}
	}
	_, err = s.core.Client.PutObject(ctx, s.bucket, object, exact, size, s.options())
	if err != nil {
		return "", s3Error(err)
	}
	return s.uri(object), nil
}

type s3Read struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r s3Read) Close() error { defer r.cancel(); return r.ReadCloser.Close() }
func (s *S3) open(ctx context.Context, key string, options minio.GetObjectOptions) (io.ReadCloser, error) {
	object, err := s.key(key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	reader, _, _, err := s.core.GetObject(ctx, s.bucket, object, options)
	if err != nil {
		cancel()
		return nil, s3Error(err)
	}
	return s3Read{reader, cancel}, nil
}
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.open(ctx, key, minio.GetObjectOptions{})
}
func (s *S3) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	object, err := s.key(key)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || length > 0 && offset > int64(^uint64(0)>>1)-(length-1) {
		return nil, ErrRange
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	info, err := s.core.StatObject(ctx, s.bucket, object, minio.StatObjectOptions{})
	if err != nil {
		cancel()
		return nil, s3Error(err)
	}
	if offset > info.Size || length > info.Size-offset {
		cancel()
		return nil, ErrRange
	}
	if length == 0 {
		cancel()
		return io.NopCloser(strings.NewReader("")), nil
	}
	options := minio.GetObjectOptions{}
	if err := options.SetRange(offset, offset+length-1); err != nil {
		cancel()
		return nil, ErrRange
	}
	if err := options.SetMatchETag(info.ETag); err != nil {
		cancel()
		return nil, err
	}
	reader, err := s.open(ctx, key, options)
	if minio.ToErrorResponse(err).Code == "InvalidRange" {
		cancel()
		return nil, ErrRange
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return s3Read{reader, cancel}, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	object, err := s.key(key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return s3Error(s.core.RemoveObject(ctx, s.bucket, object, minio.RemoveObjectOptions{}))
}
func (s *S3) Begin(ctx context.Context, key string) (string, error) {
	object, err := s.key(key)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err = s.core.StatObject(ctx, s.bucket, object, minio.StatObjectOptions{})
	if err == nil {
		return "", ErrExists
	}
	if !errors.Is(s3Error(err), ErrNotFound) {
		return "", err
	}
	id, err := s.core.NewMultipartUpload(ctx, s.bucket, object, minio.PutObjectOptions{ServerSideEncryption: s.sse})
	return id, s3Error(err)
}
func (s *S3) WritePart(ctx context.Context, key, handle string, index int, reader io.Reader, size int64) (PartReceipt, error) {
	object, err := s.key(key)
	if err != nil {
		return PartReceipt{}, err
	}
	if handle == "" || index < 1 || index > 10000 || reader == nil || size <= 0 || size > 5<<30 {
		return PartReceipt{}, ErrLength
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	part, err := s.core.PutObjectPart(ctx, s.bucket, object, handle, index, &exactReader{source: contextReader{ctx, reader}, remaining: size}, size, minio.PutObjectPartOptions{SSE: s.sse})
	return PartReceipt{Index: index, Length: part.Size, Token: part.ETag}, s3Error(err)
}
func (s *S3) InspectParts(ctx context.Context, key, handle string) ([]PartReceipt, error) {
	object, err := s.key(key)
	if err != nil {
		return nil, err
	}
	if handle == "" {
		return nil, ErrInvalidKey
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	parts := []PartReceipt{}
	marker := 0
	var offset int64
	for {
		page, err := s.core.ListObjectParts(ctx, s.bucket, object, handle, marker, 1000)
		if err != nil {
			return nil, s3Error(err)
		}
		for _, part := range page.ObjectParts {
			parts = append(parts, PartReceipt{Index: part.PartNumber, Offset: offset, Length: part.Size, Token: strings.Trim(part.ETag, "\"")})
			offset += part.Size
		}
		if !page.IsTruncated {
			return parts, nil
		}
		if page.NextPartNumberMarker <= marker {
			return nil, ErrUnsupported
		}
		marker = page.NextPartNumberMarker
	}
}
func (s *S3) Complete(ctx context.Context, key, handle string, receipts []PartReceipt) (string, error) {
	object, err := s.key(key)
	if err != nil {
		return "", err
	}
	if handle == "" || len(receipts) == 0 || len(receipts) > 10000 {
		return "", ErrLength
	}
	actual, err := s.InspectParts(ctx, key, handle)
	if err != nil {
		return "", err
	}
	if len(actual) != len(receipts) {
		return "", ErrLength
	}
	parts := make([]minio.CompletePart, len(receipts))
	for i, p := range receipts {
		if p.Index != i+1 || actual[i].Index != p.Index || actual[i].Token != p.Token || actual[i].Length != p.Length || i < len(receipts)-1 && p.Length < 5<<20 {
			return "", ErrLength
		}
		parts[i] = minio.CompletePart{PartNumber: p.Index, ETag: p.Token}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err = s.core.CompleteMultipartUpload(ctx, s.bucket, object, handle, parts, s.options())
	if err != nil {
		return "", s3Error(err)
	}
	return s.uri(object), nil
}
func (s *S3) Abort(ctx context.Context, key, handle string) error {
	object, err := s.key(key)
	if err != nil {
		return err
	}
	if handle == "" {
		return ErrInvalidKey
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err = s.core.AbortMultipartUpload(ctx, s.bucket, object, handle)
	if minio.ToErrorResponse(err).Code == "NoSuchUpload" {
		return nil
	}
	return s3Error(err)
}
func (s *S3) Copy(ctx context.Context, source, destination string) (string, error) {
	src, err := s.key(source)
	if err != nil {
		return "", err
	}
	dst, err := s.key(destination)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	info, err := s.core.StatObject(ctx, s.bucket, src, minio.StatObjectOptions{})
	if err != nil {
		return "", s3Error(err)
	}
	// CopyObject ignores destination preconditions on some S3 servers. A
	// multipart-copy followed by conditional completion fences publication.
	if info.Size == 0 {
		return s.Put(ctx, destination, strings.NewReader(""), 0)
	}
	handle, err := s.core.NewMultipartUpload(ctx, s.bucket, dst, minio.PutObjectOptions{ServerSideEncryption: s.sse})
	if err != nil {
		return "", s3Error(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.core.AbortMultipartUpload(cleanup, s.bucket, dst, handle)
	}()
	parts := []minio.CompletePart{}
	const partSize int64 = 512 << 20
	for offset := int64(0); offset < info.Size; offset += partSize {
		length := min(partSize, info.Size-offset)
		part, err := s.core.CopyObjectPart(ctx, s.bucket, src, s.bucket, dst, handle, len(parts)+1, offset, length, map[string]string{"x-amz-copy-source-if-match": "\"" + info.ETag + "\""})
		if err != nil {
			return "", s3Error(err)
		}
		parts = append(parts, part)
	}
	_, err = s.core.CompleteMultipartUpload(ctx, s.bucket, dst, handle, parts, s.options())
	if err != nil {
		return "", s3Error(err)
	}
	return s.uri(dst), nil
}
func (s *S3) ReclaimWrite(ctx context.Context, key string) error {
	object, err := s.key(key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	keyMarker, uploadMarker := "", ""
	for {
		page, err := s.core.ListMultipartUploads(ctx, s.bucket, object, keyMarker, uploadMarker, "", 1000)
		if err != nil {
			return err
		}
		sort.Slice(page.Uploads, func(i, j int) bool { return page.Uploads[i].UploadID < page.Uploads[j].UploadID })
		for _, upload := range page.Uploads {
			if upload.Key == object {
				if err := s.Abort(ctx, key, upload.UploadID); err != nil {
					return err
				}
			}
		}
		if !page.IsTruncated {
			break
		}
		if page.NextKeyMarker == keyMarker && page.NextUploadIDMarker == uploadMarker {
			return fmt.Errorf("%w: nonadvancing upload listing", ErrUnsupported)
		}
		keyMarker, uploadMarker = page.NextKeyMarker, page.NextUploadIDMarker
	}
	return s.Delete(ctx, key)
}

var (
	_ Driver                   = (*S3)(nil)
	_ RangeDriver              = (*S3)(nil)
	_ MultipartDriver          = (*S3)(nil)
	_ CopyDriver               = (*S3)(nil)
	_ IncompleteWriteReclaimer = (*S3)(nil)
)
