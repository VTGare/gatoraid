package sqlite_test

import (
	"context"
	"path/filepath"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Guild roles", func() {
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

	It("replaces one kind of role at a time", func() {
		Expect(db.SetGuildRoles(ctx, "g", store.RoleManager, []string{"r2", "r1", "r1"})).To(Succeed())
		Expect(db.SetGuildRoles(ctx, "g", store.RoleBlacklister, []string{"r3"})).To(Succeed())

		roles, err := db.GuildRoles(ctx, "g", store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(Equal([]string{"r1", "r2"}))

		Expect(db.SetGuildRoles(ctx, "g", store.RoleManager, nil)).To(Succeed())
		roles, err = db.GuildRoles(ctx, "g", store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(BeEmpty())

		roles, err = db.GuildRoles(ctx, "g", store.RoleBlacklister)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(Equal([]string{"r3"}))

		Expect(db.SetGuildRoles(ctx, "nowhere", store.RoleManager, []string{"r"})).To(MatchError(store.ErrGuildNotFound))
	})
})
