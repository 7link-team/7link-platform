package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"

	"7link.com/runner/internal/executor"
)

const terminalFrameLimit = 16 * 1024

type terminalFrame struct {
	Kind          string `json:"kind"`
	SessionID     string `json:"session_id"`
	Workspace     string `json:"workspace,omitempty"`
	ExecutionID   string `json:"execution_id,omitempty"`
	DispatchEpoch int64  `json:"dispatch_epoch,omitempty"`
	Columns       int    `json:"columns,omitempty"`
	Rows          int    `json:"rows,omitempty"`
	Data          []byte `json:"data,omitempty"`
	Seq           int64  `json:"seq"`
	Sandboxed     bool   `json:"sandboxed"`
	Reason        string `json:"reason,omitempty"`
}

// The adapter keeps transport tests independent of the host pseudoconsole.
type tunnelShell struct {
	input        io.WriteCloser
	output       io.ReadCloser
	wait         func(context.Context) error
	resize       func(uint16, uint16) error
	close        func() error
	inputs       chan []byte
	inputMu      sync.Mutex
	inputsClosed bool
	pendingInput chan struct{}
}

func (s *tunnelShell) enqueueInput(data []byte) bool {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.inputsClosed {
		return false
	}
	select {
	case s.pendingInput <- struct{}{}:
	default:
		return false
	}
	select {
	case s.inputs <- data:
		return true
	default:
		<-s.pendingInput
		return false
	}
}
func (s *tunnelShell) stopInputs() {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.inputsClosed = true
	for {
		select {
		case <-s.inputs:
			<-s.pendingInput
		default:
			return
		}
	}
}

type terminalOpener func(context.Context, string, uint16, uint16) (*tunnelShell, error)

func openTunnelShell(ctx context.Context, workspace string, columns, rows uint16) (*tunnelShell, error) {
	attempt, ok := ctx.Value(terminalAttemptKey{}).(terminalAttempt)
	if !ok {
		return nil, errTerminalWorkspace
	}
	pin, err := resolveTerminalWorkspace(ctx, attempt.id, attempt.epoch, workspace)
	if err != nil {
		return nil, err
	}
	shellCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(pin.lifetime, cancel)
	t, err := executor.OpenTerminal(shellCtx, pin.actual, columns, rows)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	return &tunnelShell{input: t.Input, output: t.Output, wait: t.Wait, resize: t.Resize, close: func() error { stop(); cancel(); return t.Close() }}, nil
}

