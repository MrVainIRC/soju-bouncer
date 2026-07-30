package soju

import (
	"context"
	"fmt"
	"net"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/config"
	"codeberg.org/emersion/soju/database"
	"codeberg.org/emersion/soju/msgstore"
	"codeberg.org/emersion/soju/xirc"
)

var testServerPrefix = &irc.Prefix{Name: "soju-test-server"}

const (
	testUsername = "soju-test-user"
	testPassword = testUsername
)

type testingLogger struct {
	t *testing.T
}

func (tl testingLogger) Printf(format string, v ...interface{}) {
	tl.t.Logf(format, v...)
}

func createTempSqliteDB(t *testing.T) database.Database {
	if !database.SqliteEnabled {
		t.Skip("SQLite support is disabled")
	}

	db, err := database.OpenTempSqliteDB()
	if err != nil {
		t.Fatalf("failed to create temporary SQLite database: %v", err)
	}
	return db
}

func createTempPostgresDB(t *testing.T) database.Database {
	source, ok := os.LookupEnv("SOJU_TEST_POSTGRES")
	if !ok {
		t.Skip("set SOJU_TEST_POSTGRES to a connection string to execute PostgreSQL tests")
	}

	db, err := database.OpenTempPostgresDB(source)
	if err != nil {
		t.Fatalf("failed to create temporary PostgreSQL database: %v", err)
	}

	return db
}

func createTestUser(t *testing.T, db database.Database) *database.User {
	record := database.NewUser(testUsername)
	if err := record.SetPassword(testPassword); err != nil {
		t.Fatalf("failed to generate bcrypt hash: %v", err)
	}
	if err := db.StoreUser(context.Background(), record); err != nil {
		t.Fatalf("failed to store test user: %v", err)
	}

	return record
}

func createTestDownstream(t *testing.T, srv *Server) ircConn {
	c1, c2 := net.Pipe()
	go srv.serveConn(newNetIRCConn(c1))
	return newNetIRCConn(c2)
}

func createTestUpstream(t *testing.T, db database.Database, user *database.User) (*database.Network, net.Listener) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to create TCP listener: %v", err)
	}

	network := database.NewNetwork("irc+insecure://" + ln.Addr().String())
	network.Name = "testnet"
	if err := db.StoreNetwork(context.Background(), user.ID, network); err != nil {
		t.Fatalf("failed to store test network: %v", err)
	}

	return network, ln
}

func mustAccept(t *testing.T, ln net.Listener) ircConn {
	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("failed accepting connection: %v", err)
	}
	return newNetIRCConn(c)
}

func expectMessage(t *testing.T, c ircConn, cmd string) *irc.Message {
	msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read IRC message (want %q): %v", cmd, err)
	}
	if msg.Command != cmd {
		t.Fatalf("invalid message received: want %q, got: %v", cmd, msg)
	}
	return msg
}

func roundtrip(t *testing.T, c ircConn) []*irc.Message {
	c.WriteMessage(&irc.Message{Command: "PING", Params: []string{"roundtrip"}})

	var msgs []*irc.Message
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read IRC message: %v", err)
		}

		if msg.Command == "PONG" {
			break
		}

		msgs = append(msgs, msg)
	}

	return msgs
}

func registerDownstreamConn(t *testing.T, c ircConn, network *database.Network) {
	c.WriteMessage(&irc.Message{
		Command: "PASS",
		Params:  []string{testPassword},
	})
	c.WriteMessage(&irc.Message{
		Command: "NICK",
		Params:  []string{testUsername},
	})
	c.WriteMessage(&irc.Message{
		Command: "USER",
		Params:  []string{testUsername + "/" + network.Name, "0", "*", testUsername},
	})

	for {
		msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read IRC message while waiting for welcome: %v", err)
		}
		if msg.Command == irc.RPL_WELCOME {
			break
		}
	}
}

