package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type agentMachineCommand struct {
	ID               string    `json:"id"`
	AgentPrincipalID string    `json:"agent_principal_id"`
	MachineID        string    `json:"machine_id"`
	CredentialID     string    `json:"credential_id,omitempty"`
	ConfigRevision   int       `json:"config_revision"`
	Kind             string    `json:"kind"`
	State            string    `json:"state"`
	ExpiresAt        time.Time `json:"expires_at"`
	ClientID         string    `json:"client_id,omitempty"`
	Secret           string    `json:"client_secret,omitempty"`
}
type agentMachineReceipt struct {
	Command        agentMachineCommand `json:"command"`
	CredentialHash string              `json:"credential_hash,omitempty"`
	State          string              `json:"state"`
	Reason         string              `json:"reason_code,omitempty"`
}
type privateAgentCredential struct {
	AgentPrincipalID string `json:"agent_principal_id"`
	MachineID        string `json:"machine_id"`
	ConfigRevision   int    `json:"config_revision"`
	ClientID         string `json:"client_id"`
	Secret           string `json:"client_secret"`
}

// Only the trusted runner host consumes IAM credentials. The model subprocess
// receives neither the secret nor its access token through its environment.
func authenticateAgentWorker(ctx context.Context, machineClient *Client, machine Profile, worker workerProfile) (*Client, Profile, error) {
	if worker.CredentialID == "" {
		return nil, Profile{}, nil
	}
	if machineClient.store == nil || !validID(worker.PrincipalID) || !validID(worker.CredentialID) || worker.ConfigRevision < 1 {
		return nil, Profile{}, errors.New("agent identity store unavailable")
	}
	var credential privateAgentCredential
	if err := machineClient.store.Read("agent-credential-"+worker.PrincipalID+".json", &credential); err != nil {
		return nil, Profile{}, err
	}
	if credential.AgentPrincipalID != worker.PrincipalID || credential.MachineID != machine.MachineID || credential.ClientID != worker.CredentialID || credential.ConfigRevision < 1 || credential.ConfigRevision > worker.ConfigRevision || credential.Secret == "" {
		return nil, Profile{}, errors.New("agent credential binding mismatch")
	}
	identity := Profile{Server: machine.Server, TenantID: machine.TenantID, MachineID: machine.MachineID, ClientID: credential.ClientID, Secret: credential.Secret}
	client, err := NewClient(machine.Server)
	if err != nil {
		return nil, Profile{}, err
	}
	if err = client.Authenticate(ctx, identity); err != nil {
		return nil, Profile{}, err
	}
	var authenticated struct {
		Subject string `json:"subject"`
	}
	if err = client.JSON(ctx, "GET", "/me", nil, &authenticated, true); err != nil {
		return nil, Profile{}, err
	}
	if authenticated.Subject != worker.PrincipalID {
		return nil, Profile{}, errors.New("authenticated agent principal mismatch")
	}
	if credential.ConfigRevision < worker.ConfigRevision {
		var current struct {
			PrincipalID    string `json:"principal_id"`
			Runtime        string `json:"runtime"`
			Model          string `json:"model"`
			ConfigRevision int    `json:"config_revision"`
		}
		path := "/tenants/" + machine.TenantID + "/agents/" + worker.PrincipalID + "/runtime-config"
		if err = client.JSON(ctx, "GET", path, nil, &current, true); err != nil {
			return nil, Profile{}, err
		}
		if current.PrincipalID != worker.PrincipalID || current.Runtime != worker.Runtime || current.Model != worker.Model || current.ConfigRevision != worker.ConfigRevision {
			return nil, Profile{}, errors.New("authenticated agent configuration mismatch")
		}
	}
	return client, identity, nil
}

