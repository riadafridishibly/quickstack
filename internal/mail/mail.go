// Package mail sends plain-text email from a background job, through SMTP,
// the log, or an in-memory outbox for development.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/jobs"
)

type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Open returns an SMTP sender for smtpURL (smtp://user:password@host:port),
// or one that logs each message when smtpURL is empty.
func Open(smtpURL, from string) (Sender, error) {
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("mail from address %q: %w", from, err)
	}
	if smtpURL == "" {
		return Log{}, nil
	}
	u, err := url.Parse(smtpURL)
	if err != nil || u.Scheme != "smtp" || u.Hostname() == "" {
		return nil, fmt.Errorf("%q is not an smtp://user:password@host:port URL", smtpURL)
	}
	return &SMTP{url: u, from: from}, nil
}

// Log writes each message to the server log, links and all.
type Log struct{}

func (Log) Send(ctx context.Context, m Message) error {
	slog.InfoContext(ctx, "mail not sent: no SMTP server configured", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// SMTP sends through a server that offers STARTTLS, such as on port 587.
type SMTP struct {
	url  *url.URL
	from string
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	port := s.url.Port()
	if port == "" {
		port = "587"
	}
	var auth smtp.Auth
	if s.url.User != nil {
		pw, _ := s.url.User.Password()
		auth = smtp.PlainAuth("", s.url.User.Username(), pw, s.url.Hostname())
	}
	from, err := mail.ParseAddress(s.from)
	if err != nil {
		return err
	}
	msg, err := Format(s.from, m, time.Now())
	if err != nil {
		return err
	}
	return smtp.SendMail(net.JoinHostPort(s.url.Hostname(), port), auth, from.Address, []string{m.To}, msg)
}

// Format renders m as an RFC 5322 message.
func Format(from string, m Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(m.To, "\r\n") {
		return nil, errors.New("mail: newline in recipient")
	}
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, err
	}
	_, domain, _ := strings.Cut(fromAddr.Address, "@")
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", fromAddr.String())
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", rand.Text(), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))
	return b.Bytes(), nil
}

// Outbox keeps the last messages in memory for /dev/mail, and logs them.
type Outbox struct {
	mu   sync.Mutex
	msgs []Sent
}

type Sent struct {
	Message
	At time.Time
}

const outboxSize = 50

func (o *Outbox) Send(ctx context.Context, m Message) error {
	o.mu.Lock()
	o.msgs = append(o.msgs, Sent{Message: m, At: time.Now()})
	if len(o.msgs) > outboxSize {
		o.msgs = o.msgs[len(o.msgs)-outboxSize:]
	}
	o.mu.Unlock()
	return Log{}.Send(ctx, m)
}

// Messages returns the kept messages, newest first.
func (o *Outbox) Messages() []Sent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Sent, len(o.msgs))
	for i, m := range o.msgs {
		out[len(o.msgs)-1-i] = m
	}
	return out
}

type SendArgs struct {
	Message
}

func (SendArgs) Kind() string { return "send_email" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 10}
}

type worker struct {
	river.WorkerDefaults[SendArgs]
	s Sender
}

func (w *worker) Work(ctx context.Context, j *river.Job[SendArgs]) error {
	return w.s.Send(ctx, j.Args.Message)
}

func AddWorker(w *river.Workers, s Sender) {
	river.AddWorker(w, &worker{s: s})
}

// Enqueue sends m once tx commits, so mail about a change goes out only if
// the change does.
func Enqueue(ctx context.Context, jc *jobs.Client, tx pgx.Tx, m Message) error {
	_, err := jc.InsertTx(ctx, tx, SendArgs{Message: m}, nil)
	return err
}
