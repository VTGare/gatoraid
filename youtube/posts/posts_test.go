package posts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/VTGare/gatoraid/youtube/posts"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Posts", func() {
	var (
		files  map[string]string
		bodies []map[string]any
		client *posts.Client
	)

	BeforeEach(func() {
		files = map[string]string{}
		bodies = nil
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.URL.Path).To(Equal("/youtubei/v1/browse"))

			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			Expect(json.Unmarshal(raw, &body)).To(Succeed())
			bodies = append(bodies, body)

			data, err := os.ReadFile("testdata/" + files[body["browseId"].(string)])
			Expect(err).NotTo(HaveOccurred())
			_, _ = w.Write(data)
		}))
		DeferCleanup(srv.Close)
		client = posts.New(posts.WithBaseURL(srv.URL))
	})

	It("reads posts with their attachments, newest first", func() {
		files["UCcalli"] = "posts.json"

		list, err := client.Posts(context.Background(), "UCcalli")

		Expect(err).NotTo(HaveOccurred())
		Expect(bodies[0]["params"]).To(Equal("EgVwb3N0c_IGBAoCSgA="))
		Expect(list).To(HaveLen(4))

		first := list[0]
		Expect(first.ID).To(Equal("UgkxmUTZPiKzNTrrMbEAamADMVqaS8IUjLtj"))
		Expect(first.URL()).To(Equal("https://www.youtube.com/post/UgkxmUTZPiKzNTrrMbEAamADMVqaS8IUjLtj"))
		Expect(first.ChannelID).To(Equal("UCL_qhgtOy0dy1Agp8vkySQg"))
		Expect(first.Author).To(Equal("Mori Calliope Ch. hololive"))
		Expect(first.AvatarURL).To(HavePrefix("https://yt3.googleusercontent.com/"))
		Expect(first.Text).To(HavePrefix("!! 6th Anniversary Goods NOW ON SALE !"))
		Expect(first.Text).To(ContainSubstring("https://shop.hololivepro.com/products/moricalliope_anniversary_6th"))
		Expect(first.Text).NotTo(ContainSubstring("products..."))
		Expect(first.Images).To(HaveLen(8))
		Expect(first.Recent).To(BeTrue())

		Expect(list[1].Images).To(HaveLen(1))
		Expect(list[1].Recent).To(BeFalse())
		Expect(list[2].VideoID).To(Equal("7xYekUcLRs4"))
		Expect(list[2].Images).To(BeEmpty())
	})

	It("reads poll choices", func() {
		files["UCpoll"] = "poll.json"

		list, err := client.Posts(context.Background(), "UCpoll")

		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Poll).To(Equal([]string{"Yes (Physical Reminder + It's Merch)", "No (Use for Other Projects)"}))
	})

	It("finds no posts on channels without a Posts tab", func() {
		files["UChome"] = "no_posts.json"

		list, err := client.Posts(context.Background(), "UChome")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("reports channels that don't exist", func() {
		files["UCnobody"] = "unknown.json"

		_, err := client.Posts(context.Background(), "UCnobody")
		Expect(err).To(MatchError(posts.ErrNotFound))
	})
})
