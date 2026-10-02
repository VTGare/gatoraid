//go:build live

package posts_test

import (
	"context"
	"time"

	"github.com/VTGare/gatoraid/youtube/posts"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live YouTube posts", func() {
	It("reads a channel's posts", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		DeferCleanup(cancel)

		list, err := posts.New().Posts(ctx, "UCL_qhgtOy0dy1Agp8vkySQg")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).NotTo(BeEmpty())
		for _, p := range list {
			Expect(p.ID).NotTo(BeEmpty())
			Expect(p.ChannelID).To(Equal("UCL_qhgtOy0dy1Agp8vkySQg"))
			Expect(p.Author).To(ContainSubstring("Mori Calliope"))
		}

		_, err = posts.New().Posts(ctx, "UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(err).To(MatchError(posts.ErrNotFound))
	})
})
