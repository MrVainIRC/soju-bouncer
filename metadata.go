package soju

import (
	"gopkg.in/irc.v4"

	"codeberg.org/emersion/soju/xirc"
)

type metadataVersion uint

const (
	metadataVersionNone metadataVersion = iota
	metadataVersion2
	metadataVersion3
)

var metadataCapNames = []string{
	"draft/metadata-3",
	"draft/metadata-2",
}

var registeredMetadataKeys = map[string]struct{}{
	"avatar":       {},
	"bot":          {},
	"color":        {},
	"display-name": {},
	"homepage":     {},
	"pronouns":     {},
	"status":       {},
}

func isRegisteredMetadataKey(key string) bool {
	_, ok := registeredMetadataKeys[key]
	return ok
}

func isMetadataCap(name string) bool {
	for _, capName := range metadataCapNames {
		if name == capName {
			return true
		}
	}
	return false
}

func metadataVersionForCap(name string) metadataVersion {
	switch name {
	case "draft/metadata-2":
		return metadataVersion2
	case "draft/metadata-3":
		return metadataVersion3
	default:
		return metadataVersionNone
	}
}

func metadataVersionFromCaps(caps *xirc.CapRegistry) metadataVersion {
	if caps.IsEnabled("draft/metadata-3") {
		return metadataVersion3
	}
	if caps.IsEnabled("draft/metadata-2") {
		return metadataVersion2
	}
	return metadataVersionNone
}

func (dc *downstreamConn) metadataVersion() metadataVersion {
	return dc.metadataDialect
}

func (uc *upstreamConn) metadataVersion() metadataVersion {
	return metadataVersionFromCaps(&uc.caps)
}

func (dc *downstreamConn) hasMetadataCap() bool {
	return dc.metadataVersion() != metadataVersionNone
}

func (uc *upstreamConn) hasMetadataCap() bool {
	return uc.metadataVersion() != metadataVersionNone
}

func metadataInvalidKeyCode(version metadataVersion) string {
	if version == metadataVersion3 {
		return "INVALID_KEY"
	}
	return "KEY_INVALID"
}

func metadataInvalidValueCode(version metadataVersion) string {
	if version == metadataVersion3 {
		return "INVALID_VALUE"
	}
	return "VALUE_INVALID"
}

func metadataNotification(version metadataVersion, recipient, target, key string, value *string) *irc.Message {
	if version == metadataVersion3 {
		if value == nil {
			return &irc.Message{
				Command: xirc.RPL_KEYNOTSET,
				Params:  []string{recipient, target, key, "Key not set"},
			}
		}
		return &irc.Message{
			Command: xirc.RPL_KEYVALUE,
			Params:  []string{recipient, target, key, "*", *value},
		}
	}

	params := []string{target, key, "*"}
	if value != nil {
		params = append(params, *value)
	}
	return &irc.Message{Command: "METADATA", Params: params}
}

func convertMetadataMessage(msg *irc.Message, source, destination metadataVersion, recipient string, notification bool) *irc.Message {
	if source == destination || source == metadataVersionNone || destination == metadataVersionNone {
		return msg
	}

	switch {
	case source == metadataVersion2 && destination == metadataVersion3 && msg.Command == "METADATA":
		if len(msg.Params) < 3 {
			return msg
		}
		var value *string
		if len(msg.Params) > 3 {
			value = &msg.Params[3]
		}
		converted := metadataNotification(destination, recipient, msg.Params[0], msg.Params[1], value)
		converted.Prefix = msg.Prefix
		converted.Tags = msg.Tags
		return converted
	case source == metadataVersion3 && destination == metadataVersion2 && notification && msg.Command == xirc.RPL_KEYVALUE:
		if len(msg.Params) < 5 {
			return msg
		}
		converted := metadataNotification(destination, recipient, msg.Params[1], msg.Params[2], &msg.Params[4])
		converted.Prefix = msg.Prefix
		converted.Tags = msg.Tags
		return converted
	case source == metadataVersion3 && destination == metadataVersion2 && notification && msg.Command == xirc.RPL_KEYNOTSET:
		if len(msg.Params) < 3 {
			return msg
		}
		converted := metadataNotification(destination, recipient, msg.Params[1], msg.Params[2], nil)
		converted.Prefix = msg.Prefix
		converted.Tags = msg.Tags
		return converted
	}

	if msg.Command == xirc.RPL_KEYNOTSET && len(msg.Params) >= 4 {
		converted := msg.Copy()
		if destination == metadataVersion3 {
			converted.Params[3] = "Key not set"
		} else {
			converted.Params[3] = "*"
		}
		return converted
	}

	if (msg.Command == "FAIL" || msg.Command == "WARN" || msg.Command == "NOTE") &&
		len(msg.Params) >= 2 && msg.Params[0] == "METADATA" {
		converted := msg.Copy()
		switch {
		case destination == metadataVersion3 && converted.Params[1] == "KEY_INVALID":
			converted.Params[1] = "INVALID_KEY"
		case destination == metadataVersion3 && converted.Params[1] == "VALUE_INVALID":
			converted.Params[1] = "INVALID_VALUE"
		case destination == metadataVersion2 && converted.Params[1] == "INVALID_KEY":
			converted.Params[1] = "KEY_INVALID"
		case destination == metadataVersion2 && converted.Params[1] == "INVALID_VALUE":
			converted.Params[1] = "VALUE_INVALID"
		}
		return converted
	}

	return msg
}
