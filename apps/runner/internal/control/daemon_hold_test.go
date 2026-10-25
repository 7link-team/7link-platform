package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fixtureProcessControl struct {
	paused  bool
	unready bool
	err     error
	calls   int
}

func (c *fixtureProcessControl) SetPaused(paused bool) (bool, error) {
	c.calls++
	if c.err != nil || c.unready {
		return false, c.err
	}
	changed := c.paused != paused
	c.paused = paused
	return changed, nil
}
func (c *fixtureProcessControl) IsPaused() bool { return c.paused }

func TestReceiptHoldACKRequiresSuccessfulNativeControl(t *testing.T) {
	for _, mode := range []string{"success", "unready", "unsupported", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			controller := &fixtureProcessControl{unready: mode == "unready"}
			if mode == "unsupported" {
				controller.err = errors.New("native suspension unsupported")
			}
			j := journal{Attempt: &attempt{ID: "21", Epoch: 1}, control: controller}
			var kinds []string
			resume := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Receipt receipt `json:"receipt"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
				}
				kinds = append(kinds, in.Receipt.Kind)
				if in.Receipt.Kind == "PAUSED" && !controller.paused {
					t.Error("pause ACK preceded native pause")
				}
				if in.Receipt.Kind == "RESUMED" && controller.paused {
					t.Error("resume ACK preceded native resume")
				}
				respond(w, receiptAck{Seq: in.Receipt.Seq, State: "RUNNING", Pause: in.Receipt.Kind == "HEARTBEAT" && !resume, Resume: in.Receipt.Kind == "HEARTBEAT" && resume, Cancel: mode == "cancel"})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "test-token", time.Now().Add(time.Hour)
			if err := j.enqueue(store, receipt{Kind: "HEARTBEAT"}); err != nil {
				t.Fatal(err)
			}
			cancelled, err := j.flush(context.Background(), store, client, fence{})
			if mode == "unsupported" {
				if !errors.Is(err, controller.err) || len(kinds) != 1 || controller.paused {
					t.Fatalf("unsupported pause ACK: %v %v", kinds, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				if !cancelled || controller.calls != 0 || len(kinds) != 1 {
					t.Fatalf("cancel must take precedence: %v %+v", kinds, controller)
				}
				return
			}
			if mode == "unready" {
				if len(kinds) != 1 || controller.paused {
					t.Fatalf("unstarted runtime acknowledged pause: %v", kinds)
				}
				return
			}
			if len(kinds) != 2 || kinds[1] != "PAUSED" {
				t.Fatalf("missing truthful pause receipt: %v", kinds)
			}
			resume = true
			if err = j.enqueue(store, receipt{Kind: "HEARTBEAT"}); err != nil {
				t.Fatal(err)
			}
			if _, err = j.flush(context.Background(), store, client, fence{}); err != nil {
				t.Fatal(err)
			}
			if len(kinds) != 4 || kinds[3] != "RESUMED" || controller.paused {
				t.Fatalf("missing truthful resume receipt: %v", kinds)
			}
			var saved journal
			if err = store.Read("journal.json", &saved); err != nil || len(saved.Outbox) != 0 || saved.Attempt.Seq != 4 {
				t.Fatalf("control receipts not durably sequenced: %+v %v", saved, err)
			}
		})
	}
}
