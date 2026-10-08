package relay

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/translate"
)

const (
	lineBuffer    = 4096
	lineBatch     = 500
	flushInterval = time.Second
	storeTimeout  = 10 * time.Second
	// Past this the line goes out without its translation.
	translateTimeout = 5 * time.Second
	// A streamer live on YouTube and Twitch at once often says the same
	// thing in both chats within this long.
	streamerRepeatWindow = time.Minute
)

type Chats interface {
	Start(ctx context.Context, t chat.Target)
	Stop(videoID string)
	Events() <-chan chat.Event
}

type Sender interface {
	Send(sender.Message) bool
}

type Translator interface {
	Translate(ctx context.Context, text, target, background string) (translate.Result, error)
}

type Store interface {
	Guild(ctx context.Context, guildID string) (*store.Guild, error)
	store.NoticeStore
	store.LineStore
}

type Config struct {
	Streams   <-chan stream.Event
	Chats     Chats
	Registry  *streamers.Registry
	Subs      *subs.Service
	Store     Store
	Sender    Sender
	Formatter *Formatter
	// A guild's blacklist and filters. Optional.
	Moderation func(guildID string) *Moderation
	// Called when a stream that went live ends. Optional.
	OnEnded func(stream.Stream)
	// Translates VTuber lines for guilds with auto-translate on. Optional.
	Translator Translator
	Log        *slog.Logger
}

// Engine decides which chats to read and sends their lines where the
// subscriptions say. Its state belongs to the Run goroutine.
type Engine struct {
	cfg   Config
	lines chan store.Line
	quit  chan struct{}
	saved chan struct{}
	close sync.Once
	// Lines waiting for their translation.
	translating sync.WaitGroup

	streams map[string]*stream.Stream
	running map[string]bool
	// Chats that stopped on their own, with the stream's status then. They
	// aren't restarted until the status changes.
	stopped      map[string]stream.Status
	notified     map[noticeKey]bool
	streamerSaid map[streamerLine]said
	// Guarded by settingsMu, since SettingsChanged runs on other goroutines.
	settingsMu sync.Mutex
	settings   map[string]*store.Settings
	resync     chan struct{}
}

type streamerLine struct {
	discordChannelID string
	hostChannelID    string
	text             string
}

type said struct {
	videoID string
	at      time.Time
}

type noticeKey struct {
	videoID   string
	kind      store.NoticeKind
	channelID string
}

// Lines in the archive are what a guild with default settings and no
// blacklist would get.
var archiveSettings = store.DefaultSettings()

func NewEngine(cfg Config) *Engine {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Moderation == nil {
		cfg.Moderation = func(string) *Moderation { return nil }
	}

	e := &Engine{
		cfg:          cfg,
		lines:        make(chan store.Line, lineBuffer),
		quit:         make(chan struct{}),
		saved:        make(chan struct{}),
		streams:      map[string]*stream.Stream{},
		running:      map[string]bool{},
		stopped:      map[string]stream.Status{},
		notified:     map[noticeKey]bool{},
		streamerSaid: map[streamerLine]said{},
		settings:     map[string]*store.Settings{},
		resync:       make(chan struct{}, 1),
	}
	go e.saveLines()

	return e
}

// Close saves the lines still waiting. Close the sender first, since
// messages it finishes add lines.
func (e *Engine) Close() {
	e.close.Do(func() { close(e.quit) })
	<-e.saved
}

// Run blocks until ctx is done, then stops the chats it started.
func (e *Engine) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			for id := range e.running {
				e.cfg.Chats.Stop(id)
			}
			e.translating.Wait()
			return ctx.Err()
		case ev := <-e.cfg.Streams:
			e.onStream(ctx, ev)
		case ev := <-e.cfg.Chats.Events():
			e.onChat(ev)
		case <-e.cfg.Subs.Changed():
			e.updateAll(ctx)
		case <-e.resync:
			e.updateAll(ctx)
		}
	}
}

func (e *Engine) updateAll(ctx context.Context) {
	streams := slices.Collect(maps.Values(e.streams))
	slices.SortFunc(streams, stream.Urgency)
	for _, s := range streams {
		e.update(ctx, s)
	}
}

