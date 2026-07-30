package soju

import (
	"testing"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/xirc"
)

func newTestBotUpstreamConn() *upstreamConn {
	return &upstreamConn{
		network: &network{
			casemap: stdCaseMapping,
		},
		nick: "testbot",
		isupport: map[string]*string{
			"BOT": newString("B"),
		},
		users: xirc.NewCaseMappingMap[*upstreamUser](stdCaseMapping),
	}
}

func TestUpstreamConnAddBotTag(t *testing.T) {
	uc := newTestBotUpstreamConn()
	uc.modes = "B"

	msg := &irc.Message{
		Prefix:  &irc.Prefix{Name: "testbot"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "hello"},
	}

	got := uc.addBotTag(msg)
	if got == msg {
		t.Fatalf("addBotTag mutated the original message")
	}
	if _, ok := got.Tags["bot"]; !ok {
		t.Fatalf("missing bot tag: %v", got.Tags)
	}
	if _, ok := msg.Tags["bot"]; ok {
		t.Fatalf("original message was tagged: %v", msg.Tags)
	}
}

func TestUpstreamConnAddBotTagOnlyForOwnBotNick(t *testing.T) {
	uc := newTestBotUpstreamConn()
	uc.modes = "B"

	msg := &irc.Message{
		Prefix:  &irc.Prefix{Name: "someoneelse"},
		Command: "PRIVMSG",
		Params:  []string{"#chan", "hello"},
	}

	if got := uc.addBotTag(msg); got != msg {
		t.Fatalf("tagged a message from another nick")
	}

	uc.modes = ""
	msg.Prefix.Name = "testbot"
	if got := uc.addBotTag(msg); got != msg {
		t.Fatalf("tagged own message without bot mode")
	}
}

func TestUpstreamConnUpdateCachedBotMode(t *testing.T) {
	uc := newTestBotUpstreamConn()
	uc.users.Set("testbot", &upstreamUser{
		Nickname: "testbot",
		Flags:    "H",
	})

	uc.updateCachedBotMode("testbot", true)
	if flags := uc.users.Get("testbot").Flags; flags != "HB" {
		t.Fatalf("unexpected bot flags: got %q, want %q", flags, "HB")
	}

	uc.updateCachedBotMode("testbot", true)
	if flags := uc.users.Get("testbot").Flags; flags != "HB" {
		t.Fatalf("duplicated bot flag: got %q", flags)
	}

	uc.updateCachedBotMode("testbot", false)
	if flags := uc.users.Get("testbot").Flags; flags != "H" {
		t.Fatalf("unexpected non-bot flags: got %q, want %q", flags, "H")
	}
}
