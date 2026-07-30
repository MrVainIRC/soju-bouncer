package msgstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
)

func testReactionEventsLoadLatestID(t *testing.T, store Store, network *database.Network) {
	t.Helper()

	ctx := context.Background()
	entity := "#chan"
	original := &irc.Message{
		Tags:    irc.Tags{"time": "2026-07-22T12:34:50.000Z", "msgid": "msgid"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{entity, "hello"},
	}
	startID, err := store.Append(ctx, network, entity, original)
	if err != nil {
		t.Fatalf("Append(original) failed: %v", err)
	}

	events := []*irc.Message{
		{
			Tags: irc.Tags{
				"time":         "2026-07-22T12:34:51.000Z",
				"+reply":       "msgid",
				"+draft/react": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
		{
			Tags: irc.Tags{
				"time":           "2026-07-22T12:34:52.000Z",
				"+reply":         "msgid",
				"+draft/unreact": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
	}
	for i, msg := range events {
		if _, err := store.Append(ctx, network, entity, msg); err != nil {
			t.Fatalf("Append(events[%d]) failed: %v", i, err)
		}
	}

	got, err := store.LoadLatestID(ctx, startID, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   len(events),
	})
	if err != nil {
		t.Fatalf("LoadLatestID() failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadLatestID() returned %d messages without events, want 0: %v", len(got), got)
	}

	got, err = store.LoadLatestID(ctx, startID, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   len(events),
		Events:  true,
	})
	if err != nil {
		t.Fatalf("LoadLatestID(events) failed: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("LoadLatestID(events) returned %d messages, want %d: %v", len(got), len(events), got)
	}
	if got[0].Command != "TAGMSG" || got[0].Tags["+draft/react"] != "like" || got[0].Tags["+reply"] != "msgid" {
		t.Fatalf("unexpected react event: %v", got[0])
	}
	if got[1].Command != "TAGMSG" || got[1].Tags["+draft/unreact"] != "like" || got[1].Tags["+reply"] != "msgid" {
		t.Fatalf("unexpected unreact event: %v", got[1])
	}
}

func testReactionEventsChatHistory(t *testing.T, store ChatHistoryStore, network *database.Network) {
	t.Helper()

	ctx := context.Background()
	entity := "#chan"
	events := []*irc.Message{
		{
			Tags:    irc.Tags{"time": "2026-07-22T12:34:50.000Z", "msgid": "msgid"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{entity, "hello"},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-22T12:34:50.500Z"},
			Prefix:  &irc.Prefix{Name: "carol"},
			Command: "JOIN",
			Params:  []string{entity},
		},
		{
			Tags: irc.Tags{
				"time":         "2026-07-22T12:34:51.000Z",
				"+reply":       "msgid",
				"+draft/react": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
		{
			Tags: irc.Tags{
				"time":           "2026-07-22T12:34:52.000Z",
				"+reply":         "msgid",
				"+draft/unreact": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
	}
	for i, msg := range events {
		if _, err := store.Append(ctx, network, entity, msg); err != nil {
			t.Fatalf("Append(events[%d]) failed: %v", i, err)
		}
	}

	start := time.Date(2026, 7, 22, 12, 34, 49, 0, time.UTC)
	end := time.Date(2026, 7, 22, 12, 34, 53, 0, time.UTC)
	got, err := store.LoadAfterTime(ctx, start, end, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   len(events),
	})
	if err != nil {
		t.Fatalf("LoadAfterTime() failed: %v", err)
	}
	if len(got) != 1 || got[0].Command != "PRIVMSG" {
		t.Fatalf("LoadAfterTime() returned events without event playback: %v", got)
	}

	got, err = store.LoadAfterTime(ctx, start, end, &LoadMessageOptions{
		Network:   network,
		Entity:    entity,
		Limit:     len(events),
		Reactions: true,
	})
	if err != nil {
		t.Fatalf("LoadAfterTime(reactions) failed: %v", err)
	}
	if len(got) != len(events)-1 || got[0].Command != "PRIVMSG" || got[1].Tags["+draft/react"] != "like" || got[2].Tags["+draft/unreact"] != "like" {
		t.Fatalf("LoadAfterTime(reactions) returned non-reaction events or lost reactions: %v", got)
	}

	got, err = store.LoadAfterTime(ctx, start, end, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   len(events),
		Events:  true,
	})
	if err != nil {
		t.Fatalf("LoadAfterTime(events) failed: %v", err)
	}
	if len(got) != len(events) || got[1].Command != "JOIN" || got[2].Tags["+draft/react"] != "like" || got[3].Tags["+draft/unreact"] != "like" {
		t.Fatalf("LoadAfterTime(events) returned wrong events: %v", got)
	}
}

func testReactionEventClientWindows(t *testing.T, store Store, network *database.Network) {
	t.Helper()

	ctx := context.Background()
	entity := "#chan"
	original := &irc.Message{
		Tags:    irc.Tags{"time": "2026-07-22T12:34:50.000Z", "msgid": "msgid"},
		Prefix:  &irc.Prefix{Name: "alice"},
		Command: "PRIVMSG",
		Params:  []string{entity, "hello"},
	}
	mobileID, err := store.Append(ctx, network, entity, original)
	if err != nil {
		t.Fatalf("Append(original) failed: %v", err)
	}

	react := &irc.Message{
		Tags: irc.Tags{
			"time":         "2026-07-22T12:34:51.000Z",
			"+reply":       "msgid",
			"+draft/react": "like",
		},
		Prefix:  &irc.Prefix{Name: "bob"},
		Command: "TAGMSG",
		Params:  []string{entity},
	}
	desktopID, err := store.Append(ctx, network, entity, react)
	if err != nil {
		t.Fatalf("Append(react) failed: %v", err)
	}

	unreact := &irc.Message{
		Tags: irc.Tags{
			"time":           "2026-07-22T12:34:52.000Z",
			"+reply":         "msgid",
			"+draft/unreact": "like",
		},
		Prefix:  &irc.Prefix{Name: "bob"},
		Command: "TAGMSG",
		Params:  []string{entity},
	}
	if _, err := store.Append(ctx, network, entity, unreact); err != nil {
		t.Fatalf("Append(unreact) failed: %v", err)
	}

	mobileHistory, err := store.LoadLatestID(ctx, mobileID, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   10,
		Events:  true,
	})
	if err != nil {
		t.Fatalf("LoadLatestID(mobile) failed: %v", err)
	}
	if len(mobileHistory) != 2 || mobileHistory[0].Tags["+draft/react"] != "like" || mobileHistory[1].Tags["+draft/unreact"] != "like" {
		t.Fatalf("mobile got wrong raw events: %v", mobileHistory)
	}

	desktopHistory, err := store.LoadLatestID(ctx, desktopID, &LoadMessageOptions{
		Network: network,
		Entity:  entity,
		Limit:   10,
		Events:  true,
	})
	if err != nil {
		t.Fatalf("LoadLatestID(desktop) failed: %v", err)
	}
	if len(desktopHistory) != 1 || desktopHistory[0].Tags["+draft/unreact"] != "like" {
		t.Fatalf("desktop got wrong raw events: %v", desktopHistory)
	}
}

func TestReactionEventsLoadLatestIDFS(t *testing.T) {
	user := database.NewUser("testuser")
	network := database.NewNetwork("irc.example.org")
	network.ID = 1
	network.Name = "testnet"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testReactionEventsLoadLatestID(t, store, network)
}

func TestReactionEventsChatHistoryFS(t *testing.T) {
	user := database.NewUser("testuser")
	network := database.NewNetwork("irc.example.org")
	network.ID = 1
	network.Name = "testnet"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testReactionEventsChatHistory(t, store, network)
}

func TestReactionEventClientWindowsFS(t *testing.T) {
	user := database.NewUser("testuser")
	network := database.NewNetwork("irc.example.org")
	network.ID = 1
	network.Name = "testnet"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testReactionEventClientWindows(t, store, network)
}

func TestReactionEventsLoadLatestIDDB(t *testing.T) {
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
	testReactionEventsLoadLatestID(t, store, network)
}

func TestReactionEventsChatHistoryDB(t *testing.T) {
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
	testReactionEventsChatHistory(t, store, network)
}

func TestReactionEventClientWindowsDB(t *testing.T) {
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
	testReactionEventClientWindows(t, store, network)
}

func testReactionHistoryCursors(t *testing.T, store ChatHistoryStore, network *database.Network) {
	t.Helper()

	ctx := context.Background()
	entity := "#cursor"
	base := time.Date(2026, 7, 24, 10, 11, 12, 0, time.UTC)
	messages := []*irc.Message{
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.100Z", "msgid": "event-1"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{entity, "hello"},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.200Z", "msgid": "event-2", "+reply": "event-1", "+draft/react": "like"},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.300Z", "msgid": "event-3", "+reply": "event-1", "+draft/unreact": "like"},
			Prefix:  &irc.Prefix{Name: "bob"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.400Z", "msgid": "event-4", "+reply": "event-1", "+react": "heart"},
			Prefix:  &irc.Prefix{Name: "carol"},
			Command: "TAGMSG",
			Params:  []string{entity},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.500Z", "msgid": "event-5"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "NOTICE",
			Params:  []string{entity, "done"},
		},
	}

	internalIDs := make([]string, len(messages))
	for i, msg := range messages {
		var err error
		internalIDs[i], err = store.Append(ctx, network, entity, msg)
		if err != nil {
			t.Fatalf("Append(messages[%d]) failed: %v", i, err)
		}
	}

	options := LoadMessageOptions{
		Network:   network,
		Entity:    entity,
		Limit:     10,
		Reactions: true,
	}
	cursors := make([]string, len(messages))
	for i, msg := range messages {
		var err error
		cursors[i], _, err = store.ResolveMsgID(ctx, network, entity, msg.Tags["msgid"])
		if err != nil {
			t.Fatalf("ResolveMsgID(%q) failed: %v", msg.Tags["msgid"], err)
		}
	}

	assertIDs := func(name string, got []*irc.Message, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s returned %d messages, want %d: %v", name, len(got), len(want), got)
		}
		for i, msg := range got {
			if msg.Tags["msgid"] != want[i] {
				t.Fatalf("%s message %d has msgid %q, want %q: %v", name, i, msg.Tags["msgid"], want[i], msg)
			}
			messageIndex := int(want[i][len(want[i])-1] - '1')
			if messageIndex < len(messages) && msg.Tags["time"] != messages[messageIndex].Tags["time"] {
				t.Fatalf("%s message %d changed time tag: %v", name, i, msg)
			}
		}
	}

	before, err := store.LoadBeforeID(ctx, cursors[2], &options)
	if err != nil {
		t.Fatalf("LoadBeforeID() failed: %v", err)
	}
	assertIDs("before msgid", before, "event-1", "event-2")

	after, err := store.LoadAfterID(ctx, cursors[1], &options)
	if err != nil {
		t.Fatalf("LoadAfterID() failed: %v", err)
	}
	assertIDs("after msgid", after, "event-3", "event-4", "event-5")

	between, err := store.LoadBetweenID(ctx, cursors[0], cursors[4], &options)
	if err != nil {
		t.Fatalf("LoadBetweenID() failed: %v", err)
	}
	assertIDs("between msgid", between, "event-2", "event-3", "event-4")

	reverse, err := store.LoadBetweenID(ctx, cursors[4], cursors[0], &options)
	if err != nil {
		t.Fatalf("LoadBetweenID(reverse) failed: %v", err)
	}
	assertIDs("between reverse msgid", reverse, "event-2", "event-3", "event-4")

	betweenCases := []struct {
		name   string
		first  HistoryBound
		second HistoryBound
	}{
		{
			name:   "msgid-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			second: HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
		},
		{
			name:   "timestamp-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(450 * time.Millisecond)},
		},
		{
			name:   "msgid-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			second: HistoryBound{Timestamp: base.Add(450 * time.Millisecond)},
		},
		{
			name:   "timestamp-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
		},
	}
	for _, tc := range betweenCases {
		got, err := store.LoadBetween(ctx, tc.first, tc.second, &options)
		if err != nil {
			t.Fatalf("LoadBetween(%s) failed: %v", tc.name, err)
		}
		assertIDs("between "+tc.name, got, "event-2", "event-3", "event-4")
	}

	limitedOptions := options
	limitedOptions.Limit = 2
	directionalCases := []struct {
		name   string
		first  HistoryBound
		second HistoryBound
		want   []string
	}{
		{
			name:   "forward-msgid-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			second: HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
			want:   []string{"event-2", "event-3"},
		},
		{
			name:   "forward-timestamp-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(550 * time.Millisecond)},
			want:   []string{"event-2", "event-3"},
		},
		{
			name:   "forward-msgid-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			second: HistoryBound{Timestamp: base.Add(550 * time.Millisecond)},
			want:   []string{"event-2", "event-3"},
		},
		{
			name:   "forward-timestamp-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
			want:   []string{"event-2", "event-3"},
		},
		{
			name:   "reverse-msgid-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
			second: HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			want:   []string{"event-3", "event-4"},
		},
		{
			name:   "reverse-timestamp-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(450 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			want:   []string{"event-3", "event-4"},
		},
		{
			name:   "reverse-msgid-to-timestamp",
			first:  HistoryBound{Timestamp: base.Add(500 * time.Millisecond), ID: cursors[4]},
			second: HistoryBound{Timestamp: base.Add(150 * time.Millisecond)},
			want:   []string{"event-3", "event-4"},
		},
		{
			name:   "reverse-timestamp-to-msgid",
			first:  HistoryBound{Timestamp: base.Add(450 * time.Millisecond)},
			second: HistoryBound{Timestamp: base.Add(100 * time.Millisecond), ID: cursors[0]},
			want:   []string{"event-3", "event-4"},
		},
	}
	for _, tc := range directionalCases {
		got, err := store.LoadBetween(ctx, tc.first, tc.second, &limitedOptions)
		if err != nil {
			t.Fatalf("LoadBetween(%s) failed: %v", tc.name, err)
		}
		assertIDs("limited between "+tc.name, got, tc.want...)
	}

	latestOptions := options
	latestOptions.Limit = 2
	latest, err := store.LoadLatestID(ctx, cursors[0], &latestOptions)
	if err != nil {
		t.Fatalf("LoadLatestID() failed: %v", err)
	}
	assertIDs("latest msgid", latest, "event-4", "event-5")

	reconnect, err := store.LoadLatestID(ctx, internalIDs[1], &options)
	if err != nil {
		t.Fatalf("LoadLatestID(reconnect) failed: %v", err)
	}
	assertIDs("reconnect", reconnect, "event-3", "event-4", "event-5")

	afterTime, err := store.LoadAfterTime(ctx, base.Add(150*time.Millisecond), base.Add(time.Second), &options)
	if err != nil {
		t.Fatalf("LoadAfterTime() failed: %v", err)
	}
	assertIDs("after timestamp", afterTime, "event-2", "event-3", "event-4", "event-5")

	beforeTime, err := store.LoadBeforeTime(ctx, base.Add(450*time.Millisecond), base, &options)
	if err != nil {
		t.Fatalf("LoadBeforeTime() failed: %v", err)
	}
	assertIDs("before timestamp", beforeTime, "event-1", "event-2", "event-3", "event-4")

	overlap, err := store.LoadAfterTime(ctx, base.Add(50*time.Millisecond), base.Add(550*time.Millisecond), &options)
	if err != nil {
		t.Fatalf("LoadAfterTime(overlap) failed: %v", err)
	}
	assertIDs("overlapping timestamp range", overlap, "event-1", "event-2", "event-3", "event-4", "event-5")
	for i, msg := range overlap {
		if !reflect.DeepEqual(msg, messages[i]) {
			t.Fatalf("raw history event %d changed: got %#v, want %#v", i, msg, messages[i])
		}
	}
	if messages[1].Tags["+reply"] != afterTime[0].Tags["+reply"] || messages[2].Tags["+reply"] != afterTime[1].Tags["+reply"] {
		t.Fatalf("reaction reply references changed: %v", afterTime)
	}

	legacyToggle := &irc.Message{
		Tags:    irc.Tags{"time": "2026-07-24T10:11:12.600Z", "msgid": "event-6", "+reply": "event-1", "+react": "heart"},
		Prefix:  &irc.Prefix{Name: "carol"},
		Command: "TAGMSG",
		Params:  []string{entity},
	}
	if _, err := store.Append(ctx, network, entity, legacyToggle); err != nil {
		t.Fatalf("Append(legacy toggle) failed: %v", err)
	}
	legacyHistory, err := store.LoadAfterID(ctx, cursors[3], &options)
	if err != nil {
		t.Fatalf("LoadAfterID(legacy toggle) failed: %v", err)
	}
	assertIDs("legacy react toggle", legacyHistory, "event-5", "event-6")
	if legacyHistory[1].Tags["+react"] != "heart" || legacyHistory[1].Tags["+reply"] != "event-1" {
		t.Fatalf("legacy react toggle changed during replay: %v", legacyHistory[1])
	}

	sameTimeMessages := []*irc.Message{
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.700Z", "msgid": "event-7"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "NOTICE",
			Params:  []string{entity, "same-time-first"},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.700Z", "msgid": "event-8"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "NOTICE",
			Params:  []string{entity, "same-time-second"},
		},
		{
			Tags:    irc.Tags{"time": "2026-07-24T10:11:12.800Z", "msgid": "event-9"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "NOTICE",
			Params:  []string{entity, "after-same-time"},
		},
	}
	sameTimeCursors := make([]string, len(sameTimeMessages))
	for i, msg := range sameTimeMessages {
		if _, err := store.Append(ctx, network, entity, msg); err != nil {
			t.Fatalf("Append(sameTimeMessages[%d]) failed: %v", i, err)
		}
		var err error
		sameTimeCursors[i], _, err = store.ResolveMsgID(ctx, network, entity, msg.Tags["msgid"])
		if err != nil {
			t.Fatalf("ResolveMsgID(%q) failed: %v", msg.Tags["msgid"], err)
		}
	}

	sameTimeRange, err := store.LoadBetween(ctx,
		HistoryBound{Timestamp: base.Add(600 * time.Millisecond), ID: mustResolveMsgID(t, store, network, entity, "event-6")},
		HistoryBound{Timestamp: base.Add(800 * time.Millisecond), ID: sameTimeCursors[2]},
		&options)
	if err != nil {
		t.Fatalf("LoadBetween(same timestamp) failed: %v", err)
	}
	assertIDs("same timestamp deterministic order", sameTimeRange, "event-7", "event-8")

	adjacentSameTime, err := store.LoadBetween(ctx,
		HistoryBound{Timestamp: base.Add(700 * time.Millisecond), ID: sameTimeCursors[0]},
		HistoryBound{Timestamp: base.Add(700 * time.Millisecond), ID: sameTimeCursors[1]},
		&options)
	if err != nil {
		t.Fatalf("LoadBetween(adjacent same timestamp) failed: %v", err)
	}
	assertIDs("adjacent same timestamp exclusive", adjacentSameTime)
}

func mustResolveMsgID(t *testing.T, store ChatHistoryStore, network *database.Network, entity, msgID string) string {
	t.Helper()
	id, _, err := store.ResolveMsgID(context.Background(), network, entity, msgID)
	if err != nil {
		t.Fatalf("ResolveMsgID(%q) failed: %v", msgID, err)
	}
	return id
}

func testHistoryCursorScope(t *testing.T, store ChatHistoryStore, firstNetwork, secondNetwork *database.Network) {
	t.Helper()

	ctx := context.Background()
	type scopedMessage struct {
		network *database.Network
		entity  string
		text    string
	}
	scopes := []scopedMessage{
		{firstNetwork, "#one", "first-network-first-target"},
		{firstNetwork, "#two", "first-network-second-target"},
		{secondNetwork, "#one", "second-network-first-target"},
	}
	cursors := make([]string, len(scopes))
	for i, scope := range scopes {
		msg := &irc.Message{
			Tags:    irc.Tags{"time": "2026-07-24T11:00:00.123Z", "msgid": "shared-msgid"},
			Prefix:  &irc.Prefix{Name: "alice"},
			Command: "PRIVMSG",
			Params:  []string{scope.entity, scope.text},
		}
		if _, err := store.Append(ctx, scope.network, scope.entity, msg); err != nil {
			t.Fatalf("Append(scope %d) failed: %v", i, err)
		}
		var resolved *irc.Message
		var err error
		cursors[i], resolved, err = store.ResolveMsgID(ctx, scope.network, scope.entity, "shared-msgid")
		if err != nil {
			t.Fatalf("ResolveMsgID(scope %d) failed: %v", i, err)
		}
		if resolved.Params[1] != scope.text {
			t.Fatalf("ResolveMsgID(scope %d) returned message from another scope: %v", i, resolved)
		}
	}

	options := LoadMessageOptions{
		Network: firstNetwork,
		Entity:  "#two",
		Limit:   10,
	}
	if _, err := store.LoadAfterID(ctx, cursors[0], &options); err == nil {
		t.Fatal("LoadAfterID accepted a cursor from another target")
	}
	options.Network = secondNetwork
	options.Entity = "#one"
	if _, err := store.LoadBeforeID(ctx, cursors[0], &options); err == nil {
		t.Fatal("LoadBeforeID accepted a cursor from another network")
	}
}

func TestReactionHistoryCursorsFS(t *testing.T) {
	user := database.NewUser("testuser")
	network := database.NewNetwork("irc.example.org")
	network.ID = 1
	network.Name = "testnet"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testReactionHistoryCursors(t, store, network)
}

func TestHistoryCursorScopeFS(t *testing.T) {
	user := database.NewUser("testuser")
	firstNetwork := database.NewNetwork("irc-one.example.org")
	firstNetwork.ID = 1
	firstNetwork.Name = "one"
	secondNetwork := database.NewNetwork("irc-two.example.org")
	secondNetwork.ID = 2
	secondNetwork.Name = "two"

	store := NewFSStore(t.TempDir(), user)
	defer store.Close()
	testHistoryCursorScope(t, store, firstNetwork, secondNetwork)
}

func TestReactionHistoryCursorsDB(t *testing.T) {
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

	testReactionHistoryCursors(t, NewDBStore(db), network)
}

func TestHistoryCursorScopeDB(t *testing.T) {
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
	firstNetwork := database.NewNetwork("irc-one.example.org")
	firstNetwork.Name = "one"
	if err := db.StoreNetwork(ctx, user.ID, firstNetwork); err != nil {
		t.Fatalf("StoreNetwork(first) failed: %v", err)
	}
	secondNetwork := database.NewNetwork("irc-two.example.org")
	secondNetwork.Name = "two"
	if err := db.StoreNetwork(ctx, user.ID, secondNetwork); err != nil {
		t.Fatalf("StoreNetwork(second) failed: %v", err)
	}

	testHistoryCursorScope(t, NewDBStore(db), firstNetwork, secondNetwork)
}
