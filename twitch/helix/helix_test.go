package helix_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/twitch/helix"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// In the shape the Helix docs give for GET /streams.
const streamsBody = `{
  "data": [{
    "id": "40952121085",
    "user_id": "101051819",
    "user_login": "afro",
    "user_name": "Afro",
    "game_id": "32982",
    "game_name": "Grand Theft Auto V",
    "type": "live",
    "title": "Jacob: Digital Den Laptops & Routers | NoPixel | !FCF",
    "tags": ["English"],
    "viewer_count": 1490,
    "started_at": "2021-03-10T15:04:21Z",
    "language": "en",
    "thumbnail_url": "https://static-cdn.jtvnw.net/previews-ttv/live_user_afro-{width}x{height}.jpg",
    "is_mature": false
  }],
  "pagination": {}
}`

var _ = Describe("Client", func() {
	var (
		ctx         context.Context
		mu          sync.Mutex
		tokens      int
		requests    []*http.Request
		streams     http.HandlerFunc
		tokenStatus int
		client      *helix.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		tokens, requests, tokenStatus = 0, nil, http.StatusOK
		streams = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(streamsBody)) }

		mux := http.NewServeMux()
		mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			tokens++
			n := tokens
			mu.Unlock()

			Expect(r.ParseForm()).To(Succeed())
			Expect(r.PostForm.Get("client_id")).To(Equal("id"))
			Expect(r.PostForm.Get("client_secret")).To(Equal("secret"))
			Expect(r.PostForm.Get("grant_type")).To(Equal("client_credentials"))

			w.WriteHeader(tokenStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": fmt.Sprintf("token%d", n), "expires_in": 5000000, "token_type": "bearer",
			})
		})
		mux.HandleFunc("GET /helix/streams", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests = append(requests, r)
			mu.Unlock()
			streams(w, r)
		})

		srv := httptest.NewServer(mux)
		DeferCleanup(srv.Close)
		client = helix.New("id", "secret", helix.WithBaseURL(srv.URL+"/helix"), helix.WithTokenURL(srv.URL+"/token"))
	})

	It("decodes /streams and signs in as the app", func() {
		got, err := client.Streams(ctx, []string{"afro"})

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]helix.Stream{{
			ID: "40952121085", UserID: "101051819", Username: "afro", DisplayName: "Afro", Type: "live",
			Title:        "Jacob: Digital Den Laptops & Routers | NoPixel | !FCF",
			GameName:     "Grand Theft Auto V",
			StartedAt:    time.Date(2021, 3, 10, 15, 4, 21, 0, time.UTC),
			ThumbnailURL: "https://static-cdn.jtvnw.net/previews-ttv/live_user_afro-{width}x{height}.jpg",
		}}))
		Expect(requests[0].Header.Get("Client-Id")).To(Equal("id"))
		Expect(requests[0].Header.Get("Authorization")).To(Equal("Bearer token1"))
		Expect(requests[0].URL.Query()["user_login"]).To(Equal([]string{"afro"}))
		Expect(requests[0].URL.Query().Get("first")).To(Equal("100"))
	})

	It("reuses the token", func() {
		_, err := client.Streams(ctx, []string{"afro"})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Streams(ctx, []string{"afro"})
		Expect(err).NotTo(HaveOccurred())

		Expect(tokens).To(Equal(1))
	})

	It("asks for 100 usernames at a time and skips malformed ones", func() {
		usernames := []string{"has space", "UPPER", ""}
		for i := range 250 {
			usernames = append(usernames, fmt.Sprintf("user_%d", i))
		}

		_, err := client.Streams(ctx, usernames)

		Expect(err).NotTo(HaveOccurred())
		Expect(requests).To(HaveLen(3))
		Expect(requests[0].URL.Query()["user_login"]).To(HaveLen(100))
		Expect(requests[0].URL.Query()["user_login"][0]).To(Equal("user_0"))
		Expect(requests[2].URL.Query()["user_login"]).To(HaveLen(50))
	})

	It("makes no request without usernames", func() {
		got, err := client.Streams(ctx, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		Expect(requests).To(BeEmpty())
		Expect(tokens).To(BeZero())
	})

	It("gets a new token once when Twitch rejects the old one", func() {
		streams = func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer token1" {
				http.Error(w, `{"error":"Unauthorized","status":401,"message":"Invalid OAuth token"}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(streamsBody))
		}

		got, err := client.Streams(ctx, []string{"afro"})

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(tokens).To(Equal(2))
	})

	It("returns API errors", func() {
		streams = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"Too Many Requests"}`, http.StatusTooManyRequests)
		}

		_, err := client.Streams(ctx, []string{"afro"})

		var apiErr *helix.APIError
		Expect(errors.As(err, &apiErr)).To(BeTrue())
		Expect(apiErr.Status).To(Equal(http.StatusTooManyRequests))
	})

	It("reports a rejected client secret", func() {
		tokenStatus = http.StatusForbidden

		_, err := client.Streams(ctx, []string{"afro"})

		Expect(err).To(MatchError(ContainSubstring("helix: token")))
	})

	It("fills in thumbnail sizes", func() {
		Expect(helix.Thumbnail("https://x/live_user_afro-{width}x{height}.jpg", 1280, 720)).
			To(Equal("https://x/live_user_afro-1280x720.jpg"))
	})
})
