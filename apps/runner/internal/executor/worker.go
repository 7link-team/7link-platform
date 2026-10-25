package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type workerRequest struct {
	Plan
	FullAutoGrantID string `json:"full_auto_grant_id,omitempty"`
}

type workerMessage struct {
	Permission  *PermissionRequest `json:"permission,omitempty"`
	Observation *Observation       `json:"observation,omitempty"`
	Result      *Result            `json:"result,omitempty"`
	Error       string             `json:"error,omitempty"`
}

// ServeWorker is the execution host for one bound Agent. Plans are serial; it
// executes the actual model process rather than fabricating a readiness PID.
func ServeWorker(ctx context.Context, runtime, model string, in io.Reader, out io.Writer) error {
	ctx = context.WithValue(ctx, workerContextKey{}, true)
	decoder, encoder := json.NewDecoder(in), json.NewEncoder(out)
	for {
		var request workerRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		plan := request.Plan
		plan.FullAutoGrantID = request.FullAutoGrantID
		if plan.Runtime != runtime || plan.Model != model {
			return ErrUnsupported
		}
		plan.RequestPermission = func(wait context.Context, req PermissionRequest) (PermissionDecision, error) {
			if err := encoder.Encode(workerMessage{Permission: &req}); err != nil {
				return PermissionDecision{}, err
			}
			var decision PermissionDecision
			if err := decoder.Decode(&decision); err != nil {
				return PermissionDecision{}, err
			}
			return decision, nil
		}
		result, err := Run(ctx, plan, func(o Observation) error { return encoder.Encode(workerMessage{Observation: &o}) })
		message := workerMessage{Result: &result}
		if err != nil {
			switch {
			case errors.Is(err, ErrUnsupported):
				message.Error = "UNSUPPORTED"
			case errors.Is(err, ErrRuntimeFailed):
				message.Error = "RUNTIME_FAILED"
			default:
				message.Error = "UNKNOWN"
			}
		}
		if err = encoder.Encode(message); err != nil {
			return err
		}
	}
}

type workerContextKey struct{}

func configureExecutionCancellation(cmd *exec.Cmd, ctx context.Context) *atomic.Bool {
	if runtime.GOOS != "windows" && ctx.Value(workerContextKey{}) == true {
		// Share the resident worker's process group so its supervisor can kill
		// the complete tree, including the actual runtime and tools, together.
		return nil
	}
	return configureCancellation(cmd)
}

type Worker struct {
	ports                 *processPortAuthority
	cmd                   *exec.Cmd
	input                 io.WriteCloser
	output                io.ReadCloser
	encoder               *json.Encoder
	decoder               *json.Decoder
	cancel                context.CancelFunc
	done                  chan struct{}
	mu                    sync.Mutex
	cancellationConfirmed *atomic.Bool
}

func StartWorker(ctx context.Context, runtime, model string) (*Worker, error) {
	if (runtime != "CODEX" && runtime != "CLAUDE_CODE") || model == "" {
		return nil, ErrUnsupported
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(lifetime, executable, "agent-worker", "--runtime", runtime, "--model", model)
	hideWindow(cmd)
	confirmation := configureCancellation(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = Environment(os.Environ())
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		input.Close()
		return nil, err
	}
	ports, err := startPortOwnedProcess(cmd)
	if err != nil {
		cancel()
		input.Close()
		output.Close()
		return nil, err
	}
	w := &Worker{ports: ports, cmd: cmd, input: input, output: output, encoder: json.NewEncoder(input), decoder: json.NewDecoder(output), cancel: cancel, done: make(chan struct{}), cancellationConfirmed: confirmation}
	go func() { _ = cmd.Wait(); ports.close(); close(w.done) }()
	return w, nil
}

func (w *Worker) PID() int { return w.cmd.Process.Pid }
func (w *Worker) Alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}
func (w *Worker) Close() { w.cancel(); _ = w.input.Close(); <-w.done; _ = w.output.Close() }

func (w *Worker) Run(ctx context.Context, plan Plan, emit func(Observation) error) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	unknown := Result{ResultState: "UNKNOWN"}
	if !w.Alive() {
		return unknown, ErrUnknownResult
	}
	// The resident host and its runtime/tools share the host's process group.
	// Control stays in the supervisor, so a stopped host can still be resumed.
	releaseControl := plan.ProcessControl.bindOwned(w.cmd.Process, w.ports)
	defer releaseControl()
	if err := w.encoder.Encode(workerRequest{Plan: plan, FullAutoGrantID: plan.FullAutoGrantID}); err != nil {
		return unknown, ErrUnknownResult
	}
	// Cancellation kills the resident worker and all descendant runtime/tool
	// processes. That worker is never reused after an unknown result.
	stopped := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			w.cancel()
		case <-stopped:
		}
	}()
	defer func() { close(stopped); <-joined }()
	for {
		var message workerMessage
		if err := w.decoder.Decode(&message); err != nil {
			if ctx.Err() != nil {
				w.cancel()
				<-w.done
				unknown.CancellationConfirmed = w.cancellationConfirmed.Load()
			}
			return unknown, ErrUnknownResult
		}
		if message.Permission != nil {
			decision := PermissionDecision{State: "DENY", Reason: "Permission bridge unavailable"}
			if plan.RequestPermission != nil {
				var err error
				decision, err = plan.RequestPermission(ctx, *message.Permission)
				if err != nil {
					decision = PermissionDecision{State: "DENY", Reason: "Permission request expired or unavailable"}
				}
			}
			if err := w.encoder.Encode(decision); err != nil {
				w.cancel()
				return unknown, err
			}
			continue
		}
		if message.Observation != nil {
			if emit != nil {
				if err := emit(*message.Observation); err != nil {
					w.cancel()
					return unknown, err
				}
			}
			continue
		}
		if message.Result == nil {
			return unknown, ErrUnknownResult
		}
		if ctx.Err() != nil {
			w.cancel()
			<-w.done
			unknown.CancellationConfirmed = w.cancellationConfirmed.Load()
			return unknown, ErrUnknownResult
		}
		switch message.Error {
		case "":
			return *message.Result, nil
		case "UNSUPPORTED":
			return *message.Result, ErrUnsupported
		case "RUNTIME_FAILED":
			return *message.Result, ErrRuntimeFailed
		default:
			return *message.Result, ErrUnknownResult
		}
	}
}
