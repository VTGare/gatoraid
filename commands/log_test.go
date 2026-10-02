package commands_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	dt "github.com/VTGare/gatoraid/internal/discordtest"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("/log", func() {
	const videoID = "dQw4w9WgXcQ"

	var (
		h     *harness
		ctx   context.Context
		start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	)

	BeforeEach(func() {
		ctx = context.Background()
		h = newHarness(&streamers.Seed{})
		_, _, err := h.b.Store.JoinGuild(ctx, "guild")
		Expect(err).NotTo(HaveOccurred())
	})

	save := func(guild string, d time.Duration, author, body string) {
		GinkgoHelper()
		Expect(h.b.Store.SaveLines(ctx, []store.Line{{
			VideoID: videoID, GuildID: guild, AuthorChannelID: "UC" + author, AuthorName: "@" + author,
			Body: body, Kind: store.LineTL, SaidAt: start.Add(d),
		}})).To(Succeed())
	}

	// /log is deferred, so the file arrives as an edit of the response.
	logFile := func(video string) (string, string) {
		GinkgoHelper()
		h.run(dt.Command(user, "log", dt.String("video", video)))
		edits := h.rec.Edits()
		Expect(edits).NotTo(BeEmpty())
		last := edits[len(edits)-1]
		content, _ := last.Body["content"].(string)
		return content, last.Files[videoID+".txt"]
	}

	It("sends the server's lines, with its blacklist applied", func() {
		save("guild", 0, "a", "[EN] one")
		save("guild", time.Minute, "spam", "[EN] buy")
		save("", 0, "archived", "[EN] archive only")
		_, err := h.b.Moderation.AddToBlacklist(ctx, store.BlacklistEntry{GuildID: "guild", ChannelID: "UCspam", AddedBy: "mod"})
		Expect(err).NotTo(HaveOccurred())

		content, file := logFile("https://www.youtube.com/watch?v=" + videoID + "&t=10")

		Expect(content).To(Equal("Log for [" + videoID + "](<https://youtu.be/" + videoID + ">)"))
		Expect(file).To(Equal("https://youtu.be/" + videoID + "\nTimes count from the first line.\n\n[0:00:00] @a: [EN] one\n"))
	})

	It("falls back to the archive", func() {
		save("", 30*time.Second, "archived", "[EN] archive only")

		_, file := logFile("https://youtu.be/" + videoID)
		Expect(file).To(HaveSuffix("[0:00:00] @archived: [EN] archive only\n"))
	})

	It("takes the title and start from Holodex", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"` + videoID + `","title":"Karaoke","status":"past","available_at":"2026-10-02T12:00:00.000Z"}`))
		}))
		DeferCleanup(srv.Close)
		h.b.Holodex = holodex.New("key", holodex.WithBaseURL(srv.URL))
		save("guild", 90*time.Second, "a", "[EN] one")

		content, file := logFile(videoID)
		Expect(content).To(HavePrefix("Log for [Karaoke]"))
		Expect(file).To(Equal("Karaoke\nhttps://youtu.be/" + videoID + "\nStarted 2026-10-02 12:00 UTC\n\n[0:01:30] @a: [EN] one\n"))
	})

	It("explains missing logs and bad input", func() {
		h.run(dt.Command(user, "log", dt.String("video", videoID)))
		Expect(h.rec.Edits()[len(h.rec.Edits())-1].Body["content"]).To(ContainSubstring("I have no lines from that stream"))

		h.run(dt.Command(user, "log", dt.String("video", "not a video")))
		Expect(h.rec.Edits()[len(h.rec.Edits())-1].Body["content"]).To(ContainSubstring("not a YouTube video"))
	})

	It("is open to everyone", func() {
		save("guild", 0, "a", "[EN] one")
		i := dt.Command(user, "log", dt.String("video", videoID))
		i.Member.Permissions = 0
		h.run(i)
		Expect(h.rec.Edits()[len(h.rec.Edits())-1].Files).To(HaveKey(videoID + ".txt"))
	})
})
