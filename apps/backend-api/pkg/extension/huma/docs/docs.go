// Package docs serves the statically generated API documentation site.
package docs

//go:generate npm --prefix web run generate --silent

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

const docsPath = "/docs"

var landingTemplate = template.Must(template.New("api-landing").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="light dark">
    <title>{{.Name}} API</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #101310;
        --surface: #181c19;
        --surface-hover: #1e2420;
        --border: #303731;
        --text: #f2f5f2;
        --muted: #a9b3ab;
        --accent: #65b98a;
        --status: #61c889;
        --focus: #78a9ff;
        --shadow: rgba(0, 0, 0, 0.24);
      }
      * { box-sizing: border-box; }
      html { min-width: 320px; background: var(--bg); }
      body {
        min-height: 100vh;
        min-height: 100svh;
        margin: 0;
        display: grid;
        place-items: center;
        padding: 32px 20px;
        background: var(--bg);
        color: var(--text);
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        font-size: 16px;
        line-height: 1.5;
        text-rendering: optimizeLegibility;
      }
      main { width: min(608px, 100%); }
      header { margin-bottom: 28px; }
      h1 {
        margin: 0 0 6px;
        font-size: 1.5rem;
        font-weight: 650;
        letter-spacing: 0;
      }
      .lead {
        max-width: 540px;
        margin: 0;
        color: var(--muted);
      }
      .status {
        display: inline-flex;
        min-height: 32px;
        align-items: center;
        gap: 8px;
        margin-top: 18px;
        padding: 4px 10px;
        border: 1px solid var(--border);
        border-radius: 999px;
        color: var(--status);
        font-size: 0.8125rem;
        font-weight: 600;
      }
      .status-dot {
        width: 8px;
        height: 8px;
        flex: 0 0 auto;
        border-radius: 50%;
        background: currentColor;
        box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 18%, transparent);
      }
      nav {
        display: grid;
        grid-template-columns: repeat(2, minmax(0, 1fr));
        gap: 12px;
      }
      a {
        position: relative;
        display: flex;
        min-height: 96px;
        flex-direction: column;
        justify-content: center;
        padding: 16px 44px 16px 16px;
        border: 1px solid var(--border);
        border-radius: 6px;
        background: var(--surface);
        color: var(--text);
        text-decoration: none;
        box-shadow: 0 8px 24px var(--shadow);
        transition: background-color 160ms ease, border-color 160ms ease;
      }
      a::after {
        content: "\2192";
        position: absolute;
        top: 50%;
        right: 16px;
        color: var(--muted);
        font-size: 1.125rem;
        transform: translateY(-50%);
        transition: color 160ms ease;
      }
      a:hover { border-color: var(--accent); background: var(--surface-hover); }
      a:hover::after { color: var(--accent); }
      a:focus-visible { outline: 3px solid var(--focus); outline-offset: 3px; }
      a strong { display: block; font-size: 0.9375rem; font-weight: 650; }
      a span { display: block; margin-top: 3px; color: var(--muted); font-size: 0.8125rem; }
      @media (max-width: 520px) {
        body { align-items: start; padding-top: max(32px, env(safe-area-inset-top)); }
        nav { grid-template-columns: 1fr; }
        a { min-height: 84px; }
      }
      @media (prefers-color-scheme: light) {
        :root {
          color-scheme: light;
          --bg: #f3f6f4;
          --surface: #ffffff;
          --surface-hover: #f8fbf9;
          --border: #d4ddd7;
          --text: #18201c;
          --muted: #66716a;
          --accent: #137a4b;
          --status: #0b7442;
          --focus: #1559c5;
          --shadow: rgba(35, 48, 40, 0.08);
        }
      }
      @media (prefers-reduced-motion: reduce) {
        *, *::before, *::after { scroll-behavior: auto !important; transition: none !important; }
      }
    </style>
  </head>
  <body>
    <main>
      <header>
        <h1>{{.Name}} API</h1>
        <p class="lead">Service endpoints and operational resources for developers and platform operators.</p>
        <div class="status"><span class="status-dot" aria-hidden="true"></span>Service online</div>
      </header>
      <nav aria-label="API resources">
        <a href="/docs">
          <strong>API reference</strong>
          <span>Interactive documentation for every endpoint</span>
        </a>
        <a href="{{.SpecJSON}}">
          <strong>OpenAPI specification</strong>
          <span>Machine-readable API contract</span>
        </a>
        <a href="/healthz">
          <strong>Liveness</strong>
          <span>Service and module health</span>
        </a>
        <a href="/readyz">
          <strong>Readiness</strong>
          <span>Dependency readiness status</span>
        </a>
      </nav>
    </main>
  </body>
