package sqlite_test

import (
	"context"
	"path/filepath"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Streamers", func() {
	var (
		ctx context.Context
		db  *sqlite.Store
	)

	groups := []store.Group{
		{ID: "holo-en", Name: "Hololive EN", ParentID: "holo"}, // child first on purpose
		{ID: "holo", Name: "Hololive"},
		{ID: "holo-id", Name: "Hololive ID", ParentID: "holo", SkipAutoTranslate: true},
	}

	ame := store.Streamer{ChannelID: "UCame", Name: "Watson Amelia", GroupID: "holo-en", Twitter: "watsonameliaen", Aliases: []string{"ame"}}
	kiara := store.Streamer{ChannelID: "UCkiara", Name: "Takanashi Kiara", GroupID: "holo-en"}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
	})

	It("adds groups in order and streamers from the seed", func() {
		res, err := db.SyncSeed(ctx, groups, []store.Streamer{ame, kiara})

		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Added: 2}))

		got, err := db.StreamerGroups(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(groups))

		st, err := db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Name).To(Equal("Watson Amelia"))
		Expect(st.Aliases).To(Equal([]string{"ame"}))
		Expect(st.Source).To(Equal(store.SourceSeed))
		Expect(st.Curated()).To(BeTrue())
	})

	It("only counts real changes as updates on later syncs", func() {
		_, err := db.SyncSeed(ctx, groups, []store.Streamer{ame, kiara})
		Expect(err).NotTo(HaveOccurred())

		res, err := db.SyncSeed(ctx, groups, []store.Streamer{ame, kiara})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{}))

		renamed := ame
		renamed.Aliases = []string{"ame", "amelia"}
		res, err = db.SyncSeed(ctx, groups, []store.Streamer{renamed, kiara})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Updated: 1}))
	})

	It("removes seed streamers that left the seed", func() {
		_, err := db.SyncSeed(ctx, groups, []store.Streamer{ame, kiara})
		Expect(err).NotTo(HaveOccurred())

		res, err := db.SyncSeed(ctx, groups, []store.Streamer{ame})

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Removed).To(Equal(1))
		_, err = db.Streamer(ctx, "UCkiara")
		Expect(err).To(MatchError(store.ErrStreamerNotFound))
	})

	It("never overwrites or removes owner edits", func() {
		_, err := db.SyncSeed(ctx, groups, []store.Streamer{ame})
		Expect(err).NotTo(HaveOccurred())

		edited := ame
		edited.Name = "Amelia Watson"
		edited.Source = store.SourceOwner
		Expect(db.SaveStreamer(ctx, edited)).To(Succeed())

		res, err := db.SyncSeed(ctx, groups, []store.Streamer{ame})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Kept: 1}))

		res, err = db.SyncSeed(ctx, groups, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Removed).To(BeZero())

		st, err := db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Name).To(Equal("Amelia Watson"))
	})

	It("takes back owner entries once the seed has an identical copy", func() {
		_, err := db.SyncSeed(ctx, groups, nil)
		Expect(err).NotTo(HaveOccurred())

		added := ame
		added.Source = store.SourceOwner
		Expect(db.SaveStreamer(ctx, added)).To(Succeed())

		different := ame
		different.Aliases = []string{"amelia"}
		res, err := db.SyncSeed(ctx, groups, []store.Streamer{different})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Kept: 1}))

		res, err = db.SyncSeed(ctx, groups, []store.Streamer{ame})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(store.SeedResult{Returned: 1}))

		st, err := db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Source).To(Equal(store.SourceSeed))
	})

	It("turns a guild-added channel into a seed streamer and keeps its avatar", func() {
		_, err := db.SyncSeed(ctx, groups, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.SaveStreamer(ctx, store.Streamer{
			ChannelID: "UCame", Name: "ame ch", AvatarURL: "https://example.com/a.png",
			Source: store.SourceUser, AddedByGuild: "g1",
		})).To(Succeed())

		user, err := db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
		Expect(user.Curated()).To(BeFalse())

		res, err := db.SyncSeed(ctx, groups, []store.Streamer{ame})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Updated).To(Equal(1))

		st, err := db.Streamer(ctx, "UCame")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Source).To(Equal(store.SourceSeed))
		Expect(st.Name).To(Equal("Watson Amelia"))
		Expect(st.AddedByGuild).To(BeEmpty())
		Expect(st.AvatarURL).To(Equal("https://example.com/a.png"))
	})

	It("ungroups streamers whose group was removed", func() {
		_, err := db.SyncSeed(ctx, groups, nil)
		Expect(err).NotTo(HaveOccurred())
		owned := kiara
		owned.Source = store.SourceOwner
		Expect(db.SaveStreamer(ctx, owned)).To(Succeed())

		_, err = db.SyncSeed(ctx, groups[1:2], nil)
		Expect(err).NotTo(HaveOccurred())

		st, err := db.Streamer(ctx, "UCkiara")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.GroupID).To(BeEmpty())
	})

	It("deletes streamers", func() {
		_, err := db.SyncSeed(ctx, groups, []store.Streamer{ame})
		Expect(err).NotTo(HaveOccurred())

		Expect(db.DeleteStreamer(ctx, "UCame")).To(Succeed())
		Expect(db.DeleteStreamer(ctx, "UCame")).To(MatchError(store.ErrStreamerNotFound))

		all, err := db.Streamers(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(BeEmpty())
	})
})
