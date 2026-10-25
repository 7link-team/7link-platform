package control

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type workspaceCommand struct {
	ID               string    `json:"id"`
	AgentPrincipalID string    `json:"agent_principal_id"`
	MachineID        string    `json:"machine_id"`
	SessionID        string    `json:"session_id"`
	ConnEpoch        int64     `json:"conn_epoch"`
	ConfigRevision   int       `json:"config_revision"`
	Path             string    `json:"path"`
	Kind             string    `json:"kind"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type workspaceEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}
type workspaceResult struct {
	Items   []workspaceEntry `json:"items"`
	Content string           `json:"content"`
	Reason  string           `json:"reason,omitempty"`
}

// The private root is runner-owned and cannot be supplied by the server. Root
// operations enforce containment even if a link changes during the request.
func readAgentWorkspace(store *Store, command workspaceCommand) workspaceResult {
	result := workspaceResult{Items: []workspaceEntry{}}
	fail := func(reason string) workspaceResult { return workspaceResult{Items: []workspaceEntry{}, Reason: reason} }
	if store == nil || !validID(command.AgentPrincipalID) || command.ConfigRevision < 1 || !command.ExpiresAt.After(time.Now()) || len(command.Path) > 2048 || !utf8.ValidString(command.Path) || strings.ContainsAny(command.Path, "\\:\x00") || command.Path != "" && !fs.ValidPath(command.Path) {
		return fail("INVALID_PATH")
	}
	var credential privateAgentCredential
	if err := store.Read("agent-credential-"+command.AgentPrincipalID+".json", &credential); err != nil || credential.AgentPrincipalID != command.AgentPrincipalID || credential.MachineID != command.MachineID || credential.ConfigRevision != command.ConfigRevision {
		return fail("UNAVAILABLE")
	}
	profile, e := os.OpenRoot(store.Dir)
	if e != nil {
		return fail("UNAVAILABLE")
	}
	defer profile.Close()
	name := "agent-private-" + command.AgentPrincipalID
	info, e := profile.Lstat(name)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return fail("NOT_FOUND")
		}
		return fail("UNAVAILABLE")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("INVALID_PATH")
	}
	root, e := profile.OpenRoot(name)
	if e != nil {
		return fail("INVALID_PATH")
	}
	defer root.Close()
	target := command.Path
	if target == "" {
		target = "."
	}
	file, e := root.Open(target)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return fail("NOT_FOUND")
		}
		return fail("INVALID_PATH")
	}
	defer file.Close()
	info, e = file.Stat()
	if e != nil {
		return fail("UNAVAILABLE")
	}
	switch command.Kind {
	case "LIST":
		if !info.IsDir() {
			return fail("INVALID_PATH")
		}
		entries, err := file.ReadDir(1001)
		if err != nil && !errors.Is(err, io.EOF) {
			return fail("UNAVAILABLE")
		}
		if len(entries) > 1000 {
			return fail("TOO_LARGE")
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			stat, err := entry.Info()
			if err != nil {
				return fail("UNAVAILABLE")
			}
			kind := "FILE"
			if entry.IsDir() {
				kind = "DIRECTORY"
			} else if !stat.Mode().IsRegular() {
				continue
			}
			if len(entry.Name()) > 255 || !utf8.ValidString(entry.Name()) {
				continue
			}
			result.Items = append(result.Items, workspaceEntry{Name: entry.Name(), Kind: kind, Size: stat.Size()})
		}
		sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Name < result.Items[j].Name })
	case "CONTENT":
		if !info.Mode().IsRegular() {
			return fail("INVALID_PATH")
		}
		if info.Size() > 1048576 {
			return fail("TOO_LARGE")
		}
		data, err := io.ReadAll(io.LimitReader(file, 1048577))
		if err != nil {
			return fail("UNAVAILABLE")
		}
		if len(data) > 1048576 {
			return fail("TOO_LARGE")
		}
		if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			return fail("INVALID_PATH")
		}
		result.Content = string(data)
	default:
		return fail("INVALID_PATH")
	}
	return result
}
func processAgentWorkspace(ctx context.Context, store *Store, c *Client, p Profile, f fence) error {
	var commands []workspaceCommand
	if e := c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/agent-workspace-pulls", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &commands, true); e != nil {
		return e
	}
	if len(commands) > 1 {
		return errors.New("invalid workspace read batch")
	}
	for _, command := range commands {
		if !validID(command.ID) || command.MachineID != p.MachineID || command.SessionID != f.SessionID || command.ConnEpoch != f.Epoch {
			return errors.New("workspace read fence mismatch")
		}
		result := readAgentWorkspace(store, command)
		body := struct {
			SessionID string `json:"session_id"`
			ConnEpoch int64  `json:"conn_epoch"`
			workspaceResult
		}{f.SessionID, f.Epoch, result}
		if e := c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/agent-workspace-reads/"+command.ID+"/result", body, nil, true); e != nil {
			var rejected *APIError
			if errors.As(e, &rejected) && rejected.Status == http.StatusConflict {
				// The browser may have cancelled or timed out after this command
				// was pulled. A conflict can also mean the session was fenced, so
				// revalidate it before discarding only the obsolete read result.
				// Pulls do not consume commands; any new read remains queued.
				var current []workspaceCommand
				if check := c.JSON(ctx, "POST", "/machines/"+p.MachineID+"/agent-workspace-pulls", map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch}, &current, true); check != nil {
					return check
				}
				continue
			}
			return e
		}
	}
	return nil
}
