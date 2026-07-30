package znclog

import (
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
)

func TestRawLinePreservesReplyTag(t *testing.T) {
	ts := time.Date(2026, 7, 22, 12, 34, 56, 0, time.Local)
	msg := &irc.Message{
		Tags: irc.Tags{
			"time":   "2026-07-22T12:34:56.000Z",
			"+reply": "abc",
		},
		Prefix:  &irc.Prefix{Name: "bob"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "same"},
	}

	line := MarshalLine(msg, ts)
	if line == "" {
		t.Fatalf("MarshalLine() returned an empty line")
	}

	got, _, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
	if err != nil {
		t.Fatalf("UnmarshalLine() failed: %v", err)
	}
	if got.Command != "PRIVMSG" || got.Tags["+reply"] != "abc" || got.Params[1] != "same" {
		t.Fatalf("unexpected message: %v", got)
	}
}

func TestRawLinePreservesReactionTagmsg(t *testing.T) {
	ts := time.Date(2026, 7, 22, 12, 34, 56, 0, time.Local)
	msg := &irc.Message{
		Tags: irc.Tags{
			"time":         "2026-07-22T12:34:56.000Z",
			"+reply":       "abc",
			"+draft/react": "like",
		},
		Prefix:  &irc.Prefix{Name: "bob"},
		Command: "TAGMSG",
		Params:  []string{"#chan"},
	}

	line := MarshalLine(msg, ts)
	if line == "" {
		t.Fatalf("MarshalLine() returned an empty line")
	}

	got, _, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
	if err != nil {
		t.Fatalf("UnmarshalLine() failed: %v", err)
	}
	if got.Command != "TAGMSG" || got.Tags["+reply"] != "abc" || got.Tags["+draft/react"] != "like" {
		t.Fatalf("unexpected message: %v", got)
	}
}

func TestRawLinePreservesUnreactionTagmsg(t *testing.T) {
	ts := time.Date(2026, 7, 22, 12, 34, 56, 0, time.Local)
	msg := &irc.Message{
		Tags: irc.Tags{
			"time":           "2026-07-22T12:34:56.000Z",
			"+reply":         "abc",
			"+draft/unreact": "like",
		},
		Prefix:  &irc.Prefix{Name: "bob"},
		Command: "TAGMSG",
		Params:  []string{"#chan"},
	}

	line := MarshalLine(msg, ts)
	if line == "" {
		t.Fatalf("MarshalLine() returned an empty line")
	}

	got, _, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
	if err != nil {
		t.Fatalf("UnmarshalLine() failed: %v", err)
	}
	if got.Command != "TAGMSG" || got.Tags["+reply"] != "abc" || got.Tags["+draft/unreact"] != "like" {
		t.Fatalf("unexpected message: %v", got)
	}
}
