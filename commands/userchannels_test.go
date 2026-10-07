package commands_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/disgoorg/disgo/discord"

	dt "github.com/VTGare/gumi/v2/gumitest"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/youtube/channel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Channels outside the registry", func() {
	const (
		tracked  = "UCtrackedtrackedtracked1"
		tracked2 = "UCtrackedtrackedtracked2"
		inactive = "UCinactiveinactiveinact1"
		unknown  = "UCuntrackeduntrackeduntr"
	)

	var h *harness

	page := func(id, title, handle string) string {
		return fmt.Sprintf(`<html><script>var ytInitialData = {"metadata":{"channelMetadataRenderer":{
			"title":%q,"externalId":%q,"vanityChannelUrl":"http://www.youtube.com/%s","avatar":{"thumbnails":[{"url":"https://yt3/a.png"}]}}}};</script></html>`,
			title, id, handle)
	}

	BeforeEach(func() {
		seed, err := streamers.LoadSeed()
		Expect(err).NotTo(HaveOccurred())
		h = newHarness(seed)
		_, _, err = h.b.Store.JoinGuild(context.Background(), testGuild)
		Expect(err).NotTo(HaveOccurred())

		pages := map[string]string{
			"/@tracked":      page(tracked, "Tracked Ch.", "@tracked"),
			"/@tracked2":     page(tracked2, "Second Ch.", "@tracked2"),
			"/@inactive":     page(inactive, "Old Ch.", "@inactive"),
			"/@untracked":    page(unknown, "Random Ch.", "@untracked"),
			"/@MoriCalliope": page(calliID, "Mori Calliope Ch. hololive", "@MoriCalliope"),
		}
		yt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := pages[r.URL.Path]; ok {
				_, _ = w.Write([]byte(p))
				return
			}
			http.NotFound(w, r)
		}))
		DeferCleanup(yt.Close)
		h.b.Channels = channel.New(channel.WithBaseURL(yt.URL))

		hdx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch strings.TrimPrefix(r.URL.Path, "/channels/") {
			case tracked:
				_, _ = w.Write([]byte(`{"id":"` + tracked + `","name":"Tracked Ch.","english_name":"Tracked","twitter":"tracked"}`))
			case tracked2:
				_, _ = w.Write([]byte(`{"id":"` + tracked2 + `","name":"Second Ch."}`))
			case inactive:
				_, _ = w.Write([]byte(`{"id":"` + inactive + `","name":"Old Ch.","inactive":true}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
			}
		}))
		DeferCleanup(hdx.Close)
		h.b.Holodex = holodex.New("key", holodex.WithBaseURL(hdx.URL))
	})

	// Adding an outside channel defers, so the reply is an edit.
	add := func(command string, sub string, target string) string {
		GinkgoHelper()
		before := len(h.rec.Edits())
		var opts dt.Option
		if command == "notify" {
			opts = dt.Group(sub, dt.Sub("add", dt.String("target", target)))
		} else {
			opts = dt.Sub("add", dt.String("target", target))
		}
		data := h.run(dt.Command(user, command, opts).WithPermissions(discord.PermissionManageGuild))
		if edits := h.rec.Edits(); len(edits) > before {
			return replyText(edits[len(edits)-1].Body)
		}
		return replyText(data)
	}

	It("adds channels Holodex tracks as user channels", func() {
		Expect(add("relay", "", "https://www.youtube.com/@tracked")).To(Equal("Now relaying **Tracked** in <#3000>."))

		st, ok := h.b.Streamers.Streamer(tracked)
		Expect(ok).To(BeTrue())
		Expect(st.Source).To(Equal(store.SourceUser))
		Expect(st.AddedByGuild).To(Equal(testGuild))
		Expect(st.ChannelName).To(Equal("Tracked Ch."))
		Expect(st.Twitter).To(Equal("tracked"))
		Expect(h.b.Streamers.ChannelIDs()).To(ContainElement(tracked))
		Expect(h.b.Subs.Match(store.FeatureRelay, tracked)).To(HaveLen(1))

		Expect(add("notify", "posts", tracked)).To(Equal("Now posting new posts by **Tracked** in <#3000>."))
	})

	It("uses the registry entry when the link is a registry streamer", func() {
		Expect(add("relay", "", "@MoriCalliope")).To(Equal("Now relaying **Mori Calliope** in <#3000>."))
		st, _ := h.b.Streamers.Streamer(calliID)
		Expect(st.Source).To(Equal(store.SourceSeed))
	})

	It("refuses channels Holodex doesn't track or lists as inactive", func() {
		Expect(add("relay", "", "@untracked")).To(ContainSubstring("Holodex doesn't track **Random Ch.**"))
		Expect(add("relay", "", "@inactive")).To(ContainSubstring("Holodex lists **Old Ch.** as inactive"))
		Expect(add("relay", "", "@nobody")).To(ContainSubstring("There's no YouTube channel at `@nobody`"))

		_, ok := h.b.Streamers.Streamer(unknown)
		Expect(ok).To(BeFalse())
	})

	It("keeps each server under the limit", func() {
		h.b.Config.Limits.UserChannels = 1

		Expect(add("relay", "", "@tracked")).To(HavePrefix("Now relaying"))
		Expect(add("notify", "youtube", "@tracked")).To(HavePrefix("Now posting live notifications"))
		Expect(add("relay", "", "@tracked2")).To(ContainSubstring("reached its limit for channels from outside the streamer list (1)"))
	})

	It("leaves cameos and gossip to registry streamers", func() {
		Expect(add("cameos", "", "@tracked")).To(ContainSubstring(`I don't know a streamer called "@tracked"`))
	})
})
