package relay_test

import (
	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	calli = &store.Streamer{ChannelID: "UCcalli", Name: "Mori Calliope", GroupID: "holo-en",
		Aliases: []string{"calli", "calliope", "森カリオペ"}, Source: store.SourceSeed}
	kiara = &store.Streamer{ChannelID: "UCkiara", Name: "Takanashi Kiara", GroupID: "holo-en",
		Aliases: []string{"kiara", "tenchou"}, Source: store.SourceOwner}
	user = &store.Streamer{ChannelID: "UCuser", Name: "Some Channel", Source: store.SourceUser}
)

func calliStream() *stream.Stream {
	return &stream.Stream{VideoID: "vid", ChannelID: "UCcalli", ChannelName: "Mori Calliope Ch.", Status: stream.Live}
}

// A plain viewer line in Calli's live chat.
func comment(text string) *relay.Comment {
	return &relay.Comment{
		Comment: chat.Comment{AuthorChannelID: "UCviewer", AuthorName: "@viewer", Text: text},
		Stream:  calliStream(),
		Host:    calli,
	}
}

func fromCalli(c *relay.Comment) *relay.Comment {
	c.AuthorChannelID, c.AuthorName, c.Owner, c.Author = calli.ChannelID, "@MoriCalliope", true, calli
	return c
}

func fromKiara(c *relay.Comment) *relay.Comment {
	c.AuthorChannelID, c.AuthorName, c.Author = kiara.ChannelID, "@TakanashiKiara", kiara
	return c
}

var _ = Describe("IsTL", func() {
	DescribeTable("TL patterns",
		func(text string, want bool) { Expect(relay.IsTL(text)).To(Equal(want)) },
		Entry("[EN]", "[EN] hello", true),
		Entry("[en] lowercase", "[en] hello", true),
		Entry("(EN)", "(EN) hello", true),
		Entry("[ENG]", "[ENG] hello", true),
		Entry("TL:", "TL: hello", true),
		Entry("EN:", "EN: hello", true),
		Entry("name tl:", "kiara tl: hello", true),
		Entry("fullwidth brackets", "【EN】 hello", true),
		Entry("bracket mid-line", "hello [EN] world", true),
		Entry("slash", "hello /en] world", true),
		Entry("no tag", "hello everyone", false),
		Entry("english as a word", "english please", false),
		Entry("tl inside a word", "bottle: nice", false),
		Entry("en without a closer", "en route", false),
	)
})

