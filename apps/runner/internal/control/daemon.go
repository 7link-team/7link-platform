package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"time"

	"7link.com/runner/internal/executor"
	"7link.com/runner/internal/inspection"
)

type fence struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Epoch     int64  `json:"conn_epoch"`
}
type command struct {
	FullAutoGrantID string        `json:"full_auto_grant_id"`
	ID              string        `json:"id"`
	MachineID       string        `json:"machine_id"`
	Epoch           int64         `json:"dispatch_epoch"`
	LastSeq         int64         `json:"last_seq"`
	State           string        `json:"state"`
	Plan            executor.Plan `json:"plan"`
	CancelRequested *time.Time    `json:"cancel_requested_at"`
}

// Keep every field, including empty strings: the backend hashes these bytes'
// decoded representation to identify exact receipt replay.
type receipt struct {
	Progress      *executor.ProgressObservation `json:"progress,omitempty"`
	Epoch         int64                         `json:"dispatch_epoch"`
	Seq           int64                         `json:"seq"`
	Kind          string                        `json:"kind"`
	Content       string                        `json:"content"`
	HeadCommit    string                        `json:"head_commit"`
	EvidenceKind  string                        `json:"evidence_kind"`
	EvidenceTitle string                        `json:"evidence_title"`
	Upload        *artifactUpload               `json:"upload,omitempty"`
}
type artifactUpload struct {
	CriterionID string `json:"criterion_id,omitempty"`
	Name        string `json:"name"`
	Data        []byte `json:"data"`
	Coverage    string `json:"coverage"`
	Note        string `json:"note"`
}
type queued struct {
	ExecutionID string  `json:"execution_id"`
	Receipt     receipt `json:"receipt"`
}
type attempt struct {
	ID      string `json:"id"`
	Epoch   int64  `json:"epoch"`
	Seq     int64  `json:"seq"`
	Running bool   `json:"running"`
}
type journal struct {
	control runtimeProcessControl
	Attempt *attempt         `json:"attempt"`
	Outbox  []queued         `json:"outbox"`
	Seen    map[string]int64 `json:"seen_attempts"`
}
type receiptAck struct {
	Seq    int64  `json:"seq"`
	State  string `json:"state"`
	Cancel bool   `json:"cancel_requested"`
	Pause  bool   `json:"pause_requested"`
	Resume bool   `json:"resume_requested"`
}

func (j *journal) enqueue(store *Store, r receipt) error {
	if j.Attempt == nil || len(j.Outbox) >= 10000 || j.Attempt.Seq == math.MaxInt64 {
		return errors.New("receipt outbox full or no active attempt; execution must stop")
	}
	j.Attempt.Seq++
	r.Epoch = j.Attempt.Epoch
	r.Seq = j.Attempt.Seq
	j.Outbox = append(j.Outbox, queued{ExecutionID: j.Attempt.ID, Receipt: r})
	return store.Write("journal.json", j)
}
func (j *journal) flush(ctx context.Context, store *Store, c *Client, f fence) (bool, error) {
	cancelled := false
	for len(j.Outbox) > 0 {
		head := j.Outbox[0]
		var ack receiptAck
		if err := c.JSON(ctx, "POST", "/executions/"+head.ExecutionID+"/receipts", struct {
			Fence   fence   `json:"fence"`
			Receipt receipt `json:"receipt"`
		}{f, head.Receipt}, &ack, true); err != nil {
			return cancelled, err
		}
		if ack.Seq != head.Receipt.Seq {
			return cancelled, errors.New("receipt acknowledgement sequence mismatch")
		}
		cancelled = cancelled || ack.Cancel
		j.Outbox = j.Outbox[1:]
		if ack.State == "SUCCEEDED" || ack.State == "FAILED" || ack.State == "CANCELLED" || ack.State == "INTERRUPTED" {
			j.Attempt = nil
		}
		if err := store.Write("journal.json", j); err != nil {
			return cancelled, err
		}
		if err := j.acknowledgeProcessControl(store, ack, cancelled); err != nil {
			return cancelled, err
		}
	}
	return cancelled, nil
}

