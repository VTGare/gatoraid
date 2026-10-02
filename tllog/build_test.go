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
		text, ok := tllog.Build(meta, []store.Line{
			at(time.Hour+2*time.Minute+3*time.Second, "tl", store.LineTL, "[EN] later"),
			at(-5*time.Minute, "calli", store.LineOwner, "soon"),
			at(12*time.Second+900*time.Millisecond, "mod", store.LineMod, "no spam"),
			at(time.Minute, "kiara", store.LineVTuber, "hi"),
		}, nil)

		Expect(ok).To(BeTrue())
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
		text, ok := tllog.Build(tllog.Meta{VideoID: "vid"}, []store.Line{
			at(time.Minute, "b", store.LineTL, "two"),
			at(0, "a", store.LineTL, "one"),
		}, nil)

		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("https://youtu.be/vid\nTimes count from the first line.\n\n[0:00:00] @a: one\n[0:01:00] @b: two\n"))
	})

	It("applies the blacklist and filters, except to the streamer", func() {
		mod := &relay.Moderation{Blacklist: map[string]bool{"UCspam": true, "UCcalli": true}, Banned: []string{"spoiler"}}

		text, ok := tllog.Build(meta, []store.Line{
			at(0, "spam", store.LineTL, "[EN] buy"),
			at(time.Second, "tl", store.LineTL, "[EN] SPOILER ahead"),
			at(2*time.Second, "calli", store.LineOwner, "spoiler: I win"),
			at(3*time.Second, "tl", store.LineTL, "[EN] fine"),
		}, mod)

		Expect(ok).To(BeTrue())
		Expect(text).To(HaveSuffix("\n[0:00:02] @calli: spoiler: I win\n[0:00:03] @tl: [EN] fine\n"))
		Expect(text).NotTo(ContainSubstring("buy"))
	})

	It("drops cameos, gossip and lines relayed twice", func() {
		twice := at(time.Second, "tl", store.LineTL, "[EN] once")
		again := twice
		again.ChannelID = "other"

		text, ok := tllog.Build(meta, []store.Line{
			twice, again,
			at(2*time.Second, "kiara", store.LineCameo, "cameo"),
			at(3*time.Second, "kiara", store.LineGossip, "gossip"),
		}, nil)

		Expect(ok).To(BeTrue())
		Expect(text).To(HaveSuffix("\n\n[0:00:01] @tl: [EN] once\n"))
	})

	It("reports logs with nothing left", func() {
		_, ok := tllog.Build(meta, []store.Line{at(0, "kiara", store.LineCameo, "cameo")}, nil)
		Expect(ok).To(BeFalse())

		_, ok = tllog.Build(meta, nil, nil)
		Expect(ok).To(BeFalse())
	})
})
