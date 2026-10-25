package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type previewFrame struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	ExecutionID   string `json:"execution_id,omitempty"`
	DispatchEpoch int64  `json:"dispatch_epoch,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Port          int    `json:"port,omitempty"`
	Data          []byte `json:"data,omitempty"`
	Ports         []int  `json:"ports,omitempty"`
}
type previewSocket struct {
	conn    net.Conn
	cancel  context.CancelFunc
	input   chan []byte
	credits chan struct{}
	fin     chan struct{}
	finOnce sync.Once
}

func consumePreviewTunnel(ctx context.Context, c *Client, f fence) {
	delay := time.Second
	for ctx.Err() == nil {
		_ = previewTunnelOnce(ctx, c, f)
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

func previewTunnelOnce(parent context.Context, c *Client, f fence) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	path := "/machines/" + url.PathEscape(f.MachineID) + "/preview-tunnel"
	query := url.Values{"session_id": {f.SessionID}, "conn_epoch": {fmt.Sprint(f.Epoch)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	c.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+c.token)
	c.mu.RUnlock()
	req.Header.Set("Accept", "application/x-ndjson")
	client := *c.http
	client.Timeout = 0
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrConnection
	}
	var sendMu, mu sync.Mutex
	sockets := map[string]*previewSocket{}
	closed := map[string]bool{}
	var closedOrder []string
	rememberClosedLocked := func(id string) {
		if closed[id] {
			return
		}
		closed[id] = true
		closedOrder = append(closedOrder, id)
		if len(closedOrder) > 512 {
			delete(closed, closedOrder[0])
			closedOrder = closedOrder[1:]
		}
	}
	rememberClosed := func(id string) { mu.Lock(); defer mu.Unlock(); rememberClosedLocked(id) }
	pending := make(chan struct{}, 256)
	var workers sync.WaitGroup
	send := func(frame previewFrame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		err := c.JSON(ctx, http.MethodPost, path+"/frames", struct {
			SessionID string         `json:"session_id"`
			Epoch     int64          `json:"conn_epoch"`
			Frames    []previewFrame `json:"frames"`
		}{f.SessionID, f.Epoch, []previewFrame{frame}}, nil, true)
		if err != nil {
			cancel()
		}
		return err
	}
	closeSocket := func(id string, s *previewSocket) {
		mu.Lock()
		if sockets[id] != s {
			mu.Unlock()
			return
		}
		delete(sockets, id)
		for draining := true; draining; {
			select {
			case <-s.input:
				<-pending
			default:
				draining = false
			}
		}
		rememberClosedLocked(id)
		mu.Unlock()
		s.cancel()
		_ = s.conn.Close()
	}
	defer func() {
		cancel()
		mu.Lock()
		for _, s := range sockets {
			s.cancel()
			_ = s.conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	}()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		var frame previewFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil || len(frame.Data) > 16*1024 || len(frame.ID) > 128 {
			return errors.New("invalid preview frame")
		}
		if frame.Kind == "ping" {
			if err := send(previewFrame{Kind: "pong"}); err != nil {
				return err
			}
			continue
		}
		if frame.ID == "" {
			return errors.New("missing preview stream")
		}
		mu.Lock()
		s := sockets[frame.ID]
		wasClosed := closed[frame.ID]
		count := len(sockets)
		mu.Unlock()
		switch frame.Kind {
		case "open", "probe":
			if s != nil || wasClosed {
				return errors.New("duplicate preview stream")
			}
			pin, resolveErr := resolveTerminalWorkspace(ctx, frame.ExecutionID, frame.DispatchEpoch, frame.Workspace)
			if resolveErr != nil || pin.processControl == nil || frame.Port < 0 || frame.Port > 65535 || (frame.Port == 0 && frame.Kind != "probe") || count >= 4096 {
				rememberClosed(frame.ID)
				_ = send(previewFrame{Kind: "rejected", ID: frame.ID})
				continue
			}
			if frame.Kind == "probe" && frame.Port == 0 {
				ports, listErr := pin.processControl.OwnedLoopbackPorts(ctx)
				if listErr != nil || len(ports) > 4096 {
					rememberClosed(frame.ID)
					_ = send(previewFrame{Kind: "rejected", ID: frame.ID})
					continue
				}
				if err := send(previewFrame{Kind: "opened", ID: frame.ID, Ports: ports}); err != nil {
					return err
				}
				rememberClosed(frame.ID)
				continue
			}
			address, resolveErr := pin.processControl.OwnedLoopbackAddress(ctx, uint16(frame.Port))
			if resolveErr != nil {
				rememberClosed(frame.ID)
				_ = send(previewFrame{Kind: "rejected", ID: frame.ID})
				continue
			}
			if frame.Kind == "probe" {
				if err := send(previewFrame{Kind: "opened", ID: frame.ID, Ports: []int{frame.Port}}); err != nil {
					return err
				}
				rememberClosed(frame.ID)
				continue
			}
			socketCtx, socketCancel := context.WithCancel(ctx)
			stop := context.AfterFunc(pin.lifetime, socketCancel)
			conn, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(socketCtx, "tcp", address)
			if dialErr == nil {
				var current string
				current, dialErr = pin.processControl.OwnedLoopbackAddress(socketCtx, uint16(frame.Port))
				if current != address {
					dialErr = errors.New("preview listener changed")
				}
			}
			if dialErr != nil {
				if conn != nil {
					_ = conn.Close()
				}
				stop()
				socketCancel()
				rememberClosed(frame.ID)
				_ = send(previewFrame{Kind: "rejected", ID: frame.ID})
				continue
			}
			s = &previewSocket{conn: conn, cancel: func() { stop(); socketCancel() }, input: make(chan []byte, 16), credits: make(chan struct{}, 16), fin: make(chan struct{})}
			for i := 0; i < 16; i++ {
				s.credits <- struct{}{}
			}
			mu.Lock()
			sockets[frame.ID] = s
			mu.Unlock()
			id := frame.ID
			port := uint16(frame.Port)
			socket := s
			if err := send(previewFrame{Kind: "opened", ID: id}); err != nil {
				return err
			}
			workers.Add(3)
			go func() {
				defer workers.Done()
				defer closeSocket(id, socket)
				defer func() { _ = send(previewFrame{Kind: "reset", ID: id}) }()
				for {
					select {
					case <-socketCtx.Done():
						return
					case data := <-socket.input:
						for len(data) > 0 {
							n, e := socket.conn.Write(data)
							if e != nil || n == 0 {
								<-pending
								return
							}
							data = data[n:]
						}
						<-pending
						if send(previewFrame{Kind: "window", ID: id}) != nil {
							return
						}
					case <-socket.fin:
						// All data frames preceding FIN must reach the native socket first.
						for {
							select {
							case data := <-socket.input:
								for len(data) > 0 {
									n, e := socket.conn.Write(data)
									if e != nil || n == 0 {
										<-pending
										return
									}
									data = data[n:]
								}
								<-pending
								_ = send(previewFrame{Kind: "window", ID: id})
							default:
								if tcp, ok := socket.conn.(*net.TCPConn); ok {
									_ = tcp.CloseWrite()
								}
								<-socketCtx.Done()
								return
							}
						}
					}
				}
			}()
			go func() {
				defer workers.Done()
				buf := make([]byte, 16*1024)
				for {
					select {
					case <-socketCtx.Done():
						return
					case <-socket.credits:
					}
					n, e := socket.conn.Read(buf)
					if n > 0 {
						if send(previewFrame{Kind: "data", ID: id, Data: append([]byte(nil), buf[:n]...)}) != nil {
							return
						}
					}
					if e != nil {
						if errors.Is(e, io.EOF) {
							_ = send(previewFrame{Kind: "fin", ID: id})
						} else {
							closeSocket(id, socket)
							_ = send(previewFrame{Kind: "reset", ID: id})
						}
						return
					}
				}
			}()
			go func() {
				defer workers.Done()
				defer closeSocket(id, socket)
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-socketCtx.Done():
						return
					case <-ticker.C:
						current, e := pin.processControl.OwnedLoopbackAddress(socketCtx, port)
						if e != nil || current != address {
							return
						}
					}
				}
			}()
		case "reset":
			if s != nil {
				closeSocket(frame.ID, s)
			} else if !wasClosed {
				return errors.New("unknown preview stream")
			}
		case "data":
			if s == nil {
				if wasClosed {
					continue
				}
				return errors.New("unknown preview stream")
			}
			if len(frame.Data) == 0 {
				return errors.New("empty preview data")
			}
			mu.Lock()
			if sockets[frame.ID] != s {
				mu.Unlock()
				continue
			}
			select {
			case <-s.fin:
				mu.Unlock()
				return errors.New("preview data after fin")
			default:
			}
			select {
			case pending <- struct{}{}:
			default:
				mu.Unlock()
				return errors.New("preview buffer limit")
			}
			select {
			case s.input <- frame.Data:
			default:
				<-pending
				mu.Unlock()
				return errors.New("preview stream buffer limit")
			}
			mu.Unlock()
		case "window":
			if s == nil {
				if wasClosed {
					continue
				}
				return errors.New("unknown preview stream")
			}
			select {
			case s.credits <- struct{}{}:
			default:
				return errors.New("preview credit overflow")
			}
		case "fin":
			if s != nil {
				s.finOnce.Do(func() { close(s.fin) })
			} else if !wasClosed {
				return errors.New("unknown preview stream")
			}
		default:
			return errors.New("invalid preview kind")
		}
	}
	return scanner.Err()
}
