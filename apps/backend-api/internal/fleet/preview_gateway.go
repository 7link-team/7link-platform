package fleet

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/pkg/module"
)

func (m *Module) Start(ctx context.Context) error {
	c := m.config.Preview
	if c.Domain == "" {
		return nil
	}
	if c.Listen == "" || c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
		return errors.New("preview gateway requires listen address and TLS certificate")
	}
	if _, err := tls.LoadX509KeyPair(c.TLS.CertFile, c.TLS.KeyFile); err != nil {
		return errors.New("preview gateway TLS certificate unavailable")
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: m.previewGatewayHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := tls.LoadX509KeyPair(c.TLS.CertFile, c.TLS.KeyFile)
		return &cert, err
	}}}
	m.previewBroker.gateway = server
	go func() { _ = server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	return nil
}
func (m *Module) Stop(ctx context.Context) error {
	if m.previewBroker == nil {
		return nil
	}
	b := m.previewBroker
	b.mu.Lock()
	var streams []*previewStream
	for _, c := range b.connections {
		c.once.Do(func() { close(c.done) })
		for _, s := range c.streams {
			streams = append(streams, s)
		}
	}
	b.mu.Unlock()
	for _, s := range streams {
		_ = s.Close()
	}
	if b.gateway != nil {
		return b.gateway.Shutdown(ctx)
	}
	return nil
}

var _ module.Starter = (*Module)(nil)
var _ module.Stopper = (*Module)(nil)

func (m *Module) previewGatewayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := m.config.Preview
		if c.Domain == "" || m.previewAccess.Authorize == nil || m.previewBroker == nil {
			http.Error(w, "preview unavailable", http.StatusServiceUnavailable)
			return
		}
		host := strings.ToLower(r.Host)
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		label, path := "", r.URL.Path
		if c.Mode == "PATH" {
			if host != c.Domain || !strings.HasPrefix(path, "/p/") {
				http.NotFound(w, r)
				return
			}
			parts := strings.SplitN(strings.TrimPrefix(path, "/p/"), "/", 2)
			label = parts[0]
			path = "/"
			if len(parts) == 2 {
				path += parts[1]
			}
		} else {
			suffix := "." + c.Domain
			if !strings.HasSuffix(host, suffix) {
				http.NotFound(w, r)
				return
			}
			label = strings.TrimSuffix(host, suffix)
		}
		if !validPreviewLabel(label) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if path == "/__access" {
			cookie, returnPath, _, err := m.previewAccess.ExchangeCode(r.Context(), label, r.URL.Query().Get("code"))
			if err != nil {
				http.Error(w, "preview access denied", http.StatusForbidden)
				return
			}
			if !strings.HasPrefix(returnPath, "/") || strings.HasPrefix(returnPath, "//") || strings.ContainsAny(returnPath, "\\\r\n\x00") {
				http.Error(w, "preview access denied", http.StatusForbidden)
				return
			}
			w.Header().Set("Set-Cookie", cookie)
			w.Header().Set("Cache-Control", "no-store")
			if c.Mode == "PATH" {
				returnPath = "/p/" + label + returnPath
			}
			http.Redirect(w, r, returnPath, http.StatusSeeOther)
			return
		}
		grant, err := m.previewAccess.Authorize(r.Context(), label, r.Header.Get("Cookie"), r.Method)
		if err != nil {
			http.Redirect(w, r, strings.TrimSuffix(c.ConsoleOrigin, "/")+"/preview-access?label="+url.QueryEscape(label), http.StatusFound)
			return
		}
		if err = m.validatePreviewGrant(r.Context(), grant); err != nil {
			http.Error(w, "preview unavailable", http.StatusServiceUnavailable)
			return
		}
		if grant.Cookie != "" {
			w.Header().Add("Set-Cookie", grant.Cookie)
		}
		request := r.Clone(r.Context())
		request.URL.Path = path
		request.URL.RawPath = ""
		request.Body = http.MaxBytesReader(w, r.Body, 50<<20)
		transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second, ForceAttemptHTTP2: false, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Transport may cancel its dial context after handing off a connection.
			// Stream lifetime follows the browser request, including upgrades.
			return m.previewBroker.open(r.Context(), grant)
		}}
		defer transport.CloseIdleConnections()
		proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			out := proxyRequest.Out
			out.URL.RawQuery = proxyRequest.In.URL.RawQuery
			out.URL.Scheme = "http"
			out.URL.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(grant.Port))
			out.Host = out.URL.Host
			out.Header.Del("Forwarded")
			out.Header.Del("X-Forwarded-For")
			out.Header.Del("X-Forwarded-Host")
			out.Header.Del("X-Forwarded-Proto")
			if clientIP, _, err := net.SplitHostPort(proxyRequest.In.RemoteAddr); err == nil {
				out.Header.Set("X-Forwarded-For", clientIP)
			}
			out.Header.Del("Cookie")
			for _, cookie := range r.Cookies() {
				if cookie.Name != "__Host-preview" && cookie.Name != "__Secure-preview" {
					out.AddCookie(cookie)
				}
			}
		}, ModifyResponse: func(response *http.Response) error {
			if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				response.Body = previewSSEHeartbeat(r.Context(), response.Body, 30*time.Second)
				response.ContentLength = -1
				response.Header.Del("Content-Length")
			}
			cookies := response.Cookies()
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Name != "__Host-preview" && cookie.Name != "__Secure-preview" {
					response.Header.Add("Set-Cookie", cookie.String())
				}
			}
			response.Header.Set("X-Content-Type-Options", "nosniff")
			response.Header.Set("Referrer-Policy", "no-referrer")
			return nil
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "preview request too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "preview upstream unavailable", http.StatusBadGateway)
		}}
		proxy.ServeHTTP(&previewResponseWriter{ResponseWriter: w}, request)
	})
}

