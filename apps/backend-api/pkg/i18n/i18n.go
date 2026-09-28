package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// I18n manages translations.
type I18n struct {
	mu                   sync.RWMutex
	locale               string
	fallback             string
	messages             map[string]map[string]string // locale -> key -> message
	registeredNamespaces map[string]struct{}
}

// New creates a new I18n instance.
func New(fallback string) *I18n {
	return &I18n{
		fallback:             fallback,
		locale:               fallback,
		messages:             make(map[string]map[string]string),
		registeredNamespaces: make(map[string]struct{}),
	}
}

// Load loads a locale file from the given directory.
func (i *I18n) Load(locale, dir string) error {
	path := filepath.Join(dir, locale+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read locale file: %w", err)
	}

	var msgs map[string]string
	if err := json.Unmarshal(data, &msgs); err != nil {
		return fmt.Errorf("parse locale file: %w", err)
	}

	i.mu.Lock()
	i.messages[locale] = msgs
	i.mu.Unlock()
	return nil
}

// LoadFS loads all *.json locale files from an fs.FS subtree (e.g. an embed.FS),
// making locale loading independent of the process working directory. dir uses
// forward slashes (io/fs convention), e.g. "i18n/locales".
func (i *I18n) LoadFS(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read locales fs dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		locale := strings.TrimSuffix(entry.Name(), ".json")
		data, err := fs.ReadFile(fsys, dir+"/"+entry.Name())
		if err != nil {
			return fmt.Errorf("read locale file %s: %w", entry.Name(), err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			return fmt.Errorf("parse locale file %s: %w", entry.Name(), err)
		}
		i.mu.Lock()
		i.messages[locale] = msgs
		i.mu.Unlock()
	}
	return nil
}

// SetLocale sets the current locale.
func (i *I18n) SetLocale(locale string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.locale = locale
}

// Locale returns the current locale.
func (i *I18n) Locale() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.locale
}

// T translates a key with optional formatting arguments using the current locale.
func (i *I18n) T(key string, args ...any) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.translate(i.locale, key, args...)
}

// TContext translates a key using the locale from context.
func (i *I18n) TContext(ctx context.Context, key string, args ...any) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	locale := LocaleFromContext(ctx)
	if locale == "" {
		locale = i.locale
	}
	return i.translate(locale, key, args...)
}

func (i *I18n) translate(locale, key string, args ...any) string {
	msg := i.lookup(locale, key)
	if msg == "" {
		msg = i.lookup(i.fallback, key)
	}
	if msg == "" {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// lookup finds a message for the given locale and key.
func (i *I18n) lookup(locale, key string) string {
	msgs, ok := i.messages[locale]
	if !ok {
		return ""
	}
	// Try exact match first
	if msg, ok := msgs[key]; ok {
		return msg
	}
	// Try lowercase match
	if msg, ok := msgs[strings.ToLower(key)]; ok {
		return msg
	}
	return ""
}

// Translations renders key in every supported locale. It serves labels that are
// stored rather than rendered per request, such as seeded menu rows, which must
// carry each locale's text.
func (i *I18n) Translations(key string) map[string]string {
	labels := make(map[string]string, len(Supported()))
	for _, locale := range Supported() {
		labels[locale.Code] = i.TContext(WithLocale(context.Background(), locale.Code), key)
	}
	return labels
}

// Has checks if a key exists in the current locale.
func (i *I18n) Has(key string) bool {
	return i.T(key) != key
}

// LoadDir loads all locale files from the given directory.
func (i *I18n) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read locales dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		locale := strings.TrimSuffix(entry.Name(), ".json")
		if err := i.Load(locale, dir); err != nil {
			return fmt.Errorf("load locale %s: %w", locale, err)
		}
	}
	return nil
}

// NewModuleI18n creates a pre-loaded I18n instance for a module.
// It logs a warning if the directory cannot be read but returns a usable instance.
func NewModuleI18n(fallback, dir string) *I18n {
	inst := New(fallback)
	if err := inst.LoadDir(dir); err != nil {
		// Non-fatal: fallback to key passthrough when locale files are missing.
		// This is common during unit tests that run without locale file paths.
		slog.Warn("module i18n locales unavailable", "dir", dir, "error", err)
	}
	return inst
}

