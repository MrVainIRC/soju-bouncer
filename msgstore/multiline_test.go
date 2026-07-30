package msgstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
)

const multilineTestTime = "2026-07-22T12:34:56.000Z"

func multilineTestMessages() []*irc.Message {
	return []*irc.Message{
		{
			Tags:    irc.Tags{"time": multilineTestTime},
			Prefix:  &irc.Prefix{Name: "mango", User: "user", Host: "example.org"},
			Command: "BATCH",
			Params:  []string{"+u-1", "draft/multiline", "#chan"},
		},
		{
			Tags:    irc.Tags{"time": multilineTestTime, "batch": "u-1"},
			Prefix:  &irc.Prefix{Name: "mango"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "Line1"},
		},
		{
			Tags:    irc.Tags{"time": multilineTestTime, "batch": "u-1"},
			Prefix:  &irc.Prefix{Name: "mango"},
			Command: "PRIVMSG",
			Params:  []string{"#chan", "Line2"},
		},
		{
			Tags:    irc.Tags{"time": multilineTestTime},
			Prefix:  &irc.Prefix{Name: "mango", User: "user", Host: "example.org"},
			Command: "BATCH",
			Params:  []string{"-u-1"},
		},
	}
}

func testMultilineStoreRoundTrip(t *testing.T, store ChatHistoryStore, network *database.Network) {
	t.Helper()

	ctx := context.Background()
	want := multilineTestMessages()
	for i, msg := range want {
		if _, err := store.Append(ctx, network, "#chan", msg); err != nil {
			t.Fatalf("Append(messages[%d]) failed: %v", i, err)
		}
	}

	ts, err := time.Parse(time.RFC3339Nano, multilineTestTime)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadAfterTime(ctx, ts.Add(-time.Second), ts.Add(time.Second), &LoadMessageOptions{
		Network: network,
		Entity:  "#chan",
		Limit:   len(want),
	})
	if err != nil {
		t.Fatalf("LoadAfterTime() failed: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("LoadAfterTime() returned %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("message %d changed: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestMultilineRoundTripFS(t *testing.T) {
	user := database.NewUser("testuser")
	network := database.NewNetwork("irc.example.org")
	network.ID = 1
	network.Name = "testnet"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testMultilineStoreRoundTrip(t, store, network)
}

func TestMultilineRoundTripDB(t *testing.T) {
	if !database.SqliteEnabled {
		t.Skip("SQLite support is disabled")
	}

	ctx := context.Background()
	db, err := database.OpenTempSqliteDB()
	if err != nil {
		t.Fatalf("OpenTempSqliteDB() failed: %v", err)
	}
	defer db.Close()

	user := database.NewUser("testuser")
	if err := db.StoreUser(ctx, user); err != nil {
		t.Fatalf("StoreUser() failed: %v", err)
	}
	network := database.NewNetwork("irc.example.org")
	network.Name = "testnet"
	if err := db.StoreNetwork(ctx, user.ID, network); err != nil {
		t.Fatalf("StoreNetwork() failed: %v", err)
	}

	store := NewDBStore(db)
	testMultilineStoreRoundTrip(t, store, network)
}
