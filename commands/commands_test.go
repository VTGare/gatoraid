package commands_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	dt "github.com/VTGare/gumi/v2/gumitest"
	disgo "github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/commands"
	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	owner snowflake.ID = 1
	user  snowflake.ID = 2
)

// Test interactions come from these, in the store's string form.
var (
	testGuild   = dt.GuildID.String()
	testChannel = dt.ChannelID.String()
)

type harness struct {
	b   *bot.Bot
	rec *dt.Recorder
}

func newHarness(seed *streamers.Seed) *harness {
	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(db.Close)

	cfg := &config.Config{
		Discord: config.Discord{Token: dt.Token, OwnerIDs: []string{owner.String()}, OwnerGuildID: testGuild},
		Limits:  config.Limits{UserChannels: config.DefaultUserChannels},
	}
	b, err := bot.New(cfg, slog.New(slog.DiscardHandler), db)
	Expect(err).NotTo(HaveOccurred())

	_, err = b.Streamers.Sync(ctx, seed)
	Expect(err).NotTo(HaveOccurred())
	Expect(commands.Register(b)).To(Succeed())

	return &harness{b: b, rec: dt.Attach(b.Client)}
}

func (h *harness) run(i *dt.Interaction) map[string]any {
	before := len(h.rec.Responses())
	h.b.Router.HandleInteraction(i.Event(h.b.Client))

	responses := h.rec.Responses()
	if len(responses) == before {
		return nil
	}
	return responses[len(responses)-1]
}

func embed(data map[string]any) map[string]any {
	embeds, _ := data["embeds"].([]any)
	Expect(embeds).NotTo(BeEmpty())
	return embeds[0].(map[string]any)
}

// replyText is what a reply says: its content, or the text of a success
// embed without the check mark.
func replyText(data map[string]any) string {
	if s, _ := data["content"].(string); s != "" {
		return s
	}
	if embeds, _ := data["embeds"].([]any); len(embeds) > 0 {
		d, _ := embeds[0].(map[string]any)["description"].(string)
		return strings.TrimPrefix(d, "✅ ")
	}
	return ""
}

func ephemeral(data map[string]any) bool {
	flags, _ := data["flags"].(float64)
	return disgo.MessageFlags(flags).Has(disgo.MessageFlagEphemeral)
}

func choices(data map[string]any) []string {
	var out []string
	list, _ := data["choices"].([]any)
	for _, c := range list {
		out = append(out, c.(map[string]any)["name"].(string))
	}
	return out
}