func registerUpstreamConn(t *testing.T, c ircConn) {
	msg := expectMessage(t, c, "CAP")
	if msg.Params[0] != "LS" {
		t.Fatalf("invalid CAP LS: got: %v", msg)
	}
	msg = expectMessage(t, c, "NICK")
	nick := msg.Params[0]
	if nick != testUsername {
		t.Fatalf("invalid NICK: want %q, got: %v", testUsername, msg)
	}
	expectMessage(t, c, "USER")

	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_WELCOME,
		Params:  []string{nick, "Welcome!"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_YOURHOST,
		Params:  []string{nick, "Your host is soju-test-server"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_CREATED,
		Params:  []string{nick, "Who cares when the server was created?"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_MYINFO,
		Params:  []string{nick, testServerPrefix.Name, "soju", "aiwroO", "OovaimnqpsrtklbeI"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.ERR_NOMOTD,
		Params:  []string{nick, "No MOTD"},
	})
}

func registerUpstreamConnWithCaps(t *testing.T, c ircConn, caps ...string) {
	msg := expectMessage(t, c, "CAP")
	if msg.Params[0] != "LS" {
		t.Fatalf("invalid CAP LS: got: %v", msg)
	}

	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: "CAP",
		Params:  []string{"*", "LS", strings.Join(caps, " ")},
	})

	msg = expectMessage(t, c, "NICK")
	nick := msg.Params[0]
	if nick != testUsername {
		t.Fatalf("invalid NICK: want %q, got: %v", testUsername, msg)
	}
	expectMessage(t, c, "USER")

	msg = expectMessage(t, c, "CAP")
	if msg.Params[0] != "REQ" {
		t.Fatalf("invalid CAP REQ: got: %v", msg)
	}
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: "CAP",
		Params:  []string{"*", "ACK", msg.Params[len(msg.Params)-1]},
	})
	expectMessage(t, c, "CAP")

	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_WELCOME,
		Params:  []string{nick, "Welcome!"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_YOURHOST,
		Params:  []string{nick, "Your host is soju-test-server"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_CREATED,
		Params:  []string{nick, "Who cares when the server was created?"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.RPL_MYINFO,
		Params:  []string{nick, testServerPrefix.Name, "soju", "aiwroO", "OovaimnqpsrtklbeI"},
	})
	c.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: irc.ERR_NOMOTD,
		Params:  []string{nick, "No MOTD"},
	})
}

func newDebugLogger(t *testing.T) *DebugLogger {
	l := &DebugLogger{
		logger: &testingLogger{t},
	}
	l.debug.Store(true)
	return l
}

func testBroadcast(t *testing.T, db database.Database) {
	user := createTestUser(t, db)
	network, upstream := createTestUpstream(t, db, user)
	defer upstream.Close()

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	uc := mustAccept(t, upstream)
	defer uc.Close()
	registerUpstreamConn(t, uc)

	dc := createTestDownstream(t, srv)
	defer dc.Close()
	registerDownstreamConn(t, dc, network)

	noticeText := "This is a very important server notice."
	uc.WriteMessage(&irc.Message{
		Prefix:  testServerPrefix,
		Command: "NOTICE",
		Params:  []string{testUsername, noticeText},
	})

	var msg *irc.Message
	for {
		var err error
		msg, err = dc.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read IRC message: %v", err)
		}
		if msg.Command == "NOTICE" {
			break
		}
	}

	if msg.Params[1] != noticeText {
		t.Fatalf("invalid NOTICE text: want %q, got: %v", noticeText, msg)
	}
}

func TestServer_broadcast(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db := createTempSqliteDB(t)
		testBroadcast(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		db := createTempPostgresDB(t)
		testBroadcast(t, db)
	})
}

func testChatHistory(t *testing.T, msgStoreDriver msgstore.Driver, msgStorePath string) {
	db := createTempSqliteDB(t)

	user := createTestUser(t, db)
	network, upstream := createTestUpstream(t, db, user)
	defer upstream.Close()

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)

	cfg := *srv.Config()
	cfg.MsgStore = config.MsgStore{Driver: msgStoreDriver, Source: msgStorePath}
	srv.SetConfig(&cfg)

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	uc := mustAccept(t, upstream)
	defer uc.Close()
	registerUpstreamConn(t, uc)

	texts := []string{
		"Hiya!",
		"How are you doing?",
		"Can I take a sip from your glass of soju?",
	}

	baseTime := time.Date(2023, 05, 23, 6, 0, 0, 0, time.UTC)
	for i, text := range texts {
		msgTime := baseTime.Add(time.Duration(i) * time.Second)
		uc.WriteMessage(&irc.Message{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(msgTime)},
			Prefix:  &irc.Prefix{Name: "foo"},
			Command: "PRIVMSG",
			Params:  []string{testUsername, text},
		})
	}
	roundtrip(t, uc)

	dc := createTestDownstream(t, srv)
	defer dc.Close()
	registerDownstreamConn(t, dc, network)
	roundtrip(t, dc) // drain post-connection-registration messages

	testCases := []struct {
		Name   string
		After  time.Time
		Around time.Time
		Texts  []string
	}{
		{
			Name:  "after_all",
			After: baseTime.Add(-time.Second),
			Texts: texts,
		},
		{
			Name:  "after_none",
			After: baseTime.Add(time.Duration(len(texts)-1) * time.Second),
			Texts: nil,
		},
		{
			Name:  "after_all_but_first",
			After: baseTime,
			Texts: texts[1:],
		},
		{
			Name:   "around_all",
			Around: baseTime.Add(time.Second),
			Texts:  texts,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			if !tc.After.IsZero() {
				dc.WriteMessage(&irc.Message{
					Command: "CHATHISTORY",
					Params:  []string{"AFTER", "foo", "timestamp=" + xirc.FormatServerTime(tc.After), "100"},
				})
			} else if !tc.Around.IsZero() {
				dc.WriteMessage(&irc.Message{
					Command: "CHATHISTORY",
					Params:  []string{"AROUND", "foo", "timestamp=" + xirc.FormatServerTime(tc.Around), "100"},
				})
			} else {
				panic("no timestamp specified in test case")
			}

			var got []string
			for _, msg := range roundtrip(t, dc) {
				if msg.Command != "PRIVMSG" {
					t.Fatalf("unexpected reply: %v", msg)
				}
				got = append(got, msg.Params[1])
			}

			if !reflect.DeepEqual(got, tc.Texts) {
				t.Errorf("got %v, want %v", got, tc.Texts)
			}
		})
	}
}

