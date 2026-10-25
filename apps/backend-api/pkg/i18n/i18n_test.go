package i18n

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalLocaleBundlesHaveValidKeysAndIntactChinese(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	files := 0
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Base(filepath.Dir(path)) != "locales" || filepath.Ext(path) != ".json" {
			return nil
		}
		files++
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, utf8.Valid(data), "%s must be UTF-8", path)
		var messages map[string]string
		require.NoError(t, json.Unmarshal(data, &messages), "%s", path)
		for key, value := range messages {
			require.True(t, validLocalKey(key), "%s: invalid local key %q", path, key)
			if entry.Name() == "zh-CN.json" {
				require.NotContains(t, value, "??", "%s: corrupted Chinese for %q", path, key)
				require.NotContains(t, value, "\uFFFD", "%s: invalid Unicode replacement for %q", path, key)
			}
		}
		return nil
	}))
	require.Positive(t, files, "the scan must find the real module locale files")
}

// writeLocale is a small helper to create a locale JSON file in dir.
func writeLocale(t *testing.T, dir, locale, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, locale+".json"), []byte(body), 0o600))
}

func TestI18n_Translate(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeLocale(t, dir, "en", `{"hello":"Hello","greet":"Hello, %s!","only_en":"EnglishOnly"}`)
	writeLocale(t, dir, "zh", `{"hello":"你好","greet":"你好，%s！"}`)

	i := New("en")
	require.NoError(t, i.Load("en", dir))
	require.NoError(t, i.Load("zh", dir))

	tests := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{name: "existing key default locale", locale: "en", key: "hello", want: "Hello"},
		{name: "existing key non-default locale", locale: "zh", key: "hello", want: "你好"},
		{name: "missing key returns key", locale: "en", key: "does_not_exist", want: "does_not_exist"},
		{name: "interpolation with args", locale: "en", key: "greet", args: []any{"World"}, want: "Hello, World!"},
		{name: "interpolation non-default locale", locale: "zh", key: "greet", args: []any{"世界"}, want: "你好，世界！"},
		{name: "fallback to default locale when missing in current", locale: "zh", key: "only_en", want: "EnglishOnly"},
		{name: "unknown locale falls back to default", locale: "fr", key: "hello", want: "Hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			i.SetLocale(tc.locale)
			got := i.T(tc.key, tc.args...)

			// Assert
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestI18n_LookupLowercaseMatch(t *testing.T) {
	// Arrange: stored key is lowercase, queried with mixed case.
	dir := t.TempDir()
	writeLocale(t, dir, "en", `{"some_key":"Lowercased"}`)

	i := New("en")
	require.NoError(t, i.Load("en", dir))

	// Act
	got := i.T("SOME_KEY")

	// Assert
	assert.Equal(t, "Lowercased", got)
}

func TestI18n_Has(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeLocale(t, dir, "en", `{"present":"yes"}`)

	i := New("en")
	require.NoError(t, i.Load("en", dir))

	// Act + Assert
	assert.True(t, i.Has("present"), "existing key should be reported as present")
	assert.False(t, i.Has("absent"), "missing key should be reported as absent")
}

func TestI18n_TContext(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeLocale(t, dir, "en", `{"hello":"Hello"}`)
	writeLocale(t, dir, "zh", `{"hello":"你好"}`)

	i := New("en")
	require.NoError(t, i.Load("en", dir))
	require.NoError(t, i.Load("zh", dir))

	t.Run("locale set in context overrides default", func(t *testing.T) {
		// Arrange
		ctx := WithLocale(context.Background(), "zh")
		// Act
		got := i.TContext(ctx, "hello")
		// Assert
		assert.Equal(t, "你好", got)
	})

	t.Run("no locale in context uses default", func(t *testing.T) {
		// Act
		got := i.TContext(context.Background(), "hello")
		// Assert
		assert.Equal(t, "Hello", got)
	})

	t.Run("context locale with args", func(t *testing.T) {
		dir2 := t.TempDir()
		writeLocale(t, dir2, "zh", `{"greet":"你好，%s"}`)
		j := New("en")
		require.NoError(t, j.Load("zh", dir2))

		ctx := WithLocale(context.Background(), "zh")
		got := j.TContext(ctx, "greet", "世界")
		assert.Equal(t, "你好，世界", got)
	})
}

func TestI18n_LocaleGetter(t *testing.T) {
	i := New("en")
	assert.Equal(t, "en", i.Locale())
	i.SetLocale("zh")
	assert.Equal(t, "zh", i.Locale())
}

func TestI18n_Load_Errors(t *testing.T) {
	t.Run("missing file returns error", func(t *testing.T) {
		i := New("en")
		err := i.Load("en", t.TempDir())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read locale file")
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeLocale(t, dir, "en", `{not valid json`)
		i := New("en")
		err := i.Load("en", dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse locale file")
	})
}

func TestI18n_LoadDir(t *testing.T) {
	t.Run("loads all json files and ignores non-json and dirs", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		writeLocale(t, dir, "en", `{"hello":"Hello"}`)
		writeLocale(t, dir, "zh", `{"hello":"你好"}`)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0755))

		i := New("en")
		// Act
		require.NoError(t, i.LoadDir(dir))

		// Assert
		assert.Equal(t, "Hello", i.T("hello"))
		i.SetLocale("zh")
		assert.Equal(t, "你好", i.T("hello"))
	})

	t.Run("missing dir returns error", func(t *testing.T) {
		i := New("en")
		err := i.LoadDir(filepath.Join(t.TempDir(), "nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read locales dir")
	})

	t.Run("invalid json in dir returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeLocale(t, dir, "en", `{broken`)
		i := New("en")
		err := i.LoadDir(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load locale")
	})
}

func TestI18n_LoadFS(t *testing.T) {
	t.Run("loads json files from fs.FS subtree", func(t *testing.T) {
		// Arrange
		fsys := fstest.MapFS{
			"i18n/locales/en.json":  {Data: []byte(`{"hello":"Hello","greet":"Hi %s"}`)},
			"i18n/locales/zh.json":  {Data: []byte(`{"hello":"你好"}`)},
			"i18n/locales/skip.txt": {Data: []byte("ignored")},
		}

		i := New("en")
		// Act
		require.NoError(t, i.LoadFS(fsys, "i18n/locales"))

		// Assert
		assert.Equal(t, "Hello", i.T("hello"))
		assert.Equal(t, "Hi Bob", i.T("greet", "Bob"))
		i.SetLocale("zh")
		assert.Equal(t, "你好", i.T("hello"))
	})

	t.Run("missing dir returns error", func(t *testing.T) {
		i := New("en")
		err := i.LoadFS(fstest.MapFS{}, "does/not/exist")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read locales fs dir")
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		fsys := fstest.MapFS{
			"locales/en.json": {Data: []byte(`{broken`)},
		}
		i := New("en")
		err := i.LoadFS(fsys, "locales")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse locale file")
	})
}

// readFailFS lists locale entries but fails when a JSON file is opened.
type readFailFS struct {
	fstest.MapFS
}

func (f readFailFS) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, ".json") {
		return nil, fs.ErrPermission
	}
	return f.MapFS.Open(name)
}

