//go:build live

package chat

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/holodex/tldex"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/youtube/livechat"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live chat manager", func() {
	It("reads the busiest live stream with TLdex merged in", func() {
		key := os.Getenv("GATORAID_HOLODEX_API_KEY")
		if key == "" {
			Skip("GATORAID_HOLODEX_API_KEY isn't set")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, st := range seed.Streamers {
			ids = append(ids, st.ChannelID)
		}
		videos, err := holodex.New(key).UsersLive(ctx, ids)
		Expect(err).NotTo(HaveOccurred())

		videos = slices.DeleteFunc(videos, func(v holodex.Video) bool {
			return v.Status != holodex.StatusLive || v.TopicID == "membersonly" || v.TopicID == "FreeChat"
		})
		if len(videos) == 0 {
			Skip("nobody is live")
		}
		slices.SortFunc(videos, func(a, b holodex.Video) int { return b.LiveViewers - a.LiveViewers })
		video := videos[0]

		tl := tldex.New()
		go func() { _ = tl.Run(ctx) }()

		youtube := livechat.New()
		m := NewManager(Config{
			Open: func(ctx context.Context, id string) (Reader, error) {
				c, err := youtube.Open(ctx, id)
				if err != nil {
					return nil, err
				}
				return c, nil
			},
			TLdex: tl,
		})
		m.Start(ctx, Target{VideoID: video.ID})

		counts := map[Source]int{}
		started := false
		for deadline := time.After(40 * time.Second); ; {
			select {
			case e := <-m.Events():
				switch e.Kind {
				case EventComment:
					counts[e.Comment.Source]++
				case EventStarted:
					started = true
				case EventStopped:
					Fail("session stopped: " + e.Err.Error())
				}
				continue
			case <-deadline:
			}
			break
		}

		GinkgoWriter.Printf("%s (%d viewers): %v, TLdex start time: %v\n", video.ID, video.LiveViewers, counts, started)
		Expect(counts[SourceYouTube]).To(BeNumerically(">", 0))
		cancel()
		m.Wait()
	})
})