// daemonOnce runs one session. A transport failure stops dispatch and cancels
// the executor before the supervisor can reconnect.
// Interrupted attempts are reported LOST, never automatically launched again.
func daemonOnce(ctx context.Context, store *Store, out io.Writer) error {
	p, err := store.Profile()
	if err != nil {
		return err
	}
	c, err := NewClient(p.Server)
	if err != nil {
		return err
	}
	c.store = store
	if p, err = authenticateProfile(ctx, store, c, p); err != nil {
		return err
	}
	var j journal
	if err = store.Read("journal.json", &j); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(j.Outbox) > 10000 {
		return errors.New("receipt outbox exceeds limit")
	}
	// Inspection precedes session creation, so slow probes cannot expire it.
	report := inspection.Inspect(ctx)
	incarnation, err := randomID()
	if err != nil {
		return err
	}
	var session struct {
		ID    string `json:"id"`
		Epoch int64  `json:"conn_epoch"`
	}
	if err = c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/runner-sessions", map[string]string{"incarnation_id": incarnation}, &session, true); err != nil {
		return err
	}
	if !validID(session.ID) || session.Epoch < 1 {
		return errors.New("invalid runner session")
	}
	f := fence{MachineID: p.MachineID, SessionID: session.ID, Epoch: session.Epoch}
	if err = publishInventory(ctx, c, p, f, report); err != nil {
		return err
	}
	if _, err = j.flush(ctx, store, c, f); err != nil {
		return err
	}
	if j.Attempt != nil {
		if err = j.enqueue(store, receipt{Kind: "LOST", Content: "Runner restarted; prior runtime outcome unknown. No automatic replay."}); err != nil {
			return err
		}
		if _, err = j.flush(ctx, store, c, f); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintln(out, "Runner connected. Terminal access requires server authorization and native pseudoconsole support."); err != nil {
		return err
	}
	intakeCtx, stopIntake := context.WithCancel(ctx)
	intakeCtx = withTerminalWorkspaces(intakeCtx)
	defer stopIntake()
	go consumeIntakeSuggestions(intakeCtx, store, c, p, f)
	terminalDone := make(chan struct{})
	go func() {
		defer close(terminalDone)
		consumeTerminalTunnel(intakeCtx, c, f)
	}()
	defer func() { stopIntake(); <-terminalDone }()
	previewDone := make(chan struct{})
	go func() { defer close(previewDone); consumePreviewTunnel(intakeCtx, c, f) }()
	defer func() { stopIntake(); <-previewDone }()
	workers := newAgentWorkers(ctx, report)
	defer workers.close(c, p, f)
	return daemonLoop(intakeCtx, store, c, p, f, &j, workers.run, workers)
}

func publishInventory(ctx context.Context, c *Client, p Profile, f fence, report inspection.Report) error {
	inventory, err := inventoryFromReport(report)
	if err != nil {
		return err
	}
	return c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/inventory", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch, "observation_mode": "RUNTIME_VERSIONS", "inventory": inventory}, nil, true)
}

func inventoryFromReport(report inspection.Report) (map[string]any, error) {
	runtimes := []map[string]any{}
	tools := []map[string]any{}
	for _, e := range report.Executables {
		// A failed probe is not an observed removal. Refuse a partial replacement.
		if e.Error != "" && e.Error != "NOT_FOUND" {
			return nil, errors.New("inventory probe failed; refusing partial capability replacement")
		}
		if !e.Available {
			continue
		}
		if e.Kind == "TOOL" {
			tools = append(tools, map[string]any{"name": e.Name, "version": e.Version, "executable_path": e.Path})
		} else {
			runtimes = append(runtimes, map[string]any{"kind": e.Kind, "version": e.Version, "executable_path": e.Path, "models": []string{}})
		}
	}
	return map[string]any{"runtimes": runtimes, "tools": tools}, nil
}

type runtimeResult struct {
	Result executor.Result
	Err    error
}

