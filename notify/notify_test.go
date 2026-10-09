package notify_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/guilds"
	"github.com/VTGare/gatoraid/notify"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/youtube/posts"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	calliID = "UCcalli"
	kiaraID = "UCkiara"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []sender.Message
}

func (f *fakeSender) Send(m sender.Message) bool {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	if m.OnSent != nil {
		m.OnSent(&discord.Message{ID: 1})
	}
	return true
}

func (f *fakeSender) messages() []sender.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sender.Message(nil), f.sent...)
}

type world struct {
	ctx context.Context
	db  *sqlite.Store
	reg *streamers.Registry
	svc *subs.Service
	snd *fakeSender
}

func newWorld() *world {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(db.Close)

	reg := streamers.New(db)
	_, err = reg.Sync(ctx, &streamers.Seed{
		Groups: []store.Group{{ID: "holo-en", Name: "Hololive EN"}},
		Streamers: []store.Streamer{
			{ChannelID: calliID, Name: "Mori Calliope", GroupID: "holo-en"},
			{ChannelID: kiaraID, Name: "Takanashi Kiara", GroupID: "holo-en"},
		},
	})
	Expect(err).NotTo(HaveOccurred())

	gs := guilds.New(db)
	for _, g := range []string{"g1", "g2"} {
		_, err := gs.Join(ctx, g)
		Expect(err).NotTo(HaveOccurred())
	}

	return &world{ctx: ctx, db: db, reg: reg, svc: subs.New(db, reg), snd: &fakeSender{}}
}

func (w *world) subscribe(guild string, feature store.Feature, kind store.TargetKind, target, channel, role string) {
	GinkgoHelper()
	_, err := w.svc.Add(w.ctx, store.Subscription{
		GuildID: guild, Feature: feature, Target: store.Target{Kind: kind, ID: target},
		ChannelID: channel, RoleID: role, CreatedBy: "u",
	})
	Expect(err).NotTo(HaveOccurred())
}

type fakeSource struct {
	posts map[string][]posts.Post
	err   error
}

func (f *fakeSource) Posts(_ context.Context, channelID string) ([]posts.Post, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.posts[channelID], nil
}

var _ = Describe("Posts", func() {
	var (
		w   *world
		src *fakeSource
		p   *notify.Posts
	)

	post := func(id, channel string, recent bool) posts.Post {
		return posts.Post{ID: id, ChannelID: channel, Author: channel, Text: "post " + id, Recent: recent}
	}

	ids := func() []string {
		var out []string
		for _, m := range w.snd.messages() {
			out = append(out, m.ChannelID+": "+m.Send.Embeds[0].Description)
		}
		return out
	}

	BeforeEach(func() {
		w = newWorld()
		src = &fakeSource{posts: map[string][]posts.Post{}}
		p = notify.NewPosts(notify.PostsConfig{Source: src, Registry: w.reg, Subs: w.svc, Store: w.db, Sender: w.snd})
	})

	It("records what's there on the first look and announces new posts after", func() {
		w.subscribe("g1", store.FeaturePosts, store.TargetGroup, "holo-en", "c1", "42")
		src.posts[calliID] = []posts.Post{post("old", calliID, true)}

		Expect(p.Check(w.ctx, calliID)).To(BeTrue())
		Expect(w.snd.messages()).To(BeEmpty())

		src.posts[calliID] = []posts.Post{
			post("newer", calliID, true),
			post("new", calliID, true),
			post("stale", calliID, false),
			post("shared", kiaraID, true),
			post("old", calliID, true),
		}
		Expect(p.Check(w.ctx, calliID)).To(BeTrue())
		Expect(ids()).To(Equal([]string{"c1: post new", "c1: post newer"}))
		Expect(w.snd.messages()[0].Send.Content).To(Equal("<@&42>"))

		Expect(p.Check(w.ctx, calliID)).To(BeTrue())
		Expect(w.snd.messages()).To(HaveLen(2))
	})

	It("reports failed checks", func() {
		src.err = errors.New("boom")
		Expect(p.Check(w.ctx, calliID)).To(BeFalse())
	})

	It("checks the channels guilds follow", func() {
		w.subscribe("g1", store.FeaturePosts, store.TargetChannel, kiaraID, "c1", "")
		src.posts[kiaraID] = []posts.Post{post("a", kiaraID, true)}

		checked := make(chan string, 4)
		counting := &countingSource{inner: src, checked: checked}
		p = notify.NewPosts(notify.PostsConfig{Source: counting, Registry: w.reg, Subs: w.svc, Store: w.db, Sender: w.snd})

		ctx, cancel := context.WithCancel(w.ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = p.Run(ctx) }()
		DeferCleanup(func() { cancel(); <-done })

		Eventually(checked).Should(Receive(Equal(kiaraID)))
	})
})

type countingSource struct {
	inner   notify.PostSource
	checked chan string
}

func (c *countingSource) Posts(ctx context.Context, channelID string) ([]posts.Post, error) {
	select {
	case c.checked <- channelID:
	default:
	}
	return c.inner.Posts(ctx, channelID)
}

var _ = Describe("PostMessage", func() {
	It("shows text, the first image, a video and a poll", func() {
		msg := notify.PostMessage(posts.Post{
			ID: "p1", ChannelID: calliID, Author: "Mori Calliope Ch.", AvatarURL: "a.png", Text: "hello",
			Images: []string{"1.png", "2.png"}, VideoID: "vid", Poll: []string{"yes", "no_way"},
		}, "", 7)

		Expect(msg.Content).To(BeEmpty())
		e := msg.Embeds[0]
		Expect(e.URL).To(Equal("https://www.youtube.com/post/p1"))
		Expect(e.Author.Name).To(Equal("Mori Calliope Ch."))
		Expect(e.Description).To(Equal("hello\n\nhttps://youtu.be/vid\n\n**Poll**\n- yes\n- no\\_way"))
		Expect(e.Image.URL).To(Equal("1.png"))
		Expect(e.Footer.Text).To(Equal("1 more image on YouTube"))
		Expect(e.Color).To(Equal(7))
	})
})
