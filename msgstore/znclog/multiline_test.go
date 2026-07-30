package znclog

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
)

func TestRawLinePreservesMultilineBatch(t *testing.T) {
	ts := time.Date(2026, 7, 22, 12, 34, 56, 0, time.Local)
	messages := []*irc.Message{
		{
			Prefix:  &irc.Prefix{Name: "mango", User: "user", Host: "example.org"},
			Command: "BATCH",
			Params:  []string{"+u-1", "draft/multiline", "#chan"},
		},
		{
			Tags:    irc.Tags{"batch": "u-1"},
			Prefix:  &irc.Prefix{Name: "mango"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "Line1"},
		},
		{
			Tags:    irc.Tags{"batch": "u-1"},
			Prefix:  &irc.Prefix{Name: "mango"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "Line2"},
		},
		{
			Prefix:  &irc.Prefix{Name: "mango", User: "user", Host: "example.org"},
			Command: "BATCH",
			Params:  []string{"-u-1"},
		},
	}

	for i, msg := range messages {
		line := MarshalLine(msg, ts)
		if line == "" {
			t.Fatalf("MarshalLine(messages[%d]) returned an empty line", i)
		}

		got, _, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
		if err != nil {
			t.Fatalf("UnmarshalLine(messages[%d]) failed: %v", i, err)
		}
		delete(got.Tags, "time")
		if got.String() != msg.String() {
			t.Fatalf("unexpected message %d: got %v, want %v", i, got, msg)
		}
	}

	for i := 1; i <= 2; i++ {
		line := MarshalLine(messages[i], ts)
		got, _, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
		if err != nil {
			t.Fatalf("UnmarshalLine(messages[%d]) failed: %v", i, err)
		}
		if got.Tags["batch"] != "u-1" {
			t.Fatalf("messages[%d] lost batch tag: %v", i, got)
		}
	}
}

func TestRawLinePreservesMillisecondTimestamp(t *testing.T) {
	ts := time.Date(2026, 7, 24, 10, 11, 12, 345*int(time.Millisecond), time.Local)
	msg := &irc.Message{
		Tags:    irc.Tags{"time": "2026-07-24T08:11:12.345Z", "msgid": "event-1"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "hello"},
	}

	line := MarshalLine(msg, ts)
	if !strings.HasPrefix(line, "[10:11:12.345] ") {
		t.Fatalf("MarshalLine() lost millisecond precision: %q", line)
	}
	got, parsedTime, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ts, false)
	if err != nil {
		t.Fatalf("UnmarshalLine() failed: %v", err)
	}
	if got.Tags["time"] != msg.Tags["time"] || got.Tags["msgid"] != msg.Tags["msgid"] {
		t.Fatalf("raw tags changed: got %v, want %v", got.Tags, msg.Tags)
	}
	if parsedTime.Nanosecond()/int(time.Millisecond) != 345 {
		t.Fatalf("parsed timestamp lost milliseconds: %v", parsedTime)
	}
}

func TestUnmarshalLegacySecondTimestamp(t *testing.T) {
	ref := time.Date(2026, 7, 24, 0, 0, 0, 0, time.Local)
	line := "[10:11:12] <alice> legacy"
	msg, parsedTime, err := UnmarshalLine(line, &database.User{}, &database.Network{}, "#chan", ref, false)
	if err != nil {
		t.Fatalf("UnmarshalLine() failed for legacy timestamp: %v", err)
	}
	if parsedTime.Nanosecond() != 0 || parsedTime.Hour() != 10 || parsedTime.Minute() != 11 || parsedTime.Second() != 12 {
		t.Fatalf("legacy timestamp parsed incorrectly: %v", parsedTime)
	}
	if msg.Command != "PRIVMSG" || msg.Params[1] != "legacy" {
		t.Fatalf("legacy line parsed incorrectly: %v", msg)
	}
}