func TestServer_chatHistory(t *testing.T) {
	t.Run("fs", func(t *testing.T) {
		testChatHistory(t, msgstore.DriverFS, t.TempDir())
	})

	t.Run("db", func(t *testing.T) {
		testChatHistory(t, msgstore.DriverDB, "")
	})
}

func registerDownstreamConnWithCaps(t *testing.T, c ircConn, network *database.Network, caps ...string) {
	c.WriteMessage(&irc.Message{
		Command: "CAP",
		Params:  []string{"LS", "302"},
	})
	expectMessage(t, c, "CAP")

	if len(caps) > 0 {
		c.WriteMessage(&irc.Message{
			Command: "CAP",
			Params:  []string{"REQ", strings.Join(caps, " ")},
		})
		expectMessage(t, c, "CAP")
	}

	c.WriteMessage(&irc.Message{
		Command: "PASS",
		Params:  []string{testPassword},
	})
	c.WriteMessage(&irc.Message{
		Command: "NICK",
		Params:  []string{testUsername},
	})
	c.WriteMessage(&irc.Message{
		Command: "USER",
		Params:  []string{testUsername + "/" + network.Name, "0", "*", testUsername},
	})
	c.WriteMessage(&irc.Message{
		Command: "CAP",
		Params:  []string{"END"},
	})

	for {
		msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read IRC message while waiting for welcome: %v", err)
		}
		if msg.Command == irc.RPL_WELCOME {
			break
		}
	}
}