// NewModuleI18nFS creates a pre-loaded I18n instance from an embedded fs.FS, so a
// module's locales are compiled into the binary and resolve regardless of the
// process working directory.
func NewModuleI18nFS(fallback string, fsys fs.FS, dir string) *I18n {
	inst := New(fallback)
	if err := inst.LoadFS(fsys, dir); err != nil {
		// Non-fatal: fallback to key passthrough. With a correctly embedded FS
		// this should never happen, so surface it at Error level (with the dir
		// for context) rather than silently degrading every translation to its
		// key. Returning the error is not viable here: all ~19 call sites use a
		// sync.Once package-var pattern that cannot propagate one.
		slog.Error("i18n: failed to load module locales", "dir", dir, "error", err)
	}
	return inst
}

// RegisterFS registers one embedded module locale bundle. JSON files contain
// module-local keys; namespace is prepended as "namespace.key" so independently
// owned bundles cannot collide. Every supported locale must define the same
// non-empty keys as en-US, and a namespace can be registered only once.
func RegisterFS(namespace string, fsys fs.FS, dir string) error {
	if defaultI18n == nil {
		return fmt.Errorf("register locales: i18n is not initialized")
	}
	if !validNamespace(namespace) {
		return fmt.Errorf("register locales: invalid namespace %q", namespace)
	}
	bundle := New(Base)
	if err := bundle.LoadFS(fsys, dir); err != nil {
		return fmt.Errorf("register locales %q: %w", namespace, err)
	}
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	base := bundle.messages[Base]
	if len(base) == 0 {
		return fmt.Errorf("register locales %q: %s is missing or empty", namespace, Base)
	}
	for _, locale := range Supported() {
		messages := bundle.messages[locale.Code]
		if len(messages) == 0 {
			return fmt.Errorf("register locales %q: %s is missing or empty", namespace, locale.Code)
		}
		for key, value := range base {
			if !validLocalKey(key) {
				return fmt.Errorf("register locales %q: invalid local key %q", namespace, key)
			}
			if value == "" || messages[key] == "" {
				return fmt.Errorf("register locales %q: %s is missing key %q", namespace, locale.Code, key)
			}
		}
		for key := range messages {
			if base[key] == "" {
				return fmt.Errorf("register locales %q: %s has key %q missing from %s", namespace, locale.Code, key, Base)
			}
		}
	}

	defaultI18n.mu.Lock()
	defer defaultI18n.mu.Unlock()
	if _, exists := defaultI18n.registeredNamespaces[namespace]; exists {
		return fmt.Errorf("register locales: namespace %q is already registered", namespace)
	}
	for locale, messages := range bundle.messages {
		target := defaultI18n.messages[locale]
		for key := range messages {
			qualified := namespace + "." + key
			if _, exists := target[qualified]; exists {
				return fmt.Errorf("register locales: key %q is already registered for %s", qualified, locale)
			}
		}
	}
	for locale, messages := range bundle.messages {
		target := defaultI18n.messages[locale]
		if target == nil {
			target = make(map[string]string, len(messages))
			defaultI18n.messages[locale] = target
		}
		for key, value := range messages {
			target[namespace+"."+key] = value
		}
	}
	defaultI18n.registeredNamespaces[namespace] = struct{}{}
	return nil
}

func validNamespace(namespace string) bool {
	for index, character := range namespace {
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 && ((character >= '0' && character <= '9') || character == '-') {
			continue
		}
		return false
	}
	return namespace != ""
}

func validLocalKey(key string) bool {
	for index, character := range key {
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 && ((character >= '0' && character <= '9') || character == '_') {
			continue
		}
		return false
	}
	return key != ""
}

// default global instance
var defaultI18n *I18n

// SetLocale sets the global locale.
func SetLocale(locale string) {
	if defaultI18n != nil {
		defaultI18n.SetLocale(locale)
	}
}

// T translates a key using the global instance.
func T(key string, args ...any) string {
	if defaultI18n == nil {
		return key
	}
	return defaultI18n.T(key, args...)
}

// TContext translates a key using the locale from context via the global instance.
func TContext(ctx context.Context, key string, args ...any) string {
	if defaultI18n == nil {
		return key
	}
	return defaultI18n.TContext(ctx, key, args...)
}

// Locale returns the global locale.
func Locale() string {
	if defaultI18n == nil {
		return ""
	}
	return defaultI18n.Locale()
}
