// Package docs serves the statically generated API documentation site.
package docs

//go:generate npm --prefix web run generate --silent

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

const docsPath = "/docs"

//go:embed site
var site embed.FS

type runtimeConfig struct {
	Name     string `json:"name"`
	SpecJSON string `json:"specJSON"`
	SpecYAML string `json:"specYAML"`
}

// Register mounts the generated documentation site and disables Huma's
// built-in documentation page. OpenAPI documents remain owned by Huma.
func Register(router chi.Router, config huma.Config, name string) huma.Config {
	config.DocsPath = ""
	dist, err := fs.Sub(site, "site")
	if err != nil {
		panic(fmt.Errorf("open embedded docs site: %w", err))
	}

	runtime := runtimeConfig{
		Name:     name,
		SpecJSON: config.OpenAPIPath + ".json",
		SpecYAML: config.OpenAPIPath + ".yaml",
	}
	router.Get("/", func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, docsPath+"/", http.StatusTemporaryRedirect)
	})
	router.Get(docsPath+"/config.js", configHandler(runtime))
	router.Get(docsPath, staticHandler(dist))
	router.Get(docsPath+"/*", staticHandler(dist))
	return config
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