func daemonLoop(ctx context.Context, store *Store, c *Client, p Profile, f fence, j *journal, run func(context.Context, executor.Plan, func(executor.Observation) error) (executor.Result, error), workers ...*agentWorkers) error {
	ctx = withTerminalWorkspaces(ctx)
	defer clearTerminalWorkspaces(ctx)
	var err error
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var done chan runtimeResult
	var activePlan executor.Plan
	var stop context.CancelFunc
	stopRuntime := func() {
		if j.control != nil {
			_, _ = j.control.SetPaused(false)
		}
		if stop != nil {
			stop()
		}
	}
	var inspectionDone chan error
	var stopInspection context.CancelFunc
	defer func() {
		if stopInspection != nil {
			stopInspection()
			if inspectionDone != nil {
				<-inspectionDone
			}
		}
		if stop != nil {
			stopRuntime()
			// Wait for executor cleanup before the CLI may exit.
			if done != nil {
				<-done
			}
		}
	}()
	// An unbuffered observation stream bounds memory and applies backpressure.
	observations := make(chan executor.Observation)
	cancelRequested := false
	var lastModels time.Time
	capabilitiesSent := false
	for {
		if err = c.Authenticate(ctx, p); err != nil {
			return err
		}
		if err = c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/heartbeat", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, nil, true); err != nil {
			return err
		}
		if err = rotateCredential(ctx, store, c, &p, f); err != nil {
			return err
		}
		if err = processAgentWorkspace(ctx, store, c, p, f); err != nil {
			return err
		}
		if done == nil && inspectionDone == nil && j.Attempt == nil {
			if err = processAgentReset(ctx, store, c, p, f); err != nil {
				return err
			}
		}
		if j.Attempt != nil && done != nil {
			if err = j.enqueue(store, receipt{Kind: "HEARTBEAT"}); err != nil {
				return err
			}
			requested, e := j.flush(ctx, store, c, f)
			if e != nil {
				return e
			}
			if requested {
				cancelRequested = true
				stopRuntime()
			}
		}
		if done == nil && inspectionDone == nil {
			var merges []executor.MergePlan
			if err = c.JSON(ctx, "POST", "/runner/merge-pulls", f, &merges, true); err != nil {
				return err
			}
			if len(merges) > 1 {
				return errors.New("unsupported merge batch")
			}
			if len(merges) == 1 {
				plan := merges[0]
				if !validID(plan.ID) {
					return errors.New("invalid merge command")
				}
				mergeCtx, cancel := context.WithCancel(ctx)
				stopInspection = cancel
				inspectionDone = make(chan error, 1)
				go func(completed chan error) { completed <- runMerge(mergeCtx, c, f, plan) }(inspectionDone)
			}
		}
		if done == nil && inspectionDone == nil {
			var verification []executor.VerificationPlan
			if err = c.JSON(ctx, "POST", "/runner/verification-pulls", f, &verification, true); err != nil {
				return err
			}
			if len(verification) > 1 {
				return errors.New("unsupported verification batch")
			}
			if len(verification) == 1 {
				plan := verification[0]
				if !validID(plan.ID) {
					return errors.New("invalid verification task")
				}
				verifyCtx, cancel := context.WithCancel(ctx)
				stopInspection = cancel
				inspectionDone = make(chan error, 1)
				go func(completed chan error) { completed <- runVerification(verifyCtx, c, f, plan) }(inspectionDone)
			}
		}
		if done == nil && inspectionDone == nil {
			var requests []inspectionRequest
			if err = c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/inspection-pulls", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &requests, true); err != nil {
				return err
			}
			if len(requests) > 1 {
				return errors.New("server returned unsupported inspection batch")
			}
			if len(requests) == 1 {
				request := requests[0]
				if !validID(request.ID) || request.MachineID != p.MachineID || request.State != "RUNNING" {
					return errors.New("invalid inspection request")
				}
				scanCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
				stopInspection = cancel
				inspectionDone = make(chan error, 1)
				go func(completed chan error) {
					completed <- completeInspection(scanCtx, c, p, f, request, inspection.Inspect)
				}(inspectionDone)
			}
		}
		if done == nil && inspectionDone == nil && (time.Since(lastModels) >= 5*time.Minute || !capabilitiesSent) {
			kind := "MODELS"
			if time.Since(lastModels) < 5*time.Minute {
				kind = "CAPABILITIES"
				capabilitiesSent = true
			} else {
				lastModels = time.Now()
			}
			scanCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
			stopInspection = cancel
			inspectionDone = make(chan error, 1)
			go func(completed chan error) {
				completed <- completeInspection(scanCtx, c, p, f, inspectionRequest{Kind: kind}, inspection.Inspect)
			}(inspectionDone)
		}
		if done == nil && inspectionDone == nil {
			if len(workers) > 0 {
				if err = processAgentMachineCommands(ctx, store, c, p, f, workers[0]); err != nil {
					return err
				}
				if err = workers[0].sync(ctx, c, p, f); err != nil {
					return err
				}
			}
			var commands []command
			if err = c.JSON(ctx, "POST", "/runner/execution-pulls", f, &commands, true); err != nil {
				return err
			}
			if len(commands) > 1 {
				return errors.New("server returned unsupported concurrent command batch")
			}
			if len(commands) == 1 {
				cmd := commands[0]
				// Approval authority comes only from the server envelope, before
				// preflight validates the runtime's FULL_AUTO capability.
				cmd.Plan.FullAutoGrantID = cmd.FullAutoGrantID
				if !validID(cmd.ID) || cmd.MachineID != p.MachineID || cmd.Epoch < 1 || cmd.LastSeq < 0 || cmd.State != "CREATED" {
					return errors.New("invalid execution command")
				}
				if j.Seen == nil {
					j.Seen = map[string]int64{}
				}
				if j.Seen[cmd.ID] >= cmd.Epoch {
					return errors.New("duplicate execution attempt refused; no process relaunched")
				}
				if _, exists := j.Seen[cmd.ID]; !exists && len(j.Seen) >= 10000 {
					return errors.New("local attempt journal full; refusing dispatch")
				}
				j.Seen[cmd.ID] = cmd.Epoch
				j.Attempt = &attempt{ID: cmd.ID, Epoch: cmd.Epoch, Seq: cmd.LastSeq}
				if err = store.Write("journal.json", j); err != nil {
					return err
				}
				kind := "ACCEPTED"
				if cmd.CancelRequested != nil {
					kind = "CANCELLED"
				} else if _, _, e := executor.Command(cmd.Plan); e != nil {
					kind = "FAILED"
				}
				if err = j.enqueue(store, receipt{Kind: kind, Content: unsupportedReason(kind)}); err != nil {
					return err
				}
				requested, e := j.flush(ctx, store, c, f)
				if e != nil {
					return e
				}
				if kind == "ACCEPTED" {
					// Journal the launch fence before creating any child process.
					if j.Attempt == nil {
						return errors.New("server ended execution before launch")
					}
					j.Attempt.Running = true
					if err = store.Write("journal.json", j); err != nil {
						return err
					}
					runCtx, cancel := context.WithCancel(ctx)
					stop = cancel
					controller := new(executor.ProcessControl)
					j.control = controller
					cmd.Plan.ProcessControl = controller
					cancelRequested = requested
					if requested {
						cancel()
					}
					cmd.Plan.RequestPermission = runtimePermissionHandler(c, f, cmd.ID, cmd.Epoch)
					activePlan = cmd.Plan
					done = make(chan runtimeResult, 1)
					go func() {
						defer dropTerminalWorkspace(runCtx, cmd.ID, cmd.Epoch)
						r, e := run(runCtx, cmd.Plan, func(o executor.Observation) error {
							if o.Kind == "WORKSPACE_READY" {
								return pinTerminalWorkspace(runCtx, cmd.ID, cmd.Epoch, cmd.Plan.Workdir, o.Content, controller)
							}
							select {
							case observations <- o:
								return nil
							case <-runCtx.Done():
								return runCtx.Err()
							}
						})
						done <- runtimeResult{r, e}
					}()
				}
			}
		}
		// Drain runtime observations until the next heartbeat. Raw runtime text
		// can contain secrets; persist only the observation kind, never its body.
	waiting:
		for {
			// Keep pre-stop observations under backpressure until resume. They
			// must neither be lost nor become RUNNING-only logs while PAUSED.
			var observationInput <-chan executor.Observation = observations
			if j.control != nil && j.control.IsPaused() {
				observationInput = nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
				break waiting
			case inspectionErr := <-inspectionDone:
				stopInspection()
				stopInspection = nil
				inspectionDone = nil
				if inspectionErr != nil {
					return inspectionErr
				}
				break waiting
			case o := <-observationInput:
				if j.Attempt != nil {
					content := "Runtime observation received"
					if o.Kind == "RUNTIME_COMPLETED" {
						content = "Runtime reported completion; delivery has not been accepted"
					}
					if output := executor.CitedOutput(activePlan, o); activePlan.Planning == nil && output != "" {
						if err = j.enqueue(store, receipt{Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Runtime knowledge citation output", Content: output}); err != nil {
							return err
						}
					}
					if output := executor.DistilledOutput(activePlan, o); output != "" {
						if err = j.enqueue(store, receipt{Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Runtime skill distillation output", Content: output}); err != nil {
							return err
						}
					}
					observedReceipt := receipt{Kind: "LOG", Content: content}
					if o.Kind == "PROGRESS" {
						observedReceipt = receipt{Kind: "PROGRESS", Progress: o.Progress}
					}
					if err = j.enqueue(store, observedReceipt); err != nil {
						return err
					}
					requested, e := j.flush(ctx, store, c, f)
					if e != nil {
						return e
					}
					if requested {
						cancelRequested = true
						stopRuntime()
					}
				}
			case result := <-done:
				stopRuntime()
				j.control = nil
				stop = nil
				done = nil
				kind, content := "LOST", "Runtime outcome unknown"
				if cancelRequested {
					content = "Cancellation requested; runtime stopped, descendant shutdown not yet confirmed"
					if result.Result.CancellationConfirmed {
						kind, content = "CANCELLED", "Cancellation requested; runtime process tree terminated and exit confirmed"
					}
				} else if errors.Is(result.Err, executor.ErrUnsupported) || errors.Is(result.Err, executor.ErrRuntimeFailed) {
					kind, content = "FAILED", "Runtime failed or capability unsupported"
				} else if result.Err == nil && result.Result.RuntimeCompleted {
					content = "Runtime completed without a workspace artifact; no delivery success asserted."
					if activePlan.Planning != nil && result.Result.PlanningDocument != "" {
						kind, content = "COMPLETED", result.Result.PlanningDocument
					} else if activePlan.Handover != nil && result.Result.HandoverDocument != "" {
						kind, content = "COMPLETED", result.Result.HandoverDocument
					} else if result.Result.Workspace != "" && result.Result.HeadCommit != "" {
						requested, e := submitWorkspaceEvidence(ctx, store, c, f, j, result.Result)
						if e != nil {
							return e
						}
						if !requested {
							kind, content = "COMPLETED", "Workspace commit and diff recorded; Claim submitted for independent verification and Human acceptance"
						} else {
							content = "Cancellation requested before Claim submission; no completion asserted"
						}
					}
				}
				if j.Attempt != nil {
					terminal := receipt{Kind: kind, Content: content}
					if kind == "COMPLETED" {
						terminal.HeadCommit = result.Result.HeadCommit
					}
					if err = j.enqueue(store, terminal); err != nil {
						return err
					}
					if _, err = j.flush(ctx, store, c, f); err != nil {
						return err
					}
				}
				cancelRequested = false
			}
		}
	}
}

