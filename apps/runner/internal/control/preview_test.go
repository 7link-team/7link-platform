package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewTunnelFencesProbesAndRejectsUnpinnedPorts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	frames := make(chan previewFrame, 4)
	posted := make(chan previewFrame, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer preview-test-token" {
			t.Error("missing runner authentication")
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/frames") {
			var body struct {
				SessionID string         `json:"session_id"`
				Epoch     int64          `json:"conn_epoch"`
				Frames    []previewFrame `json:"frames"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SessionID != "session" || body.Epoch != 7 {
				t.Error("frame fence missing")
			}
			for _, f := range body.Frames {
				posted <- f
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("session_id") != "session" || r.URL.Query().Get("conn_epoch") != "7" {
			t.Error("stream fence missing")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case f := <-frames:
				if json.NewEncoder(w).Encode(f) != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	client := &Client{base: server.URL, http: server.Client(), token: "preview-test-token"}
	result := make(chan error, 1)
	go func() {
		result <- previewTunnelOnce(withTerminalWorkspaces(ctx), client, fence{MachineID: "machine", SessionID: "session", Epoch: 7})
	}()
	frames <- previewFrame{Kind: "ping"}
	select {
	case f := <-posted:
		if f.Kind != "pong" {
			t.Fatalf("got %+v", f)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, kind := range []string{"probe", "open"} {
		frames <- previewFrame{Kind: kind, ID: kind, ExecutionID: "601", DispatchEpoch: 1, Workspace: t.TempDir(), Port: 80}
		select {
		case f := <-posted:
			if f.Kind != "rejected" || f.ID != kind {
				t.Fatalf("unpinned port admitted: %+v", f)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		// The broker resets every rejected open/probe. That late cleanup must
		// preserve the connection for the next unrelated stream.
		frames <- previewFrame{Kind: "reset", ID: kind}
		frames <- previewFrame{Kind: "ping"}
		select {
		case f := <-posted:
			if f.Kind != "pong" {
				t.Fatalf("cleanup disrupted tunnel: %+v", f)
			}
		case err := <-result:
			t.Fatalf("rejected probe disconnected tunnel: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("preview tunnel did not join on cancellation")
	}
}
