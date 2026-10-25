package control

import (
	"context"
	"errors"
	"os"
	"time"
)

type agentReset struct {
	ID               string    `json:"id"`
	AgentPrincipalID string    `json:"agent_principal_id"`
	MachineID        string    `json:"machine_id"`
	Kind             string    `json:"kind"`
	State            string    `json:"state"`
	SessionEpoch     int       `json:"session_epoch"`
	WorkspaceEpoch   int       `json:"workspace_epoch"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type resetReceipt struct {
	Command   agentReset `json:"command"`
	SessionID string     `json:"session_id"`
	Epoch     int64      `json:"conn_epoch"`
	State     string     `json:"state"`
	Reason    string     `json:"reason_code"`
}

// Pulling a command does not prove an agent runtime was reset. The current
// runner launches isolated execution processes and has no persistent agent
// session/workspace owner. Report that missing capability explicitly.
func processAgentReset(ctx context.Context, store *Store, c *Client, p Profile, f fence) error {
	var commands []agentReset
	if err := c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/agent-reset-pulls", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &commands, true); err != nil {
		return err
	}
	if len(commands) > 1 {
		return errors.New("server returned unsupported reset batch")
	}
	if len(commands) == 0 {
		return nil
	}
	cmd := commands[0]
	if !validID(cmd.ID) || !validID(cmd.AgentPrincipalID) || cmd.MachineID != p.MachineID || cmd.State != "RUNNING" || cmd.SessionEpoch < 1 || cmd.WorkspaceEpoch < 1 || !cmd.ExpiresAt.After(time.Now()) || (cmd.Kind != "RESTART" && cmd.Kind != "SESSION" && cmd.Kind != "FULL") {
		return errors.New("invalid agent reset command")
	}
	var previous resetReceipt
	if err := store.Read("agent-reset-receipt.json", &previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	receipt := resetReceipt{Command: cmd, SessionID: f.SessionID, Epoch: f.Epoch, State: "FAILED", Reason: "UNSUPPORTED_AGENT_RUNTIME"}
	if previous.Command.ID == cmd.ID {
		if previous.Command != cmd || previous.SessionID != f.SessionID || previous.Epoch != f.Epoch {
			return errors.New("agent reset replay fence changed")
		}
		receipt = previous
	}
	if err := store.Write("agent-reset-receipt.json", receipt); err != nil {
		return err
	}
	var ack agentReset
	if err := c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/agent-resets/"+cmd.ID+"/result", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch, "state": receipt.State, "reason_code": receipt.Reason}, &ack, true); err != nil {
		return err
	}
	if ack.ID != cmd.ID || ack.MachineID != cmd.MachineID || ack.State != receipt.State {
		return errors.New("agent reset acknowledgement mismatch")
	}
	return nil
}
