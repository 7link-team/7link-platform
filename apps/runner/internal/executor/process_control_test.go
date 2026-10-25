package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProcessControlRequiresAProcessAndSuccessfulNativeOperation(t *testing.T) {
	c := new(ProcessControl)
	if changed, err := c.SetPaused(true); changed || err != nil || c.IsPaused() {
		t.Fatalf("unstarted process acknowledged pause: %v %v", changed, err)
	}
	// No process group may use this invalid positive PID.
	release := c.bind(&os.Process{Pid: 2147483647})
	if changed, err := c.SetPaused(true); changed || err == nil || c.IsPaused() {
		t.Fatalf("failed native pause acknowledged: %v %v", changed, err)
	}
	release()
}

func TestProcessControlStopsAndResumesActualDirectAndWorkerDescendants(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "resident_worker"}[worker], func(t *testing.T) {
			plan := runtimeFixture(t)
			plan.Model = "tree"
			controller := new(ProcessControl)
			plan.ProcessControl = controller
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			run := Run
			if worker {
				w, err := StartWorker(t.Context(), plan.Runtime, plan.Model)
				if err != nil {
					t.Fatal(err)
				}
				defer w.Close()
				run = w.Run
			}
			done := make(chan error, 1)
			go func() { _, err := run(ctx, plan, nil); done <- err }()
			defer func() {
				_, _ = controller.SetPaused(false)
				cancel()
				select {
				case <-done:
				case <-time.After(8 * time.Second):
					t.Error("runtime cleanup hung")
				}
			}()
			path := filepath.Join(plan.Workdir, "descendant-heartbeat")
			read := func() []byte { data, _ := os.ReadFile(path); return data }
			deadline := time.Now().Add(5 * time.Second)
			for len(read()) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if len(read()) == 0 {
				t.Fatal("actual descendant never started")
			}
			changed, err := controller.SetPaused(true)
			if runtime.GOOS == "windows" {
				if changed || !errors.Is(err, ErrUnsupported) || controller.IsPaused() {
					t.Fatalf("unsupported Windows pause acknowledged: %v %v", changed, err)
				}
				return
			}
			if err != nil || !changed || !controller.IsPaused() {
				t.Fatalf("native pause failed: %v %v", changed, err)
			}
			time.Sleep(50 * time.Millisecond)
			before := string(read())
			time.Sleep(100 * time.Millisecond)
			if string(read()) != before {
				t.Fatal("descendant kept running while group paused")
			}
			if changed, err = controller.SetPaused(true); changed || err != nil {
				t.Fatalf("duplicate pause: %v %v", changed, err)
			}
			if changed, err = controller.SetPaused(false); !changed || err != nil || controller.IsPaused() {
				t.Fatalf("native resume failed: %v %v", changed, err)
			}
			deadline = time.Now().Add(3 * time.Second)
			for string(read()) == before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if string(read()) == before {
				t.Fatal("descendant did not resume")
			}
			// Leave it stopped so deferred resume/cancel cleanup also has to
			// handle the shutdown path without hanging on a stopped group.
			if changed, err = controller.SetPaused(true); !changed || err != nil {
				t.Fatalf("shutdown pause failed: %v %v", changed, err)
			}
		})
	}
}
