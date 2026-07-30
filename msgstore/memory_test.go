package msgstore

import (
	"context"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
)

func TestMemoryStoreReplaysTagmsg(t *testing.T) {
	store := NewMemoryStore()
	network := &database.Network{ID: 1}
	entity := "#chan"

	startID, err := store.LastMsgID(context.Background(), network, entity, time.Time{})
	if err != nil {
		t.Fatalf("LastMsgID() failed: %v", err)
	}

	tagmsg := &irc.Message{
		Tags: irc.Tags{
			"+draft/react": "like",
			"+reply":       "123",
		},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "TAGMSG",
		Params:  []string{entity},
	}
	if _, err := store.Append(context.Background(), network, entity, tagmsg); err != nil {
		t.Fatalf("Append() failed: %v", err)
	}

	history, err := store.LoadLatestID(context.Background(), startID, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   10,
		Events:  true,
	})
	if err != nil {
		t.Fatalf("LoadLatestID() failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	if history[0].Command != "TAGMSG" || history[0].Tags["+draft/react"] != "like" {
		t.Fatalf("unexpected replayed message: %v", history[0])
	}
}
