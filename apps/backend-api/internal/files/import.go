package files

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// LegacyImporter migrates a service-owned key without exposing source paths
// to the consumer. It never deletes the original file.
type LegacyImporter func(context.Context, string) error

func importLegacy(ctx context.Context, driver Driver, roots map[string]string, key string) error {
	if !validKey(key) {
		return ErrInvalidKey
	}
	reader, err := driver.Open(ctx, key)
	exists := err == nil
	if exists {
		defer func() { _ = reader.Close() }()
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || roots[parts[0]] == "" {
		if exists {
			return nil
		}
		return os.ErrNotExist
	}
	root, err := os.OpenRoot(roots[parts[0]])
	if err != nil {
		if exists && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	source, err := root.Open(parts[1])
	if err != nil {
		if exists && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidKey
	}
	sums := NewChecksums()
	if exists {
		if _, err := io.Copy(sums, contextReader{ctx, source}); err != nil {
			return err
		}
		actual := NewChecksums()
		if _, err := io.Copy(actual, contextReader{ctx, reader}); err != nil {
			return err
		}
		if actual.Offset() != sums.Offset() || actual.Digests() != sums.Digests() {
			return ErrLength
		}
		return nil
	}
	_, err = driver.Put(ctx, key, io.TeeReader(contextReader{ctx, source}, sums), info.Size())
	if err != nil {
		return err
	}
	// Verify the copied bytes through the destination, streaming both reads.
	destination, err := driver.Open(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = destination.Close() }()
	actual := NewChecksums()
	if _, err := io.Copy(actual, contextReader{ctx, destination}); err != nil {
		return err
	}
	if actual.Offset() != info.Size() || actual.Digests() != sums.Digests() {
		return ErrLength
	}
	return nil
}