func testChatHistoryReactionEvents(t *testing.T, msgStoreDriver msgstore.Driver, msgStorePath string, expectOtherTagmsg bool, downstreamCaps ...string) {
	db := createTempSqliteDB(t)

	user := createTestUser(t, db)
	network, upstream := createTestUpstream(t, db, user)
	defer upstream.Close()

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)

	cfg := *srv.Config()
	cfg.MsgStore = config.MsgStore{Driver: msgStoreDriver, Source: msgStorePath}
	srv.SetConfig(&cfg)

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	uc := mustAccept(t, upstream)
	defer uc.Close()
	registerUpstreamConnWithCaps(t, uc, "message-tags", "batch", "server-time")

	channel := "#reactions"
	msgID := "srv-000001"
	baseTime := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	events := []*irc.Message{
		{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime), "msgid": msgID, "account": "alice"},
			Prefix:  &irc.Prefix{Name: "alice", User: "alice", Host: "example.org"},
			Command: "PRIVMSG",
			Params:  []string{channel, "hello"},
		},
		{
			Tags: irc.Tags{
				"time":         xirc.FormatServerTime(baseTime.Add(time.Second)),
				"msgid":        "srv-000002",
				"account":      "bob",
				"+reply":       msgID,
				"+draft/react": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob", User: "bob", Host: "example.org"},
			Command: "TAGMSG",
			Params:  []string{channel},
		},
		{
			Tags: irc.Tags{
				"time":           xirc.FormatServerTime(baseTime.Add(2 * time.Second)),
				"msgid":          "srv-000003",
				"account":        "bob",
				"+reply":         msgID,
				"+draft/unreact": "like",
			},
			Prefix:  &irc.Prefix{Name: "bob", User: "bob", Host: "example.org"},
			Command: "TAGMSG",
			Params:  []string{channel},
		},
		{
			Tags: irc.Tags{
				"time":    xirc.FormatServerTime(baseTime.Add(3 * time.Second)),
				"msgid":   "srv-000004",
				"account": "bob",
				"+reply":  msgID,
			},
			Prefix:  &irc.Prefix{Name: "bob", User: "bob", Host: "example.org"},
			Command: "TAGMSG",
			Params:  []string{channel},
		},
	}
	for _, msg := range events {
		uc.WriteMessage(msg)
	}
	roundtrip(t, uc)

	dc := createTestDownstream(t, srv)
	defer dc.Close()
	registerDownstreamConnWithCaps(t, dc, network, downstreamCaps...)
	registration := roundtrip(t, dc)
	foundMsgRefTypes := false
	for _, msg := range registration {
		if msg.Command == irc.RPL_ISUPPORT && slices.Contains(msg.Params, "MSGREFTYPES=msgid,timestamp") {
			foundMsgRefTypes = true
		}
	}
	if !foundMsgRefTypes {
		t.Fatalf("registration did not advertise MSGREFTYPES=msgid,timestamp: %v", registration)
	}

	requestHistory := func(params ...string) []*irc.Message {
		t.Helper()
		dc.WriteMessage(&irc.Message{
			Command: "CHATHISTORY",
			Params:  params,
		})
		var history []*irc.Message
		for _, msg := range roundtrip(t, dc) {
			if msg.Command == "PRIVMSG" || msg.Command == "NOTICE" || msg.Command == "TAGMSG" {
				history = append(history, msg)
			}
		}
		return history
	}

	history := requestHistory("LATEST", channel, "*", "10")
	wantLen := len(events) - 1
	if expectOtherTagmsg {
		wantLen = len(events)
	}
	if len(history) != wantLen {
		t.Fatalf("history returned %d events, want %d: %v", len(history), wantLen, history)
	}
	if history[0].Command != "PRIVMSG" || history[0].Params[1] != "hello" {
		t.Fatalf("unexpected original message: %v", history[0])
	}
	if history[1].Command != "TAGMSG" || history[1].Tags["+draft/react"] != "like" || history[1].Tags["+reply"] != msgID {
		t.Fatalf("unexpected react event: %v", history[1])
	}
	if history[2].Command != "TAGMSG" || history[2].Tags["+draft/unreact"] != "like" || history[2].Tags["+reply"] != msgID {
		t.Fatalf("unexpected unreact event: %v", history[2])
	}
	if expectOtherTagmsg && (history[3].Command != "TAGMSG" || history[3].Tags["+reply"] != msgID || history[3].Tags["+draft/react"] != "" || history[3].Tags["+draft/unreact"] != "") {
		t.Fatalf("unexpected non-reaction TAGMSG event: %v", history[3])
	}

	assertHistoryIDs := func(name string, got []*irc.Message, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s returned %d messages, want %d: %v", name, len(got), len(want), got)
		}
		for i, msg := range got {
			if msg.Tags["msgid"] != want[i] {
				t.Fatalf("%s message %d has msgid %q, want %q: %v", name, i, msg.Tags["msgid"], want[i], msg)
			}
		}
	}

	afterIDs := []string{"srv-000002", "srv-000003"}
	latestIDs := []string{"srv-000002", "srv-000003"}
	if expectOtherTagmsg {
		afterIDs = append(afterIDs, "srv-000004")
		latestIDs = []string{"srv-000003", "srv-000004"}
	}
	assertHistoryIDs("AFTER msgid", requestHistory("AFTER", channel, "msgid=srv-000001", "10"), afterIDs...)
	assertHistoryIDs("BEFORE msgid", requestHistory("BEFORE", channel, "msgid=srv-000003", "10"), "srv-000001", "srv-000002")
	assertHistoryIDs("BETWEEN msgid", requestHistory("BETWEEN", channel, "msgid=srv-000001", "msgid=srv-000003", "10"), "srv-000002")
	assertHistoryIDs("AROUND msgid", requestHistory("AROUND", channel, "msgid=srv-000002", "3"), "srv-000001", "srv-000002", "srv-000003")
	assertHistoryIDs("LATEST msgid", requestHistory("LATEST", channel, "msgid=srv-000001", "2"), latestIDs...)

	timestamp0 := "timestamp=" + xirc.FormatServerTime(baseTime)
	timestamp1 := "timestamp=" + xirc.FormatServerTime(baseTime.Add(time.Second))
	timestamp2 := "timestamp=" + xirc.FormatServerTime(baseTime.Add(2*time.Second))
	assertHistoryIDs("AFTER timestamp", requestHistory("AFTER", channel, timestamp0, "10"), afterIDs...)
	assertHistoryIDs("BEFORE timestamp", requestHistory("BEFORE", channel, timestamp2, "10"), "srv-000001", "srv-000002")
	assertHistoryIDs("BETWEEN timestamp", requestHistory("BETWEEN", channel, timestamp0, timestamp2, "10"), "srv-000002")
	assertHistoryIDs("BETWEEN msgid-timestamp", requestHistory("BETWEEN", channel, "msgid=srv-000001", timestamp2, "10"), "srv-000002")
	assertHistoryIDs("BETWEEN timestamp-msgid", requestHistory("BETWEEN", channel, timestamp0, "msgid=srv-000003", "10"), "srv-000002")
	assertHistoryIDs("BETWEEN reverse msgid-msgid", requestHistory("BETWEEN", channel, "msgid=srv-000003", "msgid=srv-000001", "1"), "srv-000002")
	assertHistoryIDs("BETWEEN reverse timestamp-timestamp", requestHistory("BETWEEN", channel, timestamp2, timestamp0, "1"), "srv-000002")
	assertHistoryIDs("BETWEEN reverse msgid-timestamp", requestHistory("BETWEEN", channel, "msgid=srv-000003", timestamp0, "1"), "srv-000002")
	assertHistoryIDs("BETWEEN reverse timestamp-msgid", requestHistory("BETWEEN", channel, timestamp2, "msgid=srv-000001", "1"), "srv-000002")
	assertHistoryIDs("AROUND timestamp", requestHistory("AROUND", channel, timestamp1, "3"), "srv-000001", "srv-000002")
	assertHistoryIDs("LATEST timestamp", requestHistory("LATEST", channel, timestamp0, "2"), latestIDs...)
}

