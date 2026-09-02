// Package secretx resolves inline and file-backed application secrets.
package secretx

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const DefaultMaxBytes int64 = 64 << 10

// Resolve returns an inline value or a value read from a regular file. The two
// sources are mutually exclusive so a stale secret file cannot be shadowed.
func Resolve(name, value, path string, maxBytes int64) (string, error) {
	return resolve(name, value, path, maxBytes, os.Stat, openFile)
}

type secretFile interface {
	io.Reader
	io.Closer
	Stat() (fs.FileInfo, error)
}

func resolve(
	name string,
	value string,
	path string,
	maxBytes int64,
	stat func(string) (fs.FileInfo, error),
	open func(string) (secretFile, error),
) (resolved string, err error) {
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s_file are mutually exclusive", name, name)
	}
	if path == "" {
		return value, nil
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	info, err := stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s_file: %w", name, err)
	}
	if err := validateRegularFile(name, path, maxBytes, info); err != nil {
		return "", err
	}
	f, err := open(path)
	if err != nil {
		return "", fmt.Errorf("open %s_file: %w", name, err)
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			resolved = ""
			err = fmt.Errorf("close %s_file: %w", name, closeErr)
		}
	}()
	info, err = f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s_file: %w", name, err)
	}
	// Recheck the opened descriptor so a path replacement between Stat and Open
	// cannot make us read a non-regular or oversized object.
	if err := validateRegularFile(name, path, maxBytes, info); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s_file: %w", name, err)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("%s_file exceeds %d bytes", name, maxBytes)
	}
	resolved = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if resolved == "" {
		return "", fmt.Errorf("%s_file is empty", name)
	}
	return resolved, nil
}

func validateRegularFile(name, path string, maxBytes int64, info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s_file %q is not a regular file", name, path)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("%s_file exceeds %d bytes", name, maxBytes)
	}
	return nil
}