type previewResponseWriter struct{ http.ResponseWriter }

func (w *previewResponseWriter) Write(data []byte) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.ResponseWriter.Write(data)
}
func (w *previewResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Comments are inserted only between complete SSE events, never into an
// application's partially written line or event.
func previewSSEHeartbeat(ctx context.Context, source io.ReadCloser, interval time.Duration) io.ReadCloser {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	type part struct {
		data []byte
		err  error
	}
	parts := make(chan part, 1)
	go func() {
		defer close(parts)
		for {
			data := make([]byte, 16*1024)
			n, err := source.Read(data)
			select {
			case parts <- part{data[:n], err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer cancel()
		defer func() { _ = source.Close() }()
		defer func() { _ = writer.Close() }()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		boundary := true
		newlines := 0
		for {
			select {
			case <-ctx.Done():
				_ = writer.CloseWithError(ctx.Err())
				return
			case <-ticker.C:
				if boundary {
					if _, err := writer.Write([]byte(": heartbeat\n\n")); err != nil {
						return
					}
				}
			case p, ok := <-parts:
				if !ok {
					return
				}
				if len(p.data) > 0 {
					if _, err := writer.Write(p.data); err != nil {
						return
					}
					for _, b := range p.data {
						if b == '\n' {
							newlines++
						} else if b != '\r' {
							newlines = 0
						}
					}
					boundary = newlines >= 2
				}
				if p.err != nil {
					if !errors.Is(p.err, io.EOF) {
						_ = writer.CloseWithError(p.err)
					}
					return
				}
			}
		}
	}()
	return &previewSSEReader{PipeReader: reader, cancel: cancel, source: source}
}

type previewSSEReader struct {
	*io.PipeReader
	cancel context.CancelFunc
	source io.ReadCloser
}

func (r *previewSSEReader) Close() error {
	r.cancel()
	_ = r.source.Close()
	return r.PipeReader.Close()
}
