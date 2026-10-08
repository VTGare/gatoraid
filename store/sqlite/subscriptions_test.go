package sqlite_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Subscriptions", func() {
	var (
		ctx context.Context
		db  *sqlite.Store
	)

	calli := store.Target{Kind: store.TargetChannel, ID: "UCcalli"}
	holoEN := store.Target{Kind: store.TargetGroup, ID: "holo-en"}

	sub := func(guild string, feature store.Feature, target store.Target, channel string) store.Subscription {
		return store.Subscription{GuildID: guild, Feature: feature, Target: target, ChannelID: channel, CreatedBy: "user"}
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		for _, g := range []string{"g1", "g2"} {
			_, _, err := db.JoinGuild(ctx, g)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("adds subscriptions and updates the role of existing ones", func() {
		s := sub("g1", store.FeatureRelay, calli, "c1")
		s.RoleID = "r1"
		created, err := db.AddSubscription(ctx, s)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		s.RoleID = ""
		created, err = db.AddSubscription(ctx, s)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())

		subs, err := db.GuildSubscriptions(ctx, "g1", store.FeatureRelay)
		Expect(err).NotTo(HaveOccurred())
		Expect(subs).To(HaveLen(1))
		Expect(subs[0].Target).To(Equal(calli))
		Expect(subs[0].ChannelID).To(Equal("c1"))
		Expect(subs[0].RoleID).To(BeEmpty())
		Expect(subs[0].CreatedBy).To(Equal("user"))
		Expect(subs[0].CreatedAt).To(BeTemporally("~", time.Now(), time.Minute))
	})

	It("refuses guilds it never joined", func() {
		_, err := db.AddSubscription(ctx, sub("nowhere", store.FeatureRelay, calli, "c1"))
		Expect(err).To(MatchError(store.ErrGuildNotFound))
	})

	It("keeps features, targets and channels apart", func() {
		for _, s := range []store.Subscription{
			sub("g1", store.FeatureRelay, calli, "c1"),
			sub("g1", store.FeatureRelay, calli, "c2"),
			sub("g1", store.FeatureRelay, holoEN, "c1"),
			sub("g1", store.FeatureRelay, store.Target{Kind: store.TargetAll}, "c1"),
			sub("g1", store.FeatureCameos, calli, "c1"),
			sub("g1", store.FeatureTwitch, calli, "c1"),
			sub("g2", store.FeatureRelay, calli, "c1"),
		} {
			created, err := db.AddSubscription(ctx, s)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeTrue())
		}

		relays, err := db.GuildSubscriptions(ctx, "g1", store.FeatureRelay)
		Expect(err).NotTo(HaveOccurred())
		Expect(relays).To(HaveLen(4))

		Expect(db.RemoveSubscription(ctx, "g1", store.FeatureRelay, calli, "c1")).To(Succeed())
		Expect(db.RemoveSubscription(ctx, "g1", store.FeatureRelay, calli, "c1")).To(MatchError(store.ErrSubscriptionNotFound))

		n, err := db.ClearSubscriptions(ctx, "g1", store.FeatureRelay, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))

		relays, err = db.GuildSubscriptions(ctx, "g1", store.FeatureRelay)
		Expect(err).NotTo(HaveOccurred())
		Expect(relays).To(HaveLen(1))
		Expect(relays[0].ChannelID).To(Equal("c2"))

		all, err := db.Subscriptions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(4))
	})

	It("lists only guilds the bot is in and drops them with the guild", func() {
		_, err := db.AddSubscription(ctx, sub("g1", store.FeatureRelay, calli, "c1"))
		Expect(err).NotTo(HaveOccurred())
		_, err = db.AddSubscription(ctx, sub("g2", store.FeatureRelay, calli, "c1"))
		Expect(err).NotTo(HaveOccurred())

		Expect(db.LeaveGuild(ctx, "g2", time.Now().Add(-48*time.Hour))).To(Succeed())
		all, err := db.Subscriptions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(1))
		Expect(all[0].GuildID).To(Equal("g1"))

		_, err = db.PurgeGuilds(ctx, time.Now().Add(-time.Hour))
		Expect(err).NotTo(HaveOccurred())
		_, _, err = db.JoinGuild(ctx, "g2")
		Expect(err).NotTo(HaveOccurred())
		subs, err := db.GuildSubscriptions(ctx, "g2", store.FeatureRelay)
		Expect(err).NotTo(HaveOccurred())
		Expect(subs).To(BeEmpty())
	})

	It("purges hidden streamers nothing subscribes to", func() {
		_, err := db.SyncSeed(ctx, nil, []store.Streamer{
			{ChannelID: "UCcalli", Name: "Mori Calliope"},
			{ChannelID: "UCkiara", Name: "Takanashi Kiara"},
			{ChannelID: "UCame", Name: "Watson Amelia"},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = db.AddSubscription(ctx, sub("g1", store.FeatureCameos, calli, "c1"))
		Expect(err).NotTo(HaveOccurred())
		// A group with the same ID as a channel doesn't keep it.
		_, err = db.AddSubscription(ctx, sub("g1", store.FeatureRelay, store.Target{Kind: store.TargetGroup, ID: "UCkiara"}, "c1"))
		Expect(err).NotTo(HaveOccurred())

		res, err := db.SyncSeed(ctx, nil, []store.Streamer{{ChannelID: "UCame", Name: "Watson Amelia"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Removed).To(Equal(2))

		n, err := db.PurgeStreamers(ctx, time.Now().Add(-time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())

		n, err = db.PurgeStreamers(ctx, time.Now().Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))

		_, err = db.Streamer(ctx, "UCkiara")
		Expect(err).To(MatchError(store.ErrStreamerNotFound))
		st, err := db.Streamer(ctx, "UCcalli")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Removed()).To(BeTrue())
		_, err = db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
	})

	It("hides user channels nothing subscribes to", func() {
		Expect(db.SaveStreamer(ctx, store.Streamer{ChannelID: "UCused", Name: "Used", Source: store.SourceUser})).To(Succeed())
		Expect(db.SaveStreamer(ctx, store.Streamer{ChannelID: "UCunused", Name: "Unused", Source: store.SourceUser})).To(Succeed())
		Expect(db.SaveStreamer(ctx, store.Streamer{ChannelID: "UCowner", Name: "Owner", Source: store.SourceOwner})).To(Succeed())
		_, err := db.AddSubscription(ctx, sub("g1", store.FeatureRelay, store.Target{Kind: store.TargetChannel, ID: "UCused"}, "c1"))
		Expect(err).NotTo(HaveOccurred())

		n, err := db.HideUnusedUserStreamers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))

		for id, hidden := range map[string]bool{"UCused": false, "UCunused": true, "UCowner": false} {
			st, err := db.Streamer(ctx, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Removed()).To(Equal(hidden), id)
		}
	})
})
