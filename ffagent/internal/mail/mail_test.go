package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

func listen(t *testing.T, serve func(c net.Conn)) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSendBuildsThreadedMessage(t *testing.T) {
	got := make(chan string, 1)
	port := listen(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 fake ESMTP\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					fmt.Fprint(c, "250 queued\r\n")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.Fields(line)[0]); cmd {
			case "EHLO":
				fmt.Fprint(c, "250-fake\r\n250 AUTH PLAIN\r\n")
			case "AUTH":
				fmt.Fprint(c, "235 ok\r\n")
			case "DATA":
				inData = true
				fmt.Fprint(c, "354 go\r\n")
			case "QUIT":
				fmt.Fprint(c, "221 bye\r\n")
				return
			default:
				fmt.Fprint(c, "250 ok\r\n")
			}
		}
	})
	a := &Account{Address: "bot@example.com", Password: secrets.New("app-pass"), SMTPHost: "localhost", SMTPPort: port, insecure: true}
	id, err := a.Send(context.Background(), Message{To: "me@example.com", Subject: "[ffagent #ABC] 2 moves — review", Body: "1. ADD Player\nWhy: ünïcode ok", InReplyTo: "<orig@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	raw := <-got
	for _, want := range []string{"To: me@example.com", "In-Reply-To: <orig@example.com>", "Message-ID: " + id, "quoted-printable", "=?utf-8?q?"} {
		if !strings.Contains(raw, want) {
			t.Errorf("message missing %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "app-pass") {
		t.Error("password in message")
	}
	if _, err := a.Send(context.Background(), Message{To: "me@example.com\r\nBcc: evil@example.com", Subject: "x"}); err == nil {
		t.Error("header injection accepted")
	}
}

const replyMIME = "From: Me <ME@example.com>\r\n" +
	"Subject: Re: [ffagent #ABCDEFGHIJKLMNOP] 2 moves to review\r\n" +
	"Message-ID: <reply1@example.com>\r\n" +
	"In-Reply-To: <orig@example.com>\r\n" +
	"Date: Fri, 16 Oct 2026 12:30:00 +0000\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=\"b1\"\r\n\r\n" +
	"--b1\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"approve 1, decline 2=0D\r\n\r\nOn Fri ffagent wrote:\r\n> 1. ADD\r\n" +
	"--b1\r\nContent-Type: text/html\r\n\r\n<p>approve 1</p>\r\n--b1--\r\n"

func TestRepliesParsesIMAP(t *testing.T) {
	var cmds []string
	port := listen(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		fmt.Fprint(c, "* OK fake IMAP ready\r\n")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			f := strings.SplitN(strings.TrimSpace(line), " ", 2)
			tag, cmd := f[0], f[1]
			cmds = append(cmds, cmd)
			switch {
			case strings.HasPrefix(cmd, "UID SEARCH"):
				fmt.Fprint(c, "* SEARCH 7\r\n")
			case strings.HasPrefix(cmd, "UID FETCH"):
				fmt.Fprintf(c, "* 1 FETCH (UID 7 BODY[] {%d}\r\n%s)\r\n", len(replyMIME), replyMIME)
			}
			fmt.Fprintf(c, "%s OK done\r\n", tag)
			if strings.HasPrefix(cmd, "LOGOUT") {
				return
			}
		}
	})
	a := &Account{Address: "bot@example.com", Password: secrets.New(`p"ss`), IMAPHost: "127.0.0.1", IMAPPort: port, insecure: true}
	since := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	rs, err := a.Replies(context.Background(), since, "[ffagent #")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d replies", len(rs))
	}
	r := rs[0]
	if r.From != "me@example.com" || r.MessageID != "<reply1@example.com>" || r.InReplyTo != "<orig@example.com>" ||
		!strings.Contains(r.Subject, "ABCDEFGHIJKLMNOP") || !strings.HasPrefix(r.Text, "approve 1, decline 2") {
		t.Errorf("parsed %+v", r)
	}
	if !strings.Contains(strings.Join(cmds, "|"), `LOGIN "bot@example.com" "p\"ss"`) || !strings.Contains(strings.Join(cmds, "|"), "EXAMINE INBOX") {
		t.Errorf("commands: %v", cmds)
	}
	if !strings.Contains(strings.Join(cmds, "|"), "SINCE 15-Oct-2026") {
		t.Errorf("search: %v", cmds)
	}
}
