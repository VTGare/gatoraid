package commands_test

import (
	"context"

	"github.com/bwmarrin/discordgo"

	dt "github.com/VTGare/gatoraid/internal/discordtest"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	calliID = "UCL_qhgtOy0dy1Agp8vkySQg"
	koboID  = "UCjLEmnpCNeisMxy134KPwWw"
)

var _ = Describe("Subscription commands", func() {
	var h *harness

	BeforeEach(func() {
		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		h = newHarness(seed)

		_, _, err = h.b.Store.JoinGuild(context.Background(), "guild")
		Expect(err).NotTo(HaveOccurred())
	})

	run := func(command, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) map[string]any {
		return h.run(dt.WithPermissions(dt.Command(user, command, dt.Sub(sub, opts...)), discordgo.PermissionManageGuild))
	}

	suggest := func(command, sub string, focused *discordgo.ApplicationCommandInteractionDataOption) []string {
		return choices(h.run(dt.WithPermissions(dt.Autocomplete(user, command, dt.Sub(sub, focused)), discordgo.PermissionManageGuild)))
	}

	content := replyText

	relays := func() []store.Subscription {
		GinkgoHelper()
		list, err := h.b.Subs.Guild(context.Background(), "guild", store.FeatureRelay)
		Expect(err).NotTo(HaveOccurred())
		return list
	}

	It("needs Manage Server", func() {
		data := h.run(dt.Command(user, "relay", dt.Sub("add", dt.String("target", "calli"))))

		Expect(content(data)).To(ContainSubstring("You need Manage Server, or a Manager role"))
		Expect(relays()).To(BeEmpty())
	})

	It("lets Manager roles in, but not Blacklister roles", func() {
		Expect(h.b.Store.SetGuildRoles(context.Background(), "guild", store.RoleManager, []string{"mgr"})).To(Succeed())
		Expect(h.b.Store.SetGuildRoles(context.Background(), "guild", store.RoleBlacklister, []string{"bl"})).To(Succeed())

		data := h.run(dt.WithRoles(dt.Command(user, "relay", dt.Sub("add", dt.String("target", "calli"))), "mgr"))
		Expect(content(data)).To(HavePrefix("Now relaying"))

		data = h.run(dt.WithRoles(dt.Command(user, "relay", dt.Sub("list")), "bl"))
		Expect(content(data)).To(ContainSubstring("You need Manage Server"))
	})

	It("relays a streamer here with a role, without pinging it", func() {
		data := run("relay", "add", dt.String("target", "calli"), dt.Role("role", "123"))

		Expect(content(data)).To(Equal("Now relaying **Mori Calliope** in <#channel>.\nRelay notices ping <@&123>."))
		Expect(data["allowed_mentions"]).To(HaveKeyWithValue("parse", BeNil()))
		e := embed(data)
		Expect(e["description"]).To(HavePrefix("✅ Now relaying"))
		Expect(e["color"]).To(BeEquivalentTo(0x4C9A2A))
		Expect(relays()).To(ConsistOf(And(
			HaveField("Target", store.Target{Kind: store.TargetChannel, ID: calliID}),
			HaveField("ChannelID", "channel"),
			HaveField("RoleID", "123"),
			HaveField("CreatedBy", user),
		)))
		Expect(h.b.Subs.Match(store.FeatureRelay, calliID)).To(HaveLen(1))
	})

	It("updates the role of an existing relay", func() {
		run("relay", "add", dt.String("target", calliID), dt.Role("role", "123"))
		data := run("relay", "add", dt.String("target", calliID))

		Expect(content(data)).To(Equal("Already relaying **Mori Calliope** in <#channel>.\nRelay notices don't ping anyone."))
		Expect(relays()[0].RoleID).To(BeEmpty())
	})

	It("refuses @everyone as the role", func() {
		Expect(content(run("relay", "add", dt.String("target", "calli"), dt.Role("role", "guild")))).
			To(ContainSubstring("@everyone isn't supported"))
	})

	It("takes groups and all, by suggestion or by name", func() {
		Expect(content(run("relay", "add", dt.String("target", "group:hololive-en"), dt.Channel("channel", "other")))).
			To(Equal("Now relaying everyone in **Hololive EN** in <#other>."))
		Expect(content(run("relay", "add", dt.String("target", "nijisanji en")))).
			To(Equal("Now relaying everyone in **Nijisanji EN** in <#channel>."))
		Expect(content(run("relay", "add", dt.String("target", "all")))).
			To(Equal("Now relaying every streamer in <#channel>."))

		Expect(h.b.Subs.Match(store.FeatureRelay, calliID)).To(HaveLen(2))
	})

	It("keeps gossip to single streamers", func() {
		Expect(content(run("gossip", "add", dt.String("target", "all")))).To(ContainSubstring("/gossip follows one streamer at a time"))
		Expect(content(run("gossip", "add", dt.String("target", "group:hololive")))).To(ContainSubstring("one streamer at a time"))
		Expect(content(run("gossip", "add", dt.String("target", "kobo")))).
			To(Equal("Now posting messages about **Kobo Kanaeru** in <#channel>."))
		Expect(h.b.Subs.All(store.FeatureGossip)).To(HaveLen(1))
	})

	It("removes one, even after the streamer was removed from the registry", func() {
		run("cameos", "add", dt.String("target", "kobo"))
		run("cameos", "add", dt.String("target", "calli"))
		Expect(h.b.Streamers.Remove(context.Background(), koboID)).To(Succeed())

		Expect(suggest("cameos", "remove", dt.Focused("target", ""))).
			To(ConsistOf("Kobo Kanaeru (removed)", "Mori Calliope"))

		Expect(content(run("cameos", "remove", dt.String("target", koboID)))).
			To(Equal("Stopped posting cameos by **Kobo Kanaeru** (removed) in <#channel>."))
		Expect(content(run("cameos", "remove", dt.String("target", koboID)))).
			To(ContainSubstring("I'm not posting cameos by **Kobo Kanaeru** (removed) in <#channel>."))
		Expect(h.b.Subs.All(store.FeatureCameos)).To(HaveLen(1))
	})

	It("clears a channel and lists the rest", func() {
		run("relay", "add", dt.String("target", "calli"), dt.Role("role", "123"))
		run("relay", "add", dt.String("target", "kobo"))
		run("relay", "add", dt.String("target", "all"), dt.Channel("channel", "other"))

		desc := embed(run("relay", "list"))["description"].(string)
		Expect(desc).To(Equal("<#channel>\n- **Mori Calliope** · <@&123>\n- **Kobo Kanaeru**\n\n<#other>\n- every streamer"))

		Expect(content(run("relay", "clear"))).To(Equal("Removed 2 relay subscriptions from <#channel>."))
		Expect(content(run("relay", "clear"))).To(ContainSubstring("<#channel> has no relay subscriptions."))
		Expect(relays()).To(HaveLen(1))
	})

	It("lists nothing politely", func() {
		Expect(embed(run("gossip", "list"))["description"]).To(ContainSubstring("Nothing yet. Add one with `/gossip add`."))
	})

	It("suggests all, groups and streamers", func() {
		Expect(suggest("relay", "add", dt.Focused("target", "hololive en"))).
			To(HaveExactElements("Hololive EN · group", "Hololive English · Hololive EN"))

		got := suggest("relay", "add", dt.Focused("target", ""))
		Expect(got[0]).To(Equal("Every streamer"))
		Expect(got).To(ContainElement("Hololive · group"))
		Expect(got).To(HaveLen(25))

		Expect(suggest("gossip", "add", dt.Focused("target", "hololive en"))).
			NotTo(ContainElement(ContainSubstring("group")))
	})

	It("subscribes to live and post notifications under /notify", func() {
		notify := func(group, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) string {
			GinkgoHelper()
			data := h.run(dt.WithPermissions(dt.Command(user, "notify", dt.Group(group, dt.Sub(sub, opts...))), discordgo.PermissionManageGuild))
			return replyText(data)
		}

		Expect(notify("youtube", "add", dt.String("target", "calli"), dt.Role("role", "123"))).
			To(Equal("Now posting live notifications for **Mori Calliope** in <#channel>.\nNotifications ping <@&123>."))
		Expect(notify("posts", "add", dt.String("target", "group:hololive-en"))).
			To(Equal("Now posting new posts by everyone in **Hololive EN** in <#channel>."))
		Expect(notify("posts", "list")).To(Equal("<#channel>\n- everyone in **Hololive EN**"))
		Expect(notify("youtube", "clear")).To(Equal("Removed 1 live notification subscription from <#channel>."))
		Expect(notify("youtube", "list")).To(Equal("Nothing yet. Add one with `/notify youtube add`."))

		Expect(h.b.Subs.Match(store.FeaturePosts, calliID)).To(HaveLen(1))
		Expect(h.b.Subs.Match(store.FeatureYouTube, calliID)).To(BeEmpty())
	})
})
