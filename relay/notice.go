package relay

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Twitch's brand purple.
const TwitchColor = 0x9146ff

// Notice builds the message that tells a channel a relay started.
func (f *Formatter) Notice(kind store.NoticeKind, s *stream.Stream, host *store.Streamer, roleID string) discord.MessageCreate {
	var text string
	switch kind {
	case store.NoticePrechat:
		when, date := "soon", ""
		if !s.ScheduledAt.IsZero() {
			when = fmt.Sprintf("<t:%d:R>", s.ScheduledAt.Unix())
			date = fmt.Sprintf("\n-# <t:%d:F>", s.ScheduledAt.Unix())
		}
		text = StreamerName(s, host) + " goes live " + when + "! Relaying " + StreamLink(s, "pre-stream chat") + " here." + date
	default:
		text = StreamerName(s, host) + " is " + StreamLink(s, "live on "+s.PlatformName()) + "! Relaying chat here."
	}

	return StreamMessage(s, host, roleID, text)
}

// StreamerName is the registry's name if host is known, in bold.
func StreamerName(s *stream.Stream, host *store.Streamer) string {
	name := s.ChannelName
	if host != nil {
		name = host.Name
	}
	return "**" + EscapeMarkdown(name) + "**"
}

// StreamLink masks the stream's URL with text.
func StreamLink(s *stream.Stream, text string) string {
	if s.Twitch() {
		return "[" + text + "](<" + s.URL() + ">)"
	}

	return "[" + text + "](" + s.URL() + ")"
}

// StreamMessage pings roleID before text, which should have a StreamLink.
func StreamMessage(s *stream.Stream, host *store.Streamer, roleID, text string) discord.MessageCreate {
	msg := roleMessage(roleID)
	if msg.Content != "" {
		text = msg.Content + " " + text
	}

	msg.Content = text

	if s.Twitch() {
		msg.Embeds = []discord.Embed{TwitchEmbed(s, host)}
	}

	return msg
}

// TwitchEmbed links the stream with its preview. host, if known, puts the
// registry's name and avatar on it.
func TwitchEmbed(s *stream.Stream, host *store.Streamer) discord.Embed {
	e := discord.Embed{
		Title: s.Title,
		URL:   s.URL(),
		Color: TwitchColor,
		Image: &discord.EmbedResource{URL: s.ThumbnailURL()},
		Author: &discord.EmbedAuthor{
			Name: s.ChannelName,
			URL:  s.ChannelURL(),
		},
	}

	if host != nil {
		e.Author.Name = host.Name
		e.Author.IconURL = host.AvatarURL
	}

	if s.Game != "" {
		e.Fields = []discord.EmbedField{{Name: "Category", Value: s.Game}}
	}

	return e
}

// RoleMessage pings roleID if set, and nobody else.
func RoleMessage(e discord.Embed, roleID string) discord.MessageCreate {
	msg := roleMessage(roleID)
	msg.Embeds = []discord.Embed{e}
	return msg
}

// The role has to be in the message text to notify anyone.
func roleMessage(roleID string) discord.MessageCreate {
	msg := discord.MessageCreate{AllowedMentions: &discord.AllowedMentions{}}
	if id, err := snowflake.Parse(roleID); err == nil && id != 0 {
		msg.Content = discord.RoleMention(id)
		msg.AllowedMentions.Roles = []snowflake.ID{id}
	}
	return msg
}
