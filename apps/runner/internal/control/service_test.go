package control

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestServiceWindowsDefinition(t *testing.T) {
	c := serviceConfig{OS: "windows", Profile: `C:\private dir\a'b`, Executable: `C:\tools\runner.exe`, Path: `C:\tools;C:\runtime`}
	script := windowsServiceScript(c, "install")
	for _, want := range []string{"Settings.Hidden=$true", "LogonType=3", "RunLevel=0", "MultipleInstances=2", "ExecutionTimeLimit='PT0S'", "wscript.exe", "//B //Nologo", "Triggers.Create(9)"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %s", want)
		}
	}
	data := c.launcher()[2:]
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	launcher := string(utf16.Decode(units))
	for _, want := range []string{`environment("PATH")=` + vbQuote(c.Path), `""` + c.Executable + `"" daemon --profile ""` + c.Profile + `""`, `,0,True)`} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("hidden daemon arguments not preserved: %s", want)
		}
	}
	upper := c
	upper.Profile = strings.ToUpper(c.Profile)
	if c.name() != upper.name() {
		t.Fatal("Windows service identity must ignore path case")
	}
	other := c
	other.Profile += "-other"
	if c.name() == other.name() {
		t.Fatal("profile collision")
	}
}

func TestServiceUnixLifecycleAndFailure(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			c := serviceConfig{OS: platform, Home: t.TempDir(), Profile: "/private/a & b", Executable: "/tools/runner %x", Path: "/tools:/bin", UID: "501"}
			var calls []string
			command := func(_ context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if platform == "linux" {
					return []byte("active\n"), nil
				}
				return []byte("state = running"), nil
			}
			var out bytes.Buffer
			if err := manageService(t.Context(), c, "status", &out, command); err != nil || !strings.Contains(out.String(), `"installed":false`) {
				t.Fatalf("missing status: %s %v", out.String(), err)
			}
			if len(calls) != 0 {
				t.Fatal("queried manager for absent service")
			}
			if err := manageService(t.Context(), c, "install", &out, command); err != nil {
				t.Fatal(err)
			}
			if platform == "linux" {
				if len(calls) != 2 || !strings.Contains(calls[0], "daemon-reload") || !strings.Contains(calls[1], "enable --now "+c.name()+".service") {
					t.Fatalf("service not enabled and started: %v", calls)
				}
			} else if len(calls) != 1 || !strings.Contains(calls[0], "bootstrap gui/501") {
				t.Fatalf("agent not bootstrapped in user session: %v", calls)
			}
			if err := manageService(t.Context(), c, "install", &out, command); err == nil {
				t.Fatal("existing definition overwritten")
			}
			out.Reset()
			if err := manageService(t.Context(), c, "status", &out, command); err != nil || !strings.Contains(out.String(), `"running":true`) {
				t.Fatalf("running status %s %v", out.String(), err)
			}
			failed := func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("manager unavailable")
			}
			if err := manageService(t.Context(), c, "uninstall", &out, failed); err == nil {
				t.Fatal("manager failure hidden")
			}
			out.Reset()
			if err := manageService(t.Context(), c, "status", &out, command); err != nil || !strings.Contains(out.String(), `"installed":true`) {
				t.Fatal("failed removal lost unit")
			}
			if err := manageService(t.Context(), c, "uninstall", &out, command); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := manageService(t.Context(), c, "status", &out, command); err != nil || !strings.Contains(out.String(), `"installed":false`) {
				t.Fatal("service not removed")
			}
			if err := manageService(t.Context(), c, "uninstall", &out, command); err != nil {
				t.Fatal("uninstall is not idempotent")
			}
			if platform == "linux" && !strings.Contains(c.unit(), "%%x") {
				t.Fatal("systemd specifier expanded")
			}
			if platform == "darwin" && !strings.Contains(c.plist(), "a &amp; b") {
				t.Fatal("invalid XML path")
			}
		})
	}
}

func TestServiceWindowsManagerStatusAndFailures(t *testing.T) {
	c := serviceConfig{OS: "windows", Profile: t.TempDir()}
	for _, data := range []string{`{"installed":true,"running":false}`, `{"installed":false,"running":false}`} {
		var out bytes.Buffer
		var scriptPath string
		err := manageService(t.Context(), c, "status", &out, func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "powershell.exe" || args[0] != "-NoProfile" || args[1] != "-NonInteractive" || args[2] != "-File" {
				t.Fatal("unexpected manager invocation")
			}
			if data, err := os.ReadFile(args[3]); err != nil || !bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || !strings.Contains(string(data), "Schedule.Service") {
				t.Fatal("manager script is unavailable")
			}
			scriptPath = args[3]
			return []byte(data), nil
		})
		if err != nil || strings.TrimSpace(out.String()) != data {
			t.Fatalf("status %s %v", out.String(), err)
		}
		if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
			t.Fatal("temporary manager script was retained")
		}
	}
	if err := manageService(t.Context(), c, "status", &bytes.Buffer{}, func(context.Context, string, ...string) ([]byte, error) { return []byte("not json"), nil }); err == nil {
		t.Fatal("invalid status accepted")
	}
	if err := Service(t.Context(), "unknown", t.TempDir(), &bytes.Buffer{}); err == nil {
		t.Fatal("unknown action accepted")
	}
	if err := Service(t.Context(), "install", t.TempDir(), &bytes.Buffer{}); err == nil {
		t.Fatal("unenrolled profile installed")
	}
}

// Explicit opt-in: creates and removes a real current-user Windows scheduled task.
func TestServiceWindowsLive(t *testing.T) {
	executable := os.Getenv("RUNNER_SERVICE_LIVE_EXE")
	if runtime.GOOS != "windows" || executable == "" {
		t.Skip("requires Windows and RUNNER_SERVICE_LIVE_EXE")
	}
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.SaveProfile(Profile{Server: "https://127.0.0.1:1", TenantID: "1", MachineID: "2", ClientID: "3", Secret: "throwaway-service-test"})
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	live := func(ctx context.Context, action string, out *bytes.Buffer) error {
		data, err := runServiceCommand(ctx, executable, "service", action, "--profile", dir)
		out.Write(data)
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Logf("service CLI error: %s", exit.Stderr)
		}
		return err
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := live(cleanupCtx, "uninstall", &bytes.Buffer{}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	if err := live(ctx, "install", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		status.Reset()
		if err := live(ctx, "status", &status); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(status.String(), `"running":true`) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(status.String(), `"installed":true`) || !strings.Contains(status.String(), `"running":true`) {
		t.Fatalf("actual daemon did not run: %s", status.String())
	}
	for deadline := time.Now().Add(45 * time.Second); ; {
		s, err := OpenStore(dir)
		if err != nil {
			break // The real daemon, rather than just its hidden wrapper, owns the lock.
		}
		s.Close()
		if time.Now().After(deadline) {
			t.Fatal("scheduled task wrapper never started a daemon holding its profile")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := live(ctx, "uninstall", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	status.Reset()
	if err := live(ctx, "status", &status); err != nil || !strings.Contains(status.String(), `"installed":false`) {
		t.Fatalf("task remains: %s %v", status.String(), err)
	}
	// The daemon has released its profile lock after being stopped by the manager.
	for deadline := time.Now().Add(5 * time.Second); ; {
		s, err := OpenStore(dir)
		if err == nil {
			s.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon still holds profile after uninstall")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.json")); err != nil {
		t.Fatal("uninstall removed enrollment")
	}
}
