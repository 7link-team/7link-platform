//go:build windows

package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTerminalNativeConPTYRunsInWorkspaceWithoutInheritedSecrets(t *testing.T) {
	t.Setenv("TERMINAL_SECRET_TEST", "must-not-inherit")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	term, err := OpenTerminal(ctx, dir, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	drained := make(chan struct{})
	ready := make(chan struct{})
	var readyOnce sync.Once
	go func() {
		defer close(drained)
		buf := make([]byte, 4096)
		var received bytes.Buffer
		for {
			n, e := term.Output.Read(buf)
			if n > 0 {
				received.Write(buf[:n])
				t.Logf("PTY: %q", buf[:n])
				if bytes.Contains(received.Bytes(), []byte(">")) {
					readyOnce.Do(func() { close(ready) })
				}
			}
			if e != nil {
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("no prompt")
	}
	if err = term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	// Wait for process exit, not pipe acceptance: fixes the previous proof race.
	_, err = io.WriteString(term.Input, "echo proof>proof.txt\rif defined TERMINAL_SECRET_TEST echo leaked>secret.txt\rexit\r")
	if err != nil {
		t.Fatal(err)
	}
	if err = term.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "proof.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "proof" {
		t.Fatalf("workspace proof: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.txt")); !os.IsNotExist(err) {
		t.Fatal("inherited secret environment")
	}
	term.Close()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("output did not close")
	}
}

func TestTerminalNativeSaturatedOutputCancellationAndResizeJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	term, err := OpenTerminal(ctx, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if _, err = io.WriteString(term.Input, "for /L %i in (1,1,1000000) do @echo "+strings.Repeat("x", 1024)+"\r"); err != nil {
		t.Fatal(err)
	}
	// Leave output unread while the native child fills its console pipe.
	time.Sleep(100 * time.Millisecond)
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, term.Output); close(drained) }()
	resized := make(chan struct{})
	go func() {
		defer close(resized)
		for i := 0; i < 1000; i++ {
			if term.Resize(80, 24) != nil {
				return
			}
		}
	}()
	cancel()
	closed := make(chan struct{})
	go func() { _ = term.Close(); close(closed) }()
	for _, done := range []<-chan struct{}{closed, drained, resized} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("saturated console cancellation failed to join")
		}
	}
	waitCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	_ = term.Wait(waitCtx)
	if waitCtx.Err() != nil {
		t.Fatal("native process remained alive")
	}
}
