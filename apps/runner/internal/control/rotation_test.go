package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotationPersistsBeforeAckAndRecoversUncertainAck(t *testing.T) {
	store := testStore(t)
	ackCalls, pulls := 0, 0
	var profile Profile
	delivery := credentialDelivery{RotationID: "31", CredentialID: "32", ClientID: "32", Secret: "rotated-private", RetireOldAt: time.Now().Add(24 * time.Hour)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/credential-deliveries":
			pulls++
			respond(w, []credentialDelivery{delivery})
		case "/machines/11/credential-deliveries/31/ack":
			ackCalls++
			saved, err := store.Profile()
			if err != nil || saved.Secret != delivery.Secret {
				t.Error("ACK preceded durable new identity", err)
			}
			pending, err := pendingCredential(store)
			if err != nil || pending == nil || pending.Previous.Secret != profile.Secret {
				t.Error("old fallback not durable before ACK", err)
			}
			var body struct {
				Session    string `json:"session_id"`
				Epoch      int    `json:"conn_epoch"`
				Credential string `json:"credential_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Session != "21" || body.Epoch != 2 || body.Credential != "32" {
				t.Error("missing current session/credential fence")
			}
			if ackCalls == 1 {
				w.WriteHeader(503)
				return
			}
			respond(w, true)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	profile = testProfile(server.URL)
	original := profile
	if err := store.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient(server.URL)
	f := fence{MachineID: "11", SessionID: "21", Epoch: 2}
	if err := rotateCredential(t.Context(), store, c, &profile, f); err == nil {
		t.Fatal("uncertain ACK hidden")
	}
	pending, err := pendingCredential(store)
	if err != nil || pending == nil || pending.Previous.Secret != original.Secret {
		t.Fatal("uncertain ACK lost fallback", err)
	}
	restarted, err := store.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if err = rotateCredential(t.Context(), store, c, &restarted, f); err != nil {
		t.Fatal(err)
	}
	if pulls != 1 || ackCalls != 2 || restarted.Secret != delivery.Secret {
		t.Fatal("recovery did not replay same ACK")
	}
	pending, err = pendingCredential(store)
	if err != nil || pending != nil {
		t.Fatal("ACK did not clear fallback", err)
	}
}

func TestRotationWriteFailureWithholdsAcknowledgement(t *testing.T) {
	store := testStore(t)
	acks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			acks++
			respond(w, true)
			return
		}
		respond(w, []credentialDelivery{{RotationID: "31", CredentialID: "32", ClientID: "32", Secret: "new-secret", RetireOldAt: time.Now().Add(time.Hour)}})
	}))
	defer server.Close()
	// Atomic replacement cannot replace a directory with a protected file.
	if err := os.Mkdir(filepath.Join(store.Dir, "identity.json"), 0700); err != nil {
		t.Fatal(err)
	}
	p := testProfile(server.URL)
	c, _ := NewClient(server.URL)
	if err := rotateCredential(t.Context(), store, c, &p, fence{}); err == nil || acks != 0 {
		t.Fatal("write failure acknowledged", err, acks)
	}
}

func TestRotationRejectsAccountIDInPlaceOfCredentialClientID(t *testing.T) {
	store := testStore(t)
	acks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			acks++
			respond(w, true)
			return
		}
		respond(w, []credentialDelivery{{RotationID: "31", CredentialID: "32", ClientID: "12", Secret: "bad-identity", RetireOldAt: time.Now().Add(time.Hour)}})
	}))
	defer server.Close()
	p := testProfile(server.URL)
	if err := store.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient(server.URL)
	if err := rotateCredential(t.Context(), store, c, &p, fence{}); err == nil {
		t.Fatal("account ID accepted as OAuth credential ID")
	}
	saved, err := store.Profile()
	if err != nil || saved.Secret != p.Secret || acks != 0 {
		t.Fatal("invalid delivery changed or acknowledged identity", err, acks)
	}
}

func TestExpiredUnacknowledgedCredentialFallsBackAndStaleAckRestoresPrevious(t *testing.T) {
	store := testStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = r.ParseForm()
			if r.Form.Get("client_secret") == "expired-new" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "old-authority", "token_type": "Bearer", "expires_in": 60})
			return
		}
		if r.Method == "PUT" {
			w.WriteHeader(409)
			return
		}
		t.Error("unexpected redelivery before pending ACK")
		w.WriteHeader(500)
	}))
	defer server.Close()
	old := testProfile(server.URL)
	next := old
	next.ClientID = "32"
	next.Secret = "expired-new"
	pending := pendingRotation{Previous: old, Delivery: credentialDelivery{RotationID: "31", CredentialID: "32", ClientID: "32", Secret: next.Secret, RetireOldAt: time.Now().Add(time.Hour)}}
	if err := store.Write("rotation.json", pending); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile(next); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient(server.URL)
	recovered, err := authenticateProfile(context.Background(), store, c, next)
	if err != nil || recovered.Secret != old.Secret {
		t.Fatal("expired delivery stranded saved identity", err)
	}
	if err = rotateCredential(t.Context(), store, c, &recovered, fence{}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Profile()
	if err != nil || saved.Secret != old.Secret {
		t.Fatal("stale delivery replaced prior identity", err)
	}
}
