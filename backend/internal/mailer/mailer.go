// Package mailer sends short, plain-text transactional email (password
// reset links, and any future notification) over SMTP. Deliberately built
// on the stdlib net/smtp + crypto/tls rather than a third-party library --
// this codebase consistently avoids a dependency where the stdlib already
// covers the need (no HTTP framework beyond chi, no ORM), and a turnkey
// open-source deploy sending occasional one-line emails doesn't need
// anything HTML templates or attachments would justify pulling in.
package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// Config is the fully-resolved connection info a Sender needs -- Password
// is plaintext here, never a secrets.Store reference. Resolving the
// reference to a real value happens one layer up, in
// service.SMTPConfigService, the same way blobstore.Store implementations
// never know about secrets.Store either.
type Config struct {
	Host     string
	Port     int
	UseTLS   bool
	Username string
	Password string
	From     string // "Name <address>" or bare "address"
}

// Message is a single outbound email. Body is plain text -- see the
// package doc comment for why HTML is out of scope.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender abstracts actually delivering a Message so callers (and tests)
// don't need a real SMTP server -- see SMTPSender for the production
// implementation.
type Sender interface {
	Send(ctx context.Context, cfg Config, msg Message) error
}

// SMTPSender delivers over SMTP with optional STARTTLS, the standard
// port-587 flow used by Gmail/SES/most relays.
type SMTPSender struct{}

func (SMTPSender) Send(ctx context.Context, cfg Config, msg Message) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()

	if cfg.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(fromAddress(cfg.From)); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := wc.Write(buildMessage(cfg.From, msg)); err != nil {
		wc.Close()
		return fmt.Errorf("write message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("close data writer: %w", err)
	}

	return client.Quit()
}

// buildMessage assembles a minimal RFC 5322 message -- headers plus a
// blank line plus the plain-text body.
func buildMessage(from string, msg Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}

// fromAddress extracts the bare address from a "Name <address>" envelope
// sender -- SMTP's MAIL FROM command takes only the address, not the
// display name.
func fromAddress(from string) string {
	if start := strings.Index(from, "<"); start != -1 {
		if end := strings.Index(from, ">"); end > start {
			return from[start+1 : end]
		}
	}
	return from
}
