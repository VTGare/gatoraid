package relay_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeChats struct {
	mu      sync.Mutex
	running map[string]bool
	starts  []string
	events  chan chat.Event
}

func (f *fakeChats) Start(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running[id] = true
	f.starts = append(f.starts, id)
}

func (f *fakeChats) Stop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, id)
}

func (f *fakeChats) Events() <-chan chat.Event { return f.events }

func (f *fakeChats) Starts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.starts...)
}

func (f *fakeChats) Running() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id := range f.running {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

type sent struct {
	channel string
	msg     *discordgo.MessageSend
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
}

func (f *fakeSender) Send(m sender.Message) bool {
	f.mu.Lock()
	f.sent = append(f.sent, sent{m.ChannelID, m.Send})
	n := len(f.sent)
	f.mu.Unlock()

	if m.OnSent != nil {
		m.OnSent(&discordgo.Message{ID: fmt.Sprint("m", n), ChannelID: m.ChannelID})
	}
	return true
}

// Lines as "channel: content", notices as "channel: notice <description>".
func (f *fakeSender) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string
	for _, s := range f.sent {
		if len(s.msg.Embeds) > 0 {
			out = append(out, s.channel+": notice "+s.msg.Content+" "+s.msg.Embeds[0].Description)
		} else {
			out = append(out, s.channel+": "+s.msg.Content)
		}
	}
	return out
}

