package relay

import (
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Notice builds the message that tells a channel a relay started.
func (f *Formatter) Notice(kind store.NoticeKind, s *stream.Stream, host *store.Streamer, roleID string) *discordgo.MessageSend {
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
func StreamEmbed(s *stream.Stream, host *store.Streamer, color int) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title: s.Title,
		URL:   s.URL(),
		Color: color,
		Image: &discordgo.MessageEmbedImage{URL: "https://i.ytimg.com/vi/" + s.VideoID + "/hqdefault.jpg"},
		Author: &discordgo.MessageEmbedAuthor{
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
func RoleMessage(e *discordgo.MessageEmbed, roleID string) *discordgo.MessageSend {
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
