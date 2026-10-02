package holodex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/VTGare/gatoraid/holodex"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client", func() {
	var (
		ctx     context.Context
		mu      sync.Mutex
		hits    []*http.Request
		handler http.HandlerFunc
		client  *holodex.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		hits = nil
		handler = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits = append(hits, r)
			mu.Unlock()
			handler(w, r)
		}))
		DeferCleanup(srv.Close)

		client = holodex.New("secret", holodex.WithBaseURL(srv.URL))
	})

	fixture := func(name string) http.HandlerFunc {
		data, err := os.ReadFile("testdata/" + name)
		Expect(err).NotTo(HaveOccurred())
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }
	}

	It("decodes real /users/live responses", func() {
		handler = fixture("users_live.json")

		videos, err := client.UsersLive(ctx, []string{"UCa"})

		Expect(err).NotTo(HaveOccurred())
		Expect(hits[0].Header.Get("X-APIKEY")).To(Equal("secret"))
		Expect(hits[0].URL.Path).To(Equal("/users/live"))
		Expect(hits[0].URL.Query().Get("channels")).To(Equal("UCa"))

		Expect(videos).To(HaveLen(3))
		live := videos[0]
		Expect(live.Status).To(Equal(holodex.StatusLive))
		Expect(live.Channel.ID).To(HavePrefix("UC"))
		Expect(live.AvailableAt.IsZero()).To(BeFalse())
		Expect(live.Mentions).NotTo(BeEmpty())
		Expect(videos[1].TopicID).To(Equal("FreeChat"))
		Expect(videos[2].StartScheduled.IsZero()).To(BeFalse())
	})

	It("splits large channel lists into batches of 100", func() {
		handler = func(w http.ResponseWriter, r *http.Request) {
			ids := strings.Split(r.URL.Query().Get("channels"), ",")
			_ = json.NewEncoder(w).Encode([]holodex.Video{{ID: ids[0]}})
		}

		ids := make([]string, 250)
		for i := range ids {
			ids[i] = "UC" + strconv.Itoa(i)
		}

		videos, err := client.UsersLive(ctx, ids)

		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(3))
		Expect(strings.Count(hits[0].URL.Query().Get("channels"), ",")).To(Equal(99))
		Expect(strings.Count(hits[2].URL.Query().Get("channels"), ",")).To(Equal(49))
		Expect(videos).To(HaveLen(3))
		Expect(videos[1].ID).To(Equal("UC100"))
	})

	It("makes no request for no channels", func() {
		videos, err := client.UsersLive(ctx, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(videos).To(BeEmpty())
		Expect(hits).To(BeEmpty())
	})

	It("decodes a single video with its description", func() {
		handler = fixture("video.json")

		v, err := client.Video(ctx, "abc")

		Expect(err).NotTo(HaveOccurred())
		Expect(hits[0].URL.Path).To(Equal("/videos/abc"))
		Expect(v.Description).NotTo(BeEmpty())
	})

	It("decodes a channel", func() {
		handler = fixture("channel.json")

		ch, err := client.Channel(ctx, "UCyl1z3jo3XHR1riLFKG5UAg")

		Expect(err).NotTo(HaveOccurred())
		Expect(ch.Name).To(Equal("Watson Amelia Ch. hololive-EN"))
		Expect(ch.Org).To(Equal("Hololive"))
		Expect(ch.Twitter).To(Equal("watsonameliaen"))
		Expect(ch.Inactive).To(BeTrue())
	})

	It("pages through an org's channels", func() {
		handler = func(w http.ResponseWriter, r *http.Request) {
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			n := 50
			if offset == 100 {
				n = 7
			}
			page := make([]holodex.Channel, n)
			for i := range page {
				page[i] = holodex.Channel{ID: fmt.Sprintf("UC%d", offset+i)}
			}
			_ = json.NewEncoder(w).Encode(page)
		}

		chs, err := client.OrgChannels(ctx, "Independents")

		Expect(err).NotTo(HaveOccurred())
		Expect(chs).To(HaveLen(107))
		Expect(hits).To(HaveLen(3))
		Expect(hits[0].URL.Query().Get("org")).To(Equal("Independents"))
		Expect(hits[0].URL.Query().Get("type")).To(Equal("vtuber"))
	})

	It("maps 404 to ErrNotFound and other failures to APIError", func() {
		_, err := client.Video(ctx, "gone")
		Expect(err).To(MatchError(holodex.ErrNotFound))

		handler = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "slow down", http.StatusTooManyRequests)
		}
		_, err = client.UsersLive(ctx, []string{"UCa"})

		var apiErr *holodex.APIError
		Expect(err).To(BeAssignableToTypeOf(apiErr))
		Expect(err.(*holodex.APIError).Status).To(Equal(http.StatusTooManyRequests))
		Expect(err.Error()).To(ContainSubstring("slow down"))
	})
})
