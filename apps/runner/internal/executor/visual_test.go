package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisualTargetRejectsEscapesAndInvalidViewport(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{}, {"../escape.html"}, {"file:///outside.html"}, {"https://user:pass@example.invalid"}, {"index.html", "0", "900"}, {"index.html", "4097", "900"}, {"index.html", "390"}} {
		if _, _, e := visualTarget(args, root); e == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	url, views, e := visualTarget([]string{"index.html"}, root)
	if e != nil || !strings.HasPrefix(url, "file:") || len(views) != 2 || views[1] != [2]int{390, 844} {
		t.Fatal(url, views, e)
	}
}

func TestVisualCaptureInstalledBrowserActualDesktopAndMobile(t *testing.T) {
	if _, e := installedVisualBrowser(); e != nil {
		t.Skip("BROWSER_UNAVAILABLE: no installed Chrome/Edge/Chromium")
	}
	root := t.TempDir()
	// A viewport-dependent background makes cropping a desktop image observably
	// different from rendering the requested mobile viewport.
	html := `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><style>html,body{margin:0;background:rgb(255,0,0)}@media(max-width:420px){html,body{background:rgb(0,0,255)}}</style><h1>Viewport fixture</h1>`
	if e := os.WriteFile(filepath.Join(root, "index.html"), []byte(html), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	o := runVisualVerification(ctx, VerificationPlan{Argv: []string{"index.html"}}, root, VerificationObservation{StartedAt: time.Now().UTC()})
	if o.Reason != "VISUAL_REVIEW_REQUIRED" || len(o.Screenshots) != 2 || o.BrowserProvider == "" || o.ExitCode != nil {
		t.Fatalf("capture=%+v", withoutVisualBytes(o))
	}
	for i, s := range o.Screenshots {
		img, e := png.Decode(bytes.NewReader(s.Data))
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(s.Data)
		if hex.EncodeToString(h[:]) != s.SHA256 || img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
			t.Fatal("image metadata mismatch")
		}
		r, _, b, _ := img.At(s.Width-1, s.Height-1).RGBA()
		if (i == 0 && r != 65535) || (i == 1 && b != 65535) {
			t.Fatalf("viewport was cropped instead of rendered: i=%d rgb=%d/%d", i, r, b)
		}
		t.Logf("Actual %s screenshot %dx%d bytes=%d sha256=%s", o.BrowserProvider, s.Width, s.Height, len(s.Data), s.SHA256)
	}
}

func withoutVisualBytes(o VerificationObservation) VerificationObservation {
	for i := range o.Screenshots {
		o.Screenshots[i].Data = nil
	}
	return o
}

func TestVisualRunVerificationCapturesPinnedSnapshot(t *testing.T) {
	if _, e := installedVisualBrowser(); e != nil {
		t.Skip("BROWSER_UNAVAILABLE")
	}
	p := runtimeFixture(t)
	if e := os.WriteFile(filepath.Join(p.Workdir, "index.html"), []byte("<!doctype html><title>pinned</title><p>Committed artifact</p>"), 0600); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", p.Workdir}, args...)...)
		hideWindow(cmd)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v", args, e)
		}
		return strings.TrimSpace(string(b))
	}
	git("add", "index.html")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "visual fixture")
	head := git("rev-parse", "HEAD")
	if e := os.Remove(filepath.Join(p.Workdir, "index.html")); e != nil {
		t.Fatal(e)
	}
	plan := VerificationPlan{ID: "1", Nonce: "nonce", ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "VISUAL", SourceWorkdir: p.Workdir, HeadCommit: head, Argv: []string{"index.html", "320", "240"}, TimeoutSec: 40}
	o, e := RunVerification(t.Context(), plan)
	if e != nil || o.Reason != "VISUAL_REVIEW_REQUIRED" || o.HeadCommit != head || len(o.Screenshots) != 1 {
		t.Fatalf("snapshot capture: %+v %v", withoutVisualBytes(o), e)
	}
	if _, e = os.Stat(filepath.Join(p.Workdir, "index.html")); !os.IsNotExist(e) {
		t.Fatal("dirty source modified")
	}
}

func TestVisualCaptureReportsBrowserMissingExplicitly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ProgramFiles", t.TempDir())
	t.Setenv("ProgramFiles(x86)", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, e := installedVisualBrowser(); e == nil {
		t.Skip("system browser remains installed outside overridable detection locations")
	}
	o := runVisualVerification(t.Context(), VerificationPlan{Argv: []string{"http://127.0.0.1/"}}, t.TempDir(), VerificationObservation{})
	if o.Reason != "BROWSER_UNAVAILABLE" || len(o.Screenshots) != 0 || o.ExitCode != nil {
		t.Fatal(o)
	}
}

func TestVisualReferenceComparesDecodedPixelsWithoutCreatingVerdict(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	encode := func() []byte {
		var b bytes.Buffer
		if e := png.Encode(&b, img); e != nil {
			t.Fatal(e)
		}
		return b.Bytes()
	}
	a := encode()
	digest, result := compareVisualReference(a, a)
	h := sha256.Sum256(a)
	if result != "IDENTICAL" || digest != hex.EncodeToString(h[:]) {
		t.Fatal(digest, result)
	}
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	_, result = compareVisualReference(a, encode())
	if result != "DIFFERENT" {
		t.Fatal(result)
	}
	_, result = compareVisualReference(a, []byte("not PNG"))
	if result != "INVALID" {
		t.Fatal(result)
	}
}
