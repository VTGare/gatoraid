package relay_test

import (
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LiveMessage", func() {
	It("names the streamer and links the YouTube stream for Discord's player", func() {
		host := &store.Streamer{ChannelID: "UCcalli", Name: "Mori_Calliope", AvatarURL: "calli.png"}

		msg := relay.LiveMessage(stream.Stream{
			VideoID: "vid", ChannelID: "UCcalli", ChannelName: "Calli Ch.", Title: "Karaoke", StartedAt: time.Now(),
		}, host, "42")

		Expect(msg.Content).To(Equal("<@&42> **Mori\\_Calliope** is [live on YouTube](https://youtu.be/vid)!"))
		Expect(msg.Embeds).To(BeEmpty())
	})

	It("puts Twitch streams in our embed with the preview and category", func() {
		host := &store.Streamer{ChannelID: "UCcalli", Name: "Mori Calliope", AvatarURL: "calli.png"}

		msg := relay.LiveMessage(stream.Stream{
			VideoID: "twitch:1", Platform: stream.Twitch, ChannelID: "UCcalli", TwitchUsername: "moricalliope",
			Title: "Karaoke", Game: "Music", StartedAt: time.Now(),
			Thumbnail: "https://static-cdn.jtvnw.net/previews-ttv/live_user_moricalliope-1280x720.jpg?s=1",
		}, host, "")

		Expect(msg.Content).To(Equal("**Mori Calliope** is [live on Twitch](<https://www.twitch.tv/moricalliope>)!"))
		e := msg.Embeds[0]
		Expect(e.Title).To(Equal("Karaoke"))
		Expect(e.URL).To(Equal("https://www.twitch.tv/moricalliope"))
		Expect(e.Author.Name).To(Equal("Mori Calliope"))
		Expect(e.Author.IconURL).To(Equal("calli.png"))
		Expect(e.Author.URL).To(Equal("https://www.twitch.tv/moricalliope"))
		Expect(e.Image.URL).To(Equal("https://static-cdn.jtvnw.net/previews-ttv/live_user_moricalliope-1280x720.jpg?s=1"))
		Expect(e.Fields).To(Equal([]discord.EmbedField{{Name: "Category", Value: "Music"}}))
		Expect(e.Color).To(Equal(relay.TwitchColor))
		Expect(e.Timestamp).To(BeNil())
	})
})