func TestServer_chatHistoryReactionEvents(t *testing.T) {
	capSets := map[string]struct {
		caps              []string
		expectOtherTagmsg bool
	}{
		"message-tags": {
			caps: []string{"message-tags", "batch", "server-time", "draft/chathistory"},
		},
		"event-playback": {
			caps:              []string{"batch", "server-time", "draft/chathistory", "draft/event-playback"},
			expectOtherTagmsg: true,
		},
	}

	for name, tt := range capSets {
		t.Run("fs/"+name, func(t *testing.T) {
			testChatHistoryReactionEvents(t, msgstore.DriverFS, t.TempDir(), tt.expectOtherTagmsg, tt.caps...)
		})

		t.Run("db/"+name, func(t *testing.T) {
			testChatHistoryReactionEvents(t, msgstore.DriverDB, "", tt.expectOtherTagmsg, tt.caps...)
		})
	}
}

func TestServer_liveReactionEventsBetweenNamedDownstreams(t *testing.T) {
	db := createTempSqliteDB(t)
	user := createTestUser(t, db)
	network, upstream := createTestUpstream(t, db, user)
	defer upstream.Close()

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)
	cfg := *srv.Config()
	cfg.MsgStore = config.MsgStore{Driver: msgstore.DriverFS, Source: t.TempDir()}
	srv.SetConfig(&cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	uc := mustAccept(t, upstream)
	defer uc.Close()
	registerUpstreamConnWithCaps(t, uc, "message-tags", "server-time", "echo-message", "labeled-response")

	registerClient := func(c ircConn, clientName string) {
		t.Helper()
		c.WriteMessage(&irc.Message{Command: "CAP", Params: []string{"LS", "302"}})
		expectMessage(t, c, "CAP")
		c.WriteMessage(&irc.Message{Command: "CAP", Params: []string{"REQ", "message-tags echo-message"}})
		expectMessage(t, c, "CAP")
		c.WriteMessage(&irc.Message{Command: "PASS", Params: []string{testPassword}})
		c.WriteMessage(&irc.Message{Command: "NICK", Params: []string{testUsername}})
		c.WriteMessage(&irc.Message{
			Command: "USER",
			Params:  []string{fmt.Sprintf("%s/%s@%s", testUsername, network.Name, clientName), "0", "*", testUsername},
		})
		c.WriteMessage(&irc.Message{Command: "CAP", Params: []string{"END"}})
		for {
			msg, err := c.ReadMessage()
			if err != nil {
				t.Fatalf("failed to read welcome: %v", err)
			}
			if msg.Command == irc.RPL_WELCOME {
				break
			}
		}
	}

	sender := createTestDownstream(t, srv)
	defer sender.Close()
	registerClient(sender, "mobile")
	roundtrip(t, sender)

	receiver := createTestDownstream(t, srv)
	defer receiver.Close()
	registerClient(receiver, "desktop")
	roundtrip(t, receiver)

	for i, tags := range []irc.Tags{
		{"+draft/react": "like", "+reply": "target-message"},
		{"+draft/unreact": "like", "+reply": "target-message"},
		{"+react": "heart", "+reply": "target-message"},
		{"+react": "heart", "+reply": "target-message"},
	} {
		sender.WriteMessage(&irc.Message{
			Tags:    tags,
			Command: "TAGMSG",
			Params:  []string{"#reactions"},
		})

		var upstreamMsg *irc.Message
		for upstreamMsg == nil {
			msg, err := uc.ReadMessage()
			if err != nil {
				t.Fatalf("failed to read upstream reaction: %v", err)
			}
			if msg.Command == "TAGMSG" {
				upstreamMsg = msg
			}
		}
		echo := upstreamMsg.Copy()
		echo.Prefix = &irc.Prefix{Name: testUsername, User: "user", Host: "example.org"}
		echo.Tags["msgid"] = fmt.Sprintf("event-%d", i+1)
		echo.Tags["time"] = xirc.FormatServerTime(time.Now())
		uc.WriteMessage(echo)
		roundtrip(t, uc)

		readReaction := func(c ircConn) *irc.Message {
			var received *irc.Message
			for _, msg := range roundtrip(t, c) {
				if msg.Command == "TAGMSG" {
					if received != nil {
						t.Fatalf("client received duplicate reaction events: %v and %v", received, msg)
					}
					received = msg
				}
			}
			return received
		}
		senderReceived := readReaction(sender)
		receiverReceived := readReaction(receiver)
		if senderReceived == nil || receiverReceived == nil {
			t.Fatalf("reaction was not delivered to both downstreams: sender=%v receiver=%v", senderReceived, receiverReceived)
		}
		for name, received := range map[string]*irc.Message{"sender": senderReceived, "receiver": receiverReceived} {
			for tag, value := range tags {
				if received.Tags[tag] != value {
					t.Fatalf("%s received changed reaction tag %q: %v", name, tag, received)
				}
			}
			if received.Tags["msgid"] != echo.Tags["msgid"] || received.Tags["time"] == "" ||
				!strings.HasPrefix(received.Tags["time"], strings.SplitN(echo.Tags["time"], ".", 2)[0]) {
				t.Fatalf("%s received changed event identity/time: got=%v want=%v", name, received, echo)
			}
			if received.Prefix == nil || received.Prefix.Name != echo.Prefix.Name || received.Params[0] != echo.Params[0] {
				t.Fatalf("%s received changed prefix/target: got=%v want=%v", name, received, echo)
			}
		}
	}
}

