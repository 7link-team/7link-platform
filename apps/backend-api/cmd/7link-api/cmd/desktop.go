package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/maintenance"
	"7link.com/platform/pkg/config"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"
)

// Desktop has a parent-owned lifetime: EOF on stdin also stops the service.
// No setup operation is exposed over HTTP, even on the loopback listener.
type desktopRequest struct {
	Command  string `json:"command"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

type desktopEvent struct {
	Event   string `json:"event"`
	Origin  string `json:"origin,omitempty"`
	Message string `json:"message,omitempty"`
}

type desktopOptions struct {
	DataDir string
	WebDir  string
	Name    string
}

func init() {
	options := desktopOptions{}
	command := &cobra.Command{
		Use: "desktop", Short: "Run the bundled, parent-managed local desktop service",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := runDesktop(command.Context(), options, command.InOrStdin(), command.OutOrStdout())
			if err != nil {
				_ = json.NewEncoder(command.OutOrStdout()).Encode(desktopEvent{Event: "error", Message: err.Error()})
			}
			return err
		},
	}
	command.Flags().StringVar(&options.DataDir, "data-dir", "", "private persistent desktop data directory")
	command.Flags().StringVar(&options.WebDir, "web-dir", "", "bundled production Web directory")
	command.Flags().StringVar(&options.Name, "name", "Desktop", "configured application display name")
	rootCmd.AddCommand(command)
}

func runDesktop(parent context.Context, options desktopOptions, input io.Reader, output io.Writer) (result error) {
	if options.DataDir == "" || options.WebDir == "" {
		return errors.New("desktop requires data-dir and web-dir")
	}
	web, err := os.OpenRoot(options.WebDir)
	if err != nil {
		return fmt.Errorf("open bundled Web: %w", err)
	}
	defer func() { _ = web.Close() }()
	if info, err := web.Stat("index.html"); err != nil || !info.Mode().IsRegular() {
		return errors.New("bundled Web index.html is missing")
	}
	if err := os.MkdirAll(options.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(options.DataDir, 0o700); err != nil {
		return err
	}
	listener, err := desktopListener(options.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	origin := "http://" + listener.Addr().String()
	settings, err := desktopConfig(options, origin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	requests := make(chan desktopRequest)
	go readDesktopRequests(ctx, input, requests, cancel)

	application := app.New(settings)
	defer func() { result = errors.Join(result, application.Shutdown()) }()
	if err := application.Bootstrap(ctx); err != nil {
		return fmt.Errorf("start local application: %w", err)
	}
	store, err := module.Resolve[core.PlatformAdministratorStore](application.Modules().Services())
	if err != nil {
		return err
	}
	admins, err := store.List(ctx, application.Runtime().Database().Write())
	if err != nil {
		return err
	}
	encode := json.NewEncoder(output)
	if len(admins) == 0 {
		if err := encode.Encode(desktopEvent{Event: "setup"}); err != nil {
			return err
		}
		// Bootstrap is a create-only maintenance operation. Reuse it with the
		// runtime closed rather than composing a second active application.
		if err := application.Shutdown(); err != nil {
			return err
		}
		application = app.New(settings)
		for {
			select {
			case <-ctx.Done():
				return nil
			case request := <-requests:
				if request.Command != "setup" {
					if err := encode.Encode(desktopEvent{Event: "setup", Message: "Set up the first local administrator"}); err != nil {
						return err
					}
					continue
				}
				_, err = maintenance.RunPlatformAdminBootstrap(ctx, maintenance.PlatformAdminConfig{
					AppConfig: settings,
					PlatformAdminRequest: maintenance.PlatformAdminRequest{
						Login: request.Login,
					},
				}, request.Password)
				request.Password = ""
				if err != nil {
					if err := encode.Encode(desktopEvent{Event: "setup", Message: err.Error()}); err != nil {
						return err
					}
					continue
				}
			}
			break
		}
		if err := application.Bootstrap(ctx); err != nil {
			return fmt.Errorf("start initialized local application: %w", err)
		}
	}
	api := chi.NewRouter()
	if err := application.RegisterHTTP(ctx, api); err != nil {
		return err
	}
	server := &http.Server{
		Handler:           desktopHandler(web, api, listener.Addr().String(), origin),
		ReadHeaderTimeout: settings.HttpServer.ReadHeaderTimeout,
		IdleTimeout:       settings.HttpServer.IdleTimeout,
		MaxHeaderBytes:    settings.HttpServer.MaxHeaderBytes,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := server.Shutdown(stop); err != nil {
			result = errors.Join(result, err, server.Close())
		}
	}()
	if err := encode.Encode(desktopEvent{Event: "ready", Origin: origin}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-requests:
			// Keep consuming after setup so the reader can always reach EOF
			// or stop. A repeated setup cannot create another administrator.
			if err := encode.Encode(desktopEvent{Event: "ready", Origin: origin}); err != nil {
				return err
			}
		case err := <-served:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}

func readDesktopRequests(ctx context.Context, input io.Reader, requests chan<- desktopRequest, cancel context.CancelFunc) {
	defer cancel()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16*1024)
	for scanner.Scan() {
		var request desktopRequest
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.Command == "stop" {
			return
		}
		select {
		case requests <- request:
		case <-ctx.Done():
			return
		}
	}
}

func desktopListener(directory string) (net.Listener, error) {
	file := filepath.Join(directory, "http-port")
	port := 0
	data, err := os.ReadFile(file)
	if err == nil {
		port, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid persisted local HTTP port")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("local service port is occupied; close the other instance or service: %w", err)
	}
	if port == 0 {
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			_ = listener.Close()
			return nil, errors.New("local listener has an unexpected address type")
		}
		port = address.Port
		file, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("reserve persistent local port: %w", err)
		}
		_, writeErr := fmt.Fprintln(file, port)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	return listener, nil
}

func desktopConfig(options desktopOptions, origin string) (app.AppConfig, error) {
	keyFiles := map[string]string{}
	for _, name := range []string{"signing", "mfa", "federation", "business", "provisioning"} {
		file := filepath.Join(options.DataDir, name+".key")
		if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return app.AppConfig{}, err
			}
			if err := os.WriteFile(file, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
				return app.AppConfig{}, err
			}
		} else if err != nil {
			return app.AppConfig{}, err
		}
		keyFiles[name] = file
	}
	values := map[string]any{
		"name": options.Name, "version": buildVersion, "deployment.name": options.Name,
		"migrate": true, "debug": false, "timezone": "UTC",
		"http_server.address": "127.0.0.1", "grpc_server.address": "127.0.0.1",
		"database.main.type": "sqlite", "database.main.dsn": filepath.Join(options.DataDir, "application.sqlite"),
		"database.main.readable": true, "database.main.writable": true,
		"files.local_root": filepath.Join(options.DataDir, "files"),
		"authn.enabled":    true, "authn.issuer": origin + "/api",
		"authn.signing_key_file": keyFiles["signing"], "authn.mfa.encryption_key_file": keyFiles["mfa"],
		"authn.web.enabled": true, "authn.web.cookie_secure": false,
		"authn.web.post_login_url": origin + "/", "authn.web.post_logout_url": origin + "/",
		"authn.web.allowed_return_urls": []string{origin + "/"}, "authn.web.allowed_origins": []string{origin},
		"authn.web.cookie_name": "desktop_session",
		"secrets.enabled":       true, "secrets.active_key_file": keyFiles["business"],
		"federation.enabled": true, "federation.encryption_key_file": keyFiles["federation"],
		"provisioning.enabled": true, "provisioning.encryption_key_file": keyFiles["provisioning"],
		"shutdown.drain_delay": "1ms", "shutdown.timeout": "30s",
	}
	// Use the application's tagged defaults and decoder, with a separate env
	// namespace. Desktop-owned settings cannot be changed by server env vars.
	var settings app.AppConfig
	loader := config.New()
	loader.GetViper().SetEnvPrefix("DESKTOP_LOCAL")
	if err := loader.Bind(&settings, "desktop"); err != nil {
		return settings, err
	}
	for key, value := range values {
		loader.GetViper().Set(key, value)
	}
	if err := loader.Load(&settings); err != nil {
		return settings, err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return settings, nil
}

func desktopHandler(web *os.Root, api http.Handler, host, origin string) http.Handler {
	files := http.FileServerFS(web.FS())
	api = http.StripPrefix("/api", api)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			http.Error(w, "local origin required", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		info, err := web.Stat(name)
		if err != nil || !info.Mode().IsRegular() {
			if strings.HasPrefix(r.URL.Path, "/assets/") || path.Ext(r.URL.Path) != "" {
				http.NotFound(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
