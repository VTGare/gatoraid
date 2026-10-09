package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQLite store", func() {
	var (
		ctx  context.Context
		path string
		db   *sqlite.Store
	)

	BeforeEach(func() {
		ctx = context.Background()
		path = filepath.Join(GinkgoT().TempDir(), "test.db")

		var err error
		db, err = sqlite.Open(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(db.Close()).To(Succeed()) })
	})

	It("reopens an existing database without reapplying migrations", func() {
		_, _, err := db.JoinGuild(ctx, "g")
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Close()).To(Succeed())

		db, err = sqlite.Open(ctx, path)
		Expect(err).NotTo(HaveOccurred())

		g, err := db.Guild(ctx, "g")
		Expect(err).NotTo(HaveOccurred())
		Expect(g.ID).To(Equal("g"))
	})

	Describe("guilds", func() {
		It("reports a missing guild", func() {
			_, err := db.Guild(ctx, "nope")
			Expect(err).To(MatchError(store.ErrGuildNotFound))
		})

		It("lists every guild, including ones the bot left", func() {
			for _, id := range []string{"b", "a"} {
				_, _, err := db.JoinGuild(ctx, id)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(db.LeaveGuild(ctx, "b", time.Now())).To(Succeed())

			guilds, err := db.Guilds(ctx)

			Expect(err).NotTo(HaveOccurred())
			Expect(guilds).To(HaveLen(2))
			Expect(guilds[0].ID).To(Equal("a"))
			Expect(guilds[0].Active()).To(BeTrue())
			Expect(guilds[1].ID).To(Equal("b"))
			Expect(guilds[1].Active()).To(BeFalse())
			Expect(guilds[1].Settings).To(Equal(store.DefaultSettings()))
		})

		It("creates a guild with default settings on first join", func() {
			before := time.Now().Add(-time.Second)

			g, kind, err := db.JoinGuild(ctx, "g")

			Expect(err).NotTo(HaveOccurred())
			Expect(kind).To(Equal(store.JoinNew))
			Expect(g.ID).To(Equal("g"))
			Expect(g.Active()).To(BeTrue())
			Expect(g.Settings).To(Equal(store.DefaultSettings()))
			Expect(g.JoinedAt).To(BeTemporally(">", before))
		})

		It("leaves an active guild untouched on rejoin", func() {
			first, _, err := db.JoinGuild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())

			again, kind, err := db.JoinGuild(ctx, "g")

			Expect(err).NotTo(HaveOccurred())
			Expect(kind).To(Equal(store.JoinExisting))
			Expect(again.JoinedAt).To(Equal(first.JoinedAt))
		})

		It("soft-deletes on leave and restores settings on rejoin", func() {
			g, _, err := db.JoinGuild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())

			g.Settings.YouTubeModMessages = false
			g.Settings.LogChannelID = "123"
			Expect(db.UpdateGuildSettings(ctx, "g", g.Settings)).To(Succeed())

			leftAt := time.Now().Truncate(time.Millisecond)
			Expect(db.LeaveGuild(ctx, "g", leftAt)).To(Succeed())

			left, err := db.Guild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())
			Expect(left.Active()).To(BeFalse())
			Expect(left.LeftAt).To(HaveValue(BeTemporally("==", leftAt)))

			back, kind, err := db.JoinGuild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())
			Expect(kind).To(Equal(store.JoinRestored))
			Expect(back.Active()).To(BeTrue())
			Expect(back.Settings.YouTubeModMessages).To(BeFalse())
			Expect(back.Settings.LogChannelID).To(Equal("123"))
		})

		It("keeps the first leave time when left twice", func() {
			_, _, err := db.JoinGuild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())

			first := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
			Expect(db.LeaveGuild(ctx, "g", first)).To(Succeed())
			Expect(db.LeaveGuild(ctx, "g", time.Now())).To(Succeed())

			g, err := db.Guild(ctx, "g")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.LeftAt).To(HaveValue(BeTemporally("==", first)))
		})

		It("marks active guilds missing from Discord's list as left", func() {
			for _, id := range []string{"a", "b", "c", "gone"} {
				_, _, err := db.JoinGuild(ctx, id)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(db.LeaveGuild(ctx, "gone", time.Now())).To(Succeed())

			left, err := db.ReconcileGuilds(ctx, []string{"a", "c"}, time.Now())

			Expect(err).NotTo(HaveOccurred())
			Expect(left).To(ConsistOf("b"))

			b, err := db.Guild(ctx, "b")
			Expect(err).NotTo(HaveOccurred())
			Expect(b.Active()).To(BeFalse())

			a, err := db.Guild(ctx, "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(a.Active()).To(BeTrue())
		})

		It("purges only guilds left before the cutoff", func() {
			now := time.Now()
			for _, id := range []string{"old", "recent", "active"} {
				_, _, err := db.JoinGuild(ctx, id)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(db.LeaveGuild(ctx, "old", now.Add(-31*24*time.Hour))).To(Succeed())
			Expect(db.LeaveGuild(ctx, "recent", now.Add(-time.Hour))).To(Succeed())

			n, err := db.PurgeGuilds(ctx, now.Add(-store.GuildRetention))

			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))
			_, err = db.Guild(ctx, "old")
			Expect(err).To(MatchError(store.ErrGuildNotFound))
			_, err = db.Guild(ctx, "recent")
			Expect(err).NotTo(HaveOccurred())
			_, err = db.Guild(ctx, "active")
			Expect(err).NotTo(HaveOccurred())
		})

		It("reports updating a missing guild", func() {
			err := db.UpdateGuildSettings(ctx, "nope", store.DefaultSettings())
			Expect(err).To(MatchError(store.ErrGuildNotFound))
		})

		It("handles concurrent writers without busy errors", func() {
			var wg sync.WaitGroup
			errs := make(chan error, 100)
			for i := range 50 {
				wg.Go(func() {
					id := string(rune('a' + i%10))
					if _, _, err := db.JoinGuild(ctx, id); err != nil {
						errs <- err
					}
					if _, err := db.Guild(ctx, id); err != nil {
						errs <- err
					}
				})
			}
			wg.Wait()
			close(errs)

			Expect(errs).To(BeEmpty())
		})
	})
})

var _ = Describe("Settings", func() {
	It("fills fields missing from older documents with defaults", func() {
		s, err := store.DecodeSettings([]byte(`{"v": 1, "mod_messages": false}`))

		Expect(err).NotTo(HaveOccurred())
		want := store.DefaultSettings()
		want.YouTubeModMessages = false
		Expect(s).To(Equal(want))
	})

	It("round-trips", func() {
		in := store.DefaultSettings()
		in.TargetLanguage = "JA"
		in.NotifyMembersOnly = true

		data, err := store.EncodeSettings(in)
		Expect(err).NotTo(HaveOccurred())
		out, err := store.DecodeSettings(data)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(in))
	})
})
