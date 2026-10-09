package notify_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/notify"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/stream"
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

// Messages as "channel: content | author: title".
func (f *fakeSender) contents() []string {
	var out []string
	for _, m := range f.messages() {
		line := m.ChannelID + ": " + m.Send.Content
		if len(m.Send.Embeds) > 0 {
			e := m.Send.Embeds[0]
			line += " | " + e.Author.Name + ": " + e.Title
		}
		out = append(out, line)
	}
	return out
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

	for _, g := range []string{"g1", "g2"} {
		_, _, err := db.JoinGuild(ctx, g)
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

var _ = Describe("Live", func() {
	var (
		w       *world
		streams chan stream.Event
		now     = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	)

	BeforeEach(func() {
		w = newWorld()
		streams = make(chan stream.Event, 8)
	})

	run := func() {
		l := notify.NewLive(notify.LiveConfig{
			Streams: streams, Registry: w.reg, Subs: w.svc, Store: w.db, Sender: w.snd,
			Now: func() time.Time { return now },
		})
		ctx, cancel := context.WithCancel(w.ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = l.Run(ctx) }()
		DeferCleanup(func() { cancel(); <-done })
	}

	live := func(id, channel string, startedAgo time.Duration) stream.Event {
		return stream.Event{Kind: stream.EventLive, Stream: stream.Stream{
			VideoID: id, ChannelID: channel, ChannelName: channel, Title: "Karaoke_time", Status: stream.Live,
			StartedAt: now.Add(-startedAgo),
		}}
	}

	It("announces live streams once, pinging the role", func() {
		w.subscribe("g1", store.FeatureYouTube, store.TargetGroup, "holo-en", "c1", "42")
		w.subscribe("g2", store.FeatureYouTube, store.TargetChannel, kiaraID, "c2", "")
		run()

		streams <- live("calli", calliID, time.Minute)
		streams <- live("calli", calliID, time.Minute)
		streams <- live("kiara", kiaraID, time.Minute)

		Eventually(w.snd.contents).Should(ConsistOf(
			"c1: <@&42> **Mori Calliope** is [live on YouTube](https://youtu.be/calli)",
			"c1: <@&42> **Takanashi Kiara** is [live on YouTube](https://youtu.be/kiara)",
			"c2: **Takanashi Kiara** is [live on YouTube](https://youtu.be/kiara)",
		))
		Consistently(w.snd.contents, 50*time.Millisecond).Should(HaveLen(3))
		Expect(w.snd.messages()[0].Send.AllowedMentions.Roles).To(Equal([]snowflake.ID{42}))
	})

	It("skips streams that went live a while ago", func() {
		w.subscribe("g1", store.FeatureYouTube, store.TargetChannel, calliID, "c1", "")
		run()

		streams <- live("old", calliID, 2*time.Hour)
		Consistently(w.snd.contents, 50*time.Millisecond).Should(BeEmpty())
	})

	It("announces members-only streams only when the guild wants them", func() {
		g, err := w.db.Guild(w.ctx, "g2")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.NotifyMembersOnly = true
		Expect(w.db.UpdateGuildSettings(w.ctx, "g2", g.Settings)).To(Succeed())

		w.subscribe("g1", store.FeatureYouTube, store.TargetChannel, calliID, "c1", "")
		w.subscribe("g2", store.FeatureYouTube, store.TargetChannel, calliID, "c2", "")
		run()

		members := live("members", calliID, time.Minute)
		members.Stream.MembersOnly = true
		streams <- members

		Eventually(w.snd.contents).Should(ConsistOf("c2: **Mori Calliope** started a [members-only stream](https://youtu.be/members)"))
		Consistently(w.snd.contents, 50*time.Millisecond).Should(HaveLen(1))
	})

	It("announces Twitch streams to Twitch subscriptions only", func() {
		w.subscribe("g1", store.FeatureYouTube, store.TargetChannel, calliID, "c1", "")
		w.subscribe("g2", store.FeatureTwitch, store.TargetGroup, "holo-en", "c2", "")
		run()

		tw := live("twitch:1", calliID, time.Minute)
		tw.Stream.Platform = stream.Twitch
		tw.Stream.TwitchUsername = "moricalliope"
		streams <- tw
		streams <- live("yt", calliID, time.Minute)

		Eventually(w.snd.contents).Should(ConsistOf(
			"c2: **Mori Calliope** is [live on Twitch](<https://www.twitch.tv/moricalliope>) | Mori Calliope: Karaoke_time",
			"c1: **Mori Calliope** is [live on YouTube](https://youtu.be/yt)",
		))
		Consistently(w.snd.contents, 50*time.Millisecond).Should(HaveLen(2))
	})
})

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

var _ = Describe("LiveMessage", func() {
	It("names the streamer and links the YouTube stream for Discord's player", func() {
		host := &store.Streamer{ChannelID: calliID, Name: "Mori_Calliope", AvatarURL: "calli.png"}

		msg := notify.LiveMessage(stream.Stream{
			VideoID: "vid", ChannelID: calliID, ChannelName: "Calli Ch.", Title: "Karaoke", StartedAt: time.Now(),
		}, host, "42")

		Expect(msg.Content).To(Equal("<@&42> **Mori\\_Calliope** is [live on YouTube](https://youtu.be/vid)"))
		Expect(msg.Embeds).To(BeEmpty())
	})

	It("puts Twitch streams in our embed with the preview and category", func() {
		host := &store.Streamer{ChannelID: calliID, Name: "Mori Calliope", AvatarURL: "calli.png"}

		msg := notify.LiveMessage(stream.Stream{
			VideoID: "twitch:1", Platform: stream.Twitch, ChannelID: calliID, TwitchUsername: "moricalliope",
			Title: "Karaoke", Game: "Music", StartedAt: time.Now(),
			Thumbnail: "https://static-cdn.jtvnw.net/previews-ttv/live_user_moricalliope-1280x720.jpg?s=1",
		}, host, "")

		Expect(msg.Content).To(Equal("**Mori Calliope** is [live on Twitch](<https://www.twitch.tv/moricalliope>)"))
		e := msg.Embeds[0]
		Expect(e.Title).To(Equal("Karaoke"))
		Expect(e.URL).To(Equal("https://www.twitch.tv/moricalliope"))
		Expect(e.Author.Name).To(Equal("Mori Calliope"))
		Expect(e.Author.IconURL).To(Equal("calli.png"))
		Expect(e.Author.URL).To(Equal("https://www.twitch.tv/moricalliope"))
		Expect(e.Image.URL).To(Equal("https://static-cdn.jtvnw.net/previews-ttv/live_user_moricalliope-1280x720.jpg?s=1"))
		Expect(e.Fields).To(Equal([]discord.EmbedField{{Name: "Category", Value: "Music"}}))
		Expect(e.Color).To(Equal(relay.TwitchColor))
		Expect(e.Timestamp).To(BeNil())
	})
})

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
