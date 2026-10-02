package tllog_test

import (
	"time"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/tllog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func at(d time.Duration, author string, kind store.LineKind, body string) store.Line {
	return store.Line{
		VideoID: "vid", GuildID: "g", ChannelID: "c", AuthorChannelID: "UC" + author, AuthorName: "@" + author,
		Body: body, Kind: kind, SaidAt: start.Add(d),
	}
}

var _ = Describe("Build", func() {
	meta := tllog.Meta{VideoID: "vid", Title: "【MINECRAFT】dig", Start: start}

	It("writes relayed lines with times since the start", func() {
		text, n := tllog.Build(meta, []store.Line{
			at(time.Hour+2*time.Minute+3*time.Second, "tl", store.LineTL, "[EN] later"),
			at(-5*time.Minute, "calli", store.LineOwner, "soon"),
			at(12*time.Second+900*time.Millisecond, "mod", store.LineMod, "no spam"),
			at(time.Minute, "kiara", store.LineVTuber, "hi"),
		}, nil)

		Expect(n).To(Equal(4))
		Expect(text).To(Equal(`【MINECRAFT】dig
https://youtu.be/vid
Started 2026-10-02 12:00 UTC

[-0:05:00] @calli: soon
[0:00:12] @mod: no spam
[0:01:00] @kiara: hi
[1:02:03] @tl: [EN] later
`))
	})

	It("counts from the first line without a start", func() {
		text, n := tllog.Build(tllog.Meta{VideoID: "vid"}, []store.Line{
			at(time.Minute, "b", store.LineTL, "two"),
			at(0, "a", store.LineTL, "one"),
		}, nil)

		Expect(n).To(Equal(2))
		Expect(text).To(Equal("https://youtu.be/vid\nTimes count from the first line.\n\n[0:00:00] @a: one\n[0:01:00] @b: two\n"))
	})

	It("applies the blacklist and filters, except to the streamer", func() {
		mod := &relay.Moderation{Blacklist: map[string]bool{"UCspam": true, "UCcalli": true}, Banned: []string{"spoiler"}}

		text, n := tllog.Build(meta, []store.Line{
			at(0, "spam", store.LineTL, "[EN] buy"),
			at(time.Second, "tl", store.LineTL, "[EN] SPOILER ahead"),
			at(2*time.Second, "calli", store.LineOwner, "spoiler: I win"),
			at(3*time.Second, "tl", store.LineTL, "[EN] fine"),
		}, mod)

		Expect(n).To(Equal(2))
		Expect(text).To(HaveSuffix("\n[0:00:02] @calli: spoiler: I win\n[0:00:03] @tl: [EN] fine\n"))
		Expect(text).NotTo(ContainSubstring("buy"))
	})

	It("drops cameos, gossip and lines relayed twice", func() {
		twice := at(time.Second, "tl", store.LineTL, "[EN] once")
		again := twice
		again.ChannelID = "other"

		text, n := tllog.Build(meta, []store.Line{
			twice, again,
			at(2*time.Second, "kiara", store.LineCameo, "cameo"),
			at(3*time.Second, "kiara", store.LineGossip, "gossip"),
		}, nil)

		Expect(n).To(Equal(1))
		Expect(text).To(HaveSuffix("\n\n[0:00:01] @tl: [EN] once\n"))
	})

	It("reports logs with nothing left", func() {
		_, n := tllog.Build(meta, []store.Line{at(0, "kiara", store.LineCameo, "cameo")}, nil)
		Expect(n).To(BeZero())

		_, n = tllog.Build(meta, nil, nil)
		Expect(n).To(BeZero())
	})
})

var _ = Describe("Message", func() {
	It("summarises the log above the file", func() {
		msg := tllog.Message(tllog.Meta{
			VideoID: "vid", Title: "Karaoke", Author: "Mori Calliope", AuthorIcon: "calli.png",
			Duration: 2*time.Hour + 14*time.Minute + 30*time.Second,
		}, "log text", 312, 7)

		Expect(msg.Content).To(BeEmpty())
		e := msg.Embeds[0]
		Expect(e.Title).To(Equal("Karaoke"))
		Expect(e.URL).To(Equal("https://youtu.be/vid"))
		Expect(e.Author.Name).To(Equal("Mori Calliope"))
		Expect(e.Author.IconURL).To(Equal("calli.png"))
		Expect(e.Description).To(Equal("Stream log · 2 h 14 min · 312 lines"))
		Expect(e.Color).To(Equal(7))
		Expect(msg.Files[0].Name).To(Equal("vid.txt"))
	})

	It("leaves out what it doesn't know", func() {
		e := tllog.Message(tllog.Meta{VideoID: "vid", Duration: 45 * time.Minute}, "x", 1, 0).Embeds[0]
		Expect(e.Title).To(Equal("vid"))
		Expect(e.Author).To(BeNil())
		Expect(e.Description).To(Equal("Stream log · 45 min · 1 line"))

		e = tllog.Message(tllog.Meta{VideoID: "vid"}, "x", 3, 0).Embeds[0]
		Expect(e.Description).To(Equal("Stream log · 3 lines"))
	})
})
