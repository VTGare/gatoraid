package guilds_test

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/guilds"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("State", func() {
	var (
		ctx context.Context
		db  *sqlite.Store
		svc *guilds.State
	)

	join := func(id string) {
		GinkgoHelper()
		_, err := svc.Join(ctx, id)
		Expect(err).NotTo(HaveOccurred())
	}

	settings := func(id string) store.Settings {
		GinkgoHelper()
		g, ok := svc.Guild(id)
		Expect(ok).To(BeTrue())
		return g.Settings
	}

	saved := func(id string) store.Settings {
		GinkgoHelper()
		g, err := db.Guild(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		return g.Settings
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		svc = guilds.New(db)
	})

	It("loads settings and left guilds from the store", func() {
		for _, id := range []string{"g1", "g2"} {
			_, _, err := db.JoinGuild(ctx, id)
			Expect(err).NotTo(HaveOccurred())
		}
		s := store.DefaultSettings()
		s.Prechat = false
		Expect(db.UpdateGuildSettings(ctx, "g1", s)).To(Succeed())
		Expect(db.LeaveGuild(ctx, "g2", time.Now())).To(Succeed())

		Expect(svc.Reload(ctx)).To(Succeed())

		Expect(settings("g1").Prechat).To(BeFalse())
		g2, ok := svc.Guild("g2")
		Expect(ok).To(BeTrue())
		Expect(g2.Active()).To(BeFalse())
		_, ok = svc.Guild("nope")
		Expect(ok).To(BeFalse())
	})

	Describe("Update", func() {
		BeforeEach(func() { join("g1") })

		It("edits the current settings, saves them and signals the change", func() {
			Expect(svc.Update(ctx, "g1", func(s *store.Settings) { s.Prechat = false })).To(Succeed())
			Expect(svc.Update(ctx, "g1", func(s *store.Settings) { s.TargetLanguage = "JA" })).To(Succeed())

			Expect(settings("g1").Prechat).To(BeFalse())
			Expect(settings("g1").TargetLanguage).To(Equal("JA"))
			Expect(saved("g1")).To(Equal(settings("g1")))
			Expect(svc.Changed()).To(Receive())
			Expect(svc.Changed()).NotTo(Receive())
		})

		It("keeps both of two updates that run at once", func() {
			var wg sync.WaitGroup
			for _, edit := range []func(*store.Settings){
				func(s *store.Settings) { s.Prechat = false },
				func(s *store.Settings) { s.ShowChat = false },
			} {
				wg.Go(func() {
					defer GinkgoRecover()
					Expect(svc.Update(ctx, "g1", edit)).To(Succeed())
				})
			}
			wg.Wait()

			Expect(saved("g1").Prechat).To(BeFalse())
			Expect(saved("g1").ShowChat).To(BeFalse())
		})

		It("refuses unknown guilds without a signal", func() {
			Expect(svc.Update(ctx, "nope", func(*store.Settings) {})).To(MatchError(store.ErrGuildNotFound))
			Expect(svc.Changed()).NotTo(Receive())
		})

		It("leaves the index alone when the store fails", func() {
			Expect(db.Close()).To(Succeed())

			Expect(svc.Update(ctx, "g1", func(s *store.Settings) { s.Prechat = false })).NotTo(Succeed())

			Expect(settings("g1").Prechat).To(BeTrue())
			Expect(svc.Changed()).NotTo(Receive())
		})
	})

	It("keeps settings when a guild leaves and comes back", func() {
		kind, err := svc.Join(ctx, "g1")
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(store.JoinNew))
		Expect(settings("g1")).To(Equal(store.DefaultSettings()))

		Expect(svc.Update(ctx, "g1", func(s *store.Settings) { s.Prechat = false })).To(Succeed())

		at := time.Now()
		Expect(svc.Leave(ctx, "g1", at)).To(Succeed())
		g, _ := svc.Guild("g1")
		Expect(g.Active()).To(BeFalse())
		Expect(g.LeftAt.UnixMilli()).To(Equal(at.UnixMilli()))

		kind, err = svc.Join(ctx, "g1")
		Expect(err).NotTo(HaveOccurred())
		Expect(kind).To(Equal(store.JoinRestored))
		g, _ = svc.Guild("g1")
		Expect(g.Active()).To(BeTrue())
		Expect(g.Settings.Prechat).To(BeFalse())
	})

	It("reconciles guilds that left while offline and purges old ones", func() {
		join("g1")
		join("g2")

		left, err := svc.Reconcile(ctx, []string{"g1"}, time.Now().Add(-time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(left).To(Equal([]string{"g2"}))
		g, _ := svc.Guild("g2")
		Expect(g.Active()).To(BeFalse())

		n, err := svc.Purge(ctx, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		_, ok := svc.Guild("g2")
		Expect(ok).To(BeFalse())
		_, ok = svc.Guild("g1")
		Expect(ok).To(BeTrue())
	})
})
