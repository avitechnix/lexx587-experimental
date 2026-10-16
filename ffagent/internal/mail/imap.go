package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Received is a reply read from the inbox.
type Received struct {
	UID       uint32
	MessageID string
	InReplyTo string
	From      string // bare address, lower-cased
	Subject   string
	Date      time.Time
	Text      string // the text/plain body
}

const maxMessageBytes = 1 << 20

// Replies returns messages received since the given time whose subject
// contains tag. It opens the inbox read-only and never changes flags.
func (a *Account) Replies(ctx context.Context, since time.Time, tag string) ([]Received, error) {
	if a.Password.Empty() {
		return nil, errors.New("mail: no email app password in the vault (run `ffagent secrets set email`)")
	}
	if strings.ContainsAny(tag, "\"\\\r\n") {
		return nil, errors.New("mail: invalid search tag")
	}
	d := net.Dialer{Timeout: 30 * time.Second}
	addr := net.JoinHostPort(a.IMAPHost, fmt.Sprint(a.IMAPPort))
	var conn net.Conn
	var err error
	if a.insecure {
		conn, err = d.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: a.tlsFor(a.IMAPHost)}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("mail: connecting to %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	c := &imapConn{r: bufio.NewReader(conn), w: conn}

	if line, _, err := c.readLine(); err != nil || !strings.HasPrefix(line, "* OK") {
		return nil, fmt.Errorf("mail: unexpected IMAP greeting: %q %v", line, err)
	}
	user, err := quote(a.Address)
	if err != nil {
		return nil, err
	}
	pass, err := quote(a.Password.Reveal())
	if err != nil {
		return nil, errors.New("mail: the app password contains characters IMAP LOGIN can't send")
	}
	if _, err := c.cmd("LOGIN " + user + " " + pass); err != nil {
		return nil, fmt.Errorf("mail: IMAP login failed (check the app password): %w", redactErr(err, a.Password))
	}
	defer c.cmd("LOGOUT")
	if _, err := c.cmd("EXAMINE INBOX"); err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}
	lines, err := c.cmd(fmt.Sprintf(`UID SEARCH SINCE %s SUBJECT "%s"`, since.UTC().Format("2-Jan-2006"), tag))
	if err != nil {
		return nil, fmt.Errorf("mail: search: %w", err)
	}
	var uids []uint32
	for _, l := range lines {
		if strings.HasPrefix(l.text, "* SEARCH") {
			for _, f := range strings.Fields(strings.TrimPrefix(l.text, "* SEARCH")) {
				if n, err := strconv.ParseUint(f, 10, 32); err == nil {
					uids = append(uids, uint32(n))
				}
			}
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) > 50 {
		uids = uids[len(uids)-50:]
	}

	var out []Received
	for _, uid := range uids {
		lines, err := c.cmd(fmt.Sprintf("UID FETCH %d (BODY.PEEK[])", uid))
		if err != nil {
			return out, fmt.Errorf("mail: fetch: %w", err)
		}
		for _, l := range lines {
			if len(l.literal) == 0 {
				continue
			}
			r, err := parseMessage(l.literal)
			if err != nil {
				continue
			}
			r.UID = uid
			if !r.Date.IsZero() && r.Date.Before(since) {
				continue
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// --- minimal IMAP4rev1 client --------------------------------------------

type imapConn struct {
	r   *bufio.Reader
	w   io.Writer
	tag int
}

type respLine struct {
	text    string
	literal []byte
}

var literalRe = regexp.MustCompile(`\{(\d+)\+?\}$`)

// readLine reads one response line, including any literal it announces.
func (c *imapConn) readLine() (string, []byte, error) {
	var text strings.Builder
	var lit []byte
	for {
		s, err := c.r.ReadString('\n')
		if err != nil {
			return text.String(), lit, err
		}
		s = strings.TrimRight(s, "\r\n")
		text.WriteString(s)
		m := literalRe.FindStringSubmatch(s)
		if m == nil {
			return text.String(), lit, nil
		}
		n, _ := strconv.Atoi(m[1])
		if n > maxMessageBytes {
			return "", nil, fmt.Errorf("message too large (%d bytes)", n)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return "", nil, err
		}
		if lit == nil {
			lit = buf
		}
	}
}

func (c *imapConn) cmd(command string) ([]respLine, error) {
	c.tag++
	tag := fmt.Sprintf("a%d", c.tag)
	if _, err := io.WriteString(c.w, tag+" "+command+"\r\n"); err != nil {
		return nil, err
	}
	var out []respLine
	for {
		text, lit, err := c.readLine()
		if err != nil {
			return out, err
		}
		if strings.HasPrefix(text, tag+" ") {
			status := strings.TrimPrefix(text, tag+" ")
			if strings.HasPrefix(status, "OK") {
				return out, nil
			}
			return out, errors.New(status)
		}
		out = append(out, respLine{text: text, literal: lit})
	}
}

func quote(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return "", errors.New("mail: unsupported character")
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
}

// --- message parsing ------------------------------------------------------

var wordDecoder = &mime.WordDecoder{}

func parseMessage(raw []byte) (Received, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Received{}, err
	}
	r := Received{
		MessageID: strings.TrimSpace(msg.Header.Get("Message-ID")),
		InReplyTo: strings.TrimSpace(msg.Header.Get("In-Reply-To")),
	}
	if subj, err := wordDecoder.DecodeHeader(msg.Header.Get("Subject")); err == nil {
		r.Subject = subj
	} else {
		r.Subject = msg.Header.Get("Subject")
	}
	if addr, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
		r.From = strings.ToLower(addr.Address)
	}
	r.Date, _ = msg.Header.Date()
	r.Text, err = textBody(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, 0)
	return r, err
}

// textBody returns the first text/plain part of a (possibly multipart) body.
func textBody(contentType, encoding string, body io.Reader, depth int) (string, error) {
	if depth > 5 {
		return "", errors.New("mail: MIME nesting too deep")
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if contentType == "" || err != nil {
		mt = "text/plain"
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return "", errors.New("mail: no text part")
			}
			if t, err := textBody(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p, depth+1); err == nil {
				return t, nil
			}
		}
	}
	if mt != "text/plain" {
		return "", errors.New("mail: not text/plain")
	}
	var rd io.Reader = body
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		rd = quotedprintable.NewReader(body)
	case "base64":
		rd = base64.NewDecoder(base64.StdEncoding, &stripNewlines{r: body})
	}
	b, err := io.ReadAll(io.LimitReader(rd, maxMessageBytes))
	return string(b), err
}

type stripNewlines struct{ r io.Reader }

func (s *stripNewlines) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for _, c := range p[:n] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}
