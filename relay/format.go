package relay

import (
	"strings"
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

const maxLinkTitle = 50

type Formatter struct {
	// Returns fallback when the key isn't configured.
	Emoji func(key, fallback string) string
	// A group and its parents, nearest first.
	Lineage func(groupID string) []*store.Group
	// For notice embeds.
	Color int
}

// Relay formats a relayed line. translation adds a DeepL line, and showChat
// a link to the chat for lines that could have come from more than one.
func (f *Formatter) Relay(c *Comment, kind Kind, showChat bool, translation string) string {
	var sb strings.Builder

	if c.Stream.Status == stream.Upcoming {
		sb.WriteString(f.Emoji(EmojiPrechat, fallbackPrechat) + " ")
	}

	name := EscapeMarkdown(c.AuthorName)
	switch {
	case kind == KindTL:
		sb.WriteString(iconTL + " ||" + name + ":||")
	case c.fromVTuber() || kind == KindOwner:
		sb.WriteString(f.agencyEmoji(c.Author, EmojiVTuber, fallbackVTuber) + " **" + name + ":**")
	default:
		sb.WriteString(iconOther + " **" + name + ":**")
	}

	sb.WriteString(" " + code(c.Text))

	if translation != "" {
		sb.WriteString("\n" + f.Emoji(EmojiDeepL, fallbackDeepL) + " **DeepL:** " + code(translation))
	}

	if showChat {
		label := hostName(c)

		// A streamer's waiting rooms only differ by title.
		if c.Stream.Status == stream.Upcoming && c.Stream.Title != "" {
			label += " · " + Truncate(c.Stream.Title, maxLinkTitle)
		}

		sb.WriteString("\n**Chat:** [")
		sb.WriteString(EscapeMarkdown(label))
		sb.WriteString("](<")
		sb.WriteString(c.Stream.URL())
		sb.WriteString(">)")
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
		EscapeMarkdown(hostName(c)) + "**'s chat](<" + c.Stream.URL() + ">): " + code(c.Text)
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
