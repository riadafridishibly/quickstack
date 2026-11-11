package mail

import (
	"strings"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	t.Parallel()
	b, err := Format("Quickstack <noreply@example.com>", Message{
		To: "ana@example.com", Subject: "Réinitialiser", Text: "line one\nline two",
	}, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"From: \"Quickstack\" <noreply@example.com>\r\n",
		"To: ana@example.com\r\n",
		"Subject: =?utf-8?q?R=C3=A9initialiser?=\r\n",
		"@example.com>\r\n",
		"\r\n\r\nline one\r\nline two",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if _, err := Format("noreply@example.com", Message{To: "a@b.c\r\nBcc: x@y.z"}, time.Now()); err == nil {
		t.Error("Format accepted a newline in To")
	}
}

func TestOutboxKeepsNewestFirst(t *testing.T) {
	t.Parallel()
	var o Outbox
	for i := range outboxSize + 2 {
		_ = o.Send(t.Context(), Message{Subject: string(rune('a' + i%26))})
	}
	got := o.Messages()
	if len(got) != outboxSize {
		t.Fatalf("kept %d messages, want %d", len(got), outboxSize)
	}
	if got[0].Subject != string(rune('a'+(outboxSize+1)%26)) {
		t.Errorf("newest = %q", got[0].Subject)
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	if s, err := Open("", "noreply@example.com"); err != nil || s != (Log{}) {
		t.Errorf("Open without URL = %v, %v", s, err)
	}
	if _, err := Open("http://example.com", "noreply@example.com"); err == nil {
		t.Error("Open accepted an http URL")
	}
	if _, err := Open("", "not an address"); err == nil {
		t.Error("Open accepted a bad from address")
	}
}
