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

var _ = Describe("Relayed lines", func() {
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

		_, _, err = db.JoinGuild(ctx, "g")
		Expect(err).NotTo(HaveOccurred())
	})

	line := func(guild, body string, at time.Time) store.Line {
		return store.Line{VideoID: "v", GuildID: guild, AuthorChannelID: "UCa", AuthorName: "@a", Body: body,
			Kind: store.LineTL, SaidAt: at}
	}

	It("keeps guild lines apart from the archive", func() {
		now := time.Now().Truncate(time.Millisecond)
		guildLine := line("g", "second", now)
		guildLine.ChannelID, guildLine.MessageID = "c", "m"

		Expect(db.SaveLines(ctx, []store.Line{guildLine, line("g", "first", now.Add(-time.Second)), line("", "archived", now)})).To(Succeed())

		lines, err := db.VideoLines(ctx, "v", "g")
		Expect(err).NotTo(HaveOccurred())
		Expect(lines).To(HaveLen(2))
		Expect(lines[0].Body).To(Equal("first"))
		Expect(lines[1]).To(Equal(guildLine))

		archive, err := db.VideoLines(ctx, "v", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(archive).To(ConsistOf(HaveField("Body", "archived")))
	})

	It("prunes guild lines and the archive on their own schedules", func() {
		now := time.Now()
		Expect(db.SaveLines(ctx, []store.Line{
			line("g", "recent", now.Add(-2*24*time.Hour)),
			line("g", "old", now.Add(-8*24*time.Hour)),
			line("", "recent", now.Add(-time.Hour)),
			line("", "old", now.Add(-2*24*time.Hour)),
		})).To(Succeed())

		n, err := db.PruneLines(ctx, now.Add(-store.GuildLineRetention), now.Add(-store.ArchiveLineRetention))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))

		for _, guild := range []string{"g", ""} {
			lines, err := db.VideoLines(ctx, "v", guild)
			Expect(err).NotTo(HaveOccurred())
			Expect(lines).To(ConsistOf(HaveField("Body", "recent")))
		}
	})

	It("finds the line behind a Discord message", func() {
		l := line("g", "hello", time.Now().Truncate(time.Millisecond))
		l.ChannelID, l.MessageID = "c", "m1"
		Expect(db.SaveLines(ctx, []store.Line{l})).To(Succeed())

		found, err := db.LineByMessage(ctx, "g", "m1")
		Expect(err).NotTo(HaveOccurred())
		Expect(*found).To(Equal(l))

		_, err = db.LineByMessage(ctx, "other", "m1")
		Expect(err).To(MatchError(store.ErrLineNotFound))
	})

	It("lists recent authors by name, latest first", func() {
		now := time.Now()
		author := func(id, name string, at time.Time) store.Line {
			l := line("g", "x", at)
			l.AuthorChannelID, l.AuthorName = id, name
			return l
		}
		Expect(db.SaveLines(ctx, []store.Line{
			author("UCa", "@old_name", now.Add(-time.Hour)),
			author("UCb", "@bob", now.Add(-30*time.Minute)),
			author("UCa", "@new_name", now),
			author("UCc", "@carol%", now.Add(-time.Minute)),
		})).To(Succeed())

		authors, err := db.RecentAuthors(ctx, "g", "", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(authors).To(HaveExactElements(
			HaveField("AuthorName", "@new_name"), HaveField("AuthorName", "@carol%"), HaveField("AuthorName", "@bob"),
		))

		authors, err = db.RecentAuthors(ctx, "g", "_name", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(authors).To(ConsistOf(HaveField("AuthorName", "@new_name")))

		authors, err = db.RecentAuthors(ctx, "g", "%", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(authors).To(ConsistOf(HaveField("AuthorName", "@carol%")))

		authors, err = db.RecentAuthors(ctx, "g", "", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(authors).To(HaveLen(1))
	})
})
