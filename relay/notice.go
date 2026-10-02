package relay

import (
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Notice builds the message that tells a channel a relay started. It pings
// roleID if set, and nobody else.
func (f *Formatter) Notice(kind store.NoticeKind, s *stream.Stream, host *store.Streamer, roleID string) *discordgo.MessageSend {
	e := &discordgo.MessageEmbed{
		Title:     s.Title,
		URL:       s.URL(),
		Color:     f.Color,
		Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://i.ytimg.com/vi/" + s.VideoID + "/mqdefault.jpg"},
		Author: &discordgo.MessageEmbedAuthor{
			Name: s.ChannelName,
			URL:  "https://www.youtube.com/channel/" + s.ChannelID,
		},
	}

	if host != nil {
		e.Author.Name = host.Name
		e.Author.IconURL = host.AvatarURL
	}

	switch kind {
	case store.NoticePrechat:
		e.Description = "Relaying the waiting room chat here."
		if !s.ScheduledAt.IsZero() {
			e.Description += fmt.Sprintf(" The stream starts <t:%d:R>.", s.ScheduledAt.Unix())
		}
	default:
		e.Description = "Relaying the live chat here."
	}

	msg := &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{e},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
	if roleID != "" {
		msg.Content = "<@&" + roleID + ">"
		msg.AllowedMentions.Roles = []string{roleID}
	}

	return msg
}
