package soju

import (
	"testing"

	"gopkg.in/irc.v4"
)

func TestIsHighlight(t *testing.T) {
	nick := "SojuUser"
	testCases := []struct {
		name string
		text string
		hl   bool
	}{
		{"noContains", "hi there Soju User!", false},
		{"standalone", "SojuUser", true},
		{"middle", "hi there SojuUser!", true},
		{"start", "SojuUser: how are you doing?", true},
		{"end", "maybe ask SojuUser", true},
		{"inWord", "but OtherSojuUserSan is a different nick", false},
		{"startWord", "and OtherSojuUser is another different nick", false},
		{"endWord", "and SojuUserSan is yet a different nick", false},
		{"underscore", "and SojuUser_san has nothing to do with me", false},
		{"zeroWidthSpace", "writing S\u200BojuUser shouldn't trigger a highlight", false},
		{"url", "https://SojuUser.example", false},
		{"startURL", "https://SojuUser.example is a nice website", false},
		{"endURL", "check out my website: https://SojuUser.example", false},
		{"parenthesizedURL", "see my website (https://SojuUser.example)", false},
		{"afterURL", "see https://SojuUser.example (cc SojuUser)", true},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			hl := isHighlight(tc.text, nick)
			if hl != tc.hl {
				t.Errorf("isHighlight(%q, %q) = %v, but want %v", tc.text, nick, hl, tc.hl)
			}
		})
	}
}

func TestValidateReplyReactTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    irc.Tags
		wantErr bool
	}{
		{
			name: "reply",
			tags: irc.Tags{
				"+reply": "msgid",
			},
		},
		{
			name: "react with reply",
			tags: irc.Tags{
				"+reply":       "msgid",
				"+draft/react": "like",
			},
		},
		{
			name: "react with draft reply",
			tags: irc.Tags{
				"+draft/reply": "msgid",
				"+draft/react": "like",
			},
		},
		{
			name: "react alias with reply",
			tags: irc.Tags{
				"+reply": "msgid",
				"+react": "like",
			},
		},
		{
			name: "unreact with reply",
			tags: irc.Tags{
				"+reply":         "msgid",
				"+draft/unreact": "like",
			},
		},
		{
			name: "unreact alias with draft reply",
			tags: irc.Tags{
				"+draft/reply": "msgid",
				"+unreact":     "like",
			},
		},
		{
			name: "react without reply",
			tags: irc.Tags{
				"+draft/react": "like",
			},
			wantErr: true,
		},
		{
			name: "react and unreact",
			tags: irc.Tags{
				"+reply":         "msgid",
				"+draft/react":   "like",
				"+draft/unreact": "like",
			},
			wantErr: true,
		},
		{
			name: "conflicting reply tags",
			tags: irc.Tags{
				"+reply":       "msgid",
				"+draft/reply": "other-msgid",
			},
			wantErr: true,
		},
		{
			name: "conflicting react tags",
			tags: irc.Tags{
				"+reply":       "msgid",
				"+react":       "like",
				"+draft/react": "heart",
			},
			wantErr: true,
		},
		{
			name: "conflicting unreact tags",
			tags: irc.Tags{
				"+reply":         "msgid",
				"+unreact":       "like",
				"+draft/unreact": "heart",
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := validateReplyReactTags(tc.tags)
			if tc.wantErr && errMsg == "" {
				t.Fatalf("expected validation error")
			}
			if !tc.wantErr && errMsg != "" {
				t.Fatalf("unexpected validation error: %v", errMsg)
			}
		})
	}
}

func TestNormalizeReplyReactTags(t *testing.T) {
	tests := []struct {
		name  string
		tags  irc.Tags
		left  string
		right string
		value string
	}{
		{
			name:  "reply",
			tags:  irc.Tags{"+reply": "msgid"},
			left:  "+reply",
			right: "+draft/reply",
			value: "msgid",
		},
		{
			name:  "draft reply",
			tags:  irc.Tags{"+draft/reply": "msgid"},
			left:  "+reply",
			right: "+draft/reply",
			value: "msgid",
		},
		{
			name:  "react",
			tags:  irc.Tags{"+react": "like"},
			left:  "+react",
			right: "+draft/react",
			value: "like",
		},
		{
			name:  "draft react",
			tags:  irc.Tags{"+draft/react": "like"},
			left:  "+react",
			right: "+draft/react",
			value: "like",
		},
		{
			name:  "unreact",
			tags:  irc.Tags{"+unreact": "like"},
			left:  "+unreact",
			right: "+draft/unreact",
			value: "like",
		},
		{
			name:  "draft unreact",
			tags:  irc.Tags{"+draft/unreact": "like"},
			left:  "+unreact",
			right: "+draft/unreact",
			value: "like",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalizeReplyReactTags(tc.tags)
			if tc.tags[tc.left] != tc.value || tc.tags[tc.right] != tc.value {
				t.Fatalf("unexpected tags: %v", tc.tags)
			}
		})
	}
}

func TestMessageSupportsBacklogTagmsg(t *testing.T) {
	dc := &downstreamConn{}
	msg := &irc.Message{
		Tags: irc.Tags{
			"+draft/react": "like",
			"+reply":       "123",
		},
		Command: "TAGMSG",
		Params:  []string{"#chan"},
	}

	if !dc.messageSupportsBacklog(msg) {
		t.Fatalf("TAGMSG with reaction tags should be eligible for backlog replay")
	}
}