// Evidence receipts share the persistent replay journal and exact sequence
// fencing used by logs. No report, criterion coverage or acceptance is invented.
func submitWorkspaceEvidence(ctx context.Context, store *Store, c *Client, f fence, j *journal, result executor.Result) (bool, error) {
	receipts := []receipt{}
	if result.Progress != nil {
		receipts = append(receipts, receipt{Kind: "PROGRESS", Progress: result.Progress})
	}
	if len(result.Diff) > 0 {
		receipts = append(receipts, receipt{Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Execution Git diff", Upload: &artifactUpload{Name: "changes.patch", Data: result.Diff, Coverage: "NOT_APPLICABLE", Note: "Actual Git delta from pinned base to head; independent criterion verification has not run."}})
	}
	for i, a := range result.Artifacts {
		receipts = append(receipts, receipt{Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: fmt.Sprintf("Planned artifact %d", i+1), Upload: &artifactUpload{CriterionID: a.CriterionID, Name: a.Name, Data: a.Data, Coverage: "COVERED", Note: a.Note}})
	}
	receipts = append(receipts, receipt{Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Execution head commit", Content: result.HeadCommit})
	for _, r := range receipts {
		if err := j.enqueue(store, r); err != nil {
			return false, err
		}
		requested, err := j.flush(ctx, store, c, f)
		if err != nil || requested {
			return requested, err
		}
	}
	return false, nil
}

func unsupportedReason(kind string) string {
	if kind == "FAILED" {
		return "Runtime or authorization mode unsupported; no process launched"
	}
	return ""
}

// Status exposes identity and queue size only, never tokens or credentials.
func Status(store *Store, out io.Writer) error {
	p, err := store.Profile()
	if err != nil {
		return err
	}
	var j journal
	if err = store.Read("journal.json", &j); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"server": p.Server, "machine_id": p.MachineID, "pending_receipts": len(j.Outbox), "interrupted_attempt": j.Attempt != nil, "remote_access": "UNAVAILABLE", "credential_storage": storageDescription()})
}

func storageDescription() string {
	if runtime.GOOS == "windows" {
		return "OS_USER_PROTECTED"
	}
	return "PRIVATE_FILE_FALLBACK"
}
