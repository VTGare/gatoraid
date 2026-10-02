//go:build live

package channel_test

import (
	"context"
	"time"

	"github.com/VTGare/gatoraid/youtube/channel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live YouTube channel pages", func() {
	It("resolves handles, IDs and unknown channels", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		DeferCleanup(cancel)
		client := channel.New()

		for _, input := range []string{"@MoriCalliope", "UCL_qhgtOy0dy1Agp8vkySQg", "https://www.youtube.com/@MoriCalliope/live"} {
			ch, err := client.Resolve(ctx, input)
			Expect(err).NotTo(HaveOccurred(), input)
			Expect(ch.ID).To(Equal("UCL_qhgtOy0dy1Agp8vkySQg"), input)
			Expect(ch.Name).To(ContainSubstring("Mori Calliope"), input)
			Expect(ch.Handle).To(Equal("@MoriCalliope"), input)
			Expect(ch.AvatarURL).NotTo(BeEmpty(), input)
		}

		_, err := client.Resolve(ctx, "UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(err).To(MatchError(channel.ErrNotFound))
		_, err = client.Resolve(ctx, "@thishandledoesnotexist9q8w7e")
		Expect(err).To(MatchError(channel.ErrNotFound))
	})
})