func (e *Engine) onStream(ctx context.Context, ev stream.Event) {
	s := ev.Stream
	id := s.VideoID

	if ev.Kind == stream.EventEnded {
		if ev.WasLive && e.cfg.OnEnded != nil {
			// TLdex may know the real start better than the tracker.
			if prev, ok := e.streams[id]; ok && !prev.StartedAt.IsZero() {
				s.StartedAt = prev.StartedAt
			}
			e.cfg.OnEnded(s)
		}
		if e.running[id] {
			e.cfg.Chats.Stop(id)
			delete(e.running, id)
		}
		delete(e.streams, id)
		delete(e.stopped, id)
		for k := range e.notified {
			if k.videoID == id {
				delete(e.notified, k)
			}
		}
		return
	}

	// TLdex may already have said when it really started.
	if prev, ok := e.streams[id]; ok && s.StartedAt.IsZero() {
		s.StartedAt = prev.StartedAt
	}
	e.streams[id] = &s
	if status, ok := e.stopped[id]; ok && status != s.Status {
		delete(e.stopped, id)
	}

	e.update(ctx, &s)
}

// update starts or stops the stream's chat and sends any notices it's
// owed.
func (e *Engine) update(ctx context.Context, s *stream.Stream) {
	relays := e.relays(s)

	read := len(relays) > 0 || e.readForOthers(s)
	if _, ok := e.stopped[s.VideoID]; ok {
		read = false
	}

	switch {
	case read && !e.running[s.VideoID]:
		e.cfg.Chats.Start(ctx, chat.Target{VideoID: s.VideoID, TwitchUsername: s.TwitchUsername})
		e.running[s.VideoID] = true
	case !read && e.running[s.VideoID]:
		e.cfg.Chats.Stop(s.VideoID)
		delete(e.running, s.VideoID)
	}

	if read {
		e.notify(ctx, s, relays)
	}
}

// relays are the relay subscriptions that want the stream's chat now.
func (e *Engine) relays(s *stream.Stream) []*store.Subscription {
	if s.MembersOnly {
		return nil
	}

	var out []*store.Subscription
	for _, sub := range e.cfg.Subs.Match(store.FeatureRelay, s.ChannelID) {
		if st := e.guildSettings(sub.GuildID); st != nil && relayable(s, st) {
			out = append(out, sub)
		}
	}
	return out
}

func relayable(s *stream.Stream, st *store.Settings) bool {
	switch {
	case s.MembersOnly,
		s.FreeChat && !st.RelayFreeChat,
		s.Status == stream.Upcoming && !st.Prechat,
		s.Twitch() && !st.RelayTwitch:
		return false
	}
	return true
}

// Cameos and gossip can turn up in any curated streamer's live chat, so
// all of those are read while anyone follows either.
func (e *Engine) readForOthers(s *stream.Stream) bool {
	if s.Status != stream.Live || s.MembersOnly || s.FreeChat {
		return false
	}
	if len(e.cfg.Subs.All(store.FeatureCameos)) == 0 && len(e.cfg.Subs.All(store.FeatureGossip)) == 0 {
		return false
	}

	host, ok := e.cfg.Registry.Streamer(s.ChannelID)
	return ok && host.Curated()
}

func (e *Engine) notify(ctx context.Context, s *stream.Stream, subs []*store.Subscription) {
	// The tracker sends the stream again once it's near, and that's when
	// the notice goes out. Free chat rooms never get one, because they
	// aren't streams and their scheduled time is only a placeholder.
	if s.Distant || s.FreeChat {
		return
	}

	kind := store.NoticeRelay
	if s.Status == stream.Upcoming {
		kind = store.NoticePrechat
	}

	host, _ := e.cfg.Registry.Streamer(s.ChannelID)
	for _, sub := range subs {
		key := noticeKey{s.VideoID, kind, sub.ChannelID}
		if e.notified[key] {
			continue
		}
		e.notified[key] = true

		n := store.Notice{GuildID: sub.GuildID, VideoID: s.VideoID, Kind: kind, ChannelID: sub.ChannelID}
		claimed, err := e.cfg.Store.ClaimNotice(ctx, n)
		if err != nil {
			e.cfg.Log.Error("failed to record a notice", slog.String("video_id", s.VideoID), slog.Any("error", err))
			continue
		}
		if !claimed {
			continue
		}

		e.cfg.Sender.Send(sender.Message{
			ChannelID: sub.ChannelID,
			Send:      e.cfg.Formatter.Notice(kind, s, host, sub.RoleID),
		})
	}
}

func (e *Engine) onChat(ev chat.Event) {
	s, ok := e.streams[ev.VideoID]
	if !ok {
		return
	}

	switch ev.Kind {
	case chat.EventComment:
		e.dispatch(s, ev.Comment)
	case chat.EventStarted:
		s.StartedAt = ev.StartedAt
	case chat.EventStopped:
		delete(e.running, ev.VideoID)
		e.stopped[ev.VideoID] = s.Status
		e.cfg.Log.Info("chat stopped", slog.String("video_id", ev.VideoID), slog.Any("reason", ev.Err))
	}
}