var _ = Describe("Engine", func() {
	const (
		calliID = "UCcalli"
		kiaraID = "UCkiara"
		eliraID = "UCelira"
	)

	var (
		ctx     context.Context
		db      *sqlite.Store
		reg     *streamers.Registry
		svc     *subs.Service
		chats   *fakeChats
		snd     *fakeSender
		streams chan stream.Event
		stop    context.CancelFunc
		done    chan struct{}
		engine  *relay.Engine
		rules   map[string]*relay.Moderation
		ended   chan stream.Stream
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		reg = streamers.New(db)
		_, err = reg.Sync(ctx, &streamers.Seed{
			Groups: []store.Group{{ID: "holo-en", Name: "Hololive EN"}, {ID: "niji", Name: "Nijisanji"}},
			Streamers: []store.Streamer{
				{ChannelID: calliID, Name: "Mori Calliope", GroupID: "holo-en", Aliases: []string{"calli"}},
				{ChannelID: kiaraID, Name: "Takanashi Kiara", GroupID: "holo-en", Aliases: []string{"kiara"}},
				{ChannelID: eliraID, Name: "Elira Pendora", GroupID: "niji", Aliases: []string{"elira"}},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		for _, g := range []string{"g1", "g2"} {
			_, _, err := db.JoinGuild(ctx, g)
			Expect(err).NotTo(HaveOccurred())
		}

		svc = subs.New(db, reg)
		chats = &fakeChats{running: map[string]bool{}, events: make(chan chat.Event, 16)}
		snd = &fakeSender{}
		streams = make(chan stream.Event, 16)
		rules = map[string]*relay.Moderation{}
		ended = make(chan stream.Stream, 4)
	})

	start := func() {
		engine = relay.NewEngine(relay.Config{
			Streams:    streams,
			Chats:      chats,
			Registry:   reg,
			Subs:       svc,
			Store:      db,
			Sender:     snd,
			Formatter:  &relay.Formatter{Emoji: func(_, fallback string) string { return fallback }, Lineage: reg.Lineage},
			Moderation: func(guildID string) *relay.Moderation { return rules[guildID] },
			OnEnded:    func(s stream.Stream) { ended <- s },
		})

		var runCtx context.Context
		runCtx, stop = context.WithCancel(ctx)
		done = make(chan struct{})
		e := engine
		go func() {
			defer close(done)
			_ = e.Run(runCtx)
		}()
		DeferCleanup(func() { stop(); <-done })
		DeferCleanup(e.Close)
	}

	// Stops the engine and saves the lines it holds.
	finish := func() {
		stop()
		<-done
		engine.Close()
	}

	subscribe := func(guild string, feature store.Feature, kind store.TargetKind, target, channel, role string) {
		GinkgoHelper()
		_, err := svc.Add(ctx, store.Subscription{
			GuildID: guild, Feature: feature, Target: store.Target{Kind: kind, ID: target},
			ChannelID: channel, RoleID: role, CreatedBy: "u",
		})
		Expect(err).NotTo(HaveOccurred())
	}

	live := func(videoID, channelID string) stream.Event {
		return stream.Event{Kind: stream.EventLive, Stream: stream.Stream{
			VideoID: videoID, ChannelID: channelID, ChannelName: channelID, Status: stream.Live, Title: "stream",
		}}
	}

	say := func(videoID, authorID, author, text string) {
		chats.events <- chat.Event{Kind: chat.EventComment, VideoID: videoID, Comment: &chat.Comment{
			VideoID: videoID, ID: text, AuthorChannelID: authorID, AuthorName: author, Text: text,
			Owner: authorID == calliID && videoID == "calli-live", Time: time.Now(),
		}}
	}

	It("reads relayed streams, sends a notice with the role and relays lines", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "role")
		start()

		streams <- live("calli-live", calliID)
		streams <- live("kiara-live", kiaraID)
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
		Eventually(snd.lines).Should(Equal([]string{"c1: notice <@&role> Relaying the live chat here."}))

		say("calli-live", "UCviewer", "@viewer", "lol")
		say("calli-live", "UCviewer", "@viewer", "[EN] hello")
		say("calli-live", calliID, "@calli", "hi")
		Eventually(snd.lines).Should(HaveExactElements(
			ContainSubstring("notice"),
			"c1: 💬 ||@viewer:|| `[EN] hello`",
			"c1: 🎙️ **@calli:** `hi`",
		))

		finish()
		lines, err := db.VideoLines(ctx, "calli-live", "g1")
		Expect(err).NotTo(HaveOccurred())
		Expect(lines).To(HaveExactElements(
			And(HaveField("Kind", store.LineTL), HaveField("ChannelID", "c1"), HaveField("MessageID", "m2")),
			HaveField("Kind", store.LineOwner),
		))
		archive, err := db.VideoLines(ctx, "calli-live", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(archive).To(HaveLen(2))
	})

	It("notices once across restarts", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()
		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(HaveLen(1))
		finish()

		start()
		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
		Consistently(snd.lines, 50*time.Millisecond).Should(HaveLen(1))
	})

	It("starts and stops chats as subscriptions change", func() {
		start()
		streams <- live("calli-live", calliID)
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())

		subscribe("g1", store.FeatureRelay, store.TargetGroup, "holo-en", "c1", "")
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
		Eventually(snd.lines).Should(HaveLen(1))

		_, err := svc.Clear(ctx, "g1", store.FeatureRelay, "c1")
		Expect(err).NotTo(HaveOccurred())
		Eventually(chats.Running).Should(BeEmpty())
	})

	It("reads prechat only for guilds that want it and notices again at live", func() {
		g, err := db.Guild(ctx, "g2")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.Prechat = false
		Expect(db.UpdateGuildSettings(ctx, "g2", g.Settings)).To(Succeed())

		subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "c2", "")
		start()

		prechat := stream.Event{Kind: stream.EventPrechat, Stream: stream.Stream{
			VideoID: "calli-live", ChannelID: calliID, Status: stream.Upcoming, ScheduledAt: time.Now().Add(time.Hour),
		}}
		streams <- prechat
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())

		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
		Eventually(snd.lines).Should(ConsistOf(ContainSubstring("c1: notice  Relaying the waiting room chat here.")))

		say("calli-live", calliID, "@calli", "soon")
		Eventually(snd.lines).Should(ContainElement("c1: ⏳ 🎙️ **@calli:** `soon`"))

		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(ContainElements(
			"c1: notice  Relaying the live chat here.",
			"c2: notice  Relaying the live chat here.",
		))
		Expect(chats.Starts()).To(HaveLen(1))
	})

	It("skips members-only streams and free chats unless the guild relays free chats", func() {
		subscribe("g1", store.FeatureRelay, store.TargetGroup, "holo-en", "c1", "")
		start()

		members := live("members", calliID)
		members.Stream.MembersOnly = true
		freeChat := live("free", kiaraID)
		freeChat.Stream.FreeChat = true
		streams <- members
		streams <- freeChat
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())
	})

	It("reads every live curated stream for cameos and gossip, and posts each line once per channel", func() {
		subscribe("g1", store.FeatureCameos, store.TargetChannel, kiaraID, "cameos", "")
		subscribe("g1", store.FeatureGossip, store.TargetChannel, eliraID, "gossip", "")
		subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "both", "")
		subscribe("g2", store.FeatureCameos, store.TargetGroup, "holo-en", "both", "")
		start()

		streams <- live("calli-live", calliID)
		streams <- live("elira-live", eliraID)
		Eventually(chats.Running).Should(Equal([]string{"calli-live", "elira-live"}))

		say("calli-live", kiaraID, "@kiara", "hi calli, elira says hi")
		Eventually(snd.lines).Should(ContainElements(
			"both: 🎙️ **@kiara:** `hi calli, elira says hi`",
			"cameos: 👀 **Takanashi Kiara** in [**Mori Calliope**'s chat](<https://youtu.be/calli-live>): `hi calli, elira says hi`",
			"gossip: 👀 **Takanashi Kiara** in [**Mori Calliope**'s chat](<https://youtu.be/calli-live>): `hi calli, elira says hi`",
		))
		Consistently(snd.lines, 50*time.Millisecond).Should(HaveLen(4))
	})

	It("doesn't restart a chat that stopped until its status changes", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()

		prechat := stream.Event{Kind: stream.EventPrechat, Stream: stream.Stream{
			VideoID: "calli-live", ChannelID: calliID, Status: stream.Upcoming,
		}}
		streams <- prechat
		Eventually(chats.Running).Should(HaveLen(1))

		chats.Stop("calli-live")
		chats.events <- chat.Event{Kind: chat.EventStopped, VideoID: "calli-live"}
		streams <- prechat
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())

		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(HaveLen(1))

		streams <- stream.Event{Kind: stream.EventEnded, Stream: live("calli-live", calliID).Stream}
		Eventually(chats.Running).Should(BeEmpty())
	})

	It("applies each guild's blacklist and filters", func() {
		rules["g1"] = &relay.Moderation{Blacklist: map[string]bool{"UCspam": true}, Banned: []string{"spoiler"}, Wanted: []string{"es:"}}
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "c2", "")
		start()

		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(HaveLen(2))

		say("calli-live", "UCspam", "@spam", "[EN] buy now")
		say("calli-live", "UCviewer", "@viewer", "[EN] big SPOILER")
		say("calli-live", "UCviewer", "@viewer", "ES: hola")
		Eventually(snd.lines).Should(HaveLen(5))
		Consistently(snd.lines, 50*time.Millisecond).Should(HaveLen(5))
		Expect(snd.lines()).To(ContainElements(
			"c1: 💬 ||@viewer:|| `ES: hola`",
			"c2: 💬 ||@spam:|| `[EN] buy now`",
			"c2: 💬 ||@viewer:|| `[EN] big SPOILER`",
		))
		Expect(snd.lines()).NotTo(ContainElement(HavePrefix("c2: 💬 ||@viewer:|| `ES:")))
	})

	It("reports streams that ended after going live, with TLdex's start", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()
		tldexStart := time.Date(2026, 10, 2, 12, 0, 5, 0, time.UTC)

		// Stream and chat events come in on separate channels, so each is
		// waited for before the next.
		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(HaveLen(1))
		chats.events <- chat.Event{Kind: chat.EventStarted, VideoID: "calli-live", StartedAt: tldexStart}
		Eventually(func() int { return len(chats.events) }).Should(BeZero())
		streams <- stream.Event{Kind: stream.EventEnded, Stream: live("calli-live", calliID).Stream, WasLive: true}
		streams <- stream.Event{Kind: stream.EventEnded, Stream: live("cancelled", kiaraID).Stream}

		var s stream.Stream
		Eventually(ended).Should(Receive(&s))
		Expect(s.VideoID).To(Equal("calli-live"))
		Expect(s.StartedAt).To(Equal(tldexStart))
		Consistently(ended, 50*time.Millisecond).ShouldNot(Receive())
	})

	It("adds the chat link when a channel relays several streamers", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		subscribe("g1", store.FeatureRelay, store.TargetChannel, kiaraID, "c1", "")
		start()

		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(HaveLen(1))
		say("calli-live", calliID, "@calli", "hi")
		Eventually(snd.lines).Should(ContainElement("c1: 🎙️ **@calli:** `hi`\n**Chat:** [Mori Calliope](<https://youtu.be/calli-live>)"))
	})
})
