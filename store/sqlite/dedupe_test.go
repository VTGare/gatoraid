package sqlite_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/VTGare/gatoraid/store/sqlite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Dedupe", func() {
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
	})

	It("returns only keys it hasn't seen, per kind", func() {
		fresh, err := db.MarkSeen(ctx, "post", []string{"a", "b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).To(Equal([]string{"a", "b"}))

		fresh, err = db.MarkSeen(ctx, "post", []string{"c", "a", "b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).To(Equal([]string{"c"}))

		fresh, err = db.MarkSeen(ctx, "other", []string{"a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).To(Equal([]string{"a"}))
	})

	It("prunes keys that stopped turning up", func() {
		_, err := db.MarkSeen(ctx, "post", []string{"old", "kept"})
		Expect(err).NotTo(HaveOccurred())
		cutoff := time.Now().Add(time.Millisecond)
		time.Sleep(5 * time.Millisecond)
		_, err = db.MarkSeen(ctx, "post", []string{"kept"})
		Expect(err).NotTo(HaveOccurred())

		n, err := db.PruneSeen(ctx, cutoff)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))

		fresh, err := db.MarkSeen(ctx, "post", []string{"old", "kept"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).To(Equal([]string{"old"}))
	})
})
