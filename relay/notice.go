package relay

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Notice builds the message that tells a channel a relay started.
func (f *Formatter) Notice(kind store.NoticeKind, s *stream.Stream, host *store.Streamer, roleID string) discord.MessageCreate {
	e := StreamEmbed(s, host, f.Color)

	switch kind {
	case store.NoticePrechat:
		e.Description = "Relaying the waiting room chat here."
		if !s.ScheduledAt.IsZero() {
			e.Description += fmt.Sprintf(" The stream starts <t:%d:R>.", s.ScheduledAt.Unix())
		}
	default:
		e.Description = "Relaying the live chat here."
	}

	return RoleMessage(e, roleID)
}

// StreamEmbed links the stream with its thumbnail. host, if known, puts
// the registry's name and avatar on it.
func StreamEmbed(s *stream.Stream, host *store.Streamer, color int) discord.Embed {
	e := discord.Embed{
		Title: s.Title,
		URL:   s.URL(),
		Color: color,
		Image: &discord.EmbedResource{URL: "https://i.ytimg.com/vi/" + s.VideoID + "/hqdefault.jpg"},
		Author: &discord.EmbedAuthor{
			Name: s.ChannelName,
			URL:  "https://www.youtube.com/channel/" + s.ChannelID,
		},
	}

	if host != nil {
		e.Author.Name = host.Name
		e.Author.IconURL = host.AvatarURL
	}

	return e
}

// RoleMessage pings roleID if set, and nobody else. The role has to be in
// the message text to notify anyone.
func RoleMessage(e discord.Embed, roleID string) discord.MessageCreate {
	msg := discord.MessageCreate{
		Embeds:          []discord.Embed{e},
		AllowedMentions: &discord.AllowedMentions{},
	}
	if id, err := snowflake.Parse(roleID); err == nil && id != 0 {
		msg.Content = discord.RoleMention(id)
		msg.AllowedMentions.Roles = []snowflake.ID{id}
	}

	return msg
}
