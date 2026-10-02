package channel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/VTGare/gatoraid/youtube/channel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Path", func() {
	DescribeTable("accepted input",
		func(input, want string) {
			Expect(channel.Path(input)).To(Equal(want))
		},
		Entry("channel ID", "UCL_qhgtOy0dy1Agp8vkySQg", "/channel/UCL_qhgtOy0dy1Agp8vkySQg"),
		Entry("handle", " @MoriCalliope ", "/@MoriCalliope"),
		Entry("Japanese handle", "@ぺこら", "/@%E3%81%BA%E3%81%93%E3%82%89"),
		Entry("handle URL with a tab", "https://www.youtube.com/@MoriCalliope/streams", "/@MoriCalliope"),
		Entry("channel URL", "youtube.com/channel/UCL_qhgtOy0dy1Agp8vkySQg/videos", "/channel/UCL_qhgtOy0dy1Agp8vkySQg"),
		Entry("mobile URL", "https://m.youtube.com/@MoriCalliope", "/@MoriCalliope"),
		Entry("legacy URL", "https://www.youtube.com/c/MoriCalliope", "/c/MoriCalliope"),
	)

	DescribeTable("rejected input",
		func(input string) {
			_, err := channel.Path(input)
			Expect(err).To(MatchError(channel.ErrInvalid))
		},
		Entry("a name", "Mori Calliope"),
		Entry("a lone @", "@"),
		Entry("a video URL", "https://www.youtube.com/watch?v=abc"),
		Entry("another site", "https://example.com/@calli"),
		Entry("a short ID", "UCshort"),
	)
})

var _ = Describe("Resolve", func() {
	var (
		pages  map[string]string
		client *channel.Client
	)

	BeforeEach(func() {
		pages = map[string]string{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			file, ok := pages[r.URL.EscapedPath()]
			if !ok {
				http.NotFound(w, r)
				return
			}
			body, err := os.ReadFile("testdata/" + file)
			Expect(err).NotTo(HaveOccurred())
			_, _ = w.Write(body)
		}))
		DeferCleanup(srv.Close)
		client = channel.New(channel.WithBaseURL(srv.URL))
	})

	It("reads the channel from its page", func() {
		pages["/@MoriCalliope"] = "channel.html"

		ch, err := client.Resolve(context.Background(), "https://youtube.com/@MoriCalliope")

		Expect(err).NotTo(HaveOccurred())
		Expect(ch.ID).To(Equal("UCL_qhgtOy0dy1Agp8vkySQg"))
		Expect(ch.Name).To(Equal("Mori Calliope Ch. hololive"))
		Expect(ch.Handle).To(Equal("@MoriCalliope"))
		Expect(ch.AvatarURL).To(HavePrefix("https://yt3.googleusercontent.com/"))
	})

	It("reports channels that don't exist", func() {
		pages["/channel/UCaaaaaaaaaaaaaaaaaaaaaa"] = "missing.html"

		_, err := client.Resolve(context.Background(), "UCaaaaaaaaaaaaaaaaaaaaaa")
		Expect(err).To(MatchError(channel.ErrNotFound))

		_, err = client.Resolve(context.Background(), "@nobody")
		Expect(err).To(MatchError(channel.ErrNotFound))
	})

	It("rejects input that isn't a channel without a request", func() {
		_, err := client.Resolve(context.Background(), "just a name")
		Expect(err).To(MatchError(channel.ErrInvalid))
	})
})
