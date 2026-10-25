package inspection

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectionProbeCancellationKillsDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestInspectionProbeHelper$")
	cmd.Env = append(os.Environ(), "INSPECTION_PROBE_HELPER=parent", "INSPECTION_PROBE_DIR="+dir)
	hideWindow(cmd)
	configureProbeCancellation(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatal("helper not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled probe unexpectedly succeeded")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "descendant-survived")); !os.IsNotExist(err) {
		t.Fatal("version probe left a descendant running")
	}
}

func TestInspectionProbeHelper(t *testing.T) {
	mode := os.Getenv("INSPECTION_PROBE_HELPER")
	if mode == "" {
		return
	}
	dir := os.Getenv("INSPECTION_PROBE_DIR")
	if mode == "child" {
		time.Sleep(time.Second)
		if err := os.WriteFile(filepath.Join(dir, "descendant-survived"), []byte("alive"), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(exe, "-test.run=^TestInspectionProbeHelper$")
	child.Env = append(os.Environ(), "INSPECTION_PROBE_HELPER=child")
	hideWindow(child)
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	_ = child.Wait()
	time.Sleep(time.Minute)
	os.Exit(0)
}
