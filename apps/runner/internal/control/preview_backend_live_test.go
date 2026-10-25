package control

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestPreviewNativeRuntimeFixture(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows Job fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", args...)
		hideServiceWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", root)
	git("-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "native preview fixture")
	head := git("-C", root, "rev-parse", "HEAD")
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err = os.WriteFile(filepath.Join(bin, "codex.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	portFile := filepath.Join(t.TempDir(), "port")
	controller := new(executor.ProcessControl)
	done := make(chan error, 1)
	go func() {
		_, err := executor.Run(ctx, executor.Plan{ProcessControl: controller, Runtime: "CODEX", Model: "fixture", Workdir: root, BaseCommit: head, Objective: "native-preview-fixture:" + portFile, AuthorizationMode: "READ_ONLY"}, nil)
		done <- err
	}()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("native fixture did not publish port")
		case <-ticker.C:
			contents, err := os.ReadFile(portFile)
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(string(contents))
			if err != nil {
				t.Fatal(err)
			}
			address, err := controller.OwnedLoopbackAddress(ctx, uint16(port))
			if err != nil {
				continue
			}
			response, err := http.Get("http://" + address + "/binary")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != string([]byte{0, 255, 1, 2, 0, 128}) {
				t.Fatalf("native fixture response: body=%v err=%v", body, err)
			}
			return
		}
	}
}

// The copied executable is used only by the opt-in composed-backend fixture.
// Running it through executor.Run exercises the real suspended launch and Job.
func TestMain(m *testing.M) {
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"), "codex") && len(os.Args) > 1 && os.Args[1] == "exec" {
		prompt, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		const prefix = "native-preview-fixture:"
		if !strings.HasPrefix(string(prompt), prefix) {
			os.Exit(2)
		}
		portFile := strings.TrimPrefix(string(prompt), prefix)
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		if os.WriteFile(portFile, []byte(strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)), 0600) != nil {
			os.Exit(2)
		}
		fmt.Println(`{"type":"thread.started","thread_id":"native-preview-fixture"}`)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/binary":
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write([]byte{0, 255, 1, 2, 0, 128})
			case "/range":
				http.ServeContent(w, r, "fixture.txt", time.Unix(1, 0), strings.NewReader("0123456789abcdefghijklmnopqrstuvwxyz"))
			case "/headers":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(r.Header)
			case "/sse":
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
				_, _ = io.WriteString(w, "data: second\n\n")
				w.(http.Flusher).Flush()
			case "/ws":
				if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					http.Error(w, "upgrade required", 400)
					return
				}
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
				_ = rw.Flush()
				for {
					var header [2]byte
					if _, err := io.ReadFull(rw, header[:]); err != nil {
						return
					}
					n := int(header[1] & 127)
					if n > 125 {
						return
					}
					var mask [4]byte
					if header[1]&128 != 0 {
						if _, err := io.ReadFull(rw, mask[:]); err != nil {
							return
						}
					}
					data := make([]byte, n)
					if _, err := io.ReadFull(rw, data); err != nil {
						return
					}
					if header[1]&128 != 0 {
						for i := range data {
							data[i] ^= mask[i%4]
						}
					}
					if header[0]&15 == 8 {
						return
					}
					if _, err := rw.Write(append([]byte{header[0], byte(n)}, data...)); err != nil {
						return
					}
					if rw.Flush() != nil {
						return
					}
				}
			default:
				_, _ = io.WriteString(w, "preview-native-ok")
			}
		})
		if http.Serve(listener, handler) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