func TestI18n_LoadFSReadError(t *testing.T) {
	i := New(Base)
	fsys := readFailFS{MapFS: fstest.MapFS{"en.json": {Data: []byte(`{"hello":"Hi"}`)}}}
	err := i.LoadFS(fsys, ".")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read locale file")
}

func TestNewModuleI18n(t *testing.T) {
	t.Run("loads locales from valid dir", func(t *testing.T) {
		dir := t.TempDir()
		writeLocale(t, dir, "en", `{"hello":"Hello"}`)
		inst := NewModuleI18n("en", dir)
		assert.Equal(t, "Hello", inst.T("hello"))
	})

	t.Run("missing dir returns usable passthrough instance", func(t *testing.T) {
		inst := NewModuleI18n("en", filepath.Join(t.TempDir(), "missing"))
		require.NotNil(t, inst)
		assert.Equal(t, "hello", inst.T("hello"))
	})
}

func TestNewModuleI18nFS(t *testing.T) {
	t.Run("loads locales from fs", func(t *testing.T) {
		fsys := fstest.MapFS{
			"locales/en.json": {Data: []byte(`{"hello":"Hello"}`)},
		}
		inst := NewModuleI18nFS("en", fsys, "locales")
		assert.Equal(t, "Hello", inst.T("hello"))
	})

	t.Run("missing dir returns usable passthrough instance", func(t *testing.T) {
		inst := NewModuleI18nFS("en", fstest.MapFS{}, "missing")
		require.NotNil(t, inst)
		assert.Equal(t, "hello", inst.T("hello"))
	})
}

func TestRegisterFSValidatesAndMergesModuleLocales(t *testing.T) {
	require.NoError(t, NewModule(newTestRuntime(), Base).Initialize(t.Context()))
	assert.ErrorContains(t, RegisterFS("test", fstest.MapFS{}, "missing"), "read locales fs dir")
	for _, namespace := range []string{"", "Health", "health.i18n", "health_i18n"} {
		assert.ErrorContains(t, RegisterFS(namespace, fstest.MapFS{}, "missing"), "invalid namespace")
	}

	emptyBase := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{}`)},
		"locales/zh-CN.json": {Data: []byte(`{"module_error":"明确的模块错误"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"module_error":"Ralat modul yang jelas"}`)},
	}
	assert.ErrorContains(t, RegisterFS("empty", emptyBase, "locales"), "en-US is missing or empty")

	missingKey := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"module_error":"Clear module error"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"other_error":"其他错误"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"module_error":"Ralat modul yang jelas"}`)},
	}
	assert.ErrorContains(t, RegisterFS("missing", missingKey, "locales"), `zh-CN is missing key "module_error"`)

	extraKey := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"module_error":"Clear module error"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"module_error":"明确的模块错误","other_error":"其他错误"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"module_error":"Ralat modul yang jelas"}`)},
	}
	assert.ErrorContains(t, RegisterFS("extra", extraKey, "locales"), `zh-CN has key "other_error" missing from en-US`)

	complete := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"module_error":"Clear module error"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"module_error":"明确的模块错误"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"module_error":"Ralat modul yang jelas"}`)},
	}
	require.NoError(t, RegisterFS("module", complete, "locales"))
	assert.Equal(t, "明确的模块错误", TContext(WithLocale(context.Background(), "zh-CN"), "module.module_error"))
	assert.Equal(t, "module_error", TContext(WithLocale(context.Background(), "zh-CN"), "module_error"))
	assert.ErrorContains(t, RegisterFS("module", complete, "locales"), `namespace "module" is already registered`)

	// Identical local keys are independent when their owning namespaces differ.
	require.NoError(t, RegisterFS("another", complete, "locales"))
	assert.Equal(t, "Clear module error", T("another.module_error"))

	incomplete := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"other_error":"Other error"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"other_error":"其他错误"}`)},
	}
	assert.ErrorContains(t, RegisterFS("incomplete", incomplete, "locales"), "ms-MY")

	prefixedKey := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"module.error":"Module error"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"module.error":"模块错误"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"module.error":"Ralat modul"}`)},
	}
	assert.ErrorContains(t, RegisterFS("prefixed", prefixedKey, "locales"), `invalid local key "module.error"`)
}