// dispatch sends a comment to every guild that wants it. A Discord channel
// gets it once, even if it follows the stream and the author both.
func (e *Engine) dispatch(s *stream.Stream, cc *chat.Comment) {
	c := &Comment{Comment: *cc, Stream: s}
	c.Host, _ = e.cfg.Registry.Streamer(s.ChannelID)
	if cc.Source == chat.SourceTwitch {
		c.Author, _ = e.cfg.Registry.TwitchStreamer(cc.AuthorUsername)
	} else {
		c.Author, _ = e.cfg.Registry.Streamer(cc.AuthorChannelID)
	}

	if kind, ok := Relay(c, &archiveSettings, nil); ok {
		e.save(c, kind, "", "", nil)
	}

	sent := map[string]bool{}
	post := func(sub *store.Subscription, kind Kind, content string) {
		sent[sub.ChannelID] = true
		e.post(c, sub, kind, content)
	}
	toTranslate := map[string][]waiting{}

	// A streamer can have a live stream and waiting rooms open at once, and
	// only the link says which one a line came from.
	severalChats := e.openChats(s.ChannelID) > 1

	for _, sub := range e.cfg.Subs.Match(store.FeatureRelay, s.ChannelID) {
		st := e.guildSettings(sub.GuildID)
		if st == nil || !relayable(s, st) {
			continue
		}

		kind, ok := Relay(c, st, e.cfg.Moderation(sub.GuildID))
		if !ok || (kind == KindOwner && e.saidElsewhere(c, sub.ChannelID)) {
			continue
		}

		showChat := st.ShowChat && (severalChats ||
			sub.Target.Kind != store.TargetChannel || e.cfg.Subs.Count(store.FeatureRelay, sub.ChannelID) > 1)
		if e.translates(c, kind, st) {
			sent[sub.ChannelID] = true
			toTranslate[st.TargetLanguage] = append(toTranslate[st.TargetLanguage], waiting{sub, kind, showChat})
			continue
		}
		post(sub, kind, e.cfg.Formatter.Relay(c, kind, showChat, ""))
	}

	if c.Author != nil {
		for _, sub := range e.cfg.Subs.Match(store.FeatureCameos, c.Author.ChannelID) {
			if !sent[sub.ChannelID] && Cameo(c, e.cfg.Moderation(sub.GuildID)) {
				post(sub, KindCameo, e.cfg.Formatter.Cameo(c))
			}
		}
	}

	for _, sub := range e.cfg.Subs.All(store.FeatureGossip) {
		if sent[sub.ChannelID] {
			continue
		}
		subject, ok := e.cfg.Registry.Streamer(sub.Target.ID)
		if ok && Gossip(c, subject, e.cfg.Moderation(sub.GuildID)) {
			post(sub, KindGossip, e.cfg.Formatter.Gossip(c))
		}
	}

	for target, lines := range toTranslate {
		e.translateAndPost(c, target, lines)
	}
}

// saidElsewhere reports whether the streamer said the line in another of
// their chats a moment ago, and the Discord channel already got it.
func (e *Engine) saidElsewhere(c *Comment, discordChannelID string) bool {
	now := time.Now()
	for k, v := range e.streamerSaid {
		if now.Sub(v.at) > streamerRepeatWindow {
			delete(e.streamerSaid, k)
		}
	}

	key := streamerLine{discordChannelID, c.Stream.ChannelID, chat.Normalize(c.translatable())}
	if prev, ok := e.streamerSaid[key]; ok && prev.videoID != c.VideoID {
		return true
	}

	e.streamerSaid[key] = said{c.VideoID, now}
	return false
}

func (e *Engine) openChats(channelID string) int {
	n := 0
	for id := range e.running {
		if s, ok := e.streams[id]; ok && s.ChannelID == channelID {
			n++
		}
	}
	return n
}

func (e *Engine) post(c *Comment, sub *store.Subscription, kind Kind, content string) {
	e.cfg.Sender.Send(sender.Message{
		ChannelID: sub.ChannelID,
		Send:      discord.MessageCreate{Content: content, AllowedMentions: &discord.AllowedMentions{}},
		OnSent:    func(m *discord.Message) { e.save(c, kind, sub.GuildID, sub.ChannelID, m) },
	})
}

type waiting struct {
	sub      *store.Subscription
	kind     Kind
	showChat bool
}

