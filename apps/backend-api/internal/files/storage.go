// Package files owns file storage and virtual filesystem capabilities.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

var (
	ErrInvalidKey  = errors.New("files: invalid storage key")
	ErrUnsupported = errors.New("files: unsupported storage capability")
	ErrLength      = errors.New("files: content length mismatch")
	ErrRange       = errors.New("files: invalid byte range")
	ErrNotFound    = errors.New("files: object not found")
	ErrExists      = errors.New("files: immutable object already exists")
)

// Capabilities describes operations a driver can perform without emulation.
type Capabilities struct {
	RangeRead  bool
	Multipart  bool
	ServerCopy bool
}

// Driver streams immutable objects into their final storage backend.
// Keys are service-generated; they must never be derived from display names.
type Driver interface {
	Capabilities() Capabilities
	Put(context.Context, string, io.Reader, int64) (string, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// RangeDriver reads exactly one byte range without fetching the whole object.
type RangeDriver interface {
	OpenRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}

// IncompleteWriteReclaimer releases backend-local pending bytes for a key whose
// durable intent has been abandoned. It must also remove any published object.
// This does not assert remote network fencing; uncertain intents remain tracked.
type IncompleteWriteReclaimer interface {
	ReclaimWrite(context.Context, string) error
}

// CopyDriver performs an immutable copy within the same backend.
type CopyDriver interface {
	Copy(context.Context, string, string) (string, error)
}

// PartReceipt is a confirmed storage write, not a whole-file checksum.
type PartReceipt struct {
	Index  int
	Offset int64
	Length int64
	Token  string
}

// MultipartDriver exposes native resumable storage operations. Handles and
// receipt tokens are opaque and must remain server-owned.
type MultipartDriver interface {
	Begin(context.Context, string) (string, error)
	WritePart(context.Context, string, string, int, io.Reader, int64) (PartReceipt, error)
	InspectParts(context.Context, string, string) ([]PartReceipt, error)
	Complete(context.Context, string, string, []PartReceipt) (string, error)
	Abort(context.Context, string, string) error
}

// DriverConfig contains backend-specific configuration, not a shared S3 schema.
type DriverConfig map[string]string

// DriverFactory validates configuration and constructs one storage driver.
type DriverFactory func(DriverConfig) (Driver, error)

// Registry maps explicit backend names to constructors. Registration is local
// to the application, not a package-global mutable plugin collection.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]DriverFactory
}

func NewRegistry() *Registry { return &Registry{factories: make(map[string]DriverFactory)} }

func (r *Registry) Register(name string, factory DriverFactory) error {
	if name == "" || name != strings.ToLower(name) || factory == nil {
		return errors.New("files: invalid driver registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("files: driver %q already registered", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *Registry) Build(name string, config DriverConfig) (Driver, error) {
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("%w: driver %q", ErrUnsupported, name)
	}
	driver, err := factory(config)
	if err == nil && driver == nil {
		return nil, errors.New("files: driver factory returned nil")
	}
	if err == nil {
		caps := driver.Capabilities()
		if _, ok := driver.(RangeDriver); caps.RangeRead && !ok {
			return nil, errors.New("files: range capability has no implementation")
		}
		if _, ok := driver.(MultipartDriver); caps.Multipart && !ok {
			return nil, errors.New("files: multipart capability has no implementation")
		}
		if _, ok := driver.(CopyDriver); caps.ServerCopy && !ok {
			return nil, errors.New("files: copy capability has no implementation")
		}
	}
	return driver, err
}

func validKey(key string) bool {
	if key == "" || len(key) > 512 {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
