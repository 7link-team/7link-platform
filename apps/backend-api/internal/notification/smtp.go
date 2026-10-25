package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

func messageBytes(config EmailConfig, p emailPayload, id string) ([]byte, error) {
	var b bytes.Buffer
	from := mail.Address{Name: p.FromName, Address: config.FromAddress}
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(p.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", p.Subject))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", id)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	for _, key := range []string{"Reply-To", "In-Reply-To", "References", "Auto-Submitted", "List-Unsubscribe"} {
		value := p.Headers[key]
		if value == "" {
			continue
		}
		line := key + ": "
		for _, word := range strings.Fields(value) {
			if len(line)+len(word) > 990 {
				b.WriteString(strings.TrimRight(line, " ") + "\r\n")
				line = " "
			}
			line += word + " "
		}
		b.WriteString(strings.TrimRight(line, " ") + "\r\n")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, value string }{{"text/plain", p.Text}, {"text/html", p.HTML}} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.kind+"; charset=utf-8")
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		dst, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		encoded := quotedPrintable(part.value)
		if _, err = dst.Write(encoded); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
	b.Write(body.Bytes())
	return b.Bytes(), nil
}
func (m *Module) sendSMTP(ctx context.Context, p emailPayload, id string) (resultErr error) {
	defer func() {
		if resultErr != nil {
			m.smtpFailures.Add(1)
		} else {
			m.smtpFailures.Store(0)
		}
	}()
	c := m.config.Email
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	host := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	dialer := &net.Dialer{Timeout: c.Timeout}
	tlsConfig := &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if c.TLSMode == "implicit" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", host)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	client, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if c.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("notification SMTP requires STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.SMTPHost)); err != nil {
			return err
		}
	}
	if err = client.Mail(c.FromAddress); err != nil {
		return err
	}
	for _, address := range p.To {
		if err = client.Rcpt(address); err != nil {
			return err
		}
	}
	data, err := client.Data()
	if err != nil {
		return err
	}
	message, err := messageBytes(c, p, id)
	if err != nil {
		return err
	}
	if _, err = data.Write(message); err != nil {
		_ = data.Close()
		return err
	}
	if err = data.Close(); err != nil {
		return err
	}
	// DATA's final 250 is acceptance by the MTA. A QUIT failure after that must
	// not turn an accepted message into a retry and duplicate external mail.
	_ = client.Quit()
	return nil
}

func quotedPrintable(s string) []byte {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}
