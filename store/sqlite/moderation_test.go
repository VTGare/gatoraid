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

var _ = Describe("Moderation", func() {
	var (
		ctx context.Context
		db  *sqlite.Store
	)

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

	blacklist := func(guild, channel, name string) bool {
		GinkgoHelper()
		created, err := db.AddToBlacklist(ctx, store.BlacklistEntry{
			GuildID: guild, ChannelID: channel, Name: name, Reason: "spam", AddedBy: "mod",
		})
		Expect(err).NotTo(HaveOccurred())
		return created
	}

	It("blacklists per guild, once", func() {
		Expect(blacklist("g1", "UCa", "@a")).To(BeTrue())
		Expect(blacklist("g1", "UCa", "@renamed")).To(BeFalse())
		Expect(blacklist("g2", "UCa", "@a")).To(BeTrue())

		all, err := db.AllBlacklists(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))
		Expect(all[0]).To(And(
			HaveField("GuildID", "g1"), HaveField("ChannelID", "UCa"), HaveField("Name", "@a"),
			HaveField("Reason", "spam"), HaveField("AddedBy", "mod"),
		))
		Expect(all[0].CreatedAt).To(BeTemporally("~", time.Now(), time.Minute))

		_, err = db.AddToBlacklist(ctx, store.BlacklistEntry{GuildID: "nowhere", ChannelID: "UCa", AddedBy: "mod"})
		Expect(err).To(MatchError(store.ErrGuildNotFound))
	})

	It("removes a channel or the newest entry", func() {
		blacklist("g1", "UCa", "@a")
		time.Sleep(2 * time.Millisecond)
		blacklist("g1", "UCb", "@b")
		time.Sleep(2 * time.Millisecond)
		blacklist("g1", "UCc", "@c")

		removed, err := db.RemoveFromBlacklist(ctx, "g1", "UCb")
		Expect(err).NotTo(HaveOccurred())
		Expect(removed.Name).To(Equal("@b"))

		removed, err = db.RemoveFromBlacklist(ctx, "g1", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(removed.ChannelID).To(Equal("UCc"))

		_, err = db.RemoveFromBlacklist(ctx, "g1", "UCb")
		Expect(err).To(MatchError(store.ErrNotBlacklisted))
		_, err = db.RemoveFromBlacklist(ctx, "g2", "")
		Expect(err).To(MatchError(store.ErrNotBlacklisted))

		all, err := db.AllBlacklists(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(ConsistOf(HaveField("ChannelID", "UCa")))
	})

	It("adds and removes filters by kind", func() {
		for _, f := range []store.Filter{
			{GuildID: "g1", Kind: store.FilterBanned, Pattern: "spoiler"},
			{GuildID: "g1", Kind: store.FilterWanted, Pattern: "spoiler"},
			{GuildID: "g1", Kind: store.FilterWanted, Pattern: "es:"},
		} {
			created, err := db.AddFilter(ctx, f)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeTrue())
		}

		created, err := db.AddFilter(ctx, store.Filter{GuildID: "g1", Kind: store.FilterWanted, Pattern: "es:"})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())

		Expect(db.RemoveFilter(ctx, "g1", store.FilterBanned, "spoiler")).To(Succeed())
		Expect(db.RemoveFilter(ctx, "g1", store.FilterBanned, "spoiler")).To(MatchError(store.ErrFilterNotFound))

		all, err := db.AllFilters(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))
		Expect(all).To(HaveEach(HaveField("Kind", store.FilterWanted)))
	})
})
