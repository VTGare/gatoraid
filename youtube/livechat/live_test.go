//go:build live

package livechat_test

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/youtube/livechat"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Live YouTube chat", Ordered, func() {
	var (
		ctx    context.Context
		videos []holodex.Video
		client *livechat.Client
	)

	BeforeAll(func() {
		key := os.Getenv("GATORAID_HOLODEX_API_KEY")
		if key == "" {
			Skip("GATORAID_HOLODEX_API_KEY isn't set")
		}
		ctx = context.Background()
		client = livechat.New()

		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, st := range seed.Streamers {
			ids = append(ids, st.ChannelID)
		}
		videos, err = holodex.New(key).UsersLive(ctx, ids)
		Expect(err).NotTo(HaveOccurred())
	})

	pick := func(match func(holodex.Video) bool) *holodex.Video {
		var found []holodex.Video
		for _, v := range videos {
			if v.Type == "stream" && match(v) {
				found = append(found, v)
			}
		}
		if len(found) == 0 {
			return nil
		}
		slices.SortFunc(found, func(a, b holodex.Video) int { return b.LiveViewers - a.LiveViewers })
		return &found[0]
	}

	It("reads the busiest public live chat", func() {
		v := pick(func(v holodex.Video) bool {
			return v.Status == holodex.StatusLive && v.TopicID != "membersonly" && v.TopicID != "FreeChat"
		})
		if v == nil {
			Skip("nobody is live")
		}
		GinkgoWriter.Printf("reading %s (%d viewers): %s\n", v.ID, v.LiveViewers, v.Title)

		chat, err := client.Open(ctx, v.ID)
		Expect(err).NotTo(HaveOccurred())

		var msgs []livechat.Message
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); {
			time.Sleep(chat.Wait())
			batch, err := chat.Poll(ctx)
			Expect(err).NotTo(HaveOccurred())
			msgs = append(msgs, batch...)
		}

		badges := map[string]int{}
		for _, m := range msgs {
			Expect(m.ID).NotTo(BeEmpty())
			Expect(m.AuthorChannelID).To(HavePrefix("UC"))
			Expect(m.Time.IsZero()).To(BeFalse())
			for name, on := range map[string]bool{"owner": m.Owner, "moderator": m.Moderator, "verified": m.Verified, "member": m.Member, "superchat": m.SuperChat != ""} {
				if on {
					badges[name]++
				}
			}
		}
		GinkgoWriter.Printf("%d messages in 30s, wait %s, badges %v\n", len(msgs), chat.Wait(), badges)
	})

	It("reads prechat of an upcoming stream", func() {
		now := time.Now()
		v := pick(func(v holodex.Video) bool {
			return v.Status == holodex.StatusUpcoming && v.TopicID != "membersonly" && v.TopicID != "FreeChat" &&
				v.StartScheduled.After(now) && v.StartScheduled.Before(now.Add(12*time.Hour))
		})
		if v == nil {
			Skip("no upcoming stream in the next 12 hours")
		}

		chat, err := client.Open(ctx, v.ID)
		Expect(err).NotTo(HaveOccurred())
		GinkgoWriter.Printf("prechat %s opened, wait %s\n", v.ID, chat.Wait())
	})

	It("reports members-only chats as unavailable", func() {
		v := pick(func(v holodex.Video) bool { return v.TopicID == "membersonly" })
		if v == nil {
			Skip("no members-only stream right now")
		}

		_, err := client.Open(ctx, v.ID)
		Expect(err).To(MatchError(livechat.ErrUnavailable))
		GinkgoWriter.Printf("members-only %s: %v\n", v.ID, err)
	})
})
