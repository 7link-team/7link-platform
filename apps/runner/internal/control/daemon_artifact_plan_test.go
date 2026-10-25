package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestPlannedArtifactReceiptPreservesCriterionBytesAndProvenance(t *testing.T) {
	store := testStore(t)
	j := journal{Attempt: &attempt{ID: "21", Epoch: 2, Seq: 1, Running: true}}
	var received []receipt
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Receipt receipt `json:"receipt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received = append(received, body.Receipt)
		respond(w, receiptAck{Seq: body.Receipt.Seq, State: "RUNNING"})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	client.token, client.expires = "test-token", time.Now().Add(time.Hour)
	a := executor.CapturedArtifact{CriterionID: "77", Name: "hello.txt", Data: []byte("hello\n"), Note: "Frozen output association; no verification verdict"}
	cancel, err := submitWorkspaceEvidence(t.Context(), store, client, fence{MachineID: "11", SessionID: "22", Epoch: 2}, &j, executor.Result{HeadCommit: strings.Repeat("a", 40), Artifacts: []executor.CapturedArtifact{a}})
	if err != nil || cancel || len(received) != 2 {
		t.Fatal(cancel, err, received)
	}
	u := received[0].Upload
	if received[0].EvidenceTitle != "Planned artifact 1" || u == nil || u.CriterionID != a.CriterionID || !bytes.Equal(u.Data, a.Data) || u.Coverage != "COVERED" || u.Note != a.Note || received[1].EvidenceKind != "COMMIT" {
		t.Fatal("criterion association changed", received)
	}
}