func TestRegisterFSRequiresInitializedRegistry(t *testing.T) {
	previous := defaultI18n
	defaultI18n = nil
	defer func() { defaultI18n = previous }()

	err := RegisterFS("test", fstest.MapFS{"en.json": {Data: []byte(`{"x":"y"}`)}}, ".")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestRegisterFSIncludesAdditionalLocales(t *testing.T) {
	require.NoError(t, NewModule(newTestRuntime(), Base).Initialize(t.Context()))
	bundle := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"success":"OK"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"success":"OK"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"success":"OK"}`)},
		"locales/fr.json":    {Data: []byte(`{"success":"D'accord"}`)},
	}

	require.NoError(t, RegisterFS("test", bundle, "locales"))
	assert.Equal(t, "D'accord", TContext(WithLocale(t.Context(), "fr"), "test.success"))
}

func TestNamespaceAndLocalKeyFormat(t *testing.T) {
	for _, namespace := range []string{"health", "worker-id", "module2"} {
		assert.Truef(t, validNamespace(namespace), "namespace %q", namespace)
	}
	for _, namespace := range []string{"", "Health", "worker_id", "2module", "module.name"} {
		assert.Falsef(t, validNamespace(namespace), "namespace %q", namespace)
	}
	for _, key := range []string{"message", "message_key", "message2"} {
		assert.Truef(t, validLocalKey(key), "local key %q", key)
	}
	for _, key := range []string{"", "Message", "_message", "message-key", "module.message"} {
		assert.Falsef(t, validLocalKey(key), "local key %q", key)
	}
}

func TestGlobalFunctions(t *testing.T) {
	require.NoError(t, NewModule(newTestRuntime(), Base).Initialize(t.Context()))
	bundle := fstest.MapFS{
		"locales/en-US.json": {Data: []byte(`{"hello":"Hello"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"hello":"你好"}`)},
		"locales/ms-MY.json": {Data: []byte(`{"hello":"Helo"}`)},
	}
	require.NoError(t, RegisterFS("test", bundle, "locales"))

	t.Run("global T and Locale", func(t *testing.T) {
		assert.Equal(t, "Hello", T("test.hello"))
		assert.Equal(t, Base, Locale())
	})

	t.Run("global SetLocale", func(t *testing.T) {
		SetLocale("zh-CN")
		assert.Equal(t, "zh-CN", Locale())
		assert.Equal(t, "你好", T("test.hello"))
		SetLocale(Base)
	})

	t.Run("global TContext with context locale", func(t *testing.T) {
		ctx := WithLocale(context.Background(), "zh-CN")
		assert.Equal(t, "你好", TContext(ctx, "test.hello"))
	})

	t.Run("global TContext without context locale uses default", func(t *testing.T) {
		assert.Equal(t, "Hello", TContext(context.Background(), "test.hello"))
	})
}

func TestGlobalFunctions_NilDefault(t *testing.T) {
	// Arrange: force the global instance to nil to exercise nil guards.
	defaultI18n = nil

	// Act + Assert
	assert.Equal(t, "key", T("key"))
	assert.Equal(t, "key", TContext(context.Background(), "key"))
	assert.Empty(t, Locale())
	// SetLocale on nil should be a no-op (not panic).
	assert.NotPanics(t, func() { SetLocale("zh") })
}

func TestTranslationsRenderAKeyInEverySupportedLocale(t *testing.T) {
	bundle := New(Base)
	bundle.messages = map[string]map[string]string{
		"en-US": {"label": "Label"},
		"zh-CN": {"label": "标签"},
	}
	labels := bundle.Translations("label")
	require.Len(t, labels, len(Supported()))
	assert.Equal(t, "Label", labels["en-US"])
	assert.Equal(t, "标签", labels["zh-CN"])
	assert.Equal(t, "Label", labels["ms-MY"], "a locale without the key falls back")
}
