package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
)

// Local streams into the configured final local backend, never an API spool.
type Local struct{ directory string }

func NewLocal(directory string) (*Local, error) {
	if directory == "" {
		return nil, errors.New("files: local root is required")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	return &Local{directory: abs}, nil
}

func (*Local) Capabilities() Capabilities { return Capabilities{RangeRead: true} }

func (s *Local) root() (*os.Root, error) {
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(s.directory)
}

func (s *Local) Put(ctx context.Context, key string, reader io.Reader, size int64) (string, error) {
	if !validKey(key) {
		return "", ErrInvalidKey
	}
	if size < 0 || reader == nil {
		return "", ErrLength
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := s.root()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(key), 0700); err != nil {
		return "", err
	}
	// The pending inode is in the final backend, not an API-server spool. Link
	// publishes the same inode atomically without replacing an existing object.
	pendingKey := localPendingKey(key)
	file, err := root.OpenFile(pendingKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", localError(err)
	}
	committed := false
	linked := false
	defer func() {
		file.Close()
		_ = root.Remove(pendingKey)
		if linked && !committed {
			_ = root.Remove(key)
		}
	}()
	written, err := io.CopyN(file, contextReader{ctx, reader}, size)
	if err != nil || written != size {
		if err == nil || errors.Is(err, io.EOF) {
			err = ErrLength
		}
		return "", err
	}
	var extra [1]byte
	n, err := io.ReadFull(contextReader{ctx, reader}, extra[:])
	if n != 0 {
		return "", ErrLength
	}
	if !errors.Is(err, io.EOF) {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := root.Link(pendingKey, key); err != nil {
		return "", localError(err)
	}
	linked = true
	if err := root.Remove(pendingKey); err != nil {
		return "", err
	}
	parent, err := root.Open(path.Dir(key))
	if err != nil {
		return "", err
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	committed = true
	location := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(s.directory, filepath.FromSlash(key)))}
	return location.String(), nil
}

func (s *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !validKey(key) {
		return nil, ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(key)
	if err != nil {
		return nil, localError(err)
	}
	return readCloser{contextReader{ctx, file}, file}, nil
}

func (s *Local) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if !validKey(key) {
		return nil, ErrInvalidKey
	}
	if offset < 0 || length < 0 {
		return nil, ErrRange
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(key)
	if err != nil {
		return nil, localError(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || offset > info.Size() || length > info.Size()-offset {
		file.Close()
		return nil, ErrRange
	}
	return readCloser{contextReader{ctx, io.NewSectionReader(file, offset, length)}, file}, nil
}

func (s *Local) Delete(ctx context.Context, key string) error {
	if !validKey(key) {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := s.root()
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncLocalDirectory(root, path.Dir(key))
}

func localPendingKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return path.Join(path.Dir(key), ".pending-"+hex.EncodeToString(sum[:]))
}

// ReclaimWrite removes the publication source before the final link. A writer
// already streaming to an unlinked inode cannot publish it after this operation.
// Durable abandoned intents still handle a writer starting after reclamation.
func (s *Local) ReclaimWrite(ctx context.Context, key string) error {
	if !validKey(key) {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := s.root()
	if err != nil {
		return err
	}
	defer root.Close()
	removed := false
	if err := root.Remove(localPendingKey(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil {
		removed = true
	}
	if err := root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil {
		removed = true
	}
	if removed {
		return syncLocalDirectory(root, path.Dir(key))
	}
	return nil
}

func syncLocalDirectory(root *os.Root, key string) error {
	directory, err := root.Open(key)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func localError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrNotFound, err)
	}
	if errors.Is(err, os.ErrExist) {
		return errors.Join(ErrExists, err)
	}
	return err
}
