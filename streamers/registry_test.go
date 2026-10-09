package streamers_test

import (
	"context"
	"path/filepath"
	"testing/fstest"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func names(sts []*store.Streamer) []string {
	out := make([]string, len(sts))
	for i, s := range sts {
		out[i] = s.Name
	}
	return out
}

var _ = Describe("Seed", func() {
	It("loads the embedded seed", func() {
		seed, err := streamers.LoadSeed()

		Expect(err).NotTo(HaveOccurred())
		Expect(seed.Groups).To(HaveLen(15))
		Expect(seed.Streamers).To(HaveLen(690))
	})

	It("orders groups as a tree, subgroups by order then name", func() {
		seed, err := streamers.ParseSeed(fstest.MapFS{
			"z.toml":       {Data: []byte("[group]\nid = \"z\"\nname = \"Zeta\"")},
			"a/a.toml":     {Data: []byte("[group]\nid = \"a\"\nname = \"Alpha\"")},
			"a/late.toml":  {Data: []byte("[group]\nid = \"a-late\"\nname = \"Aa\"\nparent = \"a\"\norder = 2")},
			"a/early.toml": {Data: []byte("[group]\nid = \"a-early\"\nname = \"Zz\"\nparent = \"a\"\norder = 1")},
		})
		Expect(err).NotTo(HaveOccurred())

		var ids []string
		for _, g := range seed.Groups {
			ids = append(ids, g.ID)
		}
		Expect(ids).To(Equal([]string{"a", "a-early", "a-late", "z"}))
	})

	It("puts streamers in their file's group", func() {
		seed, err := streamers.ParseSeed(fstest.MapFS{"g.toml": {Data: []byte(`
[group]
id = "g"
name = "G"

[[streamer]]
name = "A"
channel_id = "UCaaaaaaaaaaaaaaaaaaaaaa"
twitch = "a_live"
aliases = ["x"]
free_chat_streams = true
`)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(seed.Streamers).To(Equal([]store.Streamer{{
			ChannelID: "UCaaaaaaaaaaaaaaaaaaaaaa", Name: "A", GroupID: "g", Twitch: "a_live", Aliases: []string{"x"},
			FreeChatStreams: true, Source: store.SourceSeed,
		}}))
	})

	DescribeTable("rejects broken seeds",
		func(files map[string]string, msg string) {
			fsys := fstest.MapFS{}
			for name, data := range files {
				fsys[name] = &fstest.MapFile{Data: []byte(data)}
			}

			_, err := streamers.ParseSeed(fsys)
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("typo in a field", map[string]string{
			"g.toml": "[group]\nid = \"g\"\nname = \"G\"\n[[streamer]]\nname = \"A\"\nchanel_id = \"x\"",
		}, `g.toml: unknown field "streamer.chanel_id"`),
		Entry("missing group", map[string]string{"g.toml": "[[streamer]]\nname = \"A\""}, "g.toml: missing [group]"),
		Entry("bad channel id", map[string]string{
			"g.toml": "[group]\nid = \"g\"\nname = \"G\"\n[[streamer]]\nname = \"A\"\nchannel_id = \"@a\"",
		}, `g.toml: A: "@a" isn't a channel ID`),
		Entry("duplicate channel id", map[string]string{
			"a.toml": "[group]\nid = \"a\"\nname = \"A\"\n[[streamer]]\nname = \"One\"\nchannel_id = \"UCaaaaaaaaaaaaaaaaaaaaaa\"",
			"b.toml": "[group]\nid = \"b\"\nname = \"B\"\n[[streamer]]\nname = \"Two\"\nchannel_id = \"UCaaaaaaaaaaaaaaaaaaaaaa\"",
		}, "channel UCaaaaaaaaaaaaaaaaaaaaaa is already used by One"),
		Entry("uppercase Twitch username", map[string]string{
			"g.toml": "[group]\nid = \"g\"\nname = \"G\"\n[[streamer]]\nname = \"A\"\nchannel_id = \"UCaaaaaaaaaaaaaaaaaaaaaa\"\ntwitch = \"Alice\"",
		}, `g.toml: A: "Alice" isn't a lowercase Twitch username`),
		Entry("duplicate Twitch username", map[string]string{
			"a.toml": "[group]\nid = \"a\"\nname = \"A\"\n[[streamer]]\nname = \"One\"\nchannel_id = \"UCaaaaaaaaaaaaaaaaaaaaaa\"\ntwitch = \"same\"",
			"b.toml": "[group]\nid = \"b\"\nname = \"B\"\n[[streamer]]\nname = \"Two\"\nchannel_id = \"UCbbbbbbbbbbbbbbbbbbbbbb\"\ntwitch = \"same\"",
		}, "Twitch username same is already used by One"),
		Entry("duplicate group", map[string]string{
			"a.toml": "[group]\nid = \"g\"\nname = \"A\"",
			"b.toml": "[group]\nid = \"g\"\nname = \"B\"",
		}, `group "g" is already defined in a.toml`),
		Entry("unknown parent", map[string]string{"a.toml": "[group]\nid = \"a\"\nname = \"A\"\nparent = \"b\""}, `a.toml: unknown parent "b"`),
		Entry("parent cycle", map[string]string{
			"a.toml": "[group]\nid = \"a\"\nname = \"A\"\nparent = \"b\"",
			"b.toml": "[group]\nid = \"b\"\nname = \"B\"\nparent = \"a\"",
		}, "is its own ancestor"),
		Entry("syntax error", map[string]string{"g.toml": "[group\nid = "}, "g.toml:"),
	)
})

var _ = Describe("Registry", func() {
	var (
		ctx context.Context
		reg *streamers.Registry
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())

		reg = streamers.New(db)
		res, err := reg.Sync(ctx, seed)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Added).To(Equal(690))
	})

	resolve := func(q string) string {
		st, err := reg.Resolve(q)
		Expect(err).NotTo(HaveOccurred())
		return st.Name
	}

	It("resolves by channel ID, name, alias and name word", func() {
		Expect(resolve("UCL_qhgtOy0dy1Agp8vkySQg")).To(Equal("Mori Calliope"))
		Expect(resolve("mori calliope")).To(Equal("Mori Calliope"))
		Expect(resolve("calli")).To(Equal("Mori Calliope"))
		Expect(resolve("キアラ")).To(Equal("Takanashi Kiara"))
		Expect(resolve("kanaeru")).To(Equal("Kobo Kanaeru"))
	})

	It("finds streamers by Twitch username", func() {
		st, ok := reg.TwitchStreamer("MoriCalliope")
		Expect(ok).To(BeTrue())
		Expect(st.Name).To(Equal("Mori Calliope"))

		_, ok = reg.TwitchStreamer("nobody_here")
		Expect(ok).To(BeFalse())

		channels := reg.TwitchChannels()
		Expect(channels).To(HaveKeyWithValue("moricalliope", "UCL_qhgtOy0dy1Agp8vkySQg"))
		Expect(len(channels)).To(BeNumerically(">", 150))
	})

	It("drops hidden streamers from the Twitch usernames", func() {
		Expect(reg.Remove(ctx, "UCL_qhgtOy0dy1Agp8vkySQg")).To(Succeed())

		_, ok := reg.TwitchStreamer("moricalliope")
		Expect(ok).To(BeFalse())
		Expect(reg.TwitchChannels()).NotTo(HaveKey("moricalliope"))
	})

	It("prefers an alias over name words", func() {
		// The sub channel also has "Kiara" in its name, but only the main
		// channel has the alias.
		Expect(resolve("kiara")).To(Equal("Takanashi Kiara"))
	})

	It("reports ambiguous and unknown queries", func() {
		_, err := reg.Resolve("takanashi")
		var amb *streamers.AmbiguousError
		Expect(err).To(BeAssignableToTypeOf(amb))
		Expect(err.(*streamers.AmbiguousError).Candidates).To(HaveLen(2))

		_, err = reg.Resolve("definitely nobody")
		Expect(err).To(MatchError(streamers.ErrNotFound))
		_, err = reg.Resolve("  ")
		Expect(err).To(MatchError(streamers.ErrNotFound))
	})

	It("ranks search results for autocomplete", func() {
		Expect(names(reg.Search("calli", 3))[0]).To(Equal("Mori Calliope"))
		Expect(names(reg.Search("takanashi", 5))).To(Equal([]string{"Takanashi Kiara", "Takanashi Kiara SubCh"}))
		Expect(names(reg.Search("UCL_qhgtOy0dy1Agp8vkySQg", 5))).To(Equal([]string{"Mori Calliope"}))
		Expect(reg.Search("", 25)).To(HaveLen(25))
		Expect(reg.Search("zzzzzz", 25)).To(BeEmpty())
	})

	It("includes subgroups in group members", func() {
		en := names(reg.Members("hololive-en"))
		all := names(reg.Members("hololive"))

		Expect(en).To(ContainElements("Mori Calliope", "Hololive English"))
		Expect(en).NotTo(ContainElement("Kobo Kanaeru"))
		Expect(all).To(ContainElements("Mori Calliope", "Kobo Kanaeru", "Hololive VTuber Group"))
		Expect(len(all)).To(BeNumerically(">", len(en)))
	})

	It("walks group lineage for auto-translate and subgroups", func() {
		kobo, err := reg.Resolve("kobo")
		Expect(err).NotTo(HaveOccurred())
		calli, err := reg.Resolve("calli")
		Expect(err).NotTo(HaveOccurred())

		Expect(reg.SkipAutoTranslate(kobo)).To(BeTrue())
		Expect(reg.SkipAutoTranslate(calli)).To(BeFalse())

		var lineage []string
		for _, g := range reg.Lineage("hololive-id") {
			lineage = append(lineage, g.ID)
		}
		Expect(lineage).To(Equal([]string{"hololive-id", "hololive"}))

		var subs []string
		for _, g := range reg.Subgroups("nijisanji") {
			subs = append(subs, g.ID)
		}
		Expect(subs).To(Equal([]string{"nijisanji-jp", "nijisanji-en", "nijisanji-id", "nijisanji-kr"}))
	})

	It("searches groups by prefix before substring", func() {
		ids := func(q string) []string {
			var out []string
			for _, g := range reg.SearchGroups(q, 25) {
				out = append(out, g.ID)
			}
			return out
		}

		Expect(ids("en")).To(Equal([]string{"hololive-en", "nijisanji-en"}))
		h := ids("h")
		Expect(h[:5]).To(Equal([]string{"hololive", "hololive-jp", "hololive-en", "hololive-id", "holostars"}))
		Expect(h[5:]).To(ContainElements("chromashift", "phase-connect"))
	})

	It("exports entries that parse back into the same streamers", func() {
		calli, err := reg.Resolve("calli")
		Expect(err).NotTo(HaveOccurred())

		out, err := reg.Export([]*store.Streamer{calli})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("# streamers/seed/hololive/en.toml\n\n[[streamer]]\nname = \"Mori Calliope\"\n"))

		seed, err := streamers.ParseSeed(fstest.MapFS{"en.toml": {
			Data: []byte("[group]\nid = \"hololive-en\"\nname = \"Hololive EN\"\n\n" + out),
		}})
		Expect(err).NotTo(HaveOccurred())

		want := *calli
		want.AvatarURL, want.UpdatedAt = "", seed.Streamers[0].UpdatedAt
		Expect(seed.Streamers).To(Equal([]store.Streamer{want}))
	})

	It("labels exported streamers without a known group", func() {
		out, err := reg.Export([]*store.Streamer{
			{ChannelID: "UCaaaaaaaaaaaaaaaaaaaaaa", Name: "Loner"},
			{ChannelID: "UCbbbbbbbbbbbbbbbbbbbbbb", Name: "Lost", GroupID: "gone"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("# No group: add them to the file of the group they belong to."))
		Expect(out).To(ContainSubstring(`# Group "gone" isn't in the seed.`))
	})

	It("hands owner entries back to the seed once they're pasted in", func() {
		db, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "cycle.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		header := "[group]\nid = \"g\"\nname = \"G\"\n\n"
		empty, err := streamers.ParseSeed(fstest.MapFS{"g.toml": {Data: []byte(header)}})
		Expect(err).NotTo(HaveOccurred())

		cycle := streamers.New(db)
		_, err = cycle.Sync(ctx, empty)
		Expect(err).NotTo(HaveOccurred())

		Expect(cycle.Save(ctx, store.Streamer{
			ChannelID: "UCaaaaaaaaaaaaaaaaaaaaaa", Name: "New", GroupID: "g", Aliases: []string{"nu"}, Source: store.SourceOwner,
		})).To(Succeed())
		added, _ := cycle.Streamer("UCaaaaaaaaaaaaaaaaaaaaaa")

		out, err := cycle.Export([]*store.Streamer{added})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("# streamers/seed/g.toml"))

		pasted, err := streamers.ParseSeed(fstest.MapFS{"g.toml": {Data: []byte(header + out)}})
		Expect(err).NotTo(HaveOccurred())

		res, err := cycle.Sync(ctx, pasted)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Returned: 1}))

		back, _ := cycle.Streamer("UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(back.Source).To(Equal(store.SourceSeed))
	})

	It("reloads after saving and deleting", func() {
		calli, err := reg.Resolve("calli")
		Expect(err).NotTo(HaveOccurred())

		edited := *calli
		edited.Aliases = append(edited.Aliases, "gremlin")
		edited.Source = store.SourceOwner
		Expect(reg.Save(ctx, edited)).To(Succeed())
		Expect(resolve("gremlin")).To(Equal("Mori Calliope"))

		Expect(reg.Remove(ctx, calli.ChannelID)).To(Succeed())
		_, ok := reg.Streamer(calli.ChannelID)
		Expect(ok).To(BeFalse())
		hidden, ok := reg.Lookup(calli.ChannelID)
		Expect(ok).To(BeTrue())
		Expect(hidden.Removed()).To(BeTrue())
		Expect(names(reg.Search("calli", 5))).NotTo(ContainElement("Mori Calliope"))
	})
})
