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

var _ = Describe("Notices", func() {
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

	It("claims each notice once per channel and kind", func() {
		n := store.Notice{GuildID: "g", VideoID: "v", Kind: store.NoticePrechat, ChannelID: "c"}

		for _, tc := range []struct {
			notice store.Notice
			new    bool
		}{
			{n, true},
			{n, false},
			{store.Notice{GuildID: "g", VideoID: "v", Kind: store.NoticeRelay, ChannelID: "c"}, true},
			{store.Notice{GuildID: "g", VideoID: "v", Kind: store.NoticePrechat, ChannelID: "other"}, true},
		} {
			created, err := db.ClaimNotice(ctx, tc.notice)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(Equal(tc.new))
		}

		Expect(db.SetNoticeMessage(ctx, n, "m")).To(Succeed())
	})

	It("prunes old notices", func() {
		n := store.Notice{GuildID: "g", VideoID: "v", Kind: store.NoticeRelay, ChannelID: "c"}
		_, err := db.ClaimNotice(ctx, n)
		Expect(err).NotTo(HaveOccurred())

		pruned, err := db.PruneNotices(ctx, time.Now().Add(-time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(pruned).To(BeZero())

		pruned, err = db.PruneNotices(ctx, time.Now().Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(pruned).To(Equal(1))

		created, err := db.ClaimNotice(ctx, n)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	})

	It("claims each log once per channel and prunes old claims", func() {
		for _, tc := range []struct {
			channel string
			new     bool
		}{{"c", true}, {"c", false}, {"other", true}} {
			created, err := db.ClaimLog(ctx, "g", "v", tc.channel)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(Equal(tc.new))
		}

		n, err := db.PruneLogs(ctx, time.Now().Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))

		created, err := db.ClaimLog(ctx, "g", "v", "c")
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	})
})
