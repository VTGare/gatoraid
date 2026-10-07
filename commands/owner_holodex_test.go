package commands_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	dt "github.com/VTGare/gumi/v2/gumitest"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeSource struct{ videos []holodex.Video }

func (f fakeSource) UsersLive(context.Context, []string) ([]holodex.Video, error) {
	return f.videos, nil
}

func (f fakeSource) Video(context.Context, string) (*holodex.Video, error) {
	return nil, holodex.ErrNotFound
}

var _ = Describe("Owner Holodex tools", func() {
	var h *harness

	BeforeEach(func() {
		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		h = newHarness(seed)
	})

	It("says when there's no Holodex key", func() {
		data := h.run(dt.Command(owner, "owner", dt.Sub("streams")))
		Expect(data["content"]).To(ContainSubstring("no Holodex API key"))
	})

	It("lists tracked streams", func() {
		now := time.Now()
		h.b.Streams = stream.NewTracker(stream.Config{
			Source: fakeSource{videos: []holodex.Video{
				{ID: "live1", Type: "stream", Title: "Minecraft [part 2]", Status: holodex.StatusLive,
					Channel: holodex.Channel{ID: "UCa", Name: "A Ch."}},
				{ID: "up1", Type: "stream", Title: "Karaoke", Status: holodex.StatusUpcoming, StartScheduled: now.Add(time.Hour)},
				{ID: "mem1", Type: "stream", Title: "【MEMBERS】chat", Status: holodex.StatusUpcoming, StartScheduled: now.Add(time.Hour)},
				{ID: "fc1", Type: "stream", Title: "schedule", TopicID: "FreeChat", Status: holodex.StatusUpcoming},
			}},
			Channels: func() []string { return []string{"UCa"} },
		})
		Expect(h.b.Streams.Poll(context.Background())).To(Succeed())

		e := embed(h.run(dt.Command(owner, "owner", dt.Sub("streams"))))

		Expect(e["title"]).To(Equal("Live (1)"))
		Expect(e["description"]).To(Equal("- [Minecraft (part 2)](<https://youtu.be/live1>) · A Ch.\n"))
		Expect(e["footer"]).To(HaveKeyWithValue("text", "Also tracking 1 upcoming, 1 members-only and 1 free chat rooms"))
	})

	It("compares a Holodex org with the registry", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Query().Get("org")).To(Equal("Hololive"))
			_ = json.NewEncoder(w).Encode([]holodex.Channel{
				{ID: "UCjLEmnpCNeisMxy134KPwWw", Name: "Kobo Kanaeru Ch. hololive-ID", Inactive: true},
				{ID: "UCnewnewnewnewnewnewnewn", Name: "New Ch. hololive-EN", EnglishName: "New Talent", Twitter: "newtalent"},
				{ID: "UCgonegonegonegonegonego", Name: "Old Ch.", Inactive: true},
				{ID: "UCHsx4Hqa-1ORjQTh9TYDhww", Name: "Takanashi Kiara Ch. hololive-EN"},
			})
		}))
		DeferCleanup(srv.Close)
		h.b.Holodex = holodex.New("k", holodex.WithBaseURL(srv.URL))

		h.run(dt.Command(owner, "owner", dt.Group("streamers", dt.Sub("sync",
			dt.String("org", "Hololive"), dt.String("group", "hololive-en")))))

		edits := h.rec.Edits()
		Expect(edits).To(HaveLen(1))
		Expect(edits[0].Body["content"]).To(ContainSubstring("**Hololive**: 4 channels on Holodex. 1 in the registry are inactive there, 1 active ones aren't in the registry."))
		Expect(edits[0].Body["content"]).To(ContainSubstring("- Kobo Kanaeru (`UCjLEmnpCNeisMxy134KPwWw`)"))
		Expect(edits[0].Files["missing.toml"]).To(ContainSubstring("# streamers/seed/hololive/en.toml"))
		Expect(edits[0].Files["missing.toml"]).To(ContainSubstring(`name = "New Talent"`))
		Expect(edits[0].Files["missing.toml"]).To(ContainSubstring(`twitter = "newtalent"`))
	})
})
