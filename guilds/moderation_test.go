package guilds_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/VTGare/gatoraid/guilds"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Moderation", func() {
	var (
		ctx context.Context
		svc *guilds.State
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		svc = guilds.New(db)
		for _, g := range []string{"g1", "g2"} {
			_, err := svc.Join(ctx, g)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	blacklist := func(guild, channel string) {
		GinkgoHelper()
		created, err := svc.AddToBlacklist(ctx, store.BlacklistEntry{GuildID: guild, ChannelID: channel, AddedBy: "mod"})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	}

	filter := func(guild string, kind store.FilterKind, pattern string) {
		GinkgoHelper()
		created, err := svc.AddFilter(ctx, store.Filter{GuildID: guild, Kind: kind, Pattern: pattern})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	}

	It("has no rules for guilds without any", func() {
		Expect(svc.Moderation("g1")).To(BeNil())
		Expect(svc.Blacklist("g1")).To(BeEmpty())
		Expect(svc.Blacklisted("g1", "UCa")).To(BeFalse())
	})

	It("builds each guild's rules", func() {
		blacklist("g1", "UCa")
		time.Sleep(2 * time.Millisecond)
		blacklist("g1", "UCb")
		filter("g1", store.FilterBanned, "spoiler")
		filter("g1", store.FilterWanted, "es:")
		filter("g2", store.FilterWanted, "fr:")

		rules := svc.Moderation("g1")
		Expect(rules.Blacklist).To(Equal(map[string]bool{"UCa": true, "UCb": true}))
		Expect(rules.Banned).To(Equal([]string{"spoiler"}))
		Expect(rules.Wanted).To(Equal([]string{"es:"}))
		Expect(svc.Moderation("g2").Blacklist).To(BeEmpty())
		Expect(svc.Moderation("g2").Wanted).To(Equal([]string{"fr:"}))

		Expect(svc.Blacklist("g1")).To(HaveExactElements(HaveField("ChannelID", "UCb"), HaveField("ChannelID", "UCa")))
		Expect(svc.Blacklisted("g1", "UCa")).To(BeTrue())
		Expect(svc.Blacklisted("g2", "UCa")).To(BeFalse())
	})

	It("updates after removals", func() {
		blacklist("g1", "UCa")
		filter("g1", store.FilterBanned, "spoiler")

		removed, err := svc.RemoveFromBlacklist(ctx, "g1", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(removed.ChannelID).To(Equal("UCa"))
		Expect(svc.RemoveFilter(ctx, "g1", store.FilterBanned, "spoiler")).To(Succeed())

		Expect(svc.Blacklisted("g1", "UCa")).To(BeFalse())
		Expect(svc.Moderation("g1")).To(BeNil())
	})
})
