package znclog

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/database"
	"codeberg.org/emersion/soju/xirc"
)

func UnmarshalLine(line string, user *database.User, network *database.Network, entity string, ref time.Time, events bool) (*irc.Message, time.Time, error) {
	end := strings.Index(line, "] ")
	if end < 0 {
		return nil, time.Time{}, fmt.Errorf("malformed timestamp prefix")
	}
	var hour, minute, second, millisecond int
	n, err := fmt.Sscanf(line[:end+1], "[%02d:%02d:%02d.%03d]", &hour, &minute, &second, &millisecond)
	if err != nil {
		n, err = fmt.Sscanf(line[:end+1], "[%02d:%02d:%02d]", &hour, &minute, &second)
		if err != nil || n != 3 {
			return nil, time.Time{}, fmt.Errorf("malformed timestamp prefix: %v", err)
		}
	} else if n != 4 {
		return nil, time.Time{}, fmt.Errorf("malformed timestamp prefix")
	}
	line = line[end+2:]

	year, month, day := ref.Date()
	t := time.Date(year, month, day, hour, minute, second, millisecond*int(time.Millisecond), time.Local)

	if raw, ok := strings.CutPrefix(line, rawMessagePrefix); ok {
		b, err := base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("malformed raw message: %v", err)
		}
		msg, err := irc.ParseMessage(string(b))
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("malformed raw IRC message: %v", err)
		}
		if msg.Tags == nil {
			msg.Tags = make(map[string]string)
		}
		if tag, ok := msg.Tags["time"]; ok {
			parsed, err := time.Parse(xirc.ServerTimeLayout, tag)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("malformed raw message time tag: %v", err)
			}
			t = parsed.In(time.Local)
		} else {
			msg.Tags["time"] = xirc.FormatServerTime(t)
		}
		return msg, t, nil
	}

	var cmd string
	var prefix *irc.Prefix
	var params []string
	if events && strings.HasPrefix(line, "*** ") {
		parts := strings.SplitN(line[4:], " ", 2)
		if len(parts) != 2 {
			return nil, time.Time{}, nil
		}
		switch parts[0] {
		case "Joins:", "Parts:", "Quits:":
			args := strings.SplitN(parts[1], " ", 3)
			if len(args) < 2 {
				return nil, time.Time{}, nil
			}
			nick := args[0]
			mask := strings.TrimSuffix(strings.TrimPrefix(args[1], "("), ")")
			maskParts := strings.SplitN(mask, "@", 2)
			if len(maskParts) != 2 {
				return nil, time.Time{}, nil
			}
			prefix = &irc.Prefix{
				Name: nick,
				User: maskParts[0],
				Host: maskParts[1],
			}
			var reason string
			if len(args) > 2 {
				reason = strings.TrimSuffix(strings.TrimPrefix(args[2], "("), ")")
			}
			switch parts[0] {
			case "Joins:":
				cmd = "JOIN"
				params = []string{entity}
			case "Parts:":
				cmd = "PART"
				if reason != "" {
					params = []string{entity, reason}
				} else {
					params = []string{entity}
				}
			case "Quits:":
				cmd = "QUIT"
				if reason != "" {
					params = []string{reason}
				}
			}
		default:
			nick := parts[0]
			rem := parts[1]
			if r := strings.TrimPrefix(rem, "is now known as "); r != rem {
				cmd = "NICK"
				prefix = &irc.Prefix{
					Name: nick,
				}
				params = []string{r}
			} else if r := strings.TrimPrefix(rem, "was kicked by "); r != rem {
				args := strings.SplitN(r, " ", 2)
				if len(args) != 2 {
					return nil, time.Time{}, nil
				}
				cmd = "KICK"
				prefix = &irc.Prefix{
					Name: args[0],
				}
				reason := strings.TrimSuffix(strings.TrimPrefix(args[1], "("), ")")
				params = []string{entity, nick}
				if reason != "" {
					params = append(params, reason)
				}
			} else if r := strings.TrimPrefix(rem, "changes topic to "); r != rem {
				cmd = "TOPIC"
				prefix = &irc.Prefix{
					Name: nick,
				}
				topic := strings.TrimSuffix(strings.TrimPrefix(r, "'"), "'")
				params = []string{entity, topic}
			} else if r := strings.TrimPrefix(rem, "sets mode: "); r != rem {
				cmd = "MODE"
				prefix = &irc.Prefix{
					Name: nick,
				}
				params = append([]string{entity}, strings.Split(r, " ")...)
			} else {
				return nil, time.Time{}, nil
			}
		}
	} else {
		var sender, text string
		if strings.HasPrefix(line, "<") {
			cmd = "PRIVMSG"
			parts := strings.SplitN(line[1:], "> ", 2)
			if len(parts) != 2 {
				return nil, time.Time{}, nil
			}
			sender, text = parts[0], parts[1]
		} else if strings.HasPrefix(line, "-") {
			cmd = "NOTICE"
			parts := strings.SplitN(line[1:], "- ", 2)
			if len(parts) != 2 {
				return nil, time.Time{}, nil
			}
			sender, text = parts[0], parts[1]
		} else if strings.HasPrefix(line, "* ") {
			cmd = "PRIVMSG"
			parts := strings.SplitN(line[2:], " ", 2)
			if len(parts) != 2 {
				return nil, time.Time{}, nil
			}
			sender, text = parts[0], "\x01ACTION "+parts[1]+"\x01"
		} else {
			return nil, time.Time{}, nil
		}

		prefix = &irc.Prefix{Name: sender}
		if entity == sender {
			// This is a direct message from a user to us. We don't store own
			// our nickname in the logs, so grab it from the network settings.
			// Not very accurate since this may not match our nick at the time
			// the message was received, but we can't do a lot better.
			entity = database.GetNick(user, network)
		}
		params = []string{entity, text}
	}

	msg := &irc.Message{
		Tags: map[string]string{
			"time": xirc.FormatServerTime(t),
		},
		Prefix:  prefix,
		Command: cmd,
		Params:  params,
	}
	return msg, t, nil
}
