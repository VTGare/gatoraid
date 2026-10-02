package relay_test

import (
	"strings"
	"time"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Formatter", func() {
	groups := map[string][]*store.Group{
		"holo-en": {{ID: "holo-en"}, {ID: "holo"}},
	}
	emojis := map[string]string{"holo": "<:holo:1>", "prechat": "<:pre:2>"}

	f := &relay.Formatter{
		Emoji: func(key, fallback string) string {
			if e, ok := emojis[key]; ok {
				return e
			}
			return fallback
		},
		Lineage: func(id string) []*store.Group { return groups[id] },
	}

	DescribeTable("relay lines",
		func(c *relay.Comment, kind relay.Kind, showChat bool, want string) {
			Expect(f.Relay(c, kind, showChat, "")).To(Equal(want))
		},
		Entry("a translation, spoilered", comment("[EN] hi"), relay.KindTL, false,
			"💬 ||@viewer:|| `[EN] hi`"),
		Entry("the streamer, with the parent group's emoji", fromCalli(comment("hi")), relay.KindOwner, false,
			"<:holo:1> **@MoriCalliope:** `hi`"),
		Entry("a VTuber outside the registry", func() *relay.Comment { c := comment("hi"); c.VTuber = true; return c }(),
			relay.KindVTuber, false, "🎙️ **@viewer:** `hi`"),
		Entry("a moderator", comment("no spam"), relay.KindMod, false, "🛠️ **@viewer:** `no spam`"),
		Entry("backticks and markdown in names", func() *relay.Comment {
			c := comment("use `code`")
			c.AuthorName = "@under_score*"
			return c
		}(), relay.KindMod, false, `🛠️ **@under\_score\*:** `+"`use 'code'`"),
		Entry("the chat link", comment("[EN] hi"), relay.KindTL, true,
			"💬 ||@viewer:|| `[EN] hi`\n**Chat:** [Mori Calliope](<https://youtu.be/vid>)"),
		Entry("prechat", func() *relay.Comment {
			c := comment("[EN] soon")
			c.Stream.Status = stream.Upcoming
			return c
		}(), relay.KindTL, false, "<:pre:2> 💬 ||@viewer:|| `[EN] soon`"),
		Entry("a channel the registry doesn't know", func() *relay.Comment {
			c := comment("[EN] hi")
			c.Host = nil
			return c
		}(), relay.KindTL, true, "💬 ||@viewer:|| `[EN] hi`\n**Chat:** [Mori Calliope Ch.](<https://youtu.be/vid>)"),
	)

	It("formats cameos and gossip", func() {
		Expect(f.Cameo(fromKiara(comment("hi calli")))).
			To(Equal("<:holo:1> **Takanashi Kiara** in [**Mori Calliope**'s chat](<https://youtu.be/vid>): `hi calli`"))
		Expect(f.Gossip(comment("[EN] kiara is here"))).
			To(Equal("👀 **@viewer** in [**Mori Calliope**'s chat](<https://youtu.be/vid>): `[EN] kiara is here`"))
	})

	It("keeps long lines under Discord's limit", func() {
		line := f.Relay(comment(strings.Repeat("あ", 2000)), relay.KindTL, true, "")
		Expect(len(line)).To(BeNumerically("<", 2000))
		Expect(line).To(ContainSubstring("あ…`\n**Chat:**"))
	})
})

var _ = Describe("Notice", func() {
	f := &relay.Formatter{Color: 1}

	It("pings only the role for a live relay", func() {
		host := *calli
		host.AvatarURL = "calli.png"
		s := calliStream()
		s.Title = "【MINECRAFT】dig"

		msg := f.Notice(store.NoticeRelay, s, &host, "123")

		Expect(msg.Content).To(Equal("<@&123>"))
		Expect(msg.AllowedMentions.Roles).To(Equal([]string{"123"}))
		Expect(msg.AllowedMentions.Parse).To(BeEmpty())
		e := msg.Embeds[0]
		Expect(e.Title).To(Equal("【MINECRAFT】dig"))
		Expect(e.URL).To(Equal("https://youtu.be/vid"))
		Expect(e.Author.Name).To(Equal("Mori Calliope"))
		Expect(e.Author.IconURL).To(Equal("calli.png"))
		Expect(e.Description).To(Equal("Relaying the live chat here."))
		Expect(e.Image.URL).To(Equal("https://i.ytimg.com/vi/vid/hqdefault.jpg"))
		Expect(e.Thumbnail).To(BeNil())
	})

	It("says when a prechat's stream starts and pings nobody without a role", func() {
		s := calliStream()
		s.Status = stream.Upcoming
		s.ScheduledAt = time.Unix(1790000000, 0)

		msg := f.Notice(store.NoticePrechat, s, nil, "")

		Expect(msg.Content).To(BeEmpty())
		Expect(msg.AllowedMentions.Roles).To(BeEmpty())
		Expect(msg.Embeds[0].Author.Name).To(Equal("Mori Calliope Ch."))
		Expect(msg.Embeds[0].Description).To(Equal("Relaying the waiting room chat here. The stream starts <t:1790000000:R>."))
	})
})
