//go:build live

package stream_test

import (
	"context"
	"os"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live tracker", func() {
	It("polls the whole seed", func() {
		key := os.Getenv("GATORAID_HOLODEX_API_KEY")
		if key == "" {
			Skip("GATORAID_HOLODEX_API_KEY isn't set")
		}

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())

		var ids []string
		freeChat := map[string]bool{}
		for _, st := range seed.Streamers {
			ids = append(ids, st.ChannelID)
			freeChat[st.ChannelID] = st.FreeChatStreams
		}

		var avatars map[string]string
		tracker := stream.NewTracker(stream.Config{
			Source:      holodex.New(key),
			Channels:    func() []string { return ids },
			Classifier:  stream.Classifier{FreeChatStreams: func(id string) bool { return freeChat[id] }},
			PrechatLead: 24 * time.Hour,
			OnAvatars:   func(_ context.Context, a map[string]string) { avatars = a },
		})

		// Every upcoming room is an event, more than the channel holds, so
		// read while polling.
		polled := make(chan error)
		go func() { polled <- tracker.Poll(context.Background()) }()

		counts := map[stream.EventKind]int{}
	read:
		for {
			select {
			case e := <-tracker.Events():
				counts[e.Kind]++
			case err := <-polled:
				Expect(err).NotTo(HaveOccurred())
				break read
			}
		}
		for len(tracker.Events()) > 0 {
			counts[(<-tracker.Events()).Kind]++
		}

		var members, freeChats int
		for _, s := range tracker.Streams() {
			if s.MembersOnly {
				members++
				GinkgoWriter.Printf("members-only: %s\n", s.Title)
			}
			if s.FreeChat {
				freeChats++
			}
		}

		GinkgoWriter.Printf("tracking %d streams: %d live, %d prechat, %d members-only, %d free chat; %d avatars\n",
			len(tracker.Streams()), counts[stream.EventLive], counts[stream.EventPrechat], members, freeChats, len(avatars))
		Expect(tracker.Streams()).NotTo(BeEmpty())
		Expect(avatars).NotTo(BeEmpty())
	})
})
