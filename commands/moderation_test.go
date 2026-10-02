package commands_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/bwmarrin/discordgo"

	dt "github.com/VTGare/gatoraid/internal/discordtest"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/youtube/channel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const channelPage = `<html><script>var ytInitialData = {"metadata":{"channelMetadataRenderer":{
	"title":"Spam Channel","externalId":"UCspamspamspamspamspamsp","vanityChannelUrl":"http://www.youtube.com/@spammer"}}};</script></html>`

var _ = Describe("Moderation commands", func() {
	var (
		h   *harness
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		h = newHarness(&streamers.Seed{})

		_, _, err := h.b.Store.JoinGuild(ctx, "guild")
		Expect(err).NotTo(HaveOccurred())

		yt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/@spammer", "/channel/UCspamspamspamspamspamsp":
				_, _ = w.Write([]byte(channelPage))
			default:
				http.NotFound(w, r)
			}
		}))
		DeferCleanup(yt.Close)
		h.b.Channels = channel.New(channel.WithBaseURL(yt.URL))
	})

	asMod := func(i *discordgo.InteractionCreate) *discordgo.InteractionCreate {
		return dt.WithPermissions(i, discordgo.PermissionManageMessages)
	}

	run := func(command, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) string {
		GinkgoHelper()
		data := h.run(asMod(dt.Command(user, command, dt.Sub(sub, opts...))))
		Expect(ephemeral(data)).To(BeTrue())
		return replyText(data)
	}

	// Deferred replies arrive as an edit of the original response.
	runDeferred := func(command, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) string {
		GinkgoHelper()
		before := len(h.rec.Edits())
		h.run(asMod(dt.Command(user, command, dt.Sub(sub, opts...))))
		edits := h.rec.Edits()
		Expect(edits).To(HaveLen(before + 1))
		return replyText(edits[len(edits)-1].Body)
	}

	It("needs Manage Messages", func() {
		data := h.run(dt.Command(user, "filter", dt.Sub("list")))
		Expect(replyText(data)).To(ContainSubstring("You need Manage Messages, or a Blacklister or Manager role"))
	})

	It("lets Blacklister and Manager roles in", func() {
		Expect(h.b.Store.SetGuildRoles(ctx, "guild", store.RoleBlacklister, []string{"bl"})).To(Succeed())
		Expect(h.b.Store.SetGuildRoles(ctx, "guild", store.RoleManager, []string{"mgr"})).To(Succeed())

		for _, role := range []string{"bl", "mgr"} {
			data := h.run(dt.WithRoles(dt.Command(user, "filter", dt.Sub("list")), role))
			Expect(embed(data)["title"]).To(Equal("Filters"), role)
		}

		data := h.run(dt.WithRoles(dt.Command(user, "filter", dt.Sub("list")), "other"))
		Expect(replyText(data)).To(ContainSubstring("You need Manage Messages"))
	})

	It("blacklists a channel by handle and lists it", func() {
		Expect(runDeferred("blacklist", "add", dt.String("channel", "@spammer"), dt.String("reason", "ads"))).
			To(Equal("Blacklisted **@spammer**. Their messages won't be relayed here.\n`/blacklist remove` undoes it."))
		Expect(h.b.Moderation.Blacklisted("guild", "UCspamspamspamspamspamsp")).To(BeTrue())

		Expect(runDeferred("blacklist", "add", dt.String("channel", "https://youtube.com/channel/UCspamspamspamspamspamsp"))).
			To(ContainSubstring("**@spammer** is already blacklisted."))

		data := h.run(asMod(dt.Command(user, "blacklist", dt.Sub("list"))))
		Expect(replyText(data)).To(Equal("1 blacklisted channel."))
		last := h.rec.Requests()[len(h.rec.Requests())-1]
		Expect(last.Files["blacklist.txt"]).To(MatchRegexp(`^UCspamspamspamspamspamsp\t@spammer\t\d{4}-\d\d-\d\d\tads\n$`))
	})

	It("explains channels it can't find", func() {
		Expect(runDeferred("blacklist", "add", dt.String("channel", "some name"))).To(ContainSubstring("That's not a YouTube channel"))
		Expect(runDeferred("blacklist", "add", dt.String("channel", "@nobody"))).To(ContainSubstring("There's no YouTube channel at `@nobody`."))
	})

	It("blacklists the author of a relayed line from the context menu", func() {
		Expect(h.b.Store.SaveLines(ctx, []store.Line{{
			VideoID: "v", GuildID: "guild", ChannelID: "channel", MessageID: "m1",
			AuthorChannelID: "UCauthorauthorauthorauth", AuthorName: "@some_author", Body: "spam",
			Kind: store.LineTL, SaidAt: time.Now(),
		}})).To(Succeed())

		data := h.run(asMod(dt.MessageCommand(user, "Blacklist author", "m1")))
		Expect(ephemeral(data)).To(BeTrue())
		Expect(replyText(data)).To(Equal(`Blacklisted **@some\_author**. Their messages won't be relayed here.` + "\n`/blacklist remove` undoes it."))
		Expect(h.b.Moderation.Blacklisted("guild", "UCauthorauthorauthorauth")).To(BeTrue())

		data = h.run(asMod(dt.MessageCommand(user, "Blacklist author", "m2")))
		Expect(replyText(data)).To(ContainSubstring("That's not a line I relayed"))
	})

	It("blacklists MChad authors by name, not every MChad line", func() {
		Expect(h.b.Store.SaveLines(ctx, []store.Line{{
			VideoID: "v", GuildID: "guild", MessageID: "m1", AuthorChannelID: "mchad:Some TLer", AuthorName: "Some TLer",
			Body: "TL line", Kind: store.LineTL, SaidAt: time.Now(),
		}})).To(Succeed())

		data := h.run(asMod(dt.MessageCommand(user, "Blacklist author", "m1")))
		Expect(replyText(data)).To(HavePrefix("Blacklisted **Some TLer**."))
		Expect(h.b.Moderation.Blacklisted("guild", "mchad:Some TLer")).To(BeTrue())
		Expect(h.b.Moderation.Blacklisted("guild", "mchad:Someone Else")).To(BeFalse())
		Expect(h.b.Moderation.Blacklisted("guild", "")).To(BeFalse())
	})

	It("mentions that streamers' own lines are still relayed", func() {
		Expect(h.b.Store.SaveLines(ctx, []store.Line{{
			VideoID: "v", GuildID: "guild", MessageID: "m1", AuthorChannelID: "UCstreamer", AuthorName: "@streamer",
			Body: "hi", Kind: store.LineOwner, SaidAt: time.Now(),
		}})).To(Succeed())

		data := h.run(asMod(dt.MessageCommand(user, "Blacklist author", "m1")))
		Expect(replyText(data)).To(ContainSubstring("only stops their cameos and gossip"))
	})

	It("removes by suggestion, by name, or the last one added", func() {
		for _, e := range []store.BlacklistEntry{
			{GuildID: "guild", ChannelID: "UCa", Name: "@a", AddedBy: "mod"},
			{GuildID: "guild", ChannelID: "UCb", Name: "@b", AddedBy: "mod"},
			{GuildID: "guild", ChannelID: "UCc", Name: "@c", AddedBy: "mod"},
		} {
			_, err := h.b.Moderation.AddToBlacklist(ctx, e)
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(2 * time.Millisecond)
		}

		suggested := choices(h.run(asMod(dt.Autocomplete(user, "blacklist", dt.Sub("remove", dt.Focused("channel", "@"))))))
		Expect(suggested).To(HaveExactElements("@c · UCc", "@b · UCb", "@a · UCa"))

		Expect(run("blacklist", "remove", dt.String("channel", "UCa"))).To(Equal("Took **@a** off the blacklist."))
		Expect(run("blacklist", "remove", dt.String("channel", "@B"))).To(Equal("Took **@b** off the blacklist."))
		Expect(run("blacklist", "remove")).To(Equal("Took **@c** off the blacklist."))
		Expect(run("blacklist", "remove")).To(ContainSubstring("The blacklist is empty."))
		Expect(run("blacklist", "list")).To(Equal("The blacklist is empty."))
	})

	It("suggests recently relayed authors to blacklist", func() {
		Expect(h.b.Store.SaveLines(ctx, []store.Line{{
			VideoID: "v", GuildID: "guild", AuthorChannelID: "UCx", AuthorName: "@translator", Body: "[EN] hi",
			Kind: store.LineTL, SaidAt: time.Now(),
		}})).To(Succeed())

		suggested := choices(h.run(asMod(dt.Autocomplete(user, "blacklist", dt.Sub("add", dt.Focused("channel", "trans"))))))
		Expect(suggested).To(Equal([]string{"@translator · UCx"}))
	})

	It("adds, lists and removes filters", func() {
		Expect(run("filter", "add", dt.String("type", "banned"), dt.String("pattern", " SPOILER "))).
			To(Equal("Lines containing `spoiler` won't be relayed, except the streamer's own."))
		Expect(run("filter", "add", dt.String("type", "wanted"), dt.String("pattern", "ES:"))).
			To(Equal("Lines starting with `es:` now count as translations."))
		Expect(run("filter", "add", dt.String("type", "wanted"), dt.String("pattern", "es:"))).
			To(ContainSubstring("`es:` is already a wanted filter."))

		rules := h.b.Moderation.For("guild")
		Expect(rules.Banned).To(Equal([]string{"spoiler"}))
		Expect(rules.Wanted).To(Equal([]string{"es:"}))

		data := h.run(asMod(dt.Command(user, "filter", dt.Sub("list"))))
		Expect(embed(data)["fields"]).To(HaveLen(2))

		suggested := choices(h.run(asMod(dt.Autocomplete(user, "filter",
			dt.Sub("remove", dt.String("type", "wanted"), dt.Focused("pattern", ""))))))
		Expect(suggested).To(Equal([]string{"es:"}))

		Expect(run("filter", "remove", dt.String("type", "banned"), dt.String("pattern", "Spoiler"))).
			To(Equal("Removed the banned filter `spoiler`."))
		Expect(run("filter", "remove", dt.String("type", "banned"), dt.String("pattern", "spoiler"))).
			To(ContainSubstring("`spoiler` isn't a banned filter."))
	})

	It("sends long filter lists as a file", func() {
		for i := range 30 {
			Expect(run("filter", "add", dt.String("type", "banned"), dt.String("pattern", fmt.Sprintf("a fairly long banned phrase number %02d", i)))).
				To(HavePrefix("Lines containing"))
		}

		data := h.run(asMod(dt.Command(user, "filter", dt.Sub("list"))))
		Expect(replyText(data)).To(ContainSubstring("here's a file"))
		last := h.rec.Requests()[len(h.rec.Requests())-1]
		Expect(last.Files["filters.txt"]).To(ContainSubstring("a fairly long banned phrase number 29\n"))
	})
})
