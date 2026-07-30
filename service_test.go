package soju

import (
	"strings"
	"testing"
)

func assertSplit(t *testing.T, input string, expected []string) {
	actual, err := splitWords(input)
	if err != nil {
		t.Errorf("%q: %v", input, err)
		return
	}
	if len(actual) != len(expected) {
		t.Errorf("%q: expected %d words, got %d\nexpected: %v\ngot: %v", input, len(expected), len(actual), expected, actual)
		return
	}
	for i := 0; i < len(actual); i++ {
		if actual[i] != expected[i] {
			t.Errorf("%q: expected word #%d to be %q, got %q\nexpected: %v\ngot: %v", input, i, expected[i], actual[i], expected, actual)
		}
	}
}

func TestSplit(t *testing.T) {
	assertSplit(t, "  ch 'up' #soju    'relay'-det\"ache\"d  message  ", []string{
		"ch",
		"up",
		"#soju",
		"relay-detached",
		"message",
	})
	assertSplit(t, "net update \\\"free\\\"node -pass 'political \"stance\" desu!' -realname '' -nick lee", []string{
		"net",
		"update",
		"\"free\"node",
		"-pass",
		"political \"stance\" desu!",
		"-realname",
		"",
		"-nick",
		"lee",
	})
	assertSplit(t, "Omedeto,\\ Yui! ''", []string{
		"Omedeto, Yui!",
		"",
	})

	if _, err := splitWords("end of 'file"); err == nil {
		t.Errorf("expected error on unterminated single quote")
	}
	if _, err := splitWords("end of backquote \\"); err == nil {
		t.Errorf("expected error on unterminated backquote sequence")
	}
}

func TestServiceHelpHidesAdminCommands(t *testing.T) {
	var lines []string
	ctx := &serviceContext{
		user: &user{},
		print: func(text string) {
			lines = append(lines, text)
		},
	}

	if err := handleServiceHelp(ctx, nil); err != nil {
		t.Fatalf("handleServiceHelp() failed: %v", err)
	}

	help := strings.Join(lines, "\n")
	for _, hidden := range []string{
		"server status",
		"server notice",
		"server debug",
		"user create",
		"user run",
		"user status",
	} {
		if strings.Contains(help, hidden) {
			t.Fatalf("non-admin help contains admin command %q:\n%v", hidden, help)
		}
	}
	if !strings.Contains(help, "network status") {
		t.Fatalf("non-admin help is missing regular command:\n%v", help)
	}
}

func TestServiceHelpAdminCommands(t *testing.T) {
	var lines []string
	ctx := &serviceContext{
		user:  &user{},
		admin: true,
		print: func(text string) {
			lines = append(lines, text)
		},
	}

	if err := handleServiceHelp(ctx, []string{"server"}); err != nil {
		t.Fatalf("handleServiceHelp(server) failed: %v", err)
	}

	help := strings.Join(lines, "\n")
	for _, want := range []string{
		"server status",
		"server notice",
		"server debug",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("admin help is missing command %q:\n%v", want, help)
		}
	}
}

func TestServiceHelpRejectsAdminCommandForNonAdmin(t *testing.T) {
	ctx := &serviceContext{
		user:  &user{},
		print: func(string) {},
	}

	if err := handleServiceHelp(ctx, []string{"server"}); err == nil {
		t.Fatalf("non-admin help for admin command succeeded")
	}
}