func TestServer_metadataBatchOrderingAndReplay(t *testing.T) {
	db := createTempSqliteDB(t)
	user := createTestUser(t, db)
	network, upstream := createTestUpstream(t, db, user)
	defer upstream.Close()

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)
	cfg := *srv.Config()
	cfg.MsgStore = config.MsgStore{Driver: msgstore.DriverFS, Source: t.TempDir()}
	srv.SetConfig(&cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	uc := mustAccept(t, upstream)
	defer uc.Close()
	registerUpstreamConnWithCaps(t, uc, "message-tags", "batch", "server-time", "draft/multiline", "draft/metadata-2")

	dc := createTestDownstream(t, srv)
	defer dc.Close()
	registerDownstreamConnWithCaps(t, dc, network, "message-tags", "batch", "server-time", "draft/chathistory", "draft/event-playback", "draft/multiline", "draft/metadata-2")
	roundtrip(t, dc)

	channelA := "#batch-a"
	channelB := "#batch-b"
	baseTime := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	msgs := []*irc.Message{
		{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime), "msgid": "outside-md"},
			Prefix:  testServerPrefix,
			Command: "METADATA",
			Params:  []string{"alice", "SET", "status", "outside"},
		},
		{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime.Add(time.Second)), "msgid": "batch-a-start"},
			Prefix:  testServerPrefix,
			Command: "BATCH",
			Params:  []string{"+a", "draft/multiline", channelA},
		},
		{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime.Add(2 * time.Second)), "msgid": "batch-b-start"},
			Prefix:  testServerPrefix,
			Command: "BATCH",
			Params:  []string{"+b", "draft/multiline", channelB},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(3 * time.Second)), "msgid": "a-md-before"},
			Prefix:  testServerPrefix,
			Command: "METADATA",
			Params:  []string{"alice", "SET", "status", "before"},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(4 * time.Second)), "msgid": "a-msg-1"},
			Prefix:  &irc.Prefix{Name: "alice", User: "alice", Host: "example.org"},
			Command: "PRIVMSG",
			Params:  []string{channelA, "one"},
		},
		{
			Tags:    irc.Tags{"batch": "b", "time": xirc.FormatServerTime(baseTime.Add(5 * time.Second)), "msgid": "b-md"},
			Prefix:  testServerPrefix,
			Command: "METADATA",
			Params:  []string{"bob", "SET", "status", "parallel"},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(6 * time.Second)), "msgid": "a-react", "+reply": "a-msg-1", "+react": "heart"},
			Prefix:  &irc.Prefix{Name: "bob", User: "bob", Host: "example.org"},
			Command: "TAGMSG",
			Params:  []string{channelA},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(7 * time.Second)), "msgid": "a-md-between"},
			Prefix:  testServerPrefix,
			Command: "METADATA",
			Params:  []string{"alice", "SET", "color", "#123456"},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(8 * time.Second)), "msgid": "a-unreact", "+reply": "a-msg-1", "+unreact": "heart"},
			Prefix:  &irc.Prefix{Name: "bob", User: "bob", Host: "example.org"},
			Command: "TAGMSG",
			Params:  []string{channelA},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(9 * time.Second)), "msgid": "a-msg-2"},
			Prefix:  &irc.Prefix{Name: "alice", User: "alice", Host: "example.org"},
			Command: "PRIVMSG",
			Params:  []string{channelA, "two"},
		},
		{
			Tags:    irc.Tags{"batch": "a", "time": xirc.FormatServerTime(baseTime.Add(10 * time.Second)), "msgid": "a-md-after"},
			Prefix:  testServerPrefix,
			Command: "METADATA",
			Params:  []string{"alice", "SET", "pronouns", "she/her"},
		},
		{
			// Reproduce a clock-skewed IRCd batch end. The end must retain
			// semantic batch order even when its timestamp precedes the content.
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime), "msgid": "batch-a-end"},
			Prefix:  testServerPrefix,
			Command: "BATCH",
			Params:  []string{"-a"},
		},
		{
			Tags:    irc.Tags{"time": xirc.FormatServerTime(baseTime.Add(12 * time.Second)), "msgid": "batch-b-end"},
			Prefix:  testServerPrefix,
			Command: "BATCH",
			Params:  []string{"-b"},
		},
	}
	for _, msg := range msgs {
		uc.WriteMessage(msg)
	}

	live := readUntilSelectedBatchReplayMessages(t, dc, channelA, 9)
	assertBatchSemanticOrder(t, "live", selectBatchReplayMessages(live, channelA), []batchReplayWant{
		{Command: "BATCH", BatchRef: "+u-a", BatchType: "draft/multiline"},
		{Command: "METADATA", MsgID: "a-md-before", BatchTag: "u-a"},
		{Command: "PRIVMSG", MsgID: "a-msg-1", BatchTag: "u-a"},
		{Command: "TAGMSG", MsgID: "a-react", BatchTag: "u-a", Tag: "+react", TagValue: "heart"},
		{Command: "METADATA", MsgID: "a-md-between", BatchTag: "u-a"},
		{Command: "TAGMSG", MsgID: "a-unreact", BatchTag: "u-a", Tag: "+unreact", TagValue: "heart"},
		{Command: "PRIVMSG", MsgID: "a-msg-2", BatchTag: "u-a"},
		{Command: "METADATA", MsgID: "a-md-after", BatchTag: "u-a"},
		{Command: "BATCH", BatchRef: "-u-a"},
	})

	dc.WriteMessage(&irc.Message{Command: "CHATHISTORY", Params: []string{"LATEST", channelA, "*", "20"}})
	history := roundtrip(t, dc)
	assertBatchSemanticOrder(t, "history", selectBatchReplayMessages(history, channelA), []batchReplayWant{
		{Command: "BATCH", BatchRef: "+u-a", BatchType: "draft/multiline"},
		{Command: "METADATA", MsgID: "a-md-before", BatchTag: "u-a"},
		{Command: "PRIVMSG", MsgID: "a-msg-1", BatchTag: "u-a"},
		{Command: "TAGMSG", MsgID: "a-react", BatchTag: "u-a", Tag: "+react", TagValue: "heart"},
		{Command: "METADATA", MsgID: "a-md-between", BatchTag: "u-a"},
		{Command: "TAGMSG", MsgID: "a-unreact", BatchTag: "u-a", Tag: "+unreact", TagValue: "heart"},
		{Command: "PRIVMSG", MsgID: "a-msg-2", BatchTag: "u-a"},
		{Command: "METADATA", MsgID: "a-md-after", BatchTag: "u-a"},
		{Command: "BATCH", BatchRef: "-u-a"},
	})

	for _, msg := range history {
		if msg.Tags["msgid"] == "outside-md" {
			t.Fatalf("metadata outside a stored target batch was replayed: %v", msg)
		}
		if msg.Tags["msgid"] == "b-md" && msg.Tags["batch"] != "u-b" {
			t.Fatalf("parallel batch metadata has batch tag %q, want u-b: %v", msg.Tags["batch"], msg)
		}
	}
}