// Streamer and VTuber lines with words in them get translated, unless the
// author's group skips translation.
func (e *Engine) translates(c *Comment, kind Kind, st *store.Settings) bool {
	switch {
	case e.cfg.Translator == nil || !st.AutoTranslate,
		kind != KindOwner && kind != KindVTuber,
		c.Author != nil && e.cfg.Registry.SkipAutoTranslate(c.Author),
		!hasWords(c.translatable()):
		return false
	}
	return true
}

// translateAndPost asks for the translation off the engine's goroutine, so
// a slow DeepL never holds up other lines. A line whose translation fails,
// or comes back the same, goes out without one.
func (e *Engine) translateAndPost(c *Comment, target string, lines []waiting) {
	// The engine updates its streams in place, so the goroutine gets its
	// own copy.
	stream := *c.Stream
	own := *c
	own.Stream = &stream

	e.translating.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), translateTimeout)
		defer cancel()

		translation := ""
		text := own.translatable()
		r, err := e.cfg.Translator.Translate(ctx, text, target, chatBackground(&own))
		switch {
		case err == nil && !translate.SameLanguage(r.DetectedSource, target) && !translate.Unchanged(text, r.Text):
			translation = r.Text
		case err != nil && !errors.Is(err, translate.ErrBudget):
			e.cfg.Log.Warn("translation failed", slog.String("video_id", own.VideoID), slog.Any("error", err))
		}

		for _, w := range lines {
			e.post(&own, w.sub, w.kind, e.cfg.Formatter.Relay(&own, w.kind, w.showChat, translation))
		}
	})
}

const chatStyle = "Chat is informal and full of internet, gaming and VTuber slang, like W, kek, gg, pog and lmao. " +
	"Leave slang, names and words that are already in the target language as they are."

// chatBackground tells DeepL what it's translating.
func chatBackground(c *Comment) string {
	var sb strings.Builder
	sb.WriteString("A message from the live chat of ")
	sb.WriteString(hostName(c))
	sb.WriteString("'s ")
	sb.WriteString(c.Stream.PlatformName())
	sb.WriteString(" stream")
	if c.Stream.Title != "" {
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(c.Stream.Title))
	}
	sb.WriteString(". ")
	sb.WriteString(chatStyle)
	return sb.String()
}

// SettingsChanged drops the guild's cached settings and rechecks which
// chats to read, since prechat and free chat settings decide that.
func (e *Engine) SettingsChanged(guildID string) {
	e.settingsMu.Lock()
	delete(e.settings, guildID)
	e.settingsMu.Unlock()

	select {
	case e.resync <- struct{}{}:
	default:
	}
}

// Nil means the guild can't be loaded.
func (e *Engine) guildSettings(guildID string) *store.Settings {
	e.settingsMu.Lock()
	st, ok := e.settings[guildID]
	e.settingsMu.Unlock()
	if ok {
		return st
	}

	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()

	g, err := e.cfg.Store.Guild(ctx, guildID)
	if err != nil {
		e.cfg.Log.Error("failed to load guild settings", slog.String("guild_id", guildID), slog.Any("error", err))
		return nil
	}

	e.settingsMu.Lock()
	e.settings[guildID] = &g.Settings
	e.settingsMu.Unlock()
	return &g.Settings
}

// Called from sender goroutines too, so it only touches the channel.
func (e *Engine) save(c *Comment, kind Kind, guildID, channelID string, sent *discord.Message) {
	l := store.Line{
		VideoID:         c.VideoID,
		GuildID:         guildID,
		AuthorChannelID: c.AuthorChannelID,
		AuthorName:      c.AuthorName,
		Body:            c.Text,
		Kind:            kind,
		SaidAt:          c.Time,
	}
	if sent != nil && sent.ID != 0 {
		l.ChannelID, l.MessageID = channelID, sent.ID.String()
	}

	select {
	case e.lines <- l:
	default:
		e.cfg.Log.Warn("line buffer full, dropping a line", slog.String("video_id", c.VideoID))
	}
}

// saveLines writes lines in batches until Close, then writes what's left.
func (e *Engine) saveLines() {
	defer close(e.saved)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var batch []store.Line
	flush := func() {
		if len(batch) == 0 {
			return
		}

		wctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		if err := e.cfg.Store.SaveLines(wctx, batch); err != nil {
			e.cfg.Log.Error("failed to save relayed lines", slog.Int("lines", len(batch)), slog.Any("error", err))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-e.quit:
			for {
				select {
				case l := <-e.lines:
					batch = append(batch, l)
				default:
					flush()
					return
				}
			}
		case l := <-e.lines:
			batch = append(batch, l)
			if len(batch) >= lineBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