var _ = Describe("Register", func() {
	register := func(discord config.Discord) ([]string, map[string][]string) {
		GinkgoHelper()
		discord.Token = dt.Token
		b, err := bot.New(&config.Config{Discord: discord}, slog.New(slog.DiscardHandler), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(commands.Register(b)).To(Succeed())

		global, byGuild := b.Router.ApplicationCommands()
		names := func(cmds []disgo.ApplicationCommandCreate) []string {
			out := make([]string, 0, len(cmds))
			for _, c := range cmds {
				out = append(out, c.CommandName())
			}
			return out
		}
		guilds := map[string][]string{}
		for id, cmds := range byGuild {
			guilds[id.String()] = names(cmds)
		}
		return names(global), guilds
	}

	everyone := []string{"help", "relay", "cameos", "gossip", "notify", "log", "settings", "blacklist",
		"Blacklist author", "filter", "streamers"}

	It("registers everything but /owner globally", func() {
		global, guilds := register(config.Discord{OwnerGuildID: "77"})

		Expect(global).To(ConsistOf(everyone))
		Expect(guilds).To(Equal(map[string][]string{"77": {"owner"}}))
	})

	It("puts /owner in the dev testGuild when there's no owner guild", func() {
		_, guilds := register(config.Discord{DevGuildID: "88"})
		Expect(guilds).To(Equal(map[string][]string{"88": {"owner"}}))
	})

	It("leaves /owner out without a testGuild for it", func() {
		global, guilds := register(config.Discord{})

		Expect(global).To(ConsistOf(everyone))
		Expect(guilds).To(BeEmpty())
	})
})

var _ = Describe("/streamers", func() {
	var h *harness

	BeforeEach(func() {
		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		h = newHarness(seed)
	})

	It("shows a streamer by alias", func() {
		e := embed(h.run(dt.Command(user, "streamers", dt.Sub("info", dt.String("streamer", "calli")))))

		Expect(e["title"]).To(Equal("Mori Calliope"))
		Expect(e["url"]).To(Equal("https://www.youtube.com/channel/UCL_qhgtOy0dy1Agp8vkySQg"))
		Expect(fmt.Sprint(e["fields"])).To(ContainSubstring("Hololive › Hololive EN"))
	})

	It("explains ambiguous and unknown streamers privately", func() {
		data := h.run(dt.Command(user, "streamers", dt.Sub("info", dt.String("streamer", "takanashi"))))
		Expect(data["content"]).To(ContainSubstring("Takanashi Kiara, Takanashi Kiara SubCh"))
		Expect(ephemeral(data)).To(BeTrue())

		data = h.run(dt.Command(user, "streamers", dt.Sub("info", dt.String("streamer", "nobody at all"))))
		Expect(data["content"]).To(ContainSubstring(`I don't know a streamer called "nobody at all"`))
	})

	It("lists groups with nested subgroups", func() {
		desc := embed(h.run(dt.Command(user, "streamers", dt.Sub("list"))))["description"].(string)

		Expect(desc).To(MatchRegexp(`- \*\*Hololive\*\* \(\d+\)`))
		Expect(desc).To(MatchRegexp(`\n  - Hololive EN \(\d+\)`))
		Expect(desc).To(ContainSubstring("- **Indie** ("))
	})

	It("lists a group's members by subgroup", func() {
		e := embed(h.run(dt.Command(user, "streamers", dt.Sub("list", dt.String("group", "Nijisanji")))))
		desc := e["description"].(string)

		Expect(e["title"]).To(MatchRegexp(`^Nijisanji \(\d+\)$`))
		Expect(desc).To(MatchRegexp(`\*\*Nijisanji JP\*\* \(\d+\)`))
		Expect(desc).To(ContainSubstring("Fumino Tamaki"))
		Expect(desc).NotTo(ContainSubstring("more"))
	})

	It("rejects unknown groups", func() {
		data := h.run(dt.Command(user, "streamers", dt.Sub("list", dt.String("group", "nope"))))
		Expect(data["content"]).To(ContainSubstring(`There's no group called "nope"`))
	})

	It("suggests streamers and groups while typing", func() {
		Expect(choices(h.run(dt.Autocomplete(user, "streamers", dt.Sub("info", dt.Focused(dt.String("streamer", "takanashi"))))))).
			To(Equal([]string{"Takanashi Kiara · Hololive EN", "Takanashi Kiara SubCh · Hololive EN"}))

		Expect(choices(h.run(dt.Autocomplete(user, "streamers", dt.Sub("list", dt.Focused(dt.String("group", "niji"))))))).
			To(HaveExactElements("Nijisanji", "Nijisanji JP", "Nijisanji EN", "Nijisanji ID", "Nijisanji KR"))
	})
})

var _ = Describe("/streamers list with a huge group", func() {
	It("cuts off cleanly under Discord's limit", func() {
		seed := &streamers.Seed{Groups: []store.Group{{ID: "big", Name: "Big"}}}
		for i := range 400 {
			seed.Streamers = append(seed.Streamers, store.Streamer{
				ChannelID: fmt.Sprintf("UC%022d", i),
				Name:      fmt.Sprintf("Streamer With A Fairly Long Name %03d", i),
				GroupID:   "big",
				Source:    store.SourceSeed,
			})
		}
		h := newHarness(seed)

		desc := embed(h.run(dt.Command(user, "streamers", dt.Sub("list", dt.String("group", "big")))))["description"].(string)

		Expect(len(desc)).To(BeNumerically("<=", 4096))
		Expect(desc).To(MatchRegexp(`Streamer With A Fairly Long Name \d{3}\n…and \d+ more$`))
	})
})

var _ = Describe("/owner streamers", func() {
	var (
		h   *harness
		reg *streamers.Registry
	)

	BeforeEach(func() {
		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		h = newHarness(seed)
		reg = h.b.Streamers
	})

	ownerRun := func(sub string, opts ...dt.Option) map[string]any {
		return h.run(dt.Command(owner, "owner", dt.Group("streamers", dt.Sub(sub, opts...))))
	}

	It("ignores everyone but the owner", func() {
		data := h.run(dt.Command(user, "owner", dt.Group("streamers",
			dt.Sub("add", dt.String("channel", "UCaaaaaaaaaaaaaaaaaaaaaa"), dt.String("name", "Sneaky")))))

		Expect(data).To(BeNil())
		_, ok := reg.Streamer("UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(ok).To(BeFalse())
	})

	It("adds a streamer from a channel URL", func() {
		data := ownerRun("add",
			dt.String("channel", "https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa/streams"),
			dt.String("name", "New Indie"),
			dt.String("group", "indie"),
			dt.String("twitter", "@newindie"),
			dt.String("aliases", "newbie, NB, newbie"),
		)

		Expect(data["content"]).To(Equal("Added New Indie."))
		Expect(ephemeral(data)).To(BeTrue())

		st, ok := reg.Streamer("UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(ok).To(BeTrue())
		Expect(st.Source).To(Equal(store.SourceOwner))
		Expect(st.GroupID).To(Equal("indie"))
		Expect(st.Twitter).To(Equal("newindie"))
		Expect(st.Aliases).To(Equal([]string{"newbie", "NB"}))
	})

	It("refuses duplicates and bad channels", func() {
		Expect(ownerRun("add", dt.String("channel", "UCL_qhgtOy0dy1Agp8vkySQg"), dt.String("name", "x"))["content"]).
			To(ContainSubstring("Mori Calliope is already in the registry"))
		Expect(ownerRun("add", dt.String("channel", "not a channel"), dt.String("name", "x"))["content"]).
			To(ContainSubstring("doesn't look like a YouTube channel"))
	})

	It("edits only the given fields and clears with -", func() {
		data := ownerRun("edit",
			dt.String("streamer", "calli"),
			dt.String("aliases", "gremlin"),
			dt.String("twitter", "-"),
		)
		Expect(data["content"]).To(Equal("Updated Mori Calliope."))

		st, err := reg.Resolve("gremlin")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Name).To(Equal("Mori Calliope"))
		Expect(st.GroupID).To(Equal("hololive-en"))
		Expect(st.Twitter).To(BeEmpty())
		Expect(st.Source).To(Equal(store.SourceOwner))
	})

	It("warns that removed seed streamers come back", func() {
		data := ownerRun("remove", dt.String("streamer", "kobo"))

		Expect(data["content"]).To(ContainSubstring("Removed Kobo Kanaeru"))
		Expect(data["content"]).To(ContainSubstring("seed files"))
		_, err := reg.Resolve("kobo")
		Expect(err).To(MatchError(streamers.ErrNotFound))
	})

	It("exports owner entries as seed entries", func() {
		Expect(ownerRun("export")["content"]).To(ContainSubstring("Nothing to export"))

		ownerRun("add", dt.String("channel", "UCaaaaaaaaaaaaaaaaaaaaaa"), dt.String("name", "New Indie"), dt.String("group", "indie"))
		ownerRun("edit", dt.String("streamer", "calli"), dt.String("aliases", "gremlin"))

		content := ownerRun("export")["content"].(string)
		Expect(content).To(ContainSubstring("```toml\n# streamers/seed/hololive/en.toml"))
		Expect(content).To(ContainSubstring(`aliases = ["gremlin"]`))
		Expect(content).To(ContainSubstring("# streamers/seed/indie.toml"))
		Expect(content).To(ContainSubstring(`name = "New Indie"`))
	})

	It("exports one streamer of any source", func() {
		content := ownerRun("export", dt.String("streamer", "kobo"))["content"].(string)
		Expect(content).To(ContainSubstring("# streamers/seed/hololive/id.toml"))
		Expect(content).To(ContainSubstring(`name = "Kobo Kanaeru"`))
	})

	It("sends long exports as a file", func() {
		for i := range 30 {
			ownerRun("add", dt.String("channel", fmt.Sprintf("UC%022d", i)), dt.String("name", fmt.Sprintf("Owner Pick %02d", i)))
		}

		ownerRun("export")

		last := h.rec.Requests()[len(h.rec.Requests())-1]
		Expect(last.Files).To(HaveKey("streamers.toml"))
		Expect(last.Files["streamers.toml"]).To(ContainSubstring(`name = "Owner Pick 29"`))
	})

	It("doesn't say that for owner-added streamers", func() {
		ownerRun("add", dt.String("channel", "UCaaaaaaaaaaaaaaaaaaaaaa"), dt.String("name", "New Indie"))

		data := ownerRun("remove", dt.String("streamer", "New Indie"))
		Expect(data["content"]).To(Equal("Removed New Indie."))
		Expect(strings.Contains(data["content"].(string), "seed")).To(BeFalse())
	})
})
