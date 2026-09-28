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
	return resolve(name, name+"_file", value, path, maxBytes, os.Stat, openFile)
}

// ResolveList returns an ordered list of secrets given either the values
// themselves or the files holding them. It exists for settings that are a list
// rather than a single secret -- the previous keys a key ring still opens with
// being the case it was written for.
//
// The two sources are mutually exclusive for the reason they are in Resolve,
// and for one more: a list assembled from both halves would have an order
// nobody could predict from reading the configuration, and the order of a key
// list decides which key is the one still in use.
//
// A blank path is skipped rather than refused. Deployment configuration grows
// placeholder list entries, and failing on one would teach an operator to
// delete the line instead of filling it in.
func ResolveList(name string, values, paths []string, maxBytes int64) ([]string, error) {
	if len(values) > 0 && len(paths) > 0 {
		return nil, fmt.Errorf("%s and %s_files are mutually exclusive", name, name)
	}
	resolved := make([]string, 0, len(values)+len(paths))
	resolved = append(resolved, values...)
	for index, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		// The position is in the label because that is what an operator needs
		// to find the entry again, and it is all that can safely be said: the
		// contents are the secret and the path may name it.
		value, err := resolve(
			name, fmt.Sprintf("%s_files[%d]", name, index+1),
			"", path, maxBytes, os.Stat, openFile,
		)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, value)
	}
	return resolved, nil
}

type secretFile interface {
	io.Reader
	io.Closer
	Stat() (fs.FileInfo, error)
}

func resolve(
	name string,
	fileName string,
	value string,
	path string,
	maxBytes int64,
	stat func(string) (fs.FileInfo, error),
	open func(string) (secretFile, error),
) (resolved string, err error) {
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s are mutually exclusive", name, fileName)
	}
	if path == "" {
		return value, nil
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	info, err := stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileName, err)
	}
	if err := validateRegularFile(fileName, path, maxBytes, info); err != nil {
		return "", err
	}
	f, err := open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", fileName, err)
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			resolved = ""
			err = fmt.Errorf("close %s: %w", fileName, closeErr)
		}
	}()
	info, err = f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", fileName, err)
	}
	// Recheck the opened descriptor so a path replacement between Stat and Open
	// cannot make us read a non-regular or oversized object.
	if err := validateRegularFile(fileName, path, maxBytes, info); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileName, err)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", fileName, maxBytes)
	}
	resolved = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if resolved == "" {
		return "", fmt.Errorf("%s is empty", fileName)
	}
	return resolved, nil
}

func validateRegularFile(fileName, path string, maxBytes int64, info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q is not a regular file", fileName, path)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", fileName, maxBytes)
	}
	return nil
}
