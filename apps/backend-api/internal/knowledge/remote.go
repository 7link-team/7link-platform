package knowledge

import (
	"7link.com/platform/pkg/extension/httpx"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"golang.org/x/net/html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const remoteContentLimit = 10 << 20

var ErrRemoteAccess = errors.Join(ErrUnavailable, errors.New("knowledge: remote access denied"))
var ErrRemoteMissing = errors.Join(ErrUnavailable, errors.New("knowledge: remote document missing"))

// RemoteConfig is deployment configuration. RepositoryToken is never accepted
// from source metadata, returned through HTTP, or sent to arbitrary URL sources.
type RemoteConfig struct {
	Enabled              bool   `mapstructure:"enabled"`
	RepositoryHost       string `mapstructure:"repository_host"`
	RepositoryAPIBaseURL string `mapstructure:"repository_api_base_url"`
	RepositoryToken      string `mapstructure:"repository_token" secret:"true"`
}
type remoteConnector struct {
	config RemoteConfig
	client *http.Client
}

func newRemoteConnector(config RemoteConfig) (*remoteConnector, error) {
	if !config.Enabled {
		return nil, nil
	}
	policy := httpx.Policy{}
	config.RepositoryHost = strings.ToLower(strings.TrimSpace(config.RepositoryHost))
	config.RepositoryAPIBaseURL = strings.TrimRight(strings.TrimSpace(config.RepositoryAPIBaseURL), "/")
	if config.RepositoryHost != "" || config.RepositoryAPIBaseURL != "" || config.RepositoryToken != "" {
		if config.RepositoryHost == "" || strings.ContainsAny(config.RepositoryHost, "/:@?# ") || config.RepositoryAPIBaseURL == "" {
			return nil, ErrInvalid
		}
		if err := validateRemoteURL(config.RepositoryAPIBaseURL); err != nil {
			return nil, err
		}
	}
	client := httpx.NewClient(15*time.Second, policy)
	redirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateRemoteURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
		}
		return redirect(req, via)
	}
	return &remoteConnector{config: config, client: client}, nil
}
func (m *Module) initializeRemote() error {
	connector, err := newRemoteConnector(m.config.Remote)
	if err == nil {
		m.remote = connector
	}
	return err
}
func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return ErrInvalid
	}
	if err = (httpx.Policy{}).ValidateURL(raw); err != nil {
		return ErrInvalid
	}
	return nil
}
func (c *remoteConnector) supports(kind string) bool {
	return c != nil && (kind == "URL" || kind == "REPOSITORY" && c.config.RepositoryHost != "" && c.config.RepositoryAPIBaseURL != "")
}
func (c *remoteConnector) fetch(ctx context.Context, kind, locator, format string) (string, error) {
	if !c.supports(kind) {
		return "", ErrUnavailable
	}
	if err := validateRemoteURL(locator); err != nil {
		return "", err
	}
	if kind == "REPOSITORY" {
		return c.repository(ctx, locator)
	}
	if format == "PDF" {
		return "", ErrUnavailable
	}
	data, contentType, err := c.get(ctx, locator, false)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", ErrInvalid
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		return remoteHTMLText(data)
	case "text/plain", "text/markdown", "text/x-markdown", "application/json", "text/x-go", "text/x-python", "application/javascript", "text/javascript", "text/css", "":
		return string(data), nil
	default:
		if strings.HasPrefix(mediaType, "text/") {
			return string(data), nil
		}
		return "", ErrInvalid
	}
}
func (c *remoteConnector) get(ctx context.Context, target string, credential bool) ([]byte, string, error) {
	if err := validateRemoteURL(target); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", ErrInvalid
	}
	req.Header.Set("Accept", "text/plain, text/markdown, text/html, application/octet-stream")
	if credential && c.config.RepositoryToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.RepositoryToken)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	// Credentials, upstream bodies and URLs are deliberately absent from errors.
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, "", ErrRemoteAccess
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, "", ErrRemoteMissing
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, remoteContentLimit+1))
	if err != nil {
		return nil, "", ErrUnavailable
	}
	if len(data) > remoteContentLimit {
		return nil, "", ErrInvalid
	}
	return data, response.Header.Get("Content-Type"), nil
}
func remoteHTMLText(data []byte) (string, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(data))
	var out strings.Builder
	hidden := 0
	for {
		kind := tokenizer.Next()
		switch kind {
		case html.ErrorToken:
			if !errors.Is(tokenizer.Err(), io.EOF) {
				return "", ErrInvalid
			}
			text := strings.TrimSpace(out.String())
			if text == "" {
				return "", ErrInvalid
			}
			return text, nil
		case html.StartTagToken:
			token := tokenizer.Token()
			if slices.Contains([]string{"script", "style", "noscript", "template"}, token.Data) {
				hidden++
			}
			if hidden == 0 && slices.Contains([]string{"p", "div", "br", "h1", "h2", "h3", "li", "pre"}, token.Data) {
				out.WriteByte('\n')
			}
		case html.EndTagToken:
			token := tokenizer.Token()
			if slices.Contains([]string{"script", "style", "noscript", "template"}, token.Data) && hidden > 0 {
				hidden--
			}
		case html.TextToken:
			if hidden == 0 {
				out.Write(tokenizer.Text())
				out.WriteByte(' ')
			}
		}
	}
}
func (c *remoteConnector) repository(ctx context.Context, locator string) (string, error) {
	u, err := url.Parse(locator)
	if err != nil || strings.ToLower(u.Hostname()) != c.config.RepositoryHost || u.Port() != "" {
		return "", ErrInvalid
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", ErrInvalid
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" {
		return "", ErrInvalid
	}
	for key := range u.Query() {
		if key != "ref" && key != "path" {
			return "", ErrInvalid
		}
	}
	ref := u.Query().Get("ref")
	prefix := strings.Trim(u.Query().Get("path"), "/")
	if prefix != "" && (path.Clean(prefix) != prefix || prefix == ".." || strings.HasPrefix(prefix, "../") || strings.Contains(prefix, "\\")) {
		return "", ErrInvalid
	}
	target := c.config.RepositoryAPIBaseURL + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(repo) + "/tarball"
	if ref != "" {
		target += "/" + url.PathEscape(ref)
	}
	data, _, err := c.get(ctx, target, true)
	if err != nil {
		return "", err
	}
	return repositoryText(data, prefix)
}
func repositoryText(data []byte, prefix string) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", ErrInvalid
	}
	defer func() { _ = gz.Close() }()
	// Bound the entire decompressed archive, including files outside docs, to
	// prevent a small compressed archive from consuming unbounded CPU or memory.
	expanded := &io.LimitedReader{R: gz, N: (64 << 20) + 1}
	archive := tar.NewReader(expanded)
	type document struct{ name, body string }
	docs := []document{}
	total := 0
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if expanded.N <= 1 {
				return "", ErrInvalid
			}
			break
		}
		if err != nil {
			return "", ErrInvalid
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size < 0 || header.Size > 64<<20 {
			return "", ErrInvalid
		}
		name := strings.TrimPrefix(header.Name, "./")
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return "", ErrInvalid
		}
		_, name, found := strings.Cut(name, "/")
		if !found {
			continue
		}
		selected := name == "README" || strings.HasPrefix(strings.ToLower(name), "readme.") || strings.HasPrefix(name, "docs/")
		if prefix != "" {
			selected = name == prefix || strings.HasPrefix(name, prefix+"/")
		}
		if !selected {
			continue
		}
		extension := strings.ToLower(path.Ext(name))
		if !slices.Contains([]string{".md", ".markdown", ".txt", ".rst", ".adoc", ".go", ".ts", ".tsx", ".js", ".py", ".rs", ".java", ".sql", ".json", ".yaml", ".yml", ".css", ".html", ""}, extension) {
			continue
		}
		if len(docs) >= 200 || header.Size > 1<<20 || total+int(header.Size) > remoteContentLimit {
			return "", ErrInvalid
		}
		body, err := io.ReadAll(io.LimitReader(archive, (1<<20)+1))
		if err != nil || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
			return "", ErrInvalid
		}
		total += len(body)
		docs = append(docs, document{name: name, body: string(body)})
	}
	if len(docs) == 0 {
		return "", ErrInvalid
	}
	slices.SortFunc(docs, func(a, b document) int { return strings.Compare(a.name, b.name) })
	var out strings.Builder
	for _, doc := range docs {
		out.WriteString("# " + doc.name + "\n\n" + doc.body + "\n\n")
	}
	if out.Len() > remoteContentLimit {
		return "", ErrInvalid
	}
	return out.String(), nil
}
