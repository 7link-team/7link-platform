package pprof

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"maps"
	"net/http"
	runtimepprof "runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/pprof/driver"
	"github.com/google/pprof/profile"
)

const (
	defaultCPUSeconds  = 5
	maximumCPUSeconds  = 30
	pprofProfileSource = "runtime-profile"
)

var (
	pprofProfiles = map[string]string{
		"heap":         "Heap",
		"allocs":       "Allocations",
		"goroutine":    "Goroutines",
		"mutex":        "Mutex contention",
		"block":        "Blocking",
		"threadcreate": "Thread creation",
		"cpu":          "CPU",
	}
	pprofLandingTemplate = template.Must(template.New("pprof-landing").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="light dark">
    <title>Runtime profiler</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #0f1210;
        --bar: #131714;
        --surface: #171b18;
        --surface-hover: #1d231f;
        --border: #2b332e;
        --text: #edf2ee;
        --muted: #98a49c;
        --subtle: #7f8b83;
        --accent: #69bf89;
        --accent-hover: #82d09f;
        --accent-ink: #09140d;
        --danger: #ff9187;
        --focus: #78a9ff;
      }
      * { box-sizing: border-box; }
      html { min-width: 320px; background: var(--bg); }
      body {
        min-height: 100vh;
        min-height: 100svh;
        margin: 0;
        background: var(--bg);
        color: var(--text);
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        font-size: 16px;
        line-height: 1.5;
        text-rendering: optimizeLegibility;
      }
      a, button { touch-action: manipulation; }
      a { color: inherit; }
      a:focus-visible, button:focus-visible { outline: 3px solid var(--focus); outline-offset: 3px; }
      .appbar { border-bottom: 1px solid var(--border); background: var(--bar); }
      .appbar-inner {
        width: min(1080px, calc(100% - 40px));
        min-height: 60px;
        margin: 0 auto;
        display: flex;
        align-items: center;
        gap: 12px;
      }
      .brand {
        display: inline-flex;
        min-height: 44px;
        align-items: center;
        color: var(--text);
        font-size: 0.9375rem;
        font-weight: 700;
        text-decoration: none;
      }
      .divider { width: 1px; height: 20px; background: var(--border); }
      .product { color: var(--muted); font-size: 0.875rem; }
      .appnav { display: flex; gap: 20px; margin-left: auto; }
      .appnav a {
        display: inline-flex;
        min-height: 44px;
        align-items: center;
        color: var(--muted);
        font-size: 0.8125rem;
        text-underline-offset: 4px;
      }
      .appnav a:hover { color: var(--text); }
      .shell {
        width: min(1080px, calc(100% - 40px));
        margin: 0 auto;
        display: grid;
        grid-template-columns: 240px minmax(0, 1fr);
        gap: 40px;
        padding: 40px 0 56px;
      }
      .sidebar { min-width: 0; padding-right: 32px; border-right: 1px solid var(--border); }
      .state {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-bottom: 32px;
        color: var(--muted);
        font-size: 0.8125rem;
        font-weight: 600;
      }
      .state-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); }
      .section-label {
        margin: 0 0 8px;
        color: var(--subtle);
        font-size: 0.75rem;
        font-weight: 700;
        text-transform: uppercase;
      }
      .snapshot-name { display: block; margin-bottom: 12px; font-size: 1rem; font-weight: 650; }
      .snapshot-empty { margin: 0; color: var(--muted); font-size: 0.875rem; }
      .analysis-link {
        display: inline-flex;
        width: 100%;
        min-height: 44px;
        align-items: center;
        justify-content: center;
        border: 1px solid var(--accent);
        border-radius: 4px;
        background: var(--accent);
        color: var(--accent-ink);
        font-size: 0.8125rem;
        font-weight: 700;
        text-decoration: none;
        transition: background-color 160ms ease, border-color 160ms ease;
      }
      .analysis-link:hover { border-color: var(--accent-hover); background: var(--accent-hover); }
      .retention { margin: 28px 0 0; color: var(--subtle); font-size: 0.75rem; }
      .workspace { min-width: 0; }
      .workspace-header { margin-bottom: 20px; }
      h1 { margin: 0 0 4px; font-size: 1.5rem; font-weight: 650; line-height: 1.3; letter-spacing: 0; }
      .workspace-header p { margin: 0; color: var(--muted); font-size: 0.875rem; }
      .error {
        margin-bottom: 16px;
        padding: 12px 14px;
        border-left: 3px solid var(--danger);
        background: color-mix(in srgb, var(--danger) 10%, transparent);
        color: var(--danger);
        font-size: 0.875rem;
      }
      .profile-table { overflow: hidden; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); }
      .table-head, .profile-row {
        display: grid;
        grid-template-columns: minmax(0, 1fr) 128px 112px;
        align-items: center;
        gap: 16px;
      }
      .table-head {
        min-height: 40px;
        padding: 0 12px 0 16px;
        border-bottom: 1px solid var(--border);
        color: var(--subtle);
        font-size: 0.75rem;
        font-weight: 700;
        text-transform: uppercase;
      }
      .table-head span:last-child { text-align: right; }
      .profile-row {
        min-height: 64px;
        padding: 10px 12px 10px 16px;
        border-bottom: 1px solid var(--border);
        transition: background-color 160ms ease;
      }
      .profile-row:last-child { border-bottom: 0; }
      .profile-row:hover { background: var(--surface-hover); }
      .profile-name { min-width: 0; font-size: 0.9375rem; font-weight: 600; }
      .collection {
        color: var(--muted);
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
        font-size: 0.75rem;
        font-variant-numeric: tabular-nums;
      }
      .duration-control {
        position: relative;
        display: block;
        width: 104px;
      }
      .duration-control input {
        width: 100%;
        min-height: 44px;
        padding: 8px 38px 8px 12px;
        border: 1px solid var(--border);
        border-radius: 4px;
        background: var(--bg);
        color: var(--text);
        font: 600 0.8125rem/1 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
        font-variant-numeric: tabular-nums;
        transition: border-color 160ms ease, background-color 160ms ease;
      }
      .duration-control input:hover { border-color: var(--muted); }
      .duration-control input:focus-visible {
        border-color: var(--focus);
        outline: 3px solid var(--focus);
        outline-offset: 2px;
      }
      .duration-unit {
        position: absolute;
        top: 50%;
        right: 12px;
        color: var(--muted);
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
        font-size: 0.75rem;
        pointer-events: none;
        transform: translateY(-50%);
      }
      .sr-only {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0, 0, 0, 0);
        white-space: nowrap;
        border: 0;
      }
      .capture-button {
        min-width: 100px;
        min-height: 44px;
        padding: 8px 14px;
        border: 1px solid var(--accent);
        border-radius: 4px;
        background: transparent;
        color: var(--accent-hover);
        font: inherit;
        font-size: 0.8125rem;
        font-weight: 700;
        cursor: pointer;
        transition: background-color 160ms ease, color 160ms ease, opacity 160ms ease;
      }
      .capture-button:hover { background: var(--accent); color: var(--accent-ink); }
      .capture-button:active { background: var(--accent-hover); color: var(--accent-ink); }
      .capture-button:disabled { cursor: wait; opacity: 0.55; }
      @media (max-width: 760px) {
        .appbar-inner, .shell { width: min(100% - 32px, 1080px); }
        .appbar-inner { flex-wrap: wrap; padding: 8px 0; }
        .appnav { width: 100%; margin-left: 0; border-top: 1px solid var(--border); }
        .shell { grid-template-columns: 1fr; gap: 28px; padding-top: 28px; }
        .sidebar { padding: 0 0 24px; border-right: 0; border-bottom: 1px solid var(--border); }
        .state { margin-bottom: 20px; }
        .analysis-link { width: auto; padding: 0 16px; }
        .retention { margin-top: 16px; }
        .table-head { display: none; }
        .profile-row { grid-template-columns: minmax(0, 1fr) 112px; gap: 4px 12px; }
        .profile-name { align-self: end; }
        .collection { grid-column: 1; align-self: start; }
        .capture-button { grid-column: 2; grid-row: 1 / span 2; }
      }
      @media (prefers-color-scheme: light) {
        :root {
          color-scheme: light;
          --bg: #f5f7f6;
          --bar: #ffffff;
          --surface: #ffffff;
          --surface-hover: #f7faf8;
          --border: #d5ded8;
          --text: #17201b;
          --muted: #5e6a62;
          --subtle: #68746c;
          --accent: #258255;
          --accent-hover: #146b42;
          --accent-ink: #ffffff;
          --danger: #b42318;
          --focus: #1559c5;
        }
      }
      @media (prefers-reduced-motion: reduce) {
        *, *::before, *::after { transition: none !important; }
      }
    </style>
  </head>
  <body>
    <header class="appbar">
      <div class="appbar-inner">
        <a class="brand" href="/">{{.Name}}</a>
        <span class="divider" aria-hidden="true"></span>
        <span class="product">Runtime profiler</span>
        <nav class="appnav" aria-label="Diagnostics navigation">
          <a href="/debug/pprof/">Raw endpoints</a>
          <a href="/docs">API docs</a>
        </nav>
      </div>
    </header>
    <main class="shell">
      <aside class="sidebar" aria-label="Profiler status">
        <div class="state"><span class="state-dot" aria-hidden="true"></span>Profiling enabled</div>
        <p class="section-label">Current snapshot</p>
        {{if .Current}}
        <strong class="snapshot-name">{{.Current.Label}}</strong>
        <a class="analysis-link" href="/debug/pprof/ui/view/flamegraph">Open analysis</a>
        {{else}}
        <p class="snapshot-empty">No snapshot captured</p>
        {{end}}
        <p class="retention">New captures replace the current snapshot.</p>
      </aside>
      <section class="workspace" aria-labelledby="capture-title">
        <header class="workspace-header">
          <h1 id="capture-title">Capture profile</h1>
          <p>Live process snapshots for Google pprof analysis.</p>
        </header>
        {{if .Error}}<div class="error" role="alert">{{.Error}}</div>{{end}}
        <div class="profile-table" role="list" aria-label="Runtime profiles">
          <div class="table-head" aria-hidden="true">
            <span>Profile</span><span>Collection</span><span>Action</span>
          </div>
          {{range .Profiles}}
          <form class="profile-row" method="post" action="/debug/pprof/ui/capture" data-capture-form role="listitem">
            <input type="hidden" name="profile" value="{{.Value}}">
            <span class="profile-name">{{.Label}}</span>
            {{if eq .Value "cpu"}}
            <label class="duration-control">
              <span class="sr-only">CPU capture duration in seconds</span>
              <input name="seconds" type="number" min="1" max="30" value="{{$.Seconds}}" inputmode="numeric">
              <span class="duration-unit" aria-hidden="true">sec</span>
            </label>
            {{else}}
            <span class="collection">{{.Collection}}</span>
            {{end}}
            <button class="capture-button" type="submit" data-submit data-command>Capture</button>
          </form>
          {{end}}
        </div>
      </section>
    </main>
    <script>
      const forms = document.querySelectorAll('[data-capture-form]');
      const buttons = document.querySelectorAll('[data-submit]');
      forms.forEach((form) => form.addEventListener('submit', () => {
        buttons.forEach((button) => { button.disabled = true; });
        form.querySelector('[data-command]').textContent = 'Capturing...';
      }));
    </script>
  </body>
