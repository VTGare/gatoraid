package irc

import (
	"strconv"
	"strings"
	"time"
)

type line struct {
	tags    map[string]string
	prefix  string
	command string
	params  []string
}

func (l line) param(i int) string {
	if i < len(l.params) {
		return l.params[i]
	}
	return ""
}

// parseLine reads "@tags :prefix COMMAND params :trailing", where everything
// but the command is optional.
func parseLine(s string) (line, bool) {
	var l line

	if rest, ok := strings.CutPrefix(s, "@"); ok {
		var tags string
		tags, s, _ = strings.Cut(rest, " ")
		l.tags = parseTags(tags)
	}

	if rest, ok := strings.CutPrefix(s, ":"); ok {
		l.prefix, s, _ = strings.Cut(rest, " ")
	}

	s, trailing, hasTrailing := strings.Cut(s, " :")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return line{}, false
	}
	l.command, l.params = fields[0], fields[1:]
	if hasTrailing {
		l.params = append(l.params, trailing)
	}

	return l, true
}

var tagEscapes = strings.NewReplacer(`\:`, ";", `\s`, " ", `\\`, `\`, `\r`, "\r", `\n`, "\n")

func parseTags(s string) map[string]string {
	tags := map[string]string{}
	for kv := range strings.SplitSeq(s, ";") {
		k, v, _ := strings.Cut(kv, "=")
		tags[k] = tagEscapes.Replace(v)
	}
	return tags
}

func parseMessage(l line) (*Message, bool) {
	channel, ok := strings.CutPrefix(l.param(0), "#")
	if !ok || len(l.params) < 2 {
		return nil, false
	}

	text := l.params[1]
	// "/me waves" arrives as a CTCP ACTION.
	if action, ok := strings.CutPrefix(text, "\x01ACTION "); ok {
		text = strings.TrimSuffix(action, "\x01")
	}

	username, _, _ := strings.Cut(l.prefix, "!")
	badges := badgeSet(l.tags["badges"])

	m := &Message{
		ID:          l.tags["id"],
		Channel:     channel,
		UserID:      l.tags["user-id"],
		Username:    username,
		DisplayName: l.tags["display-name"],
		Text:        text,
		Broadcaster: badges["broadcaster"] || (l.tags["user-id"] != "" && l.tags["user-id"] == l.tags["room-id"]),
		Moderator:   badges["moderator"] || l.tags["mod"] == "1",
		Subscriber:  badges["subscriber"] || badges["founder"] || l.tags["subscriber"] == "1",
		Partner:     badges["partner"],
		Emotes:      emoteNames(text, l.tags["emotes"]),
	}
	if m.DisplayName == "" {
		m.DisplayName = username
	}
	if ms, err := strconv.ParseInt(l.tags["tmi-sent-ts"], 10, 64); err == nil {
		m.Time = time.UnixMilli(ms)
	} else {
		m.Time = time.Now()
	}
	m.Bits, _ = strconv.Atoi(l.tags["bits"])

	return m, true
}

// Badges look like "broadcaster/1,subscriber/12".
func badgeSet(s string) map[string]bool {
	set := map[string]bool{}
	for b := range strings.SplitSeq(s, ",") {
		if name, _, _ := strings.Cut(b, "/"); name != "" {
			set[name] = true
		}
	}
	return set
}

// Emotes look like "25:0-4,12-16/1902:6-10": emote IDs and where they are
// in the text, counted in runes. A range that doesn't fit the text is
// skipped.
func emoteNames(text, tag string) []string {
	if tag == "" {
		return nil
	}

	runes := []rune(text)
	seen := map[string]bool{}
	var names []string
	for emote := range strings.SplitSeq(tag, "/") {
		_, ranges, _ := strings.Cut(emote, ":")
		// Every range of one emote has the same text, so the first will do.
		first, _, _ := strings.Cut(ranges, ",")
		from, to, _ := strings.Cut(first, "-")
		start, err1 := strconv.Atoi(from)
		end, err2 := strconv.Atoi(to)
		if err1 != nil || err2 != nil || start < 0 || end < start || end >= len(runes) {
			continue
		}

		name := string(runes[start : end+1])
		if !seen[name] && !strings.ContainsAny(name, " \t") {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}
