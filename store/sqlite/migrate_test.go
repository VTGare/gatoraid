package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Migrations", func() {
	It("keeps subscriptions when 0003 copies their table", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "old.db")

		old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
		Expect(err).NotTo(HaveOccurred())
		_, err = old.ExecContext(ctx, `CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL DEFAULT 0) STRICT`)
		Expect(err).NotTo(HaveOccurred())
		for i, name := range []string{"0001_baseline", "0002_drop_notice_message_id"} {
			migration, err := os.ReadFile(filepath.Join("migrations", name+".sql"))
			Expect(err).NotTo(HaveOccurred())
			_, err = old.ExecContext(ctx, string(migration))
			Expect(err).NotTo(HaveOccurred())
			_, err = old.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, i+1, name[5:])
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = old.ExecContext(ctx, `INSERT INTO guilds (id, joined_at) VALUES ('g', 1)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = old.ExecContext(ctx, `INSERT INTO subscriptions (id, guild_id, feature, target_kind, target,
			discord_channel_id, role_id, created_by, created_at) VALUES (7, 'g', 'youtube', 'channel', 'UCa', 'c', 'r', 'u', 5)`)
		Expect(err).NotTo(HaveOccurred())
		Expect(old.Close()).To(Succeed())

		db, err := sqlite.Open(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		subs, err := db.GuildSubscriptions(ctx, "g", store.FeatureYouTube)
		Expect(err).NotTo(HaveOccurred())
		Expect(subs).To(HaveLen(1))
		Expect(subs[0].ID).To(Equal(int64(7)))
		Expect(subs[0].Target).To(Equal(store.Target{Kind: store.TargetChannel, ID: "UCa"}))
		Expect(subs[0].RoleID).To(Equal("r"))

		created, err := db.AddSubscription(ctx, store.Subscription{
			GuildID: "g", Feature: store.FeatureTwitch, Target: store.Target{Kind: store.TargetChannel, ID: "UCa"},
			ChannelID: "c", CreatedBy: "u",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		// The copy still cascades from guilds.
		Expect(db.LeaveGuild(ctx, "g", time.Now())).To(Succeed())
		Expect(db.PurgeGuilds(ctx, time.Now().Add(time.Hour))).To(Equal(1))
		all, err := db.Subscriptions(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(BeEmpty())
	})
})