var _ = Describe("Relay", func() {
	settings := store.DefaultSettings()

	DescribeTable("who gets relayed",
		func(c *relay.Comment, m *relay.Moderation, want relay.Kind) {
			kind, ok := relay.Relay(c, &settings, m)
			Expect(ok).To(Equal(want != ""))
			if ok {
				Expect(kind).To(Equal(want))
			}
		},
		Entry("a viewer", comment("lol"), nil, relay.Kind("")),
		Entry("the streamer", fromCalli(comment("hi")), nil, relay.KindOwner),
		Entry("the streamer by channel without the badge", func() *relay.Comment {
			c := comment("hi")
			c.AuthorChannelID = "UCcalli"
			return c
		}(), nil, relay.KindOwner),
		Entry("the streamer hearting a super chat", fromCalli(comment("Mori Calliope hearted a Super Chat")), nil, relay.Kind("")),
		Entry("the streamer, blacklisted anyway", fromCalli(comment("hi")),
			&relay.Moderation{Blacklist: map[string]bool{"UCcalli": true}}, relay.KindOwner),
		Entry("the streamer, filtered anyway", fromCalli(comment("bad word")),
			&relay.Moderation{Banned: []string{"bad"}}, relay.KindOwner),
		Entry("a translation", comment("[EN] hello"), nil, relay.KindTL),
		Entry("a TLdex translation", func() *relay.Comment { c := comment("hello"); c.TL = true; return c }(), nil, relay.KindTL),
		Entry("a wanted prefix", comment("ES: hola"), &relay.Moderation{Wanted: []string{"es:"}}, relay.KindTL),
		Entry("a blacklisted translator", comment("[EN] hello"),
			&relay.Moderation{Blacklist: map[string]bool{"UCviewer": true}}, relay.Kind("")),
		Entry("a filtered translation", comment("[EN] Bad words"), &relay.Moderation{Banned: []string{"bad"}}, relay.Kind("")),
		Entry("another VTuber", fromKiara(comment("hi calli")), nil, relay.KindVTuber),
		Entry("a TLdex VTuber", func() *relay.Comment { c := comment("hi"); c.VTuber = true; return c }(), nil, relay.KindVTuber),
		Entry("a user-added channel", func() *relay.Comment {
			c := comment("hi")
			c.AuthorChannelID, c.Author = user.ChannelID, user
			return c
		}(), nil, relay.Kind("")),
		Entry("a moderator", func() *relay.Comment { c := comment("no spam"); c.Moderator = true; return c }(), nil, relay.KindMod),
		Entry("an empty line", fromCalli(comment("  ")), nil, relay.Kind("")),
	)

	It("leaves out mods when the guild doesn't want them", func() {
		c := comment("no spam")
		c.Moderator = true
		off := settings
		off.ModMessages = false

		_, ok := relay.Relay(c, &off, nil)
		Expect(ok).To(BeFalse())
	})

	It("relays prechat only when the guild wants it", func() {
		c := fromCalli(comment("soon"))
		c.Stream.Status = stream.Upcoming

		_, ok := relay.Relay(c, &settings, nil)
		Expect(ok).To(BeTrue())

		off := settings
		off.Prechat = false
		_, ok = relay.Relay(c, &off, nil)
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Cameo", func() {
	It("takes VTubers in other chats", func() {
		Expect(relay.Cameo(fromKiara(comment("hi calli")), nil)).To(BeTrue())
	})

	It("skips the streamer's own chat, viewers, user channels, TLdex-only VTubers and blocked lines", func() {
		Expect(relay.Cameo(fromCalli(comment("hi")), nil)).To(BeFalse())
		Expect(relay.Cameo(comment("hi"), nil)).To(BeFalse())

		c := comment("hi")
		c.AuthorChannelID, c.Author = user.ChannelID, user
		Expect(relay.Cameo(c, nil)).To(BeFalse())

		c = comment("hi")
		c.VTuber = true
		Expect(relay.Cameo(c, nil)).To(BeFalse())

		Expect(relay.Cameo(fromKiara(comment("hi")), &relay.Moderation{Blacklist: map[string]bool{"UCkiara": true}})).To(BeFalse())
		Expect(relay.Cameo(fromKiara(comment("Bad")), &relay.Moderation{Banned: []string{"bad"}})).To(BeFalse())
	})
})

var _ = Describe("Gossip", func() {
	// In Calli's chat, about Kiara.
	DescribeTable("lines about Kiara",
		func(c *relay.Comment, m *relay.Moderation, want bool) {
			Expect(relay.Gossip(c, kiara, m)).To(Equal(want))
		},
		Entry("a VTuber naming her", fromCalli(comment("Kiara is late")), nil, true),
		Entry("a translation naming her", comment("[EN] where is tenchou?"), nil, true),
		Entry("a viewer naming her", comment("kiara!"), nil, false),
		Entry("a VTuber not naming her", fromCalli(comment("hello")), nil, false),
		Entry("only part of a word", fromCalli(comment("kiaras")), nil, false),
		Entry("her full name", fromCalli(comment("TAKANASHI KIARA!")), nil, true),
		Entry("blacklisted", comment("[EN] kiara"), &relay.Moderation{Blacklist: map[string]bool{"UCviewer": true}}, false),
	)

	It("skips her own lines, her own chat and her collabs", func() {
		Expect(relay.Gossip(fromKiara(comment("kiara here")), kiara, nil)).To(BeFalse())

		c := fromCalli(comment("kiara"))
		c.Stream.Mentions = []string{"UCkiara"}
		Expect(relay.Gossip(c, kiara, nil)).To(BeFalse())

		c = comment("[EN] kiara")
		c.Stream.ChannelID = "UCkiara"
		Expect(relay.Gossip(c, kiara, nil)).To(BeFalse())
	})
})

var _ = Describe("Mentions", func() {
	DescribeTable("name matching",
		func(text string, want bool) { Expect(relay.Mentions(text, calli)).To(Equal(want)) },
		Entry("alias", "hi calli", true),
		Entry("case", "CALLI!", true),
		Entry("punctuation around it", "(calli)", true),
		Entry("inside a word", "callisto", false),
		Entry("prefix of another word", "calliopes", false),
		Entry("later occurrence", "callisto and calli", true),
		Entry("full name", "mori calliope", true),
		Entry("Japanese inside a sentence", "森カリオペさん来た", true),
		Entry("unrelated", "mori", false),
	)
})
