package control

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"7link.com/runner/internal/executor"
	"7link.com/runner/internal/inspection"
)

type workerProfile struct {
	PrincipalID      string `json:"agent_principal_id"`
	Runtime          string `json:"runtime"`
	Model            string `json:"model"`
	CredentialID     string `json:"credential_id,omitempty"`
	ConfigRevision   int    `json:"config_revision,omitempty"`
	LaunchVersion    int    `json:"launch_version,omitempty"`
	RestartRequested bool   `json:"restart_requested,omitempty"`
}
type agentProcess struct {
	PrincipalID    string `json:"agent_principal_id"`
	PID            int    `json:"pid"`
	State          string `json:"state"`
	ConfigRevision *int   `json:"config_revision,omitempty"`
	LaunchVersion  int    `json:"launch_version,omitempty"`
	Attempted      bool   `json:"attempted,omitempty"`
}
type residentWorker struct {
	profile     workerProfile
	process     *executor.Worker
	agentClient *Client
	identity    Profile
}
type agentWorkers struct {
	ctx       context.Context
	available map[string]bool
	workers   map[string]residentWorker
	failures  map[string]workerProfile
}

func newAgentWorkers(ctx context.Context, report inspection.Report) *agentWorkers {
	w := &agentWorkers{ctx: ctx, available: map[string]bool{}, workers: map[string]residentWorker{}}
	for _, e := range report.Executables {
		if e.Available && e.Error == "" {
			w.available[e.Kind] = true
		}
	}
	return w
}
func (w *agentWorkers) sync(ctx context.Context, c *Client, p Profile, f fence) error {
	// Report dead children before resolving the next desired set. Fleet owns
	// the bounded crash window and backoff; a poll must not bypass it by
	// replacing a dead child before the actual exit reaches the server.
	if err := w.report(ctx, c, p, f); err != nil {
		return err
	}
	var profiles []workerProfile
	path := "/machines/" + p.MachineID + "/agent-workers?session_id=" + url.QueryEscape(f.SessionID) + "&conn_epoch=" + fmt.Sprint(f.Epoch)
	if err := c.JSON(ctx, "GET", path, nil, &profiles, true); err != nil {
		return err
	}
	if len(profiles) > 1000 {
		return errors.New("agent worker limit exceeded")
	}
	wanted := map[string]workerProfile{}
	for _, profile := range profiles {
		if !validID(profile.PrincipalID) || (profile.Runtime != "CODEX" && profile.Runtime != "CLAUDE_CODE") || profile.Model == "" {
			return errors.New("invalid bound agent profile")
		}
		if _, duplicate := wanted[profile.PrincipalID]; duplicate {
			return errors.New("duplicate bound agent profile")
		}
		wanted[profile.PrincipalID] = profile
	}
	for id, child := range w.workers {
		profile, exists := wanted[id]
		if !exists || workerNeedsReplacement(child.profile, profile) || !child.process.Alive() || !w.available[profile.Runtime] {
			child.process.Close()
			delete(w.workers, id)
		}
	}
	for id := range w.failures {
		if profile, exists := wanted[id]; !exists || profile != w.failures[id] {
			delete(w.failures, id)
		}
	}
	for id, profile := range wanted {
		if _, exists := w.workers[id]; exists || !w.available[profile.Runtime] {
			continue
		}
		agentClient, identity, err := authenticateAgentWorker(ctx, c, p, profile)
		if err != nil {
			return w.failed(ctx, c, p, f, profile, err)
		}
		child, err := executor.StartWorker(w.ctx, profile.Runtime, profile.Model)
		if err != nil {
			return w.failed(ctx, c, p, f, profile, err)
		}
		delete(w.failures, id)
		w.workers[id] = residentWorker{profile: profile, process: child, agentClient: agentClient, identity: identity}
	}
	return w.report(ctx, c, p, f)
}

// Configuration edits remain pending until an explicit Human restart.
func workerNeedsReplacement(current, desired workerProfile) bool {
	return current.PrincipalID != desired.PrincipalID || current.Runtime != desired.Runtime || current.Model != desired.Model || current.CredentialID != desired.CredentialID || desired.RestartRequested && desired.LaunchVersion != current.LaunchVersion
}
func (w *agentWorkers) failed(ctx context.Context, c *Client, p Profile, f fence, profile workerProfile, cause error) error {
	if w.failures == nil {
		w.failures = map[string]workerProfile{}
	}
	w.failures[profile.PrincipalID] = profile
	return errors.Join(cause, w.report(ctx, c, p, f))
}
func (w *agentWorkers) report(ctx context.Context, c *Client, p Profile, f fence) error {
	agents := []agentProcess{}
	for id, child := range w.workers {
		status, pid := "EXITED", 0
		if child.process.Alive() {
			status, pid = "RUNNING", child.process.PID()
		}
		report := agentProcess{PrincipalID: id, PID: pid, State: status, LaunchVersion: child.profile.LaunchVersion, Attempted: status == "EXITED" && child.profile.LaunchVersion > 0}
		if child.profile.ConfigRevision > 0 {
			revision := child.profile.ConfigRevision
			report.ConfigRevision = &revision
		}
		agents = append(agents, report)
	}
	for id, profile := range w.failures {
		agents = append(agents, agentProcess{PrincipalID: id, State: "EXITED", LaunchVersion: profile.LaunchVersion, Attempted: profile.LaunchVersion > 0})
	}
	return c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/agent-presences", struct {
		SessionID string         `json:"session_id"`
		Epoch     int64          `json:"conn_epoch"`
		Agents    []agentProcess `json:"agents"`
	}{f.SessionID, f.Epoch, agents}, nil, true)
}
func (w *agentWorkers) run(ctx context.Context, plan executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
	// Server pins a positive executor principal only for an assigned Agent;
	// Human executions carry zero. A missing Agent worker never falls back.
	if plan.ExecutorPrincipalID == "" || plan.ExecutorPrincipalID == "0" {
		return executor.Run(ctx, plan, emit)
	}
	child, ok := w.workers[plan.ExecutorPrincipalID]
	if !ok || !child.process.Alive() || child.profile.Runtime != plan.Runtime || child.profile.Model != plan.Model {
		return executor.Result{ResultState: "UNKNOWN"}, executor.ErrUnsupported
	}
	if child.agentClient != nil {
		if err := child.agentClient.Authenticate(ctx, child.identity); err != nil {
			return executor.Result{ResultState: "UNKNOWN"}, err
		}
	}
	return child.process.Run(ctx, plan, emit)
}
func (w *agentWorkers) close(c *Client, p Profile, f fence) {
	for id, child := range w.workers {
		child.process.Close()
		delete(w.workers, id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.report(ctx, c, p, f)
}
