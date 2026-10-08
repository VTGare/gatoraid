//go:build live

package irc_test

import (
	"context"
	"os"
	"time"

	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/twitch/helix"
	"github.com/VTGare/gatoraid/twitch/irc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live Twitch chat", func() {
	It("reads lines from seed streamers who are live", func() {
		id, secret := os.Getenv("GATORAID_TWITCH_CLIENT_ID"), os.Getenv("GATORAID_TWITCH_CLIENT_SECRET")
		if id == "" || secret == "" {
			Skip("GATORAID_TWITCH_CLIENT_ID and GATORAID_TWITCH_CLIENT_SECRET aren't set, so there's no way to find live chats")
		}

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		var usernames []string
		for _, st := range seed.Streamers {
			if st.Twitch != "" {
				usernames = append(usernames, st.Twitch)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		live, err := helix.New(id, secret).Streams(ctx, usernames)
		Expect(err).NotTo(HaveOccurred())
		if len(live) == 0 {
			Skip("no seed streamer is live on Twitch right now")
		}

		client := irc.New()
		go func() { _ = client.Run(ctx) }()

		lines := make(chan *irc.Message, 64)
		for _, s := range live[:min(20, len(live))] {
			ch, stop := client.Subscribe(s.Username)
			DeferCleanup(stop)
			go func() {
				for m := range ch {
					lines <- m
				}
			}()
		}

		var m *irc.Message
		Eventually(lines, 2*time.Minute).Should(Receive(&m))
		Expect(m.ID).NotTo(BeEmpty())
		Expect(m.UserID).NotTo(BeEmpty())
		Expect(m.Time).To(BeTemporally("~", time.Now(), time.Minute))
	})
})
