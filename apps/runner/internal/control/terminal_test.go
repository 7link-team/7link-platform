package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTerminalInputConnectionWindowAndCloseRelease(t *testing.T) {
	pending := make(chan struct{}, 256)
	shells := make([]*tunnelShell, 17)
	for i := range shells {
		shells[i] = &tunnelShell{inputs: make(chan []byte, 16), pendingInput: pending}
	}
	data := make([]byte, terminalFrameLimit)
	for _, s := range shells[:16] {
		for i := 0; i < 16; i++ {
			if !s.enqueueInput(data) {
				t.Fatal("window rejected within4MiB")
			}
		}
	}
	if shells[16].enqueueInput(data) || len(pending) != 256 {
		t.Fatal("multiple shells exceeded4MiB")
	}
	// A native write keeps its token until completion even while close discards
	// the other queued input; completing that write releases the last token.
	<-shells[0].inputs
	shells[0].stopInputs()
	if len(pending) != 241 {
		t.Fatalf("queued close accounting=%d", len(pending))
	}
	<-pending
	if shells[0].enqueueInput(data) {
		t.Fatal("input accepted after close")
	}
	if !shells[16].enqueueInput(data) {
		t.Fatal("released slots were not reusable")
	}
	for _, s := range shells {
		s.stopInputs()
		s.stopInputs()
	}
	if len(pending) != 0 {
		t.Fatalf("connection leaked%d slots", len(pending))
	}
}

func TestTerminalTunnelStreamsAndClosesWithoutReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	posted := make(chan terminalFrame, 16)
	stream := make(chan terminalFrame, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer terminal-test-token" {
			t.Error("missing tunnel authentication")
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/frames") {
			var body struct {
				SessionID string          `json:"session_id"`
				Epoch     int64           `json:"conn_epoch"`
				Frames    []terminalFrame `json:"frames"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SessionID != "runner-session" || body.Epoch != 7 {
				t.Error("missing runner fence")
			}
			for _, f := range body.Frames {
				posted <- f
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("session_id") != "runner-session" || r.URL.Query().Get("conn_epoch") != "7" {
			t.Error("missing stream fence")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case frame, ok := <-stream:
				if !ok {
					return
				}
				_ = json.NewEncoder(w).Encode(frame)
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	c.token = "terminal-test-token"
	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	closed := make(chan struct{})
	resized := make(chan [2]uint16, 1)
	var once sync.Once
	opens := 0
	open := func(_ context.Context, workspace string, columns, rows uint16) (*tunnelShell, error) {
		opens++
		if workspace != "authorized-workspace" || columns != 80 || rows != 24 {
			t.Error("incorrect native open parameters")
		}
		return &tunnelShell{input: inputW, output: outputR,
			wait: func(ctx context.Context) error {
				select {
				case <-closed:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			resize: func(c, r uint16) error { resized <- [2]uint16{c, r}; return nil },
			close: func() error {
				once.Do(func() { close(closed); inputW.Close(); inputR.Close(); outputW.Close(); outputR.Close() })
				return nil
			}}, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- terminalTunnelOnce(ctx, c, fence{MachineID: "machine", SessionID: "runner-session", Epoch: 7}, open)
	}()
	await := func(kind string) terminalFrame {
		t.Helper()
		select {
		case f := <-posted:
			if f.Kind != kind {
				t.Fatalf("frame kind = %s, want %s", f.Kind, kind)
			}
			return f
		case <-ctx.Done():
			t.Fatal("timed out waiting for " + kind)
			return terminalFrame{}
		}
	}
	stream <- terminalFrame{Kind: "open", SessionID: "terminal", Workspace: "authorized-workspace", Columns: 80, Rows: 24}
	if f := await("opened"); f.Sandboxed {
		t.Fatal("native terminal claimed sandbox isolation")
	}
	stream <- terminalFrame{Kind: "input", SessionID: "terminal", Data: []byte("echo hello\r\n")}
	input := make(chan string, 1)
	go func() { data := make([]byte, 64); n, _ := inputR.Read(data); input <- string(data[:n]) }()
	select {
	case got := <-input:
		if got != "echo hello\r\n" {
			t.Fatal("input bytes changed")
		}
	case <-ctx.Done():
		t.Fatal("input was not delivered")
	}
	stream <- terminalFrame{Kind: "resize", SessionID: "terminal", Columns: 100, Rows: 40}
	select {
	case size := <-resized:
		if size != [2]uint16{100, 40} {
			t.Fatal(size)
		}
	case <-ctx.Done():
		t.Fatal("resize was not delivered")
	}
	go func() { _, _ = outputW.Write([]byte("hello\r\n")) }()
	if frame := await("output"); string(frame.Data) != "hello\r\n" || frame.Seq != 2 {
		t.Fatal("output bytes/sequence changed")
	}
	stream <- terminalFrame{Kind: "close", SessionID: "terminal"}
	await("closed")
	close(stream)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stream did not shut down")
	}
	if opens != 1 {
		t.Fatalf("open replayed: %d", opens)
	}
}

func TestTerminalUTF8PrefixPreservesSplitRunes(t *testing.T) {
	want := []byte("prompt 中文 😀\r\n")
	for split := 0; split <= len(want); split++ {
		whole, tail := terminalUTF8Prefix(want[:split])
		if !utf8.Valid(whole) || len(tail) >= utf8.UTFMax {
			t.Fatalf("split %d emitted an incomplete rune: frame %x, tail %x", split, whole, tail)
		}
		got := append([]byte(nil), whole...)
		rest := append(append([]byte(nil), tail...), want[split:]...)
		whole, tail = terminalUTF8Prefix(rest)
		if !utf8.Valid(whole) {
			t.Fatalf("split %d emitted invalid UTF-8: %x", split, whole)
		}
		got = append(got, whole...)
		if string(got) != string(want) || len(tail) != 0 {
			t.Fatalf("split %d: got %q, tail %x", split, got, tail)
		}
	}
}

func TestTerminalTunnelDisconnectClosesShell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opened := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			close(opened)
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(terminalFrame{Kind: "open", SessionID: "terminal", Workspace: "workspace", Columns: 80, Rows: 24})
		w.(http.Flusher).Flush()
		select {
		case <-opened:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	closed := make(chan struct{})
	var once sync.Once
	r, w := io.Pipe()
	open := func(context.Context, string, uint16, uint16) (*tunnelShell, error) {
		return &tunnelShell{
			input: w, output: r, wait: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, resize: func(uint16, uint16) error { return nil },
			close: func() error { once.Do(func() { close(closed); r.Close(); w.Close() }); return nil },
		}, nil
	}
	if err := terminalTunnelOnce(ctx, c, fence{MachineID: "machine", SessionID: "runner", Epoch: 1}, open); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("disconnect leaked native shell")
	}
}