type batchReplayWant struct {
	Command   string
	MsgID     string
	BatchTag  string
	BatchRef  string
	BatchType string
	Tag       string
	TagValue  string
}

func selectBatchReplayMessages(msgs []*irc.Message, channel string) []*irc.Message {
	var selected []*irc.Message
	for _, msg := range msgs {
		switch msg.Command {
		case "BATCH":
			if len(msg.Params) >= 1 && (msg.Params[0] == "+u-a" || msg.Params[0] == "-u-a") {
				selected = append(selected, msg)
			}
		case "PRIVMSG", "TAGMSG":
			if len(msg.Params) > 0 && msg.Params[0] == channel {
				selected = append(selected, msg)
			}
		case "METADATA":
			if msg.Tags["batch"] == "u-a" {
				selected = append(selected, msg)
			}
		}
	}
	return selected
}

func readUntilSelectedBatchReplayMessages(t *testing.T, c ircConn, channel string, want int) []*irc.Message {
	t.Helper()
	var msgs []*irc.Message
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read IRC message: %v", err)
		}
		msgs = append(msgs, msg)
		if len(selectBatchReplayMessages(msgs, channel)) >= want {
			return msgs
		}
	}
}

func assertBatchSemanticOrder(t *testing.T, name string, got []*irc.Message, want []batchReplayWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s returned %d selected messages, want %d: %v", name, len(got), len(want), got)
	}
	for i, msg := range got {
		w := want[i]
		if msg.Command != w.Command {
			t.Fatalf("%s[%d] command = %q, want %q: %v", name, i, msg.Command, w.Command, msg)
		}
		if w.MsgID != "" && msg.Tags["msgid"] != w.MsgID {
			t.Fatalf("%s[%d] msgid = %q, want %q: %v", name, i, msg.Tags["msgid"], w.MsgID, msg)
		}
		if w.BatchTag != "" && msg.Tags["batch"] != w.BatchTag {
			t.Fatalf("%s[%d] batch tag = %q, want %q: %v", name, i, msg.Tags["batch"], w.BatchTag, msg)
		}
		if w.BatchRef != "" && (len(msg.Params) < 1 || msg.Params[0] != w.BatchRef) {
			t.Fatalf("%s[%d] batch ref = %v, want %q: %v", name, i, msg.Params, w.BatchRef, msg)
		}
		if w.BatchType != "" && (len(msg.Params) < 2 || msg.Params[1] != w.BatchType) {
			t.Fatalf("%s[%d] batch type = %v, want %q: %v", name, i, msg.Params, w.BatchType, msg)
		}
		if w.Tag != "" && msg.Tags[w.Tag] != w.TagValue {
			t.Fatalf("%s[%d] tag %q = %q, want %q: %v", name, i, w.Tag, msg.Tags[w.Tag], w.TagValue, msg)
		}
	}
}

func TestServer_initialCapLSIncludesEventPlayback(t *testing.T) {
	db := createTempSqliteDB(t)

	srv := NewServer(db)
	srv.Logger = newDebugLogger(t)

	cfg := *srv.Config()
	cfg.MsgStore = config.MsgStore{Driver: msgstore.DriverFS, Source: t.TempDir()}
	srv.SetConfig(&cfg)

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Shutdown()

	dc := createTestDownstream(t, srv)
	defer dc.Close()

	dc.WriteMessage(&irc.Message{
		Command: "CAP",
		Params:  []string{"LS", "302"},
	})

	msg := expectMessage(t, dc, "CAP")
	if len(msg.Params) < 3 || msg.Params[1] != "LS" {
		t.Fatalf("invalid CAP LS: %v", msg)
	}

	caps := strings.Fields(msg.Params[len(msg.Params)-1])
	for _, cap := range caps {
		if cap == "draft/event-playback" {
			return
		}
	}
	t.Fatalf("initial CAP LS is missing draft/event-playback: %v", msg.Params[len(msg.Params)-1])
}
