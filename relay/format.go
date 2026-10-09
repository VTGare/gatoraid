package relay

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Emoji keys in the config, with the Unicode fallbacks used when one isn't
// set. Group IDs are keys too, for agency emojis.
const (
	EmojiPrechat = "prechat"
	EmojiVTuber  = "vtuber"
	EmojiPeek    = "peek"
	EmojiDeepL   = "deepl"

	fallbackPrechat = "⏳"
	fallbackVTuber  = "🎙️"
	fallbackPeek    = "👀"
	fallbackDeepL   = "🌐"
	iconTL          = "💬"
	iconOther       = "🛠️"
)

// YouTube caps chat lines at 200 characters but TLdex doesn't, and the
// message has to fit Discord's 2000 with names and links around it.
const maxText = 1500

type Formatter struct {
	// Returns fallback when the key isn't configured.
	Emoji func(key, fallback string) string
	// A group and its parents, nearest first.
	Lineage func(groupID string) []*store.Group
}

// Relay formats a relayed line. translation adds a DeepL line, and showChat
// a footer with the chat's link and title for lines that could have come from
// more than one.
func (f *Formatter) Relay(c *Comment, kind Kind, showChat bool, translation string) string {
	var sb strings.Builder
	write := func(parts ...string) {
		for _, p := range parts {
			sb.WriteString(p)
		}
	}

	if c.Stream.Status == stream.Upcoming {
		write(f.Emoji(EmojiPrechat, fallbackPrechat), " ")
	}

	name := EscapeMarkdown(c.AuthorName)
	switch {
	case kind == KindTL:
		write(iconTL, " ||", name, ":||")
	case c.fromVTuber() || kind == KindOwner:
		write(f.agencyEmoji(c.Author, EmojiVTuber, fallbackVTuber), " **", name, ":**")
	default:
		write(iconOther, " **", name, ":**")
	}

	write(" ", code(c.Text))

	if translation != "" {
		write("\n", f.Emoji(EmojiDeepL, fallbackDeepL), " **DeepL:** ", code(translation))
	}

	if showChat {
		write("\n-# [", linkLabel(hostName(c)), "](<", c.Stream.URL(), ">)")
		if c.Stream.Title != "" {
			write(" · ", EscapeMarkdown(c.Stream.Title))
		}
	}

	return sb.String()
}

// Cameo formats a VTuber's line from someone else's chat.
func (f *Formatter) Cameo(c *Comment) string {
	return f.elsewhere(c, c.Author.Name)
}

// Gossip formats a line that mentions a streamer in another chat.
func (f *Formatter) Gossip(c *Comment) string {
	name := c.AuthorName
	if c.Author != nil {
		name = c.Author.Name
	}
	return f.elsewhere(c, name)
}

func (f *Formatter) elsewhere(c *Comment, author string) string {
	return f.agencyEmoji(c.Author, EmojiPeek, fallbackPeek) + " **" + EscapeMarkdown(author) + "** in [**" +
		linkLabel(hostName(c)) + "**'s chat](<" + c.Stream.URL() + ">): " + code(c.Text)
}

// The nearest group with an emoji wins, so Hololive EN falls back to
// Hololive's.
func (f *Formatter) agencyEmoji(st *store.Streamer, key, fallback string) string {
	if st != nil && f.Lineage != nil {
		for _, g := range f.Lineage(st.GroupID) {
			if e := f.Emoji(g.ID, ""); e != "" {
				return e
			}
		}
	}
	return f.Emoji(key, fallback)
}

func hostName(c *Comment) string {
	if c.Host != nil {
		return c.Host.Name
	}
	return c.Stream.ChannelName
}

// Discord shows a masked link as plain text when its label has emojis.
func linkLabel(label string) string {
	label = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.So, unicode.Sk, unicode.Cf, unicode.Variation_Selector) {
			return -1
		}
		return r
	}, label)), " ")

	if label == "" {
		label = "chat"
	}

	return EscapeMarkdown(label)
}

// Truncate cuts s to limit runes, the last one an ellipsis.
func Truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

// Inline code can't hold backticks.
func code(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "`", "'")
	if len(text) > maxText {
		cut := maxText
		for !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return "`" + text + "`"
}

var markdown = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, `~`, `\~`, `|`, `\|`, "`", "\\`", `>`, `\>`, `[`, `\[`, `]`, `\]`,
)

func EscapeMarkdown(s string) string { return markdown.Replace(s) }
