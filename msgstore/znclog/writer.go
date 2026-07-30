package znclog

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/xirc"
)

const rawMessagePrefix = "soju-raw: "

func MarshalLine(msg *irc.Message, t time.Time) string {
	s := formatMessage(msg)
	if s == "" {
		return ""
	}
	return fmt.Sprintf("[%02d:%02d:%02d.%03d] %s", t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/int(time.Millisecond), s)
}

func formatRawMessage(msg *irc.Message) string {
	raw := strings.TrimRight(msg.String(), "\r\n")
	return rawMessagePrefix + base64.RawStdEncoding.EncodeToString([]byte(raw))
}

func hasClientOnlyTags(msg *irc.Message) bool {
	for name := range msg.Tags {
		if strings.HasPrefix(name, "+") {
			return true
		}
	}
	return false
}

// formatMessage formats a message log line. It assumes a well-formed IRC
// message.
func formatMessage(msg *irc.Message) string {
	// Batch membership is structural IRC state and cannot be represented by
	// the traditional ZNC log format. Preserve the complete message instead.
	if _, ok := msg.Tags["batch"]; ok {
		return formatRawMessage(msg)
	}
	// Message IDs are protocol identity and must survive history replay.
	if _, ok := msg.Tags["msgid"]; ok {
		return formatRawMessage(msg)
	}

	switch strings.ToUpper(msg.Command) {
	case "TAGMSG", "METADATA", "BATCH":
		return formatRawMessage(msg)
	case "NICK":
		return fmt.Sprintf("*** %s is now known as %s", msg.Prefix.Name, msg.Params[0])
	case "JOIN":
		return fmt.Sprintf("*** Joins: %s (%s@%s)", msg.Prefix.Name, msg.Prefix.User, msg.Prefix.Host)
	case "PART":
		var reason string
		if len(msg.Params) > 1 {
			reason = msg.Params[1]
		}
		return fmt.Sprintf("*** Parts: %s (%s@%s) (%s)", msg.Prefix.Name, msg.Prefix.User, msg.Prefix.Host, reason)
	case "KICK":
		nick := msg.Params[1]
		var reason string
		if len(msg.Params) > 2 {
			reason = msg.Params[2]
		}
		return fmt.Sprintf("*** %s was kicked by %s (%s)", nick, msg.Prefix.Name, reason)
	case "QUIT":
		var reason string
		if len(msg.Params) > 0 {
			reason = msg.Params[0]
		}
		return fmt.Sprintf("*** Quits: %s (%s@%s) (%s)", msg.Prefix.Name, msg.Prefix.User, msg.Prefix.Host, reason)
	case "TOPIC":
		var topic string
		if len(msg.Params) > 1 {
			topic = msg.Params[1]
		}
		return fmt.Sprintf("*** %s changes topic to '%s'", msg.Prefix.Name, topic)
	case "MODE":
		return fmt.Sprintf("*** %s sets mode: %s", msg.Prefix.Name, strings.Join(msg.Params[1:], " "))
	case "NOTICE":
		if hasClientOnlyTags(msg) {
			return formatRawMessage(msg)
		}
		return fmt.Sprintf("-%s- %s", msg.Prefix.Name, msg.Params[1])
	case "PRIVMSG":
		if hasClientOnlyTags(msg) {
			return formatRawMessage(msg)
		}
		if cmd, params, ok := xirc.ParseCTCPMessage(msg); ok && cmd == "ACTION" {
			return fmt.Sprintf("* %s %s", msg.Prefix.Name, params)
		} else {
			return fmt.Sprintf("<%s> %s", msg.Prefix.Name, msg.Params[1])
		}
	default:
		return ""
	}
}
