package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoginInterruptWithdrawsPendingWithPrivateProof(t *testing.T) {
	for _, cancelStatus := range []int{200, 409, 503} {
		t.Run(http.StatusText(cancelStatus), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancellations, polls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/machine-enrollments":
					respond(w, map[string]any{"id": "9", "device": map[string]any{"device_code": "private-proof", "user_code": "ABCD-EFGH", "verification_uri": "https://console.example/device", "interval": 1, "expires_in": 30}})
				case "/machine-enrollments/9/credential":
					polls++
					w.WriteHeader(409)
					respond(w, map[string]string{"reason_code": "AUTHORIZATION_PENDING"})
					cancel()
				case "/machine-enrollments/9/cancellation":
					cancellations++
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["tenant_id"] != "7" || body["device_code"] != "private-proof" {
						t.Error("missing private cancellation proof")
					}
					if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
						t.Error("proof must stay in the body; no Human bearer")
					}
					w.WriteHeader(cancelStatus)
					respond(w, true)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
				}
			}))
			defer server.Close()
			store := testStore(t)
			var output bytes.Buffer
			err := login(ctx, store, LoginOptions{Server: server.URL, TenantID: "7", ClientID: "8", Name: "Machine"}, &output, enrollmentTestObservations)
			if !errors.Is(err, context.Canceled) || polls != 1 || cancellations != 1 {
				t.Fatalf("interrupt err=%v polls=%d cancellations=%d", err, polls, cancellations)
			}
			if strings.Contains(output.String(), "private-proof") || (cancelStatus != 200 && !strings.Contains(output.String(), "not confirmed")) {
				t.Fatal("secret disclosure or missing ambiguous-cancellation warning")
			}
			if _, err = store.Profile(); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("canceled enrollment persisted a profile", err)
			}
		})
	}
}

func TestLoginSlowDownAddsFiveSecondsAndStopsOnDenial(t *testing.T) {
	var polls []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/machine-enrollments" {
			respond(w, map[string]any{"id": "9", "device": map[string]any{"device_code": "private-proof", "user_code": "ABCD-EFGH", "verification_uri": "https://console.example/device", "interval": 1, "expires_in": 30}})
			return
		}
		if r.URL.Path != "/machine-enrollments/9/credential" {
			t.Error("unexpected endpoint", r.URL.Path)
		}
		polls = append(polls, time.Now())
		if len(polls) == 1 {
			w.WriteHeader(429)
			respond(w, map[string]string{"reason_code": "SLOW_DOWN"})
		} else {
			w.WriteHeader(409)
			respond(w, map[string]string{"reason_code": "RESTART_ENROLLMENT"})
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	err := login(t.Context(), testStore(t), LoginOptions{Server: server.URL, TenantID: "7", ClientID: "8", Name: "Machine"}, &output, enrollmentTestObservations)
	var api *APIError
	if !errors.As(err, &api) || api.Reason != "RESTART_ENROLLMENT" || len(polls) != 2 {
		t.Fatalf("denial not terminal: err=%v polls=%d", err, len(polls))
	}
	if elapsed := polls[1].Sub(polls[0]); elapsed < 6*time.Second {
		t.Fatalf("slow_down did not increase interval by five seconds: %s", elapsed)
	}
}

func enrollmentTestObservations(context.Context) (enrollmentFacts, error) {
	return enrollmentFacts{Hostname: "observed-host", Arch: "amd64", Runtimes: []enrollmentRuntime{{Kind: "CODEX", Available: true, Version: "1.2"}, {Kind: "CLAUDE_CODE", Error: "NOT_FOUND"}}}, nil
}

func TestLoginInspectionFailureCreatesNoRemoteEnrollment(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	failure := errors.New("inspection cancelled")
	err := login(t.Context(), testStore(t), LoginOptions{Server: server.URL, TenantID: "7", ClientID: "8", Name: "Machine"}, &bytes.Buffer{}, func(context.Context) (enrollmentFacts, error) { return enrollmentFacts{}, failure })
	if !errors.Is(err, failure) || calls != 0 {
		t.Fatalf("inspection failure must precede enrollment: %v calls=%d", err, calls)
	}
}