</html>
`))

//go:embed site
var site embed.FS

type runtimeConfig struct {
	Name     string `json:"name"`
	SpecJSON string `json:"specJSON"`
	SpecYAML string `json:"specYAML"`
}

type landingData struct {
	Name      string `json:"name"`
	Docs      string `json:"docs"`
	OpenAPI   string `json:"openapi"`
	Liveness  string `json:"liveness"`
	Readiness string `json:"readiness"`
}

// Register mounts the generated documentation site and disables Huma's
// built-in documentation page. OpenAPI documents remain owned by Huma.
func Register(router chi.Router, config huma.Config, name string) huma.Config {
	config.DocsPath = ""
	name = landingName(name)
	dist, err := fs.Sub(site, "site")
	if err != nil {
		panic(fmt.Errorf("open embedded docs site: %w", err))
	}

	runtime := runtimeConfig{
		Name:     name,
		SpecJSON: config.OpenAPIPath + ".json",
		SpecYAML: config.OpenAPIPath + ".yaml",
	}
	landing := indexHTML(name, runtime.SpecJSON)
	resources := landingData{
		Name:      landingName(name),
		Docs:      docsPath,
		OpenAPI:   runtime.SpecJSON,
		Liveness:  "/healthz",
		Readiness: "/readyz",
	}
	router.Get("/", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-cache")
		if !prefersHTML(request.Header.Get("Accept")) {
			respx.WriteOK(response, request, resources)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(landing)
	})
	router.Get(docsPath+"/config.js", configHandler(runtime))
	router.Get(docsPath, staticHandler(dist))
	router.Get(docsPath+"/*", staticHandler(dist))
	return config
}

func indexHTML(name, specJSON string) []byte {
	var output bytes.Buffer
	if err := landingTemplate.Execute(&output, runtimeConfig{Name: landingName(name), SpecJSON: specJSON}); err != nil {
		panic(fmt.Errorf("render API landing page: %w", err))
	}
	return output.Bytes()
}

func landingName(name string) string {
	return strings.TrimSpace(name)
}

func prefersHTML(accept string) bool {
	htmlQuality := -1.0
	jsonQuality := 0.0
	for _, value := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		quality := 1.0
		if raw := parameters["q"]; raw != "" {
			quality, err = strconv.ParseFloat(raw, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		switch {
		case mediaType == "text/html" || mediaType == "application/xhtml+xml":
			htmlQuality = max(htmlQuality, quality)
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || mediaType == "*/*":
			jsonQuality = max(jsonQuality, quality)
		}
	}
	return htmlQuality > 0 && htmlQuality >= jsonQuality
}

func configHandler(config runtimeConfig) http.HandlerFunc {
	data, err := json.Marshal(config)
	if err != nil {
		panic(fmt.Errorf("encode docs config: %w", err))
	}
	body := append([]byte("window.__DOCS_CONFIG__ = "), data...)
	body = append(body, ';', '\n')

	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = response.Write(body)
	}
}

func staticHandler(content fs.FS) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, docsPath)
		name = strings.TrimPrefix(path.Clean("/"+name), "/")
		if name == "." || name == "" {
			name = "index.html"
		} else if path.Ext(name) == "" {
			name = path.Join(name, "index.html")
		}

		data, err := fs.ReadFile(content, name)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			response.Header().Set("Content-Type", contentType)
		}
		if path.Ext(name) == ".html" {
			response.Header().Set("Cache-Control", "no-cache")
		} else {
			response.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		}
		http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(data))
	}
}
