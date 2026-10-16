// Package mail sends notification email over SMTP and reads replies over
// IMAP. It uses only the standard library and always requires TLS.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

// Account is a mailbox the agent sends from and reads replies in.
type Account struct {
	Address  string // login and From address, e.g. ffagent.bot@gmail.com
	Password secrets.Secret
	SMTPHost string
	SMTPPort int // 587 (STARTTLS) or 465 (implicit TLS)
	IMAPHost string
	IMAPPort int // 993

	// insecure disables TLS; only tests against a local fake server set it.
	insecure bool
	// rootCAs overrides system roots; only tests set it.
	tlsConfig *tls.Config
}

// Message is an outgoing email.
type Message struct {
	To        string
	Subject   string
	Body      string // plain text
	MessageID string // generated if empty
	InReplyTo string // threads the message under an earlier one
}

func (a *Account) tlsFor(host string) *tls.Config {
	if a.tlsConfig != nil {
		c := a.tlsConfig.Clone()
		c.ServerName = host
		return c
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

// NewMessageID returns a unique Message-ID for the account's domain.
func (a *Account) NewMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "ffagent.local"
	if i := strings.LastIndex(a.Address, "@"); i >= 0 {
		domain = a.Address[i+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b), domain)
}

// Send delivers m and returns its Message-ID.
func (a *Account) Send(ctx context.Context, m Message) (string, error) {
	if a.Password.Empty() {
		return "", errors.New("mail: no email app password in the vault (run `ffagent secrets set email`)")
	}
	if m.MessageID == "" {
		m.MessageID = a.NewMessageID()
	}
	for _, v := range []string{a.Address, m.To, m.Subject, m.MessageID, m.InReplyTo} {
		if strings.ContainsAny(v, "\r\n") {
			return "", errors.New("mail: header value contains a line break")
		}
	}
	raw, err := a.build(m)
	if err != nil {
		return "", err
	}

	d := net.Dialer{Timeout: 30 * time.Second}
	addr := net.JoinHostPort(a.SMTPHost, fmt.Sprint(a.SMTPPort))
	var conn net.Conn
	if a.SMTPPort == 465 && !a.insecure {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: a.tlsFor(a.SMTPHost)}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return "", fmt.Errorf("mail: connecting to %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	}
	c, err := smtp.NewClient(conn, a.SMTPHost)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("mail: %w", err)
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	if a.SMTPPort != 465 && !a.insecure {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return "", errors.New("mail: server doesn't offer STARTTLS; refusing to send credentials in the clear")
		}
		if err := c.StartTLS(a.tlsFor(a.SMTPHost)); err != nil {
			return "", fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", a.Address, a.Password.Reveal(), a.SMTPHost)); err != nil {
		return "", fmt.Errorf("mail: login failed (check the app password): %w", redactErr(err, a.Password))
	}
	if err := c.Mail(a.Address); err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("mail: %w", err)
	}
	_ = c.Quit()
	return m.MessageID, nil
}

func (a *Account) build(m Message) ([]byte, error) {
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", "ffagent <"+a.Address+">")
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", m.MessageID)
	if m.InReplyTo != "" {
		h("In-Reply-To", m.InReplyTo)
		h("References", m.InReplyTo)
	}
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// redactErr strips the password from server error text, just in case.
func redactErr(err error, s secrets.Secret) error {
	if v := s.Reveal(); v != "" && strings.Contains(err.Error(), v) {
		return errors.New(strings.ReplaceAll(err.Error(), v, "[REDACTED]"))
	}
	return err
}
