//go:build live

package holodex_test

import (
	"context"
	"os"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live Holodex API", func() {
	var (
		ctx    context.Context
		client *holodex.Client
		ids    []string
	)

	BeforeEach(func() {
		key := os.Getenv("GATORAID_HOLODEX_API_KEY")
		if key == "" {
			Skip("GATORAID_HOLODEX_API_KEY isn't set")
		}
		ctx = context.Background()
		client = holodex.New(key)

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		for _, st := range seed.Streamers {
			ids = append(ids, st.ChannelID)
		}
	})

	It("polls every seed channel in batches", func() {
		videos, err := client.UsersLive(ctx, ids)

		Expect(err).NotTo(HaveOccurred())
		Expect(videos).NotTo(BeEmpty())
		for _, v := range videos {
			Expect(v.ID).NotTo(BeEmpty())
			Expect(v.Channel.ID).To(HavePrefix("UC"))
			Expect(v.Status).To(BeElementOf(holodex.StatusUpcoming, holodex.StatusLive))
		}
		GinkgoWriter.Printf("%d live or upcoming videos for %d channels\n", len(videos), len(ids))

		v, err := client.Video(ctx, videos[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.ID).To(Equal(videos[0].ID))
		Expect(v.Description).NotTo(BeEmpty())
	})

	It("looks up channels and pages through orgs", func() {
		ch, err := client.Channel(ctx, "UCHsx4Hqa-1ORjQTh9TYDhww")
		Expect(err).NotTo(HaveOccurred())
		Expect(ch.Org).To(Equal("Hololive"))

		chs, err := client.OrgChannels(ctx, "Hololive")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(chs)).To(BeNumerically(">", 50))

		_, err = client.Video(ctx, "xxxxxxxxxxx")
		Expect(err).To(MatchError(holodex.ErrNotFound))
	})
})