</html>
`))
)

type pprofUI struct {
	mu        sync.RWMutex
	driverMu  sync.Mutex
	captureMu sync.Mutex
	name      string
	current   *pprofWebProfile
	capture   func(context.Context, string, time.Duration) (*profile.Profile, error)
}

type pprofWebProfile struct {
	Label    string
	handlers map[string]http.Handler
}

type pprofLanding struct {
	Name     string
	Profiles []pprofProfileOption
	Current  *pprofWebProfile
	Seconds  int
	Error    string
}

type pprofProfileOption struct {
	Value      string
	Label      string
	Collection string
}

func newPprofUI(name string) *pprofUI {
	name = strings.TrimSpace(name)
	return &pprofUI{name: name, capture: captureRuntimeProfile}
}

func (ui *pprofUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		ui.serveLanding(w, http.StatusOK, "")
	case r.URL.Path == "/capture" && r.Method == http.MethodPost:
		ui.captureProfile(w, r)
	case r.URL.Path == "/view":
		http.Redirect(w, r, UIPath+"/view/", http.StatusMovedPermanently)
	case strings.HasPrefix(r.URL.Path, "/view/"):
		ui.serveProfile(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (ui *pprofUI) serveLanding(w http.ResponseWriter, status int, message string) {
	options := make([]pprofProfileOption, 0, len(pprofProfiles))
	for _, value := range []string{"heap", "allocs", "goroutine", "cpu", "mutex", "block", "threadcreate"} {
		options = append(options, pprofProfileOption{
			Value:      value,
			Label:      pprofProfiles[value],
			Collection: "Snapshot",
		})
	}
	ui.mu.RLock()
	current := ui.current
	ui.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pprofLandingTemplate.Execute(w, pprofLanding{
		Name:     ui.name,
		Profiles: options,
		Current:  current,
		Seconds:  defaultCPUSeconds,
		Error:    message,
	})
}

func (ui *pprofUI) captureProfile(w http.ResponseWriter, r *http.Request) {
	if !ui.captureMu.TryLock() {
		ui.serveLanding(w, http.StatusConflict, "Another profile capture is already running.")
		return
	}
	defer ui.captureMu.Unlock()

	if err := r.ParseForm(); err != nil {
		ui.serveLanding(w, http.StatusBadRequest, "Unable to read the capture request.")
		return
	}
	name := strings.TrimSpace(r.FormValue("profile"))
	if _, ok := pprofProfiles[name]; !ok {
		ui.serveLanding(w, http.StatusBadRequest, "Unknown runtime profile.")
		return
	}
	duration, err := captureDuration(name, r.FormValue("seconds"))
	if err != nil {
		ui.serveLanding(w, http.StatusBadRequest, err.Error())
		return
	}
	captured, err := ui.capture(r.Context(), name, duration)
	if err != nil {
		ui.serveLanding(w, http.StatusInternalServerError, fmt.Sprintf("Capture failed: %v", err))
		return
	}
	handlers, err := ui.webHandlers(captured)
	if err != nil {
		ui.serveLanding(w, http.StatusInternalServerError, fmt.Sprintf("Analysis failed: %v", err))
		return
	}

	ui.mu.Lock()
	ui.current = &pprofWebProfile{Label: pprofProfiles[name], handlers: handlers}
	ui.mu.Unlock()
	http.Redirect(w, r, UIPath+"/view/flamegraph", http.StatusSeeOther)
}

func (ui *pprofUI) serveProfile(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/view")
	ui.driverMu.Lock()
	defer ui.driverMu.Unlock()
	ui.mu.RLock()
	current := ui.current
	ui.mu.RUnlock()
	if current == nil {
		http.Redirect(w, r, UIPath+"/", http.StatusSeeOther)
		return
	}
	handler := current.handlers[path]
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	request := r.Clone(r.Context())
	request.URL.Path = path
	handler.ServeHTTP(w, request)
}

func (ui *pprofUI) webHandlers(captured *profile.Profile) (map[string]http.Handler, error) {
	ui.driverMu.Lock()
	defer ui.driverMu.Unlock()
	var handlers map[string]http.Handler
	err := driver.PProf(&driver.Options{
		Flagset: &embeddedPprofFlags{},
		Fetch:   staticProfileFetcher{profile: captured},
		UI:      quietPprofUI{},
		HTTPServer: func(args *driver.HTTPServerArgs) error {
			handlers = maps.Clone(args.Handlers)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	if len(handlers) == 0 {
		return nil, fmt.Errorf("pprof returned no web handlers")
	}
	return handlers, nil
}

func captureRuntimeProfile(ctx context.Context, name string, duration time.Duration) (*profile.Profile, error) {
	var output bytes.Buffer
	if name == "cpu" {
		if err := runtimepprof.StartCPUProfile(&output); err != nil {
			return nil, err
		}
		timer := time.NewTimer(duration)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			runtimepprof.StopCPUProfile()
			return nil, ctx.Err()
		case <-timer.C:
			runtimepprof.StopCPUProfile()
		}
	} else {
		runtimeProfile := runtimepprof.Lookup(name)
		if runtimeProfile == nil {
			return nil, fmt.Errorf("runtime profile %q is unavailable", name)
		}
		if err := runtimeProfile.WriteTo(&output, 0); err != nil {
			return nil, err
		}
	}
	captured, err := profile.ParseData(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	if len(captured.Sample) == 0 {
		return nil, fmt.Errorf("profile contains no samples")
	}
	return captured, nil
}

func captureDuration(name, raw string) (time.Duration, error) {
	if name != "cpu" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds < 1 || seconds > maximumCPUSeconds {
		return 0, fmt.Errorf("CPU duration must be between 1 and %d seconds", maximumCPUSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

type staticProfileFetcher struct {
	profile *profile.Profile
}

func (fetcher staticProfileFetcher) Fetch(string, time.Duration, time.Duration) (*profile.Profile, string, error) {
	return fetcher.profile.Copy(), pprofProfileSource, nil
}

type embeddedPprofFlags struct{}

func (*embeddedPprofFlags) Bool(name string, value bool, _ string) *bool {
	if name == "no_browser" {
		value = true
	}
	return &value
}

func (*embeddedPprofFlags) Int(_ string, value int, _ string) *int { return &value }

func (*embeddedPprofFlags) Float64(_ string, value float64, _ string) *float64 { return &value }

func (*embeddedPprofFlags) String(name, value, _ string) *string {
	if name == "http" {
		value = "localhost:0"
	}
	return &value
}

func (*embeddedPprofFlags) StringList(_, value, _ string) *[]*string {
	return &[]*string{&value}
}

func (*embeddedPprofFlags) ExtraUsage() string    { return "" }
func (*embeddedPprofFlags) AddExtraUsage(string)  {}
func (*embeddedPprofFlags) Parse(func()) []string { return []string{pprofProfileSource} }

type quietPprofUI struct{}

func (quietPprofUI) ReadLine(string) (string, error)     { return "", io.EOF }
func (quietPprofUI) Print(...interface{})                {}
func (quietPprofUI) PrintErr(...interface{})             {}
func (quietPprofUI) IsTerminal() bool                    { return false }
func (quietPprofUI) WantBrowser() bool                   { return false }
func (quietPprofUI) SetAutoComplete(func(string) string) {}
