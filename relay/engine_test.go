package relay_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeChats struct {
	mu      sync.Mutex
	running map[string]bool
	starts  []string
	targets []chat.Target
	events  chan chat.Event
}

func (f *fakeChats) Start(_ context.Context, t chat.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running[t.VideoID] = true
	f.starts = append(f.starts, t.VideoID)
	f.targets = append(f.targets, t)
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

type fakeTranslator struct {
	mu          sync.Mutex
	calls       []string
	backgrounds []string
	err         error
}

// Like DeepL, it gets the language of short English lines wrong but
// leaves them as they are.
func (f *fakeTranslator) Translate(_ context.Context, text, target, background string) (translate.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, target+":"+text)
	f.backgrounds = append(f.backgrounds, background)
	if f.err != nil {
		return translate.Result{}, f.err
	}
	if len(strings.Fields(text)) <= 2 && !strings.ContainsFunc(text, func(r rune) bool { return r > unicode.MaxASCII }) {
		return translate.Result{Text: text, DetectedSource: "CS"}, nil
	}
	detected := "JA"
	if !strings.ContainsFunc(text, func(r rune) bool { return r > unicode.MaxASCII }) {
		detected = "EN"
	}
	return translate.Result{Text: "<" + text + " in " + target + ">", DetectedSource: detected}, nil
}

func (f *fakeTranslator) backgroundList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.backgrounds...)
}

func (f *fakeTranslator) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type sent struct {
	channel string
	msg     discord.MessageCreate
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
		m.OnSent(&discord.Message{ID: snowflake.ID(n)})
	}
	return true
}

