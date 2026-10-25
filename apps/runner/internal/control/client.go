// Package control implements the runner side of the published fleet protocol.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APIError deliberately excludes response bodies, which can contain credentials.
var ErrConnection = errors.New("server connection failed")

type APIError struct {
	Status int
	Reason string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server rejected request (HTTP %d, %s)", e.Status, e.Reason)
}

type Client struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	store     *Store
	base      string
	http      *http.Client
	token     string
	expires   time.Time
}

func NewClient(server string) (*Client, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return nil, errors.New("server must use HTTPS (HTTP is allowed only on loopback)")
	}
	return &Client{base: strings.TrimRight(server, "/"), http: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) request(ctx context.Context, method, path, contentType string, body []byte, authenticated bool, out any, envelope bool) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot construct server request")
	}
	req.Header.Set("Content-Type", contentType)
	if authenticated {
		c.mu.RLock()
		token := c.token
		c.mu.RUnlock()
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrConnection
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return errors.New("invalid server response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem struct {
			Data struct {
				Reason string `json:"reason_code"`
			} `json:"data"`
		}
		_ = json.Unmarshal(data, &problem)
		reason := "REQUEST_REJECTED"
		switch problem.Data.Reason {
		case "AUTHORIZATION_PENDING", "SLOW_DOWN", "RESTART_ENROLLMENT", "CREDENTIAL_ALREADY_DELIVERED":
			reason = problem.Data.Reason
		}
		return &APIError{Status: res.StatusCode, Reason: reason}
	}
	if out == nil {
		return nil
	}
	if envelope {
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &wrapper) != nil || len(wrapper.Data) == 0 || bytes.Equal(wrapper.Data, []byte("null")) {
			return errors.New("missing server response data")
		}
		data = wrapper.Data
	}
	if json.Unmarshal(data, out) != nil {
		return errors.New("invalid server response data")
	}
	return nil
}

func (c *Client) JSON(ctx context.Context, method, path string, body, out any, authenticated bool) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	c.mu.RLock()
	rejectedToken := c.token
	c.mu.RUnlock()
	err = c.request(ctx, method, path, "application/json", data, authenticated, out, true)
	var api *APIError
	if !authenticated || c.store == nil || !errors.As(err, &api) || api.Status != http.StatusUnauthorized {
		return err
	}
	// IAM credential retirement bumps the entire account's token version. A
	// rejected token can still be unexpired by clock; refresh from the durable
	// current identity, then retry once. A 401 authenticates no command, unlike
	// transport failures whose side effects are unknown and must not replay.
	c.refreshMu.Lock()
	c.mu.Lock()
	needsRefresh := c.token == rejectedToken
	if needsRefresh {
		c.token = ""
		c.expires = time.Time{}
	}
	c.mu.Unlock()
	if needsRefresh {
		p, e := c.store.Profile()
		if e == nil {
			_, e = authenticateProfile(ctx, c.store, c, p)
		}
		if e != nil {
			c.refreshMu.Unlock()
			return e
		}
	}
	c.refreshMu.Unlock()
	return c.request(ctx, method, path, "application/json", data, authenticated, out, true)
}

func (c *Client) Authenticate(ctx context.Context, profile Profile) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {profile.ClientID}, "client_secret": {profile.Secret}}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := c.request(ctx, "POST", "/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), false, &token, false); err != nil {
		return err
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.ExpiresIn < 1 || token.ExpiresIn > 86400*365 {
		return errors.New("invalid access token response")
	}
	c.token = token.AccessToken
	c.expires = time.Now().Add(time.Duration(token.ExpiresIn)*time.Second - time.Second)
	return nil
}
