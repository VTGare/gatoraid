// Package relay decides which chat lines go where and formats them.
package relay

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Kind is why a line was relayed.
type Kind = store.LineKind

const (
	KindOwner  = store.LineOwner
	KindTL     = store.LineTL
	KindVTuber = store.LineVTuber
	KindMod    = store.LineMod
	KindCameo  = store.LineCameo
	KindGossip = store.LineGossip
)

var tlPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\S+ tl[:)\]】］]`),
	regexp.MustCompile(`(?i)([(\[/［【]|^)(tl|eng?)[\]):】］]`),
	regexp.MustCompile(`(?i)^[\[(](eng?|tl)`),
}

// IsTL reports whether a line is tagged as an English translation, like
// "[EN] hello" or "kiara tl: hello".
func IsTL(text string) bool {
	return slices.ContainsFunc(tlPatterns, func(re *regexp.Regexp) bool { return re.MatchString(text) })
}

// Comment is a chat line with what the registry knows about it.
type Comment struct {
	chat.Comment
	Stream *stream.Stream
	// The streamer whose chat this is. Nil if the registry doesn't know the
	// channel.
	Host *store.Streamer
	// The author's registry entry, if they're in it and not hidden.
	Author *store.Streamer

	// Every guild's checks need these, so they're worked out once.
	lower  string
	tagged *bool
}

func (c *Comment) lowerText() string {
	if c.lower == "" {
		c.lower = strings.ToLower(c.Text)
	}
	return c.lower
}

// Not safe for concurrent use, like the rest of Comment's caches.
func (c *Comment) taggedTL() bool {
	if c.tagged == nil {
		tl := c.TL || IsTL(c.Text)
		c.tagged = &tl
	}
	return *c.tagged
}

func (c *Comment) fromOwner() bool {
	return c.Owner || c.AuthorChannelID == c.Stream.ChannelID
}

// User-added channels don't count, so random channels can't turn into
// VTubers. TLdex has its own list.
func (c *Comment) fromVTuber() bool {
	return (c.Author != nil && c.Author.Curated()) || c.VTuber
}

// When the streamer hearts a super chat, YouTube posts a line in their
// name saying so.
func (c *Comment) heartLine() bool {
	text := c.lowerText()
	return strings.Contains(text, "hearted") && strings.Contains(text, "super chat")
}

// Moderation is a guild's blacklist and filters.
type Moderation struct {
	// YouTube channel IDs.
	Blacklist map[string]bool
	// Lowercase. Lines containing one are dropped.
	Banned []string
	// Lowercase. Lines starting with one count as translations.
	Wanted []string
}

// Blocks reports whether the blacklist or a banned word drops a line.
func (m *Moderation) Blocks(authorChannelID, text string) bool {
	return m.blocksLower(authorChannelID, strings.ToLower(text))
}

func (m *Moderation) blocks(c *Comment) bool {
	return m.blocksLower(c.AuthorChannelID, c.lowerText())
}

func (m *Moderation) blocksLower(authorChannelID, text string) bool {
	if m == nil {
		return false
	}
	if m.Blacklist[authorChannelID] {
		return true
	}
	return slices.ContainsFunc(m.Banned, func(b string) bool { return strings.Contains(text, b) })
}

func (m *Moderation) wants(c *Comment) bool {
	if m == nil {
		return false
	}

	text := strings.TrimSpace(c.lowerText())
	return slices.ContainsFunc(m.Wanted, func(w string) bool { return strings.HasPrefix(text, w) })
}

func (m *Moderation) isTL(c *Comment) bool {
	return c.taggedTL() || m.wants(c)
}

// Relay decides whether a guild relaying the chat's streamer gets the
// line. The streamer's own lines skip the blacklist and filters.
func Relay(c *Comment, settings *store.Settings, m *Moderation) (Kind, bool) {
	switch {
	case strings.TrimSpace(c.Text) == "":
		return "", false
	case c.Stream.Status == stream.Upcoming && !settings.Prechat:
		return "", false
	case c.fromOwner():
		return KindOwner, !c.heartLine()
	case m.blocks(c):
		return "", false
	case m.isTL(c):
		return KindTL, true
	case c.fromVTuber():
		return KindVTuber, true
	case c.Moderator && settings.ModMessages:
		return KindMod, true
	}

	return "", false
}

// Cameo decides whether a guild following the author gets the line: what
// a VTuber says in someone else's chat.
func Cameo(c *Comment, m *Moderation) bool {
	return c.Author != nil && c.Author.Curated() && !c.fromOwner() &&
		strings.TrimSpace(c.Text) != "" && !m.blocks(c)
}

// Gossip decides whether a guild following subject gets the line: a VTuber
// or translator mentioning them in another chat. Collabs they're in don't
// count, since that's not gossip.
func Gossip(c *Comment, subject *store.Streamer, m *Moderation) bool {
	switch {
	case c.AuthorChannelID == subject.ChannelID,
		c.Stream.ChannelID == subject.ChannelID,
		slices.Contains(c.Stream.Mentions, subject.ChannelID),
		!c.fromVTuber() && !m.isTL(c),
		m.blocks(c):
		return false
	}

	return mentions(c.lowerText(), subject)
}

// Mentions reports whether text names the streamer by full name or alias.
// Names match as whole words, ignoring case. Japanese ones match anywhere,
// since Japanese doesn't put spaces between words.
func Mentions(text string, st *store.Streamer) bool {
	return mentions(strings.ToLower(text), st)
}

func mentions(text string, st *store.Streamer) bool {
	names := append([]string{st.Name}, st.Aliases...)

	return slices.ContainsFunc(names, func(name string) bool {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return false
		}
		if japanese(name) {
			return strings.Contains(text, name)
		}
		return containsWord(text, name)
	})
}

func containsWord(text, word string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}

		start, end := i+j, i+j+len(word)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if !wordRune(before) && !wordRune(after) {
			return true
		}

		_, size := utf8.DecodeRuneInString(text[start:])
		i = start + size
	}
}

// RuneError is what decoding returns at either end of the text.
func wordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func japanese(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han)
	})
}

// Links, @handles and YouTube's :emoji: shortcodes aren't worth translating.
var notWords = regexp.MustCompile(`https?://\S+|@\S+|:[^:\s]+:`)

func hasWords(text string) bool {
	return strings.ContainsFunc(notWords.ReplaceAllString(text, ""), unicode.IsLetter)
}
