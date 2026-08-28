package app

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
	pprofUIPath        = pprofPath + "/ui"
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
    <title>Runtime profiles</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #101310;
        --surface: #181c19;
        --border: #303731;
        --text: #f2f5f2;
        --muted: #a9b3ab;
        --accent: #65b98a;
        --accent-strong: #79cca0;
        --danger: #ff8a80;
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
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
        font-size: 16px;
        line-height: 1.5;
      }
      main { width: min(720px, calc(100% - 40px)); margin: 0 auto; padding: 48px 0; }
      header { display: flex; align-items: start; justify-content: space-between; gap: 24px; margin-bottom: 28px; }
      h1 { margin: 0 0 6px; font-size: 1.5rem; line-height: 1.25; letter-spacing: 0; }
      p { margin: 0; color: var(--muted); }
	  nav { display: flex; flex-wrap: wrap; gap: 8px 16px; font-size: 0.875rem; }
	  a { color: var(--accent-strong); text-underline-offset: 3px; }
	  a:hover { color: var(--text); }
	  nav a { display: inline-flex; min-height: 44px; align-items: center; }
	  a:focus-visible, button:focus-visible, input:focus-visible {
        outline: 3px solid var(--focus);
        outline-offset: 3px;
      }
	  .tool { padding: 24px 0; border-block: 1px solid var(--border); }
	  .profiles { display: grid; gap: 8px; }
	  .profile-action { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
	  .profile-action.cpu { grid-template-columns: minmax(0, 1fr) 152px; }
	  .profile-button {
		display: flex;
		width: 100%;
		min-height: 52px;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
		padding: 10px 14px;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-weight: 700;
		cursor: pointer;
		transition: background-color 160ms ease, border-color 160ms ease, color 160ms ease;
	  }
	  .profile-button:hover { border-color: var(--accent); }
	  .profile-button:active { background: color-mix(in srgb, var(--accent) 14%, var(--surface)); }
	  .profile-button:disabled { cursor: wait; opacity: 0.65; }
	  .profile-command { color: var(--accent-strong); font-size: 0.75rem; text-transform: uppercase; }
	  .duration { display: grid; gap: 4px; }
	  .field-label { color: var(--muted); font-size: 0.75rem; font-weight: 700; }
	  input[type="number"] {
		width: 100%;
		min-height: 46px;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--surface);
		color: var(--text);
		font: inherit;
	  }
	  input[type="number"]:hover:not(:disabled) { border-color: var(--accent); }
	  input[type="number"]:disabled { cursor: not-allowed; }
      .unit { position: relative; }
      .unit input { padding-right: 40px; }
      .unit span { position: absolute; right: 12px; bottom: 12px; color: var(--muted); font-size: 0.8125rem; }
	  .current { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 16px; color: var(--muted); font-size: 0.8125rem; }
	  .open {
		display: inline-flex;
		min-height: 46px;
        align-items: center;
        justify-content: center;
        padding: 10px 16px;
        border: 1px solid var(--accent);
        border-radius: 4px;
        background: var(--accent);
        color: #0d1a12;
        font: inherit;
        font-weight: 700;
        text-decoration: none;
		transition: background-color 160ms ease, border-color 160ms ease;
	  }
	  .open:hover { border-color: var(--accent-strong); background: var(--accent-strong); color: #0d1a12; }
	  .current strong { color: var(--text); }
      .error {
        margin-bottom: 16px;
        padding: 12px 14px;
        border-left: 3px solid var(--danger);
        background: color-mix(in srgb, var(--danger) 10%, transparent);
        color: var(--danger);
        font-size: 0.875rem;
      }
      footer { margin-top: 20px; }
	  @media (max-width: 620px) {
		main { width: min(100% - 32px, 720px); padding: 32px 0; }
		header { display: grid; }
		.tool { padding: 20px 0; }
		.profile-action.cpu { grid-template-columns: minmax(0, 1fr) 120px; }
		.open { width: 100%; }
		.current { align-items: stretch; flex-direction: column; }
      }
      @media (prefers-color-scheme: light) {
        :root {
          color-scheme: light;
          --bg: #f3f6f4;
          --surface: #ffffff;
          --border: #cdd8d1;
          --text: #18201c;
          --muted: #5d6962;
          --accent: #248354;
          --accent-strong: #146b42;
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
    <main>
      <header>
        <div>
          <h1>Runtime profiles</h1>
          <p>Capture a runtime snapshot and inspect it with Google pprof.</p>
        </div>
        <nav aria-label="Diagnostics navigation">
          <a href="/debug/pprof/">Endpoints</a>
          <a href="/docs">API docs</a>
        </nav>
      </header>
      {{if .Error}}<div class="error" role="alert">{{.Error}}</div>{{end}}
	  <section class="tool" aria-label="Runtime profile capture">
		{{if .Current}}
		<div class="current">
		  <span>Current <strong>{{.Current.Label}}</strong></span>
		  <a class="open" href="/debug/pprof/ui/view/flamegraph">Open analysis</a>
		</div>
		{{end}}
		<div class="profiles">
		  {{range .Profiles}}
		  <form class="profile-action{{if eq .Value "cpu"}} cpu{{end}}" method="post" action="/debug/pprof/ui/capture" data-capture-form>
			<input type="hidden" name="profile" value="{{.Value}}">
			<button class="profile-button" type="submit" data-submit>
			  <span>{{.Label}}</span>
			  <span class="profile-command" data-command>Capture</span>
			</button>
			{{if eq .Value "cpu"}}
			<label class="duration" for="seconds">
			  <span class="field-label">Duration</span>
			  <span class="unit">
				<input id="seconds" name="seconds" type="number" min="1" max="30" value="{{$.Seconds}}" inputmode="numeric">
				<span aria-hidden="true">sec</span>
			  </span>
			</label>
			{{end}}
		  </form>
		  {{end}}
		</div>
	  </section>
      <footer><p>Only one snapshot is retained. A new capture replaces the current analysis.</p></footer>
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
	current   *pprofWebProfile
	capture   func(context.Context, string, time.Duration) (*profile.Profile, error)
}

type pprofWebProfile struct {
	Label    string
	handlers map[string]http.Handler
}

type pprofLanding struct {
	Profiles []pprofProfileOption
	Current  *pprofWebProfile
	Seconds  int
	Error    string
}

type pprofProfileOption struct {
	Value string
	Label string
}

func newPprofUI() *pprofUI {
	return &pprofUI{capture: captureRuntimeProfile}
}

func (ui *pprofUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		ui.serveLanding(w, http.StatusOK, "")
	case r.URL.Path == "/capture" && r.Method == http.MethodPost:
		ui.captureProfile(w, r)
	case r.URL.Path == "/view":
		http.Redirect(w, r, pprofUIPath+"/view/", http.StatusMovedPermanently)
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
			Value: value,
			Label: pprofProfiles[value],
		})
	}
	ui.mu.RLock()
	current := ui.current
	ui.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pprofLandingTemplate.Execute(w, pprofLanding{
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
	http.Redirect(w, r, pprofUIPath+"/view/flamegraph", http.StatusSeeOther)
}

func (ui *pprofUI) serveProfile(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/view")
	ui.driverMu.Lock()
	defer ui.driverMu.Unlock()
	ui.mu.RLock()
	current := ui.current
	ui.mu.RUnlock()
	if current == nil {
		http.Redirect(w, r, pprofUIPath+"/", http.StatusSeeOther)
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
