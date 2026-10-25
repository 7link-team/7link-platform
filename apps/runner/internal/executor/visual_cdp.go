package executor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" // WebSocket handshake specified by RFC 6455, not a security digest.
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// A bounded local CDP transport avoids installing a second browser or runtime.
// It only connects to the loopback endpoint of the isolated browser we started.
type visualCDP struct {
	conn         net.Conn
	reader       *bufio.Reader
	id           int
	consoleError bool
	loaded       bool
}

func dialVisualCDP(ctx context.Context, address string) (*visualCDP, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "ws" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return nil, errVisualCDP
	}
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
	if e != nil {
		return nil, e
	}
	stopClose := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stopClose()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute)
	}
	c.SetDeadline(deadline)
	keyBytes := make([]byte, 16)
	if _, e = rand.Read(keyBytes); e != nil {
		c.Close()
		return nil, e
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	_, e = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", u.RequestURI(), u.Host, key)
	if e != nil {
		c.Close()
		return nil, e
	}
	r := bufio.NewReader(c)
	resp, e := http.ReadResponse(r, &http.Request{Method: "GET"})
	if e != nil {
		c.Close()
		return nil, e
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		c.Close()
		return nil, errVisualCDP
	}
	return &visualCDP{conn: c, reader: r}, nil
}

func (c *visualCDP) write(op byte, data []byte) error {
	header := []byte{0x80 | op, 0x80}
	if len(data) < 126 {
		header[1] |= byte(len(data))
	} else if len(data) <= 65535 {
		header[1] |= 126
		header = binary.BigEndian.AppendUint16(header, uint16(len(data)))
	} else {
		header[1] |= 127
		header = binary.BigEndian.AppendUint64(header, uint64(len(data)))
	}
	mask := make([]byte, 4)
	if _, e := rand.Read(mask); e != nil {
		return e
	}
	header = append(header, mask...)
	frame := append(header, make([]byte, len(data))...)
	for i, b := range data {
		frame[len(header)+i] = b ^ mask[i%4]
	}
	_, e := io.Copy(c.conn, bytes.NewReader(frame))
	return e
}

func (c *visualCDP) read() ([]byte, error) {
	var message []byte
	for {
		header := make([]byte, 2)
		if _, e := io.ReadFull(c.reader, header); e != nil {
			return nil, e
		}
		if header[0]&0x70 != 0 || header[1]&0x80 != 0 {
			return nil, errVisualCDP
		}
		n := uint64(header[1] & 127)
		switch n {
		case 126:
			var b [2]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return nil, e
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return nil, e
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 8<<20 || uint64(len(message))+n > 8<<20 {
			return nil, errVisualCDP
		}
		data := make([]byte, int(n))
		if _, e := io.ReadFull(c.reader, data); e != nil {
			return nil, e
		}
		op := header[0] & 15
		if op == 8 {
			return nil, io.EOF
		}
		if op == 9 {
			if e := c.write(10, data); e != nil {
				return nil, e
			}
			continue
		}
		if op == 10 {
			continue
		}
		if op != 0 && op != 1 {
			return nil, errVisualCDP
		}
		message = append(message, data...)
		if header[0]&0x80 != 0 {
			return message, nil
		}
	}
}

func (c *visualCDP) call(method string, params any, result any) error {
	c.id++
	payload, e := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	if e != nil {
		return e
	}
	if e = c.write(1, payload); e != nil {
		return e
	}
	for {
		data, e := c.read()
		if e != nil {
			return e
		}
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &msg) != nil {
			return errVisualCDP
		}
		if msg.Method == "Runtime.exceptionThrown" {
			c.consoleError = true
		}
		if msg.Method == "Page.loadEventFired" {
			c.loaded = true
		}
		if msg.Method == "Runtime.consoleAPICalled" {
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg.Params, &p) == nil && p.Type == "error" {
				c.consoleError = true
			}
		}
		if msg.ID != c.id {
			continue
		}
		if len(msg.Error) > 0 {
			return errVisualCDP
		}
		if result != nil {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}
