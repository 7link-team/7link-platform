package goosex

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"text/template"
	"time"
)

// prefixPattern constrains what may be interpolated into DDL. A table prefix
// names a SQL identifier, and identifiers cannot be bound as parameters, so the
// value is concatenated into CREATE TABLE and CREATE INDEX statements. It is
// therefore validated against an allowlist rather than escaped: anything but
// lower-case ASCII, digits and underscore is rejected outright.
var prefixPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,20}$`)

// ValidatePrefix reports whether prefix may be interpolated into DDL. The empty
// prefix is valid and is the default, so an existing deployment keeps its table
// names.
func ValidatePrefix(prefix string) error {
	if prefix == "" || prefixPattern.MatchString(prefix) {
		return nil
	}
	return fmt.Errorf("goose: invalid table prefix %q: must match %s", prefix, prefixPattern)
}

// templateData is what a migration template can reference. Keeping it a named
// struct rather than a map means an unknown field is a template error rather
// than an empty string silently creating an unprefixed table.
type templateData struct {
	Prefix string
}

// templateFS renders each migration through text/template before goose parses
// it, so a migration can name its tables and indexes as {{.Prefix}}dlocks.
// Directories and any file that fails to render are passed through unchanged is
// not an option: a render failure must surface, or a migration would silently
// create the wrong objects.
type templateFS struct {
	fsys fs.FS
	data templateData
}

// ReadDir forwards to the wrapped filesystem. goose discovers migrations with
// fs.Glob, which needs directory listing but not file contents.
func (t templateFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(t.fsys, name)
}

func (t templateFS) Open(name string) (fs.File, error) {
	info, err := fs.Stat(t.fsys, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return t.fsys.Open(name)
	}

	raw, err := fs.ReadFile(t.fsys, name)
	if err != nil {
		return nil, err
	}
	parsed, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("goose: parse migration template %s: %w", name, err)
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, t.data); err != nil {
		return nil, fmt.Errorf("goose: render migration %s: %w", name, err)
	}
	return &renderedFile{
		Reader: bytes.NewReader(rendered.Bytes()),
		info:   renderedInfo{name: info.Name(), size: int64(rendered.Len()), mod: info.ModTime()},
	}, nil
}

// renderedFile is the in-memory result of rendering one migration.
type renderedFile struct {
	*bytes.Reader
	info renderedInfo
}

func (f *renderedFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *renderedFile) Close() error               { return nil }

// renderedInfo reports the rendered size rather than the template's, so a
// reader sizing a buffer from Stat gets the bytes it will actually read.
type renderedInfo struct {
	name string
	size int64
	mod  time.Time
}

func (i renderedInfo) Name() string       { return i.name }
func (i renderedInfo) Size() int64        { return i.size }
func (i renderedInfo) Mode() fs.FileMode  { return 0o444 }
func (i renderedInfo) ModTime() time.Time { return i.mod }
func (i renderedInfo) IsDir() bool        { return false }
func (i renderedInfo) Sys() any           { return nil }

var (
	_ fs.FS         = templateFS{}
	_ fs.ReadDirFS  = templateFS{}
	_ fs.File       = (*renderedFile)(nil)
	_ io.ReadSeeker = (*renderedFile)(nil)
)
