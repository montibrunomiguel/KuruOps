package mailer_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/mailer"
)

// fakeSMTPServer is a minimal, non-TLS SMTP server that just accepts every
// command and captures the DATA payload -- enough to verify SMTPSender
// speaks the protocol correctly (EHLO/MAIL/RCPT/DATA/QUIT) without a real
// mail relay. No STARTTLS/AUTH support, so tests using it configure
// UseTLS: false and no username.
func fakeSMTPServer(t *testing.T) (addr string, dataCh chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	dataCh = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := conn

		write := func(s string) { w.Write([]byte(s + "\r\n")) }
		write("220 fake.smtp ready")

		var inData bool
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")

			if inData {
				if line == "." {
					inData = false
					dataCh <- data.String()
					write("250 OK")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}

			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				write("250-fake.smtp")
				write("250 OK")
			case strings.HasPrefix(upper, "MAIL FROM"):
				write("250 OK")
			case strings.HasPrefix(upper, "RCPT TO"):
				write("250 OK")
			case upper == "DATA":
				inData = true
				write("354 Start mail input")
			case upper == "QUIT":
				write("221 Bye")
				return
			default:
				write("250 OK")
			}
		}
	}()

	return ln.Addr().String(), dataCh
}

func TestSMTPSender_Send(t *testing.T) {
	addr, dataCh := fakeSMTPServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	var port int
	_, err = fscanPort(portStr, &port)
	require.NoError(t, err)

	sender := mailer.SMTPSender{}
	cfg := mailer.Config{
		Host: host, Port: port, UseTLS: false,
		From: "ArgusOps <no-reply@argusops.local>",
	}
	msg := mailer.Message{
		To:      "analyst@test.local",
		Subject: "Password reset",
		Body:    "Use this link to reset your password: https://example.com/reset?token=abc123",
	}

	err = sender.Send(t.Context(), cfg, msg)
	require.NoError(t, err)

	select {
	case data := <-dataCh:
		assert.Contains(t, data, "To: analyst@test.local")
		assert.Contains(t, data, "Subject: Password reset")
		assert.Contains(t, data, "https://example.com/reset?token=abc123")
	case <-context.Background().Done():
		t.Fatal("timed out waiting for DATA payload")
	}
}

func TestSMTPSender_Send_ConnectionFailure(t *testing.T) {
	sender := mailer.SMTPSender{}
	cfg := mailer.Config{Host: "127.0.0.1", Port: 1, UseTLS: false, From: "a@b.com"}
	err := sender.Send(t.Context(), cfg, mailer.Message{To: "x@y.com", Subject: "s", Body: "b"})
	assert.Error(t, err)
}

// fscanPort avoids importing fmt.Sscanf's less obvious error semantics for
// this one integer parse.
func fscanPort(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}