// Cleanup targets only runner-owned Agent-private storage under the locked
// profile. It cannot accept a server path or touch an execution/project workdir.
func cleanupPrivateAgent(store *Store, agent string) error {
	if !validID(agent) {
		return errors.New("invalid agent identity")
	}
	root, err := filepath.EvalSymlinks(store.Dir)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	leaf := filepath.Join(root, "agent-private-"+agent)
	info, err := os.Lstat(leaf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("invalid private agent directory")
		}
		resolved, e := filepath.EvalSymlinks(leaf)
		if e != nil {
			return e
		}
		parent, e := filepath.Rel(root, resolved)
		if e != nil || parent != "agent-private-"+agent {
			return errors.New("invalid private agent directory")
		}
		if err = os.RemoveAll(leaf); err != nil {
			return err
		}
	}
	credential := filepath.Join(root, "agent-credential-"+agent+".json")
	if info, e := os.Lstat(credential); e == nil {
		if !info.Mode().IsRegular() {
			return errors.New("invalid private agent credential")
		}
		if e = os.Remove(credential); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}
func processAgentMachineCommands(ctx context.Context, store *Store, c *Client, p Profile, f fence, workers *agentWorkers) error {
	var commands []agentMachineCommand
	if err := c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/agent-command-pulls", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &commands, true); err != nil {
		return err
	}
	if len(commands) > 1 {
		return errors.New("invalid agent command batch")
	}
	if len(commands) == 0 {
		return nil
	}
	cmd := commands[0]
	if !validID(cmd.ID) || !validID(cmd.AgentPrincipalID) || cmd.MachineID != p.MachineID || cmd.State != "RUNNING" || cmd.ConfigRevision < 1 || !cmd.ExpiresAt.After(time.Now()) || (cmd.Kind != "CREDENTIAL" && cmd.Kind != "CLEANUP") {
		return errors.New("invalid agent machine command")
	}
	if cmd.Kind == "CREDENTIAL" && (!validID(cmd.CredentialID) || cmd.ClientID != cmd.CredentialID || cmd.Secret == "") {
		return errors.New("invalid agent credential delivery")
	}
	if cmd.Kind == "CLEANUP" && (cmd.Secret != "" || cmd.CredentialID != "" || cmd.ClientID != "") {
		return errors.New("invalid cleanup command")
	}
	hash := ""
	if cmd.Kind == "CREDENTIAL" {
		sum := sha256.Sum256([]byte(cmd.Secret))
		hash = hex.EncodeToString(sum[:])
	}
	identity := cmd
	identity.Secret = ""
	receiptName := "agent-command-receipt-" + cmd.ID + ".json"
	var receipt agentMachineReceipt
	err := store.Read(receiptName, &receipt)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if receipt.Command != identity || receipt.CredentialHash != hash || (receipt.State != "DONE" && receipt.State != "FAILED") {
			return errors.New("agent command replay identity changed")
		}
	} else {
		receipt = agentMachineReceipt{Command: identity, CredentialHash: hash, State: "DONE"}
		if cmd.Kind == "CREDENTIAL" {
			if err = store.Write("agent-credential-"+cmd.AgentPrincipalID+".json", privateAgentCredential{AgentPrincipalID: cmd.AgentPrincipalID, MachineID: p.MachineID, ConfigRevision: cmd.ConfigRevision, ClientID: cmd.ClientID, Secret: cmd.Secret}); err != nil {
				receipt.State, receipt.Reason = "FAILED", "CREDENTIAL_STORE_FAILED"
			}
		} else {
			if workers == nil {
				return errors.New("agent process owner unavailable")
			}
			if child, exists := workers.workers[cmd.AgentPrincipalID]; exists {
				child.process.Close()
				delete(workers.workers, cmd.AgentPrincipalID)
			}
			if err = cleanupPrivateAgent(store, cmd.AgentPrincipalID); err != nil {
				receipt.State, receipt.Reason = "FAILED", "CLEANUP_FAILED"
			}
		}
		if err = store.Write(receiptName, receipt); err != nil {
			return err
		}
	}
	var ack agentMachineCommand
	if err = c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/agent-commands/"+cmd.ID+"/result", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch, "state": receipt.State, "reason_code": receipt.Reason}, &ack, true); err != nil {
		return err
	}
	if ack.ID != cmd.ID || ack.MachineID != cmd.MachineID || ack.AgentPrincipalID != cmd.AgentPrincipalID || ack.Kind != cmd.Kind || ack.State != receipt.State {
		return errors.New("agent command acknowledgement mismatch")
	}
	if err = os.Remove(filepath.Join(store.Dir, receiptName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
