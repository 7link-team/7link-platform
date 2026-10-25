package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerHostsActualRuntimeAndCanExecuteSerialPlans(t *testing.T) {
	p := runtimeFixture(t)
	p.AuthorizationMode = "WORKSPACE_WRITE"
	p.Model = "workspace-change"
	w, err := StartWorker(t.Context(), p.Runtime, p.Model)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.PID() <= 0 || w.PID() == os.Getpid() || !w.Alive() {
		t.Fatal("no actual worker process")
	}
	for i := 0; i < 2; i++ {
		var observed []Observation
		result, err := w.Run(t.Context(), p, func(o Observation) error { observed = append(observed, o); return nil })
		if err != nil || !result.RuntimeCompleted || result.HeadCommit == p.BaseCommit || result.Workspace == "" || len(observed) != 2 {
			t.Fatalf("serial plan %d: %+v %v events=%d", i, result, err, len(observed))
		}
		if observed[0].Kind != "WORKSPACE_READY" || observed[0].Content != result.Workspace || observed[1].Kind != "RUNTIME_COMPLETED" {
			t.Fatal("worker did not forward actual runtime workspace before completion", observed)
		}
		actual, err := workspaceGit(t.Context(), p.Workdir, "show", result.HeadCommit+":result.txt")
		if err != nil || string(actual) != "actual runtime change\n" {
			t.Fatal("worker did not host actual code mutation", err)
		}
	}
}

func TestWorkerCancellationStopsActualRuntimeDescendantAndRefusesReuse(t *testing.T) {
	p := runtimeFixture(t)
	p.Model = "tree"
	w, err := StartWorker(t.Context(), p.Runtime, p.Model)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := w.Run(ctx, p, nil)
		if !result.CancellationConfirmed {
			t.Error("actual worker tree shutdown was not confirmed")
		}
		done <- err
	}()
	path := filepath.Join(p.Workdir, "descendant-heartbeat")
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker runtime descendant did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknownResult) {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("worker tree did not stop")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("worker descendant survived", err)
	}
	if _, err = w.Run(t.Context(), p, nil); !errors.Is(err, ErrUnknownResult) {
		t.Fatal("cancelled worker reused", err)
	}
}
