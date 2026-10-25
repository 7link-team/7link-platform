package inspection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInspectionNeverInventsAvailabilityOrModels(t *testing.T) {
	now := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	count := 0
	r := inspect(t.Context(), func(name string) (string, error) {
		if name == "claude" {
			return "", errors.New("missing")
		}
		return "/tools/" + name, nil
	}, func(ctx context.Context, path string) (string, error) {
		count++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("version probe lacks deadline")
		}
		if strings.HasSuffix(path, "python") {
			return "private error payload", errors.New("failed")
		}
		return "v1.2.3\nmore text", nil
	}, func() time.Time { return now })
	if count != 5 || len(r.Executables) != 6 || r.RemoteAccess != "UNAVAILABLE" || !r.InspectedAt.Equal(now) {
		t.Fatalf("wrong inspection facts: %+v", r)
	}
	for _, item := range r.Executables {
		switch item.Name {
		case "claude":
			if item.Available || item.Path != "" || item.Error != "NOT_FOUND" {
				t.Fatal(item)
			}
		case "python":
			if item.Available || item.Version != "" || item.Error != "VERSION_FAILED" {
				t.Fatal(item)
			}
		default:
			if !item.Available || item.Version != "v1.2.3" {
				t.Fatal(item)
			}
		}
	}
}
func TestVersionOutputBoundedAndFirstLineOnly(t *testing.T) {
	w := &boundedOutput{}
	payload := []byte(strings.Repeat("x", 10000))
	n, err := w.Write(payload)
	if n != len(payload) || err != nil || len(w.data) != 4096 {
		t.Fatal("version output was not bounded")
	}
	if firstLine("  v1.0\r\nextra\n") != "v1.0" || len([]rune(firstLine(strings.Repeat("界", 100)))) != 64 {
		t.Fatal("version normalization failed")
	}
}
func TestInspectionHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := inspect(ctx, func(string) (string, error) { return "/missing", nil }, func(ctx context.Context, _ string) (string, error) { return "", ctx.Err() }, time.Now)
	for _, item := range r.Executables {
		if item.Available || item.Error != "VERSION_FAILED" {
			t.Fatal(item)
		}
	}
}
func TestVersionMissingExecutableFails(t *testing.T) {
	_, err := version(t.Context(), "/missing/runner-inspection-test-executable")
	if err == nil {
		t.Fatal("missing executable reported a version")
	}
}

func TestInspectionDoesNotAcceptEmptyOrControlCharacterVersions(t *testing.T) {
	for _, output := range []string{"", " \r\n ", "version\x00suffix"} {
		r := inspect(t.Context(), func(string) (string, error) { return "/tool", nil }, func(context.Context, string) (string, error) { return output, nil }, time.Now)
		for _, item := range r.Executables {
			if item.Available || item.Version != "" || item.Error != "VERSION_FAILED" {
				t.Fatalf("invalid observation: %+v", item)
			}
		}
	}
}
