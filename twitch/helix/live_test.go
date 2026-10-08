//go:build live

package helix_test

import (
	"context"
	"os"

	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/twitch/helix"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live Twitch API", func() {
	It("checks every seed username in batches", func() {
		id, secret := os.Getenv("GATORAID_TWITCH_CLIENT_ID"), os.Getenv("GATORAID_TWITCH_CLIENT_SECRET")
		if id == "" || secret == "" {
			Skip("GATORAID_TWITCH_CLIENT_ID and GATORAID_TWITCH_CLIENT_SECRET aren't set")
		}

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		usernames := map[string]bool{}
		var list []string
		for _, st := range seed.Streamers {
			if st.Twitch != "" {
				usernames[st.Twitch] = true
				list = append(list, st.Twitch)
			}
		}
		Expect(len(list)).To(BeNumerically(">", 100))

		streams, err := helix.New(id, secret).Streams(context.Background(), list)

		Expect(err).NotTo(HaveOccurred())
		GinkgoWriter.Printf("%d of %d seed Twitch channels are live\n", len(streams), len(list))
		for _, s := range streams {
			Expect(usernames).To(HaveKey(s.Username))
			Expect(s.Type).To(Equal("live"))
			Expect(s.ID).NotTo(BeEmpty())
			Expect(s.StartedAt).NotTo(BeZero())
			Expect(s.ThumbnailURL).To(ContainSubstring("{width}x{height}"))
		}
	})
})