// Terminal frames are ephemeral: a broken stream destroys all shells and no
// input or open request is replayed on a subsequent connection.
func consumeTerminalTunnel(ctx context.Context, c *Client, f fence) {
	delay := time.Second
	for ctx.Err() == nil {
		_ = terminalTunnelOnce(ctx, c, f, openTunnelShell)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func terminalTunnelOnce(parent context.Context, c *Client, f fence, open terminalOpener) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	path := "/machines/" + url.PathEscape(f.MachineID) + "/terminal-tunnel"
	query := url.Values{"session_id": {f.SessionID}, "conn_epoch": {fmt.Sprint(f.Epoch)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return ErrConnection
	}
	c.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+c.token)
	c.mu.RUnlock()
	req.Header.Set("Accept", "application/x-ndjson")
	streamClient := *c.http
	streamClient.Timeout = 0
	response, err := streamClient.Do(req)
	if err != nil {
		return ErrConnection
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &APIError{Status: response.StatusCode, Reason: "TERMINAL_TUNNEL_UNAVAILABLE"}
	}

	var mu sync.Mutex
	shells := make(map[string]*tunnelShell)
	pendingInput := make(chan struct{}, 256)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		mu.Lock()
		all := make([]*tunnelShell, 0, len(shells))
		for _, shell := range shells {
			all = append(all, shell)
		}
		mu.Unlock()
		for _, shell := range all {
			_ = shell.close()
		}
		workers.Wait()
	}()
	var sendMu sync.Mutex
	sequences := make(map[string]int64)
	send := func(frame terminalFrame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		if frame.Kind != "pong" {
			sequences[frame.SessionID]++
			frame.Seq = sequences[frame.SessionID]
			if frame.Kind == "closed" || frame.Kind == "rejected" {
				delete(sequences, frame.SessionID)
			}
		}
		err := c.JSON(ctx, http.MethodPost, path+"/frames", struct {
			SessionID string          `json:"session_id"`
			Epoch     int64           `json:"conn_epoch"`
			Frames    []terminalFrame `json:"frames"`
		}{f.SessionID, f.Epoch, []terminalFrame{frame}}, nil, true)
		if err != nil {
			cancel()
		}
		return err
	}
	scanner := bufio.NewScanner(response.Body)
	// Base64 adds 1/3, with room for trusted metadata; unbounded NDJSON is refused.
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		var frame terminalFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil || len(frame.Data) > terminalFrameLimit || len(frame.SessionID) > 128 {
			return errors.New("invalid terminal frame")
		}
		if frame.Kind == "ping" {
			if err := send(terminalFrame{Kind: "pong", SessionID: frame.SessionID, Seq: frame.Seq}); err != nil {
				return err
			}
			continue
		}
		if frame.SessionID == "" {
			return errors.New("missing terminal session")
		}
		mu.Lock()
		shell := shells[frame.SessionID]
		count := len(shells)
		mu.Unlock()
		switch frame.Kind {
		case "open":
			if shell != nil {
				return errors.New("duplicate terminal open")
			}
			if count >= 4096 || frame.Columns < 1 || frame.Columns > 500 || frame.Rows < 1 || frame.Rows > 200 {
				if err := send(terminalFrame{Kind: "rejected", SessionID: frame.SessionID, Reason: "TERMINAL_LIMIT_OR_INVALID_REQUEST"}); err != nil {
					return err
				}
				continue
			}
			openCtx := context.WithValue(ctx, terminalAttemptKey{}, terminalAttempt{id: frame.ExecutionID, epoch: frame.DispatchEpoch})
			shell, err = open(openCtx, frame.Workspace, uint16(frame.Columns), uint16(frame.Rows))
			if err != nil {
				reason := "TERMINAL_UNAVAILABLE"
				if errors.Is(err, executor.ErrUnsupported) {
					reason = "TERMINAL_NATIVE_UNSUPPORTED"
				}
				if err := send(terminalFrame{Kind: "rejected", SessionID: frame.SessionID, Reason: reason}); err != nil {
					return err
				}
				continue
			}
			mu.Lock()
			shells[frame.SessionID] = shell
			mu.Unlock()
			shell.inputs = make(chan []byte, 16)
			shell.pendingInput = pendingInput
			nativeClose := shell.close
			shell.close = func() error { shell.stopInputs(); return nativeClose() }
			if err := send(terminalFrame{Kind: "opened", SessionID: frame.SessionID}); err != nil {
				// Even an unacknowledged console may have produced its prompt.
				// Drain it while native Close flushes, without publishing bytes.
				drained := make(chan struct{})
				go func() { _, _ = io.Copy(io.Discard, shell.output); close(drained) }()
				_ = shell.close()
				<-drained
				return err
			}
			workers.Add(1)
			go func(id string, shell *tunnelShell) {
				defer workers.Done()
				// Wait concurrently with the sole output reader, so a console can
				// flush its final bytes without blocking the child process exit.
				readDone := make(chan struct{})
				inputCtx, stopInput := context.WithCancel(ctx)
				inputDone := make(chan struct{})
				go func() {
					defer close(inputDone)
					defer shell.stopInputs()
					for {
						select {
						case <-inputCtx.Done():
							return
						case data := <-shell.inputs:
							n, err := shell.input.Write(data)
							<-shell.pendingInput
							if err != nil || n != len(data) {
								_ = shell.close()
								return
							}
						}
					}
				}()
				go func() {
					defer close(readDone)
					buffer := make([]byte, terminalFrameLimit-utf8.UTFMax)
					var pending []byte
					deliver := true
					for {
						n, readErr := shell.output.Read(buffer)
						if n > 0 && deliver {
							pending = append(pending, buffer[:n]...)
							whole, tail := terminalUTF8Prefix(pending)
							if len(whole) > 0 && send(terminalFrame{Kind: "output", SessionID: id, Data: append([]byte(nil), whole...)}) != nil {
								// Continue draining until native Close finishes flushing.
								deliver = false
							}
							pending = append([]byte(nil), tail...)
						}
						if readErr != nil {
							if len(pending) > 0 && deliver {
								_ = send(terminalFrame{Kind: "output", SessionID: id, Data: []byte("\uFFFD")})
							}
							return
						}
					}
				}()
				_ = shell.wait(ctx)
				stopInput()
				_ = shell.close()
				<-readDone
				<-inputDone
				mu.Lock()
				delete(shells, id)
				mu.Unlock()
				_ = send(terminalFrame{Kind: "closed", SessionID: id})
			}(frame.SessionID, shell)
		case "input":
			if shell == nil {
				continue
			}
			if !shell.enqueueInput(frame.Data) {
				_ = shell.close()
			}
		case "resize":
			if shell == nil {
				continue
			}
			if frame.Columns < 1 || frame.Columns > 500 || frame.Rows < 1 || frame.Rows > 200 {
				return errors.New("invalid terminal dimensions")
			}
			if err := shell.resize(uint16(frame.Columns), uint16(frame.Rows)); err != nil {
				_ = shell.close()
			}
		case "close":
			if shell != nil {
				_ = shell.close()
			}
		default:
			return errors.New("unknown terminal frame")
		}
	}
	return scanner.Err()
}

// Console reads can split a Unicode rune at any byte. Keep its incomplete suffix
// for the next read so both browser output and per-frame asciicast JSON preserve
// exactly the same text. Complete invalid sequences fail closed upstream.
func terminalUTF8Prefix(data []byte) ([]byte, []byte) {
	for i := 0; i < len(data); {
		if !utf8.FullRune(data[i:]) {
			return data[:i], data[i:]
		}
		_, size := utf8.DecodeRune(data[i:])
		i += size
	}
	return data, nil
}
