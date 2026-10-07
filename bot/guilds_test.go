package bot

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	gt "github.com/VTGare/gumi/v2/gumitest"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Guild lifecycle", func() {
	const logChannel = 999

	var (
		b   *Bot
		rec *gt.Recorder
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		st, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "bot.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(st.Close)

		cfg := &config.Config{Discord: config.Discord{Token: gt.Token, LogChannelID: "999"}}
		b, err = New(cfg, slog.New(slog.DiscardHandler), st)
		Expect(err).NotTo(HaveOccurred())

		rec = gt.Attach(b.Client)
	})

	created := func(id snowflake.ID, name string) {
		b.guildCreated(discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: id, Name: name, MemberCount: 42}}})
	}

	guild := func(id string) *store.Guild {
		g, err := b.Store.Guild(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		return g
	}

	It("records and announces a new guild", func() {
		created(1, "Pomu Fan Club")

		Expect(guild("1").Active()).To(BeTrue())
		Expect(rec.Messages(logChannel)).To(ConsistOf("Joined **Pomu Fan Club** (`1`), 42 members."))
	})

	It("stays quiet for guilds it was already in, like on startup", func() {
		created(1, "Pomu Fan Club")
		created(1, "Pomu Fan Club")

		Expect(rec.Messages(logChannel)).To(HaveLen(1))
	})

	It("ignores unavailable guilds", func() {
		b.guildCreated(discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: 1}}, Unavailable: true})

		_, err := b.Store.Guild(ctx, "1")
		Expect(err).To(MatchError(store.ErrGuildNotFound))
		Expect(rec.Messages(logChannel)).To(BeEmpty())
	})

	It("soft-deletes on leave and restores on rejoin", func() {
		created(1, "Pomu Fan Club")
		b.guildLeft("1", "Pomu Fan Club")

		Expect(guild("1").Active()).To(BeFalse())

		created(1, "Pomu Fan Club")

		Expect(guild("1").Active()).To(BeTrue())
		Expect(rec.Messages(logChannel)).To(Equal([]string{
			"Joined **Pomu Fan Club** (`1`), 42 members.",
			"Left **Pomu Fan Club** (`1`). Its data is kept for 30 days.",
			"Rejoined **Pomu Fan Club** (`1`), 42 members. Settings restored.",
		}))
	})

	It("names a left guild by its ID when it wasn't cached", func() {
		created(1, "Pomu Fan Club")
		b.guildLeft("1", "")

		Expect(rec.Messages(logChannel)).To(ContainElement("Left **1** (`1`). Its data is kept for 30 days."))
	})

	It("keeps guilds through outages and handles DisGo's guild events", func() {
		b.Client.AddEventListeners(b.listeners()...)
		generic := &events.GenericGuild{GenericEvent: events.NewGenericEvent(b.Client, 0, 0), GuildID: 1}
		gg := discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: 1, Name: "Pomu Fan Club", MemberCount: 42}}}

		b.Client.EventManager.DispatchEvent(&events.GuildJoin{GenericGuild: generic, Guild: gg})
		Eventually(func() []string { return rec.Messages(logChannel) }).Should(HaveLen(1))

		b.Client.EventManager.DispatchEvent(&events.GuildUnavailable{GenericGuild: generic})
		b.Client.EventManager.DispatchEvent(&events.GuildAvailable{GenericGuild: generic, Guild: gg})
		Consistently(func() []string { return rec.Messages(logChannel) }, 50*time.Millisecond).Should(HaveLen(1))
		Expect(guild("1").Active()).To(BeTrue())

		b.Client.EventManager.DispatchEvent(&events.GuildLeave{GenericGuild: generic, Guild: gg.Guild})
		Eventually(func() bool { return guild("1").Active() }).Should(BeFalse())
		Eventually(func() []string { return rec.Messages(logChannel) }).Should(ContainElement(
			"Left **Pomu Fan Club** (`1`). Its data is kept for 30 days."))
	})

	It("marks guilds missing from READY as left", func() {
		for _, id := range []snowflake.ID{1, 2} {
			created(id, "g"+id.String())
		}

		b.onReady(&events.Ready{
			GenericEvent: events.NewGenericEvent(b.Client, 0, 0),
			EventReady: gateway.EventReady{
				User:   discord.OAuth2User{User: discord.User{Username: "GatorAid"}},
				Guilds: []discord.UnavailableGuild{{ID: 1, Unavailable: true}},
			},
		})

		Expect(guild("1").Active()).To(BeTrue())
		Expect(guild("2").Active()).To(BeFalse())
		Expect(rec.Messages(logChannel)).To(ContainElement("Removed from `2` while offline. Its data is kept for 30 days."))
	})

	It("drops a guild's subscriptions while it's gone", func() {
		created(1, "Pomu Fan Club")
		_, err := b.Subs.Add(ctx, store.Subscription{
			GuildID: "1", Feature: store.FeatureGossip, Target: store.Target{Kind: store.TargetChannel, ID: "UCpomu"},
			ChannelID: "c", CreatedBy: "u",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Subs.All(store.FeatureGossip)).To(HaveLen(1))

		b.guildLeft("1", "Pomu Fan Club")
		Expect(b.Subs.All(store.FeatureGossip)).To(BeEmpty())

		created(1, "Pomu Fan Club")
		Expect(b.Subs.All(store.FeatureGossip)).To(HaveLen(1))
	})

	It("posts nothing without a log channel", func() {
		b.Config.Discord.LogChannelID = ""

		created(1, "Pomu Fan Club")

		Expect(rec.Requests()).To(BeEmpty())
	})

	It("purges only guilds past retention", func() {
		created(1, "old")
		created(2, "recent")
		Expect(b.Store.LeaveGuild(ctx, "1", time.Now().Add(-store.GuildRetention-time.Hour))).To(Succeed())
		Expect(b.Store.LeaveGuild(ctx, "2", time.Now())).To(Succeed())

		b.purge(ctx)

		_, err := b.Store.Guild(ctx, "1")
		Expect(err).To(MatchError(store.ErrGuildNotFound))
		Expect(guild("2").Active()).To(BeFalse())
	})
})
