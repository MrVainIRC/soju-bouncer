package soju

import (
	"context"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
	"codeberg.org/emersion/soju/xirc"
)

func TestStripMultilineBatchKeepsOuterBatch(t *testing.T) {
	dc := &downstreamConn{
		strippedBatches: map[string]strippedBatch{
			"u-1": {
				tags: irc.Tags{
					"batch": "s-1",
					"msgid": "msg-1",
					"time":  "2026-07-20T12:00:00.000Z",
				},
				outerBatch: "s-1",
			},
		},
	}

	first := &irc.Message{
		Tags:    irc.Tags{"batch": "u-1"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "hello"},
	}
	dc.stripBatch(first)
	if got := first.Tags["batch"]; got != "s-1" {
		t.Fatalf("first fallback line batch = %q, want %q", got, "s-1")
	}
	if got := first.Tags["msgid"]; got != "msg-1" {
		t.Fatalf("first fallback line msgid = %q, want %q", got, "msg-1")
	}

	second := &irc.Message{
		Tags:    irc.Tags{"batch": "u-1"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "world"},
	}
	dc.stripBatch(second)
	if got := second.Tags["batch"]; got != "s-1" {
		t.Fatalf("second fallback line batch = %q, want %q", got, "s-1")
	}
	if got := second.Tags["msgid"]; got != "" {
		t.Fatalf("second fallback line msgid = %q, want empty", got)
	}
}

func TestListMessagesOrdersSameTimestampByID(t *testing.T) {
	ctx := context.Background()
	db := createTempSqliteDB(t)
	user := createTestUser(t, db)

	network := database.NewNetwork("irc+insecure://example.test")
	if err := db.StoreNetwork(ctx, user.ID, network); err != nil {
		t.Fatalf("failed to store network: %v", err)
	}

	msgTime := xirc.FormatServerTime(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	msgs := []*irc.Message{
		{
			Tags:    irc.Tags{"time": msgTime},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "one"},
		},
		{
			Tags:    irc.Tags{"time": msgTime},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "two"},
		},
		{
			Tags:    irc.Tags{"time": msgTime},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "three"},
		},
	}

	ids, err := db.StoreMessages(ctx, network.ID, "#chan", msgs)
	if err != nil {
		t.Fatalf("failed to store messages: %v", err)
	}

	got, err := db.ListMessages(ctx, network.ID, "#chan", &database.MessageOptions{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}
	assertMessageTexts(t, got, []string{"one", "two", "three"})

	got, err = db.ListMessages(ctx, network.ID, "#chan", &database.MessageOptions{
		Limit:    2,
		TakeLast: true,
	})
	if err != nil {
		t.Fatalf("failed to list latest messages: %v", err)
	}
	assertMessageTexts(t, got, []string{"two", "three"})

	lastID, err := db.GetMessageLastID(ctx, network.ID, "#chan")
	if err != nil {
		t.Fatalf("failed to get last message ID: %v", err)
	}
	if lastID != ids[2] {
		t.Fatalf("last message ID = %v, want %v", lastID, ids[2])
	}
}

func TestMessageInHistoryBatchKeepsNestedMultilineBatch(t *testing.T) {
	start := &irc.Message{
		Command: "BATCH",
		Params:  []string{"+u-1", "draft/multiline", "#chan"},
	}
	got := messageInHistoryBatch(start, "s-1")
	if got == start {
		t.Fatalf("messageInHistoryBatch reused message without batch tag")
	}
	if got.Tags["batch"] != "s-1" {
		t.Fatalf("history batch tag = %q, want %q", got.Tags["batch"], "s-1")
	}
	if _, ok := start.Tags["batch"]; ok {
		t.Fatalf("messageInHistoryBatch mutated stored batch start: %v", start.Tags)
	}

	line := &irc.Message{
		Tags:    irc.Tags{"batch": "u-1"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "hello"},
	}
	got = messageInHistoryBatch(line, "s-1")
	if got != line {
		t.Fatalf("messageInHistoryBatch copied message with existing batch tag")
	}
	if got.Tags["batch"] != "u-1" {
		t.Fatalf("inner batch tag = %q, want %q", got.Tags["batch"], "u-1")
	}
}

func TestOutgoingMultilineBatchTarget(t *testing.T) {
	dc := &downstreamConn{
		outgoingBatches: map[string]outgoingBatch{},
	}

	start := &irc.Message{
		Command: "BATCH",
		Params:  []string{"+c-1", "draft/multiline", "#chan"},
	}
	if got := dc.outgoingMultilineBatchTarget(start); got != "#chan" {
		t.Fatalf("start target = %q, want %q", got, "#chan")
	}

	end := &irc.Message{
		Command: "BATCH",
		Params:  []string{"-c-1"},
	}
	if got := dc.outgoingMultilineBatchTarget(end); got != "#chan" {
		t.Fatalf("end target = %q, want %q", got, "#chan")
	}
	if _, ok := dc.outgoingBatches["c-1"]; ok {
		t.Fatalf("outgoing batch was not cleared")
	}
}

func assertMessageTexts(t *testing.T, msgs []*irc.Message, want []string) {
	t.Helper()
	if len(msgs) != len(want) {
		t.Fatalf("got %v messages, want %v", len(msgs), len(want))
	}
	for i, msg := range msgs {
		if len(msg.Params) < 2 {
			t.Fatalf("message %v has no text: %v", i, msg)
		}
		if msg.Params[1] != want[i] {
			t.Fatalf("message %v text = %q, want %q", i, msg.Params[1], want[i])
		}
	}
}
