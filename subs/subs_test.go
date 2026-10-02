package subs_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Service", func() {
	var (
		ctx context.Context
		db  *sqlite.Store
		reg *streamers.Registry
		svc *subs.Service
	)

	add := func(guild string, feature store.Feature, kind store.TargetKind, target, channel string) {
		GinkgoHelper()
		_, err := svc.Add(ctx, store.Subscription{
			GuildID: guild, Feature: feature, Target: store.Target{Kind: kind, ID: target}, ChannelID: channel, CreatedBy: "u",
		})
		Expect(err).NotTo(HaveOccurred())
	}

	channels := func(list []*store.Subscription) []string {
		out := make([]string, len(list))
		for i, s := range list {
			out[i] = s.ChannelID
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		reg = streamers.New(db)
		_, err = reg.Sync(ctx, &streamers.Seed{
			Groups: []store.Group{
				{ID: "holo", Name: "Hololive"},
				{ID: "holo-en", Name: "Hololive EN", ParentID: "holo"},
				{ID: "niji", Name: "Nijisanji"},
			},
			Streamers: []store.Streamer{
				{ChannelID: "UCcalli", Name: "Mori Calliope", GroupID: "holo-en"},
				{ChannelID: "UCkiara", Name: "Takanashi Kiara", GroupID: "holo-en"},
				{ChannelID: "UCelira", Name: "Elira Pendora", GroupID: "niji"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(reg.Save(ctx, store.Streamer{ChannelID: "UCuser", Name: "Someone", Source: store.SourceUser})).To(Succeed())

		for _, g := range []string{"g1", "g2"} {
			_, _, err := db.JoinGuild(ctx, g)
			Expect(err).NotTo(HaveOccurred())
		}

		svc = subs.New(db, reg)
	})

	It("matches by channel, group, parent group and all", func() {
		add("g1", store.FeatureRelay, store.TargetChannel, "UCcalli", "calli")
		add("g1", store.FeatureRelay, store.TargetGroup, "holo-en", "en")
		add("g1", store.FeatureRelay, store.TargetGroup, "holo", "holo")
		add("g2", store.FeatureRelay, store.TargetAll, "", "all")
		add("g2", store.FeatureCameos, store.TargetChannel, "UCcalli", "cameos")

		Expect(channels(svc.Match(store.FeatureRelay, "UCcalli"))).To(Equal([]string{"calli", "en", "holo", "all"}))
		Expect(channels(svc.Match(store.FeatureRelay, "UCkiara"))).To(Equal([]string{"en", "holo", "all"}))
		Expect(channels(svc.Match(store.FeatureRelay, "UCelira"))).To(Equal([]string{"all"}))
		Expect(channels(svc.Match(store.FeatureCameos, "UCcalli"))).To(Equal([]string{"cameos"}))
		Expect(svc.Match(store.FeatureGossip, "UCcalli")).To(BeEmpty())
		Expect(svc.Match(store.FeatureRelay, "UCnobody")).To(BeEmpty())
	})

	It("sends each Discord channel the most specific subscription only", func() {
		add("g1", store.FeatureRelay, store.TargetAll, "", "c")
		add("g1", store.FeatureRelay, store.TargetGroup, "holo", "c")
		_, err := svc.Add(ctx, store.Subscription{
			GuildID: "g1", Feature: store.FeatureRelay, Target: store.Target{Kind: store.TargetChannel, ID: "UCcalli"},
			ChannelID: "c", RoleID: "r", CreatedBy: "u",
		})
		Expect(err).NotTo(HaveOccurred())

		matched := svc.Match(store.FeatureRelay, "UCcalli")
		Expect(matched).To(HaveLen(1))
		Expect(matched[0].RoleID).To(Equal("r"))
		Expect(svc.Match(store.FeatureRelay, "UCkiara")[0].Target.Kind).To(Equal(store.TargetGroup))
	})

	It("matches user channels only by channel", func() {
		add("g1", store.FeatureRelay, store.TargetAll, "", "all")
		Expect(svc.Match(store.FeatureRelay, "UCuser")).To(BeEmpty())

		add("g1", store.FeatureRelay, store.TargetChannel, "UCuser", "user")
		Expect(channels(svc.Match(store.FeatureRelay, "UCuser"))).To(Equal([]string{"user"}))
	})

	It("stops matching hidden streamers until they're back", func() {
		add("g1", store.FeatureRelay, store.TargetChannel, "UCcalli", "c")
		calli, _ := reg.Streamer("UCcalli")
		saved := *calli

		Expect(reg.Remove(ctx, "UCcalli")).To(Succeed())
		Expect(svc.Match(store.FeatureRelay, "UCcalli")).To(BeEmpty())

		Expect(reg.Save(ctx, saved)).To(Succeed())
		Expect(svc.Match(store.FeatureRelay, "UCcalli")).To(HaveLen(1))
	})

	It("forgets guilds the bot left after a reload", func() {
		add("g1", store.FeatureGossip, store.TargetChannel, "UCcalli", "a")
		add("g2", store.FeatureGossip, store.TargetChannel, "UCkiara", "b")
		Expect(channels(svc.All(store.FeatureGossip))).To(Equal([]string{"a", "b"}))

		Expect(db.LeaveGuild(ctx, "g2", time.Now())).To(Succeed())
		Expect(svc.Reload(ctx)).To(Succeed())
		Expect(channels(svc.All(store.FeatureGossip))).To(Equal([]string{"a"}))
	})

	It("counts per Discord channel and signals reloads", func() {
		Eventually(svc.Changed()).ShouldNot(Receive())

		add("g1", store.FeatureRelay, store.TargetChannel, "UCcalli", "c")
		add("g1", store.FeatureRelay, store.TargetGroup, "niji", "c")
		add("g1", store.FeatureCameos, store.TargetChannel, "UCcalli", "c")

		Expect(svc.Count(store.FeatureRelay, "c")).To(Equal(2))
		Expect(svc.Count(store.FeatureCameos, "c")).To(Equal(1))
		Expect(svc.Count(store.FeatureGossip, "c")).To(BeZero())
		Expect(svc.Changed()).To(Receive())
		Expect(svc.Changed()).NotTo(Receive())
	})

	It("removes and clears", func() {
		add("g1", store.FeatureRelay, store.TargetChannel, "UCcalli", "c")
		add("g1", store.FeatureRelay, store.TargetChannel, "UCkiara", "c")
		add("g1", store.FeatureRelay, store.TargetChannel, "UCkiara", "d")

		Expect(svc.Remove(ctx, "g1", store.FeatureRelay, store.Target{Kind: store.TargetChannel, ID: "UCcalli"}, "c")).To(Succeed())
		Expect(svc.Match(store.FeatureRelay, "UCcalli")).To(BeEmpty())

		n, err := svc.Clear(ctx, "g1", store.FeatureRelay, "c")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		Expect(channels(svc.Match(store.FeatureRelay, "UCkiara"))).To(Equal([]string{"d"}))
	})
})