// Lines as "channel: content".
func (f *fakeSender) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string
	for _, s := range f.sent {
		out = append(out, s.channel+": "+s.msg.Content)
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
		tl      *fakeTranslator
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlite.Open(ctx, filepath.Join(GinkgoT().TempDir(), "test.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		reg = streamers.New(db)
		_, err = reg.Sync(ctx, &streamers.Seed{
			Groups: []store.Group{
				{ID: "holo-en", Name: "Hololive EN"}, {ID: "niji", Name: "Nijisanji"},
				{ID: "holo-id", Name: "Hololive ID", SkipAutoTranslate: true},
			},
			Streamers: []store.Streamer{
				{ChannelID: calliID, Name: "Mori Calliope", GroupID: "holo-en", Aliases: []string{"calli"}, Twitch: "moricalliope"},
				{ChannelID: kiaraID, Name: "Takanashi Kiara", GroupID: "holo-en", Aliases: []string{"kiara"}, Twitch: "takanashikiara"},
				{ChannelID: eliraID, Name: "Elira Pendora", GroupID: "niji", Aliases: []string{"elira"}},
				{ChannelID: "UCrisu", Name: "Ayunda Risu", GroupID: "holo-id"},
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
		tl = &fakeTranslator{}
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
			Translator: tl,
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
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "42")
		start()

		streams <- live("calli-live", calliID)
		streams <- live("kiara-live", kiaraID)
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
		Eventually(snd.lines).Should(Equal([]string{"c1: <@&42> **Mori Calliope** is [live on YouTube](https://youtu.be/calli-live). Relaying chat here."}))

		say("calli-live", "UCviewer", "@viewer", "lol")
		say("calli-live", "UCviewer", "@viewer", "[EN] hello")
		say("calli-live", calliID, "@calli", "hi")
		Eventually(snd.lines).Should(HaveExactElements(
			ContainSubstring("Relaying"),
			"c1: 💬 ||@viewer:|| `[EN] hello`",
			"c1: 🎙️ **@calli:** `hi`",
		))

		finish()
		lines, err := db.VideoLines(ctx, "calli-live", "g1")
		Expect(err).NotTo(HaveOccurred())
		Expect(lines).To(HaveExactElements(
			And(HaveField("Kind", store.LineTL), HaveField("ChannelID", "c1"), HaveField("MessageID", "2")),
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
		Eventually(snd.lines).Should(ConsistOf(ContainSubstring("Relaying [pre-stream chat](https://youtu.be/calli-live) here.")))

		say("calli-live", calliID, "@calli", "soon")
		Eventually(snd.lines).Should(ContainElement("c1: ⏳ 🎙️ **@calli:** `soon`"))

		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(ContainElements(
			"c1: **Mori Calliope** is [live on YouTube](https://youtu.be/calli-live). Relaying chat here.",
			"c2: **Mori Calliope** is [live on YouTube](https://youtu.be/calli-live). Relaying chat here.",
		))
		Expect(chats.Starts()).To(HaveLen(1))
	})

	It("relays distant waiting rooms without a notice until they get near", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()

		room := stream.Stream{
			VideoID: "calli-room", ChannelID: calliID, Status: stream.Upcoming, ScheduledAt: time.Now().Add(30 * 24 * time.Hour),
			Distant: true,
		}
		streams <- stream.Event{Kind: stream.EventPrechat, Stream: room}
		Eventually(chats.Running).Should(Equal([]string{"calli-room"}))

		say("calli-room", calliID, "@calli", "schedule's up")
		Eventually(snd.lines).Should(ConsistOf("c1: ⏳ 🎙️ **@calli:** `schedule's up`"))

		room.ScheduledAt, room.Distant = time.Now().Add(time.Hour), false
		streams <- stream.Event{Kind: stream.EventPrechat, Stream: room}
		Eventually(snd.lines).Should(ContainElement(ContainSubstring("Relaying [pre-stream chat](https://youtu.be/calli-room) here.")))
		Expect(chats.Starts()).To(HaveLen(1))
	})

	It("starts the most urgent chats first when subscriptions change", func() {
		start()

		upcoming := func(id string, in time.Duration, distant bool) stream.Event {
			return stream.Event{Kind: stream.EventPrechat, Stream: stream.Stream{
				VideoID: id, ChannelID: calliID, Status: stream.Upcoming, ScheduledAt: time.Now().Add(in), Distant: distant,
			}}
		}
		streams <- upcoming("next-month", 30*24*time.Hour, true)
		streams <- upcoming("tonight", 5*time.Hour, false)
		streams <- upcoming("next-week", 7*24*time.Hour, true)
		streams <- live("calli-live", calliID)
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())

		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		Eventually(chats.Starts).Should(Equal([]string{"calli-live", "tonight", "next-week", "next-month"}))
	})

	It("relays free chat rooms without a notice, even when they get near", func() {
		g, err := db.Guild(ctx, "g1")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.RelayFreeChat = true
		Expect(db.UpdateGuildSettings(ctx, "g1", g.Settings)).To(Succeed())

		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()

		room := stream.Stream{
			VideoID: "calli-free", ChannelID: calliID, Status: stream.Upcoming, ScheduledAt: time.Now().Add(time.Hour),
			FreeChat: true,
		}
		streams <- stream.Event{Kind: stream.EventPrechat, Stream: room}
		Eventually(chats.Running).Should(Equal([]string{"calli-free"}))

		say("calli-free", calliID, "@calli", "schedule's up")
		Eventually(snd.lines).Should(ConsistOf("c1: ⏳ 🎙️ **@calli:** `schedule's up`"))
		Consistently(snd.lines, 50*time.Millisecond).Should(HaveLen(1))
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

	It("picks up changed settings", func() {
		g, err := db.Guild(ctx, "g1")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.Prechat = false
		Expect(db.UpdateGuildSettings(ctx, "g1", g.Settings)).To(Succeed())

		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()
		streams <- stream.Event{Kind: stream.EventPrechat, Stream: stream.Stream{
			VideoID: "calli-live", ChannelID: calliID, Status: stream.Upcoming,
		}}
		Consistently(chats.Running, 50*time.Millisecond).Should(BeEmpty())

		g.Settings.Prechat = true
		Expect(db.UpdateGuildSettings(ctx, "g1", g.Settings)).To(Succeed())
		engine.SettingsChanged("g1")
		Eventually(chats.Running).Should(Equal([]string{"calli-live"}))
	})

	It("translates VTuber lines once per language and only when it's another language", func() {
		g, err := db.Guild(ctx, "g2")
		Expect(err).NotTo(HaveOccurred())
		g.Settings.TargetLanguage = "JA"
		Expect(db.UpdateGuildSettings(ctx, "g2", g.Settings)).To(Succeed())

		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c3", "")
		subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "c2", "")
		start()
		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(HaveLen(3))

		say("calli-live", calliID, "@calli", "おはよう")
		say("calli-live", "UCviewer", "@viewer", "[EN] good morning")
		say("calli-live", "UCrisu", "@risu", "おはようございます")
		say("calli-live", kiaraID, "@kiara", ":_kiaraWave::_hic:")
		say("calli-live", calliID, "@calli", "my mech")
		say("calli-live", calliID, "@calli", "https://x.com/calli/status/1")

		Eventually(snd.lines).Should(ContainElements(
			"c1: 🎙️ **@calli:** `おはよう`\n🌐 **DeepL:** `<おはよう in EN-US>`",
			"c3: 🎙️ **@calli:** `おはよう`\n🌐 **DeepL:** `<おはよう in EN-US>`",
			"c2: 🎙️ **@calli:** `おはよう`",
			"c1: 🎙️ **@risu:** `おはようございます`",
			"c1: 🎙️ **@kiara:** `:_kiaraWave::_hic:`",
			"c1: 🎙️ **@calli:** `my mech`",
			"c2: 🎙️ **@calli:** `my mech`",
			"c1: 🎙️ **@calli:** `https://x.com/calli/status/1`",
		))
		Consistently(tl.callList, 50*time.Millisecond).Should(ConsistOf(
			"EN-US:おはよう", "JA:おはよう", "EN-US:my mech", "JA:my mech"))
		Expect(tl.backgroundList()).To(HaveEach(HavePrefix(`A message from the live chat of Mori Calliope's YouTube stream "`)))
	})

	It("posts lines without a translation when DeepL fails", func() {
		tl.err = errors.New("down")
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()
		streams <- live("calli-live", calliID)
		Eventually(snd.lines).Should(HaveLen(1))

		say("calli-live", calliID, "@calli", "おはよう")
		Eventually(snd.lines).Should(ContainElement("c1: 🎙️ **@calli:** `おはよう`"))
	})

	It("adds the chat link when the streamer has several chats open", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		start()

		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(HaveLen(1))
		say("calli-live", calliID, "@calli", "alone")
		Eventually(snd.lines).Should(ContainElement("c1: 🎙️ **@calli:** `alone`"))

		streams <- stream.Event{Kind: stream.EventPrechat, Stream: stream.Stream{
			VideoID: "calli-room", ChannelID: calliID, Status: stream.Upcoming, Title: "Free chat",
			Distant: true, ScheduledAt: time.Now().Add(30 * 24 * time.Hour),
		}}
		Eventually(chats.Running).Should(HaveLen(2))
		say("calli-live", calliID, "@calli", "hi")
		say("calli-room", calliID, "@calli", "schedule's up")
		Eventually(snd.lines).Should(ContainElements(
			"c1: 🎙️ **@calli:** `hi`\n-# [Mori Calliope](<https://youtu.be/calli-live>) · stream",
			"c1: ⏳ 🎙️ **@calli:** `schedule's up`\n-# [Mori Calliope](<https://youtu.be/calli-room>) · Free chat",
		))
	})

	It("adds the chat link when a channel relays several streamers", func() {
		subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
		subscribe("g1", store.FeatureRelay, store.TargetChannel, kiaraID, "c1", "")
		start()

		streams <- live("calli-live", calliID)
		Eventually(chats.Running).Should(HaveLen(1))
		say("calli-live", calliID, "@calli", "hi")
		Eventually(snd.lines).Should(ContainElement("c1: 🎙️ **@calli:** `hi`\n-# [Mori Calliope](<https://youtu.be/calli-live>) · stream"))
	})

	Describe("Twitch", func() {
		twitchLive := func(videoID, channelID, username string) stream.Event {
			return stream.Event{Kind: stream.EventLive, Stream: stream.Stream{
				VideoID: videoID, Platform: stream.Twitch, ChannelID: channelID, ChannelName: username,
				TwitchUsername: username, Status: stream.Live, Title: "twitch stream",
			}}
		}

		sayTwitch := func(videoID, userID, username, text string, owner bool, emotes ...string) {
			chats.events <- chat.Event{Kind: chat.EventComment, VideoID: videoID, Comment: &chat.Comment{
				VideoID: videoID, ID: "twitch:" + text, AuthorChannelID: chat.TwitchAuthorPrefix + userID,
				AuthorUsername: username, AuthorName: username, Text: text, Owner: owner, Emotes: emotes,
				Source: chat.SourceTwitch, Time: time.Now(),
			}}
		}

		setTwitch := func(guild string, on bool) {
			GinkgoHelper()
			g, err := db.Guild(ctx, guild)
			Expect(err).NotTo(HaveOccurred())
			g.Settings.RelayTwitch = on
			Expect(db.UpdateGuildSettings(ctx, guild, g.Settings)).To(Succeed())
		}

		It("reads the Twitch chat by username and links it", func() {
			subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
			start()

			streams <- twitchLive("twitch:1", calliID, "moricalliope")
			Eventually(chats.Running).Should(Equal([]string{"twitch:1"}))
			Expect(chats.targets).To(Equal([]chat.Target{{VideoID: "twitch:1", TwitchUsername: "moricalliope"}}))
			Eventually(snd.lines).Should(Equal([]string{"c1: **Mori Calliope** is [live on Twitch](<https://www.twitch.tv/moricalliope>). Relaying chat here."}))

			sayTwitch("twitch:1", "7", "moricalliope", "hi chat", true)
			Eventually(snd.lines).Should(HaveLen(2))
			Expect(snd.lines()[1]).To(Equal("c1: 🎙️ **moricalliope:** `hi chat`"))
			Expect(snd.sent[0].msg.Embeds[0].URL).To(Equal("https://www.twitch.tv/moricalliope"))
		})

		It("leaves out guilds that turned Twitch off", func() {
			setTwitch("g2", false)
			subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "c2", "")
			start()

			streams <- twitchLive("twitch:1", calliID, "moricalliope")
			streams <- live("calli-live", calliID)
			Eventually(chats.Running).Should(Equal([]string{"calli-live"}))

			subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
			Eventually(chats.Running).Should(Equal([]string{"calli-live", "twitch:1"}))

			sayTwitch("twitch:1", "7", "moricalliope", "only on twitch", true)
			Eventually(snd.lines).Should(ContainElement(ContainSubstring("only on twitch")))
			Consistently(snd.lines, 50*time.Millisecond).ShouldNot(ContainElement(HavePrefix("c2: 🎙️")))
		})

		It("knows VTubers in Twitch chats by username", func() {
			subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
			subscribe("g1", store.FeatureCameos, store.TargetChannel, kiaraID, "c3", "")
			start()

			streams <- twitchLive("twitch:1", calliID, "moricalliope")
			Eventually(chats.Running).Should(Equal([]string{"twitch:1"}))

			sayTwitch("twitch:1", "99", "TakanashiKiara", "KIKKERIKI", false)
			Eventually(snd.lines).Should(ContainElements(
				"c1: 🎙️ **TakanashiKiara:** `KIKKERIKI`",
				ContainSubstring("c3: 👀 **Takanashi Kiara** in [**Mori Calliope**'s chat](<https://www.twitch.tv/moricalliope>)"),
			))
		})

		It("sends the streamer's line once when they say it in both chats", func() {
			setTwitch("g2", false)
			subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
			subscribe("g2", store.FeatureRelay, store.TargetChannel, calliID, "c2", "")
			start()

			streams <- live("calli-live", calliID)
			streams <- twitchLive("twitch:1", calliID, "moricalliope")
			Eventually(chats.Running).Should(Equal([]string{"calli-live", "twitch:1"}))

			sayTwitch("twitch:1", "7", "moricalliope", "Hello everyone!", true)
			say("calli-live", calliID, "@calli", "hello everyone")
			sayTwitch("twitch:1", "7", "moricalliope", "something else", true)

			Eventually(snd.lines).Should(ContainElement(ContainSubstring("something else")))
			var relayed []string
			for _, l := range snd.lines() {
				if strings.Contains(l, "🎙️") {
					relayed = append(relayed, l)
				}
			}
			// g2 doesn't relay Twitch, so it gets the YouTube copy.
			Expect(relayed).To(ConsistOf(
				And(HavePrefix("c1: "), ContainSubstring("`Hello everyone!`")),
				And(HavePrefix("c2: "), ContainSubstring("`hello everyone`")),
				And(HavePrefix("c1: "), ContainSubstring("`something else`")),
			))
		})

		It("keeps Twitch emotes out of translations", func() {
			subscribe("g1", store.FeatureRelay, store.TargetChannel, calliID, "c1", "")
			start()

			streams <- twitchLive("twitch:1", calliID, "moricalliope")
			Eventually(chats.Running).Should(Equal([]string{"twitch:1"}))

			sayTwitch("twitch:1", "7", "moricalliope", "Kappa", true, "Kappa")
			sayTwitch("twitch:1", "7", "moricalliope", "みんなこんにちは Kappa", true, "Kappa")

			Eventually(tl.callList).Should(Equal([]string{"EN-US:みんなこんにちは"}))
			Expect(tl.backgroundList()[0]).To(ContainSubstring("Mori Calliope's Twitch stream"))
			Eventually(snd.lines).Should(ContainElement(ContainSubstring("`<みんなこんにちは in EN-US>`")))
		})
	})
})
