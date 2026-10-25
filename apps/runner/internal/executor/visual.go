package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// VISUAL argv is a snapshot-relative HTML artifact or an HTTP(S) preview URL,
// optionally followed by viewport width and height. Without dimensions both
// desktop and mobile acceptance views are captured. Capture is not a verdict.
type VisualScreenshot struct {
	ReferenceSHA256 string `json:"reference_sha256,omitempty"`
	Comparison      string `json:"comparison,omitempty"`
	Name            string `json:"name"`
	Data            []byte `json:"data"`
	SHA256          string `json:"sha256"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	Route           string `json:"route"`
}

func visualTarget(argv []string, workdir string) (string, [][2]int, error) {
	if len(argv) != 1 && len(argv) != 3 {
		return "", nil, ErrUnsupported
	}
	views := [][2]int{{1440, 900}, {390, 844}}
	if len(argv) == 3 {
		w, e := strconv.Atoi(argv[1])
		h, f := strconv.Atoi(argv[2])
		if e != nil || f != nil || w < 1 || h < 1 || w > 4096 || h > 4096 {
			return "", nil, ErrUnsupported
		}
		views = [][2]int{{w, h}}
	}
	u, err := url.Parse(argv[0])
	if err != nil {
		return "", nil, ErrUnsupported
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		if u.Host == "" || u.User != nil {
			return "", nil, ErrUnsupported
		}
		return u.String(), views, nil
	}
	if u.Scheme != "" || filepath.IsAbs(argv[0]) {
		return "", nil, ErrUnsupported
	}
	p, err := filepath.EvalSymlinks(filepath.Join(workdir, argv[0]))
	if err != nil {
		return "", nil, err
	}
	rel, err := filepath.Rel(workdir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, ErrUnsupported
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", nil, ErrUnsupported
	}
	path := filepath.ToSlash(p)
	if runtime.GOOS == "windows" {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String(), views, nil
}

func installedVisualBrowser() (string, error) {
	var candidates []string
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"), filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
	}
	for _, name := range []string{"msedge", "chrome", "google-chrome", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func runVisualVerification(ctx context.Context, p VerificationPlan, workdir string, out VerificationObservation) VerificationObservation {
	finish := func(reason string) VerificationObservation {
		if ctx.Err() != nil {
			reason = "TIMEOUT_OR_CANCELLED"
		}
		out.Reason = reason
		out.Unverified = []string{"VISUAL_REVIEW_REQUIRED", "TOOL_CALIBRATION_NOT_ESTABLISHED"}
		out.EndedAt = time.Now().UTC()
		return out
	}
	target, views, err := visualTarget(p.Argv, workdir)
	if err != nil {
		return finish("VISUAL_TARGET_UNAVAILABLE")
	}
	browser, err := installedVisualBrowser()
	if err != nil {
		return finish("BROWSER_UNAVAILABLE")
	}
	profile, err := os.MkdirTemp("", "verification-browser-")
	if err != nil {
		return finish("BROWSER_UNAVAILABLE")
	}
	defer os.RemoveAll(profile)
	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, browser, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	hideWindow(cmd)
	configureCancellation(cmd)
	cmd.Env = verificationEnvironment()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		cancel()
		return finish("BROWSER_UNAVAILABLE")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { cancel(); <-done }()
	var port string
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for port == "" {
		data, e := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if e == nil {
			port = strings.Split(string(data), "\n")[0]
			break
		}
		select {
		case <-ctx.Done():
			return finish("TIMEOUT_OR_CANCELLED")
		case <-tick.C:
		}
	}
	if _, err = strconv.ParseUint(port, 10, 16); err != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json", nil)
	resp, err := client.Do(req)
	if err != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&targets)
	resp.Body.Close()
	if err != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	var wsURL string
	for _, t := range targets {
		if t.Type == "page" {
			wsURL = t.URL
			break
		}
	}
	ws, err := dialVisualCDP(ctx, wsURL)
	if err != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	defer ws.conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = ws.conn.Close() })
	defer stopClose()
	var version struct {
		Product string `json:"product"`
	}
	if ws.call("Browser.getVersion", nil, &version) != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	out.BrowserProvider = version.Product
	if ws.call("Page.enable", nil, nil) != nil || ws.call("Runtime.enable", nil, nil) != nil {
		return finish("BROWSER_CAPTURE_FAILED")
	}
	for i, v := range views {
		if ws.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": v[0], "height": v[1], "deviceScaleFactor": 1, "mobile": false}, nil) != nil {
			return finish("BROWSER_CAPTURE_FAILED")
		}
		var nav struct {
			ErrorText string `json:"errorText"`
		}
		ws.loaded = false
		if ws.call("Page.navigate", map[string]any{"url": target}, &nav) != nil || nav.ErrorText != "" {
			return finish("VISUAL_NAVIGATION_FAILED")
		}
		for {
			var state struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if ws.call("Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true}, &state) != nil {
				return finish("BROWSER_CAPTURE_FAILED")
			}
			if state.Result.Value == "complete" && ws.loaded {
				break
			}
			select {
			case <-ctx.Done():
				return finish("TIMEOUT_OR_CANCELLED")
			case <-tick.C:
			}
		}
		var metrics struct {
			Result struct {
				Value struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"value"`
			} `json:"result"`
		}
		if ws.call("Runtime.evaluate", map[string]any{"expression": "({width:innerWidth,height:innerHeight})", "returnByValue": true}, &metrics) != nil || metrics.Result.Value.Width != v[0] || metrics.Result.Value.Height != v[1] {
			return finish("VIEWPORT_MISMATCH")
		}
		// Wait for fonts and two paint frames before taking the viewport image.
		if ws.call("Runtime.evaluate", map[string]any{"expression": "document.fonts.ready.then(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))))", "awaitPromise": true}, nil) != nil {
			return finish("BROWSER_CAPTURE_FAILED")
		}
		var shot struct {
			Data string `json:"data"`
		}
		if ws.call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}, &shot) != nil {
			return finish("BROWSER_CAPTURE_FAILED")
		}
		data, e := base64.StdEncoding.DecodeString(shot.Data)
		if e != nil || len(data) > 4<<20 {
			return finish("SCREENSHOT_INVALID")
		}
		cfg, e := png.DecodeConfig(bytes.NewReader(data))
		if e != nil || cfg.Width != v[0] || cfg.Height != v[1] {
			return finish("VIEWPORT_MISMATCH")
		}
		h := sha256.Sum256(data)
		screenshot := VisualScreenshot{Name: fmt.Sprintf("viewport-%d-%dx%d.png", i+1, v[0], v[1]), Data: data, SHA256: hex.EncodeToString(h[:]), Width: v[0], Height: v[1], Route: p.Argv[0]}
		if p.ReportPath != "" {
			// References are frozen in the pinned snapshot, never taken from the
			// delivery workspace. Multiple viewports use a reference directory.
			ref := p.ReportPath
			if len(views) > 1 {
				ref = filepath.Join(ref, screenshot.Name)
			}
			reference, _, e := visualTarget([]string{ref}, workdir)
			if e != nil || !strings.HasPrefix(reference, "file:") {
				return finish("VISUAL_REFERENCE_UNAVAILABLE")
			}
			u, e := url.Parse(reference)
			if e != nil {
				return finish("VISUAL_REFERENCE_UNAVAILABLE")
			}
			path := filepath.FromSlash(u.Path)
			if runtime.GOOS == "windows" {
				path = strings.TrimPrefix(path, string(filepath.Separator))
			}
			f, e := os.Open(path)
			if e != nil {
				return finish("VISUAL_REFERENCE_UNAVAILABLE")
			}
			refData, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
			f.Close()
			if e != nil || len(refData) > 4<<20 {
				return finish("VISUAL_REFERENCE_UNAVAILABLE")
			}
			screenshot.ReferenceSHA256, screenshot.Comparison = compareVisualReference(data, refData)
			if screenshot.Comparison == "INVALID" {
				return finish("VISUAL_REFERENCE_UNAVAILABLE")
			}
		}
		out.Screenshots = append(out.Screenshots, screenshot)
	}
	if ws.consoleError {
		return finish("BROWSER_CONSOLE_ERROR")
	}
	// No calibration or reviewer result is inferred from successful capture.
	return finish("VISUAL_REVIEW_REQUIRED")
}

var errVisualCDP = errors.New("visual browser protocol unavailable")

func compareVisualReference(actual, reference []byte) (string, string) {
	h := sha256.Sum256(reference)
	digest := hex.EncodeToString(h[:])
	for _, data := range [][]byte{actual, reference} {
		cfg, e := png.DecodeConfig(bytes.NewReader(data))
		if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
			return digest, "INVALID"
		}
	}
	a, e := png.Decode(bytes.NewReader(actual))
	if e != nil {
		return digest, "INVALID"
	}
	r, e := png.Decode(bytes.NewReader(reference))
	if e != nil {
		return digest, "INVALID"
	}
	if a.Bounds() != r.Bounds() {
		return digest, "DIFFERENT"
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			rr, rg, rb, ra := r.At(x, y).RGBA()
			if ar != rr || ag != rg || ab != rb || aa != ra {
				return digest, "DIFFERENT"
			}
		}
	}
	return digest, "IDENTICAL"
}
