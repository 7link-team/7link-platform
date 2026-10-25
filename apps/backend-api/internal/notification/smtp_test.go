package notification

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// smtpFixture speaks SMTP over a real loopback socket. Codes are responses to
// DATA completion, allowing tests to distinguish temporary/permanent rejection.
func smtpFixture(t *testing.T, codes ...int) (EmailConfig, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var mu sync.Mutex
	var messages []string
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := textproto.NewReader(bufio.NewReader(conn))
				fmt.Fprint(conn, "220 localhost ready\r\n")
				for {
					line, err := reader.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(conn, "250 localhost\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(conn, "250 accepted\r\n")
					case line == "DATA":
						fmt.Fprint(conn, "354 continue\r\n")
						body, err := reader.ReadDotBytes()
						if err != nil {
							return
						}
						mu.Lock()
						index := len(messages)
						messages = append(messages, string(body))
						code := 250
						if index < len(codes) {
							code = codes[index]
						}
						mu.Unlock()
						fmt.Fprintf(conn, "%d result\r\n", code)
					case line == "QUIT":
						// Deliberately drop QUIT: final DATA acceptance is authoritative.
						return
					default:
						fmt.Fprint(conn, "500 unsupported\r\n")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); workers.Wait() })
	address := listener.Addr().(*net.TCPAddr)
	return EmailConfig{Enabled: true, SMTPHost: "127.0.0.1", SMTPPort: address.Port, TLSMode: "none", AllowPlaintext: true, FromAddress: "sender@example.test", MessageIDDomain: "example.test", Timeout: time.Second}, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), messages...) }
}

func TestSMTPAcceptanceAndMIME(t *testing.T) {
	c, messages := smtpFixture(t, 250)
	m := &Module{config: Config{Email: c}}
	p := emailPayload{To: []string{"reader@example.test"}, FromName: "Service", Subject: "通知 ✓", Text: "plain\nmessage", HTML: "<p>message</p>", Headers: map[string]string{"In-Reply-To": "<old@example.test>"}}
	require.NoError(t, m.sendSMTP(t.Context(), p, "<stable@example.test>"))
	require.Len(t, messages(), 1)
	parsed, err := mail.ReadMessage(strings.NewReader(messages()[0]))
	require.NoError(t, err)
	require.Equal(t, "<stable@example.test>", parsed.Header.Get("Message-ID"))
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, p.Subject, subject)
	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	parts := multipart.NewReader(parsed.Body, params["boundary"])
	for _, expected := range []string{p.Text, p.HTML} {
		part, err := parts.NextPart()
		require.NoError(t, err)
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		require.NoError(t, err)
		require.Equal(t, expected, strings.ReplaceAll(string(body), "\r\n", "\n"))
	}
}

func TestSMTPFailuresAndCancellation(t *testing.T) {
	c, _ := smtpFixture(t, 451, 550)
	m := &Module{config: Config{Email: c}}
	p := emailPayload{To: []string{"reader@example.test"}}
	for _, code := range []int{451, 550} {
		err := m.sendSMTP(t.Context(), p, "<stable@example.test>")
		var response *textproto.Error
		require.ErrorAs(t, err, &response)
		require.Equal(t, code, response.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, m.sendSMTP(ctx, p, "<stable@example.test>"))
	m.config.Email.TLSMode = "starttls"
	require.ErrorContains(t, m.sendSMTP(t.Context(), p, "<stable@example.test>"), "requires STARTTLS")
}

func TestSMTPRejectsProtocolAndSecurityFailuresBeforeAcceptance(t *testing.T) {
	for _, stage := range []string{"greeting", "starttls", "auth", "mail", "recipient", "data", "implicit"} {
		t.Run(stage, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				if stage == "implicit" {
					return
				}
				if stage == "greeting" {
					fmt.Fprint(conn, "554 unavailable\r\n")
					return
				}
				fmt.Fprint(conn, "220 localhost\r\n")
				reader := textproto.NewReader(bufio.NewReader(conn))
				for {
					line, err := reader.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						if stage == "starttls" {
							fmt.Fprint(conn, "250-localhost\r\n250 STARTTLS\r\n")
						} else if stage == "auth" {
							fmt.Fprint(conn, "250-localhost\r\n250 AUTH PLAIN\r\n")
						} else {
							fmt.Fprint(conn, "250 localhost\r\n")
						}
					case strings.HasPrefix(line, "STARTTLS"), strings.HasPrefix(line, "AUTH"):
						fmt.Fprint(conn, "454 denied\r\n")
						return
					case strings.HasPrefix(line, "MAIL"):
						if stage == "mail" {
							fmt.Fprint(conn, "550 sender denied\r\n")
							return
						}
						fmt.Fprint(conn, "250 ok\r\n")
					case strings.HasPrefix(line, "RCPT"):
						if stage == "recipient" {
							fmt.Fprint(conn, "550 recipient denied\r\n")
							return
						}
						fmt.Fprint(conn, "250 ok\r\n")
					case line == "DATA":
						fmt.Fprint(conn, "554 data denied\r\n")
						return
					default:
						return
					}
				}
			}()
			config := EmailConfig{SMTPHost: "127.0.0.1", SMTPPort: listener.Addr().(*net.TCPAddr).Port, TLSMode: "none", FromAddress: "sender@example.test", Timeout: time.Second}
			if stage == "starttls" {
				config.TLSMode = "starttls"
			}
			if stage == "implicit" {
				config.TLSMode = "implicit"
			}
			if stage == "auth" {
				config.Username = "test"
				config.Password = "secret"
			}
			m := &Module{config: Config{Email: config}}
			require.Error(t, m.sendSMTP(t.Context(), emailPayload{To: []string{"reader@example.test"}}, "<test@example.test>"))
			<-done
		})
	}
}
