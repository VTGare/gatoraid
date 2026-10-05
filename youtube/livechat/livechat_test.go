package livechat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/VTGare/gatoraid/youtube/livechat"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLivechat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Livechat Suite")
}

func fixture(name string) []byte {
	data, err := os.ReadFile("testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	return data
}

type fakeYouTube struct {
	mu     sync.Mutex
	pages  map[string][]byte
	chats  map[string][]byte
	bodies []map[string]any
}

func (f *fakeYouTube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.URL.Path {
	case "/live_chat":
		page, ok := f.pages[r.URL.Query().Get("v")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(page)
	case "/youtubei/v1/live_chat/get_live_chat":
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies = append(f.bodies, body)

		resp, ok := f.chats[body["continuation"].(string)]
		if !ok {
			http.Error(w, "bad continuation", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(resp)
	default:
		http.NotFound(w, r)
	}
}

var _ = Describe("Chat", func() {
	var (
		ctx    context.Context
		yt     *fakeYouTube
		client *livechat.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		yt = &fakeYouTube{
			pages: map[string][]byte{
				"live":        fixture("page_live.html"),
				"members":     fixture("page_unavailable.html"),
				"disabled":    fixture("page_disabled.html"),
				"not-a-video": fixture("page_not_found.html"),
			},
			chats: map[string][]byte{
				"live-chat-token": fixture("reload.json"),
				"reload-next":     fixture("poll.json"),
				"poll-next":       fixture("ended.json"),
			},
		}
		srv := httptest.NewServer(yt)
		DeferCleanup(srv.Close)
		client = livechat.New(livechat.WithBaseURL(srv.URL))
	})

	It("opens the unfiltered chat and skips the backlog", func() {
		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())

		Expect(yt.bodies[0]["continuation"]).To(Equal("live-chat-token"))
		Expect(yt.bodies[0]["context"]).To(HaveKey("client"))
		Expect(chat.Wait()).To(Equal(10 * time.Second))

		msgs, err := chat.Poll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(yt.bodies[1]["continuation"]).To(Equal("reload-next"))

		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		Expect(ids).To(Equal([]string{"msg-superchat", "msg-poll-1", "msg-poll-2", "msg-poll-3", "msg-poll-4", "msg-owner", "msg-mod"}))
	})

	It("reads authors, badges, emojis and super chats", func() {
		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())
		msgs, err := chat.Poll(ctx)
		Expect(err).NotTo(HaveOccurred())

		byID := map[string]livechat.Message{}
		for _, m := range msgs {
			byID[m.ID] = m
		}

		owner := byID["msg-owner"]
		Expect(owner.AuthorName).To(Equal("@streamer"))
		Expect(owner.AuthorChannelID).To(Equal("UCstreamer00000000000000"))
		Expect(owner.Owner).To(BeTrue())
		Expect(owner.Moderator).To(BeFalse())
		Expect(owner.Text).To(Equal("thanks for coming 😊"))
		Expect(owner.Time).To(Equal(time.UnixMicro(1790903999000000)))

		mod := byID["msg-mod"]
		Expect(mod.Moderator).To(BeTrue())
		Expect(mod.Member).To(BeTrue())
		Expect(mod.Text).To(Equal("[EN] Translation here"))

		Expect(byID["msg-superchat"].SuperChat).To(Equal("$5.00"))
		Expect(byID["msg-superchat"].Text).To(Equal("happy birthday!"))
		Expect(byID["msg-poll-1"].SuperChat).To(BeEmpty())
	})

	It("writes channel emojis as their shortcut", func() {
		yt.chats["live-chat-token"] = []byte(`{"continuationContents":{"liveChatContinuation":{
			"continuations":[{"timedContinuationData":{"continuation":"custom","timeoutMs":500}}],"actions":[]}}}`)
		yt.chats["custom"] = []byte(`{"continuationContents":{"liveChatContinuation":{
			"continuations":[{"timedContinuationData":{"continuation":"x","timeoutMs":60000}}],
			"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"id":"e","authorName":{"simpleText":"@a"},
				"message":{"runs":[{"text":"pat "},{"emoji":{"emojiId":"UCx/abc","shortcuts":[":_MachiPat:",":MachiPat:"],"isCustomEmoji":true}}]}}}}}]}}}`)

		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Wait()).To(Equal(time.Second))

		msgs, err := chat.Poll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs[0].Text).To(Equal("pat :_MachiPat:"))
		Expect(chat.Wait()).To(Equal(30 * time.Second))
	})

	It("writes shortened links in full", func() {
		yt.chats["live-chat-token"] = []byte(`{"continuationContents":{"liveChatContinuation":{
			"continuations":[{"timedContinuationData":{"continuation":"links","timeoutMs":500}}],"actions":[]}}}`)
		yt.chats["links"] = []byte(`{"continuationContents":{"liveChatContinuation":{
			"continuations":[{"timedContinuationData":{"continuation":"x","timeoutMs":60000}}],
			"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"id":"l","authorName":{"simpleText":"@a"},
				"message":{"runs":[{"text":"look "},{"text":"https://x.com/ozq1d/status/2106907354...","navigationEndpoint":{"urlEndpoint":{
					"url":"https://www.youtube.com/redirect?event=live_chat&redir_token=t&q=https%3A%2F%2Fx.com%2Fozq1d%2Fstatus%2F2106907354123456789","target":"TARGET_NEW_WINDOW","nofollow":true}}}]}}}}}]}}}`)

		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())
		chat.Wait()

		msgs, err := chat.Poll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs[0].Text).To(Equal("look https://x.com/ozq1d/status/2106907354123456789"))
	})

	It("reports the end of the chat", func() {
		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())
		_, err = chat.Poll(ctx)
		Expect(err).NotTo(HaveOccurred())

		_, err = chat.Poll(ctx)
		Expect(err).To(MatchError(livechat.ErrEnded))
	})

	DescribeTable("explains chats that can't be opened",
		func(video string, want error, msg string) {
			_, err := client.Open(ctx, video)
			Expect(err).To(MatchError(want))
			Expect(err.Error()).To(ContainSubstring(msg))
		},
		Entry("members-only", "members", livechat.ErrUnavailable, "Sorry, live chat is currently unavailable."),
		Entry("disabled or ended", "disabled", livechat.ErrDisabled, "Chat is disabled for this live stream."),
		Entry("no chat data", "not-a-video", livechat.ErrNotFound, "not found"),
		Entry("404", "missing", livechat.ErrNotFound, "not found"),
	)

	It("returns HTTP errors", func() {
		yt.chats = map[string][]byte{}

		_, err := client.Open(ctx, "live")

		var httpErr *livechat.HTTPError
		Expect(err).To(BeAssignableToTypeOf(httpErr))
		Expect(err.(*livechat.HTTPError).Status).To(Equal(http.StatusBadRequest))
	})

	It("delivers only what was missed when reopening", func() {
		chat, err := client.Open(ctx, "live")
		Expect(err).NotTo(HaveOccurred())

		var reload map[string]any
		Expect(json.Unmarshal(fixture("reload.json"), &reload)).To(Succeed())
		lcc := reload["continuationContents"].(map[string]any)["liveChatContinuation"].(map[string]any)
		lcc["actions"] = append(lcc["actions"].([]any), map[string]any{"addChatItemAction": map[string]any{"item": map[string]any{
			"liveChatTextMessageRenderer": map[string]any{"id": "missed", "authorName": map[string]any{"simpleText": "@late"}},
		}}})
		yt.chats["live-chat-token"], _ = json.Marshal(reload)

		msgs, err := chat.Reopen(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].ID).To(Equal("missed"))
	})
})
