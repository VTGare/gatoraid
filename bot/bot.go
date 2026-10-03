package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/VTGare/gumi"
	"github.com/VTGare/gumi/middleware"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/holodex/tldex"
	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/moderation"
	"github.com/VTGare/gatoraid/notify"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/tllog"
	"github.com/VTGare/gatoraid/translate"
	"github.com/VTGare/gatoraid/youtube/channel"
	"github.com/VTGare/gatoraid/youtube/livechat"
	"github.com/VTGare/gatoraid/youtube/posts"
)

const (
	eventTimeout    = 10 * time.Second
	commandTimeout  = 2 * time.Minute
	pruneInterval   = time.Hour
	purgeInterval   = 24 * time.Hour
	streamBuffer    = 256
	distantChatPoll = 15 * time.Second
)

// Embed color for everything the bot posts.
const Color = 0x4C9A2A

type Bot struct {
	Config    *config.Config
	Log       *slog.Logger
	Store     store.Store
	Streamers *streamers.Registry
	Subs      *subs.Service
	// Blacklists and filters.
	Moderation *moderation.Service
	// Looks up YouTube channels from links and handles.
	Channels *channel.Client
	// Nil without a DeepL API key.
	Translator *translate.Service
	Session    *discordgo.Session
	Router     *gumi.Router
	// Nil without a Holodex API key.
	Holodex *holodex.Client
	Streams *stream.Tracker
	Chats   *chat.Manager
	// Nil unless holodex.tldex is on.
	TLdex  *tldex.Client
	Sender *sender.Sender
	// Nil without a Holodex API key, since nothing would tell it about
	// streams.
	Relay *relay.Engine
	Logs  *tllog.Writer
	// Nil without a Holodex API key.
	Live  *notify.Live
	Posts *notify.Posts

	// The tracker's events, copied to the relay and the live notifier.
	relayStreams, liveStreams chan stream.Event

	// Start's context, so shutting down cancels commands and event handlers.
	ctx context.Context
}

func New(cfg *config.Config, log *slog.Logger, st store.Store) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("bot: create session: %w", err)
	}

	// No message events needed, so no privileged intents either.
	s.Identify.Intents = discordgo.IntentsGuilds
	s.LogLevel = discordgo.LogWarning
	discordgo.Logger = discordLogger(log.With("component", "discordgo"))

	b := &Bot{
		Config:    cfg,
		Log:       log,
		Store:     st,
		Streamers: streamers.New(st),
		Session:   s,
		ctx:       context.Background(),
	}
	b.Subs = subs.New(st, b.Streamers)
	b.Moderation = moderation.New(st)
	b.Channels = channel.New()
	if cfg.DeepL.APIKey != "" {
		b.Translator = translate.NewService(translate.NewDeepL(cfg.DeepL.APIKey),
			int64(cfg.DeepL.MonthlyCharacterBudget), log.With("component", "deepl"))
	}

	b.Router = gumi.New(gumi.Config{
		DisablePrefixCommands: true,
		OwnerIDs:              cfg.Discord.OwnerIDs,
		DevGuildID:            cfg.Discord.DevGuildID,
		BaseContext:           func() context.Context { return b.ctx },
		AutocompleteErrorHandler: func(ctx *gumi.AutocompleteContext, err error) {
			log.Warn("autocomplete failed",
				slog.String("command", ctx.Command.QualifiedName()),
				slog.String("option", ctx.Option.Name),
				slog.Any("error", err))
		},
	})

	b.Router.Use(
		middleware.Logging(log),
		middleware.Recover(),
		middleware.Timeout(commandTimeout),
	)

	if cfg.Holodex.APIKey != "" {
		b.Holodex = holodex.New(cfg.Holodex.APIKey)
		b.Streams = stream.NewTracker(stream.Config{
			Source:   b.Holodex,
			Channels: b.Streamers.ChannelIDs,
			Classifier: stream.Classifier{FreeChatStreams: func(id string) bool {
				st, ok := b.Streamers.Streamer(id)
				return ok && st.FreeChatStreams
			}},
			PrechatLead: time.Duration(*cfg.Relay.PrechatHours) * time.Hour,
			Log:         log.With("component", "streams"),
			OnAvatars: func(ctx context.Context, avatars map[string]string) {
				if err := b.Streamers.UpdateAvatars(ctx, avatars); err != nil {
					log.Warn("failed to update avatars", slog.Any("error", err))
				}
			},
		})
	}

	if cfg.Holodex.TLdex {
		b.TLdex = tldex.New(tldex.WithLogger(log.With("component", "tldex")))
	}

	var minWait func(string) time.Duration
	if b.Streams != nil {
		minWait = func(videoID string) time.Duration {
			if b.Streams.Distant(videoID) {
				return distantChatPoll
			}
			return 0
		}
	}

	b.Chats = NewChatManager(b.TLdex, minWait, log.With("component", "chat"))

	b.Sender = sender.New(sender.Config{Poster: s, Log: log.With("component", "sender")})

	b.Logs = tllog.NewWriter(tllog.Config{
		Store:      st,
		Sender:     b.Sender,
		Moderation: b.Moderation.For,
		Streamer:   b.Streamers.Streamer,
		Color:      Color,
		Log:        log.With("component", "tllog"),
	})

	b.Posts = notify.NewPosts(notify.PostsConfig{
		Source:   posts.New(),
		Registry: b.Streamers,
		Subs:     b.Subs,
		Store:    st,
		Sender:   b.Sender,
		Color:    Color,
		Log:      log.With("component", "posts"),
	})

	if b.Streams != nil {
		b.relayStreams = make(chan stream.Event, streamBuffer)
		b.liveStreams = make(chan stream.Event, streamBuffer)

		b.Live = notify.NewLive(notify.LiveConfig{
			Streams:  b.liveStreams,
			Registry: b.Streamers,
			Subs:     b.Subs,
			Store:    st,
			Sender:   b.Sender,
			Color:    Color,
			Log:      log.With("component", "live"),
		})

		relayCfg := relay.Config{
			Streams:  b.relayStreams,
			Chats:    b.Chats,
			Registry: b.Streamers,
			Subs:     b.Subs,
			Store:    st,
			Sender:   b.Sender,
			Formatter: &relay.Formatter{
				Emoji:   cfg.Emoji,
				Lineage: b.Streamers.Lineage,
				Color:   Color,
			},
			Moderation: b.Moderation.For,
			OnEnded:    b.Logs.StreamEnded,
			Log:        log.With("component", "relay"),
		}
		// A nil *Service in the interface would look like translation is on.
		if b.Translator != nil {
			relayCfg.Translator = b.Translator
		}
		b.Relay = relay.NewEngine(relayCfg)
	}

	return b, nil
}

// NewChatManager reads YouTube chat, merged with TLdex when tl isn't nil.
// minWait is optional.
func NewChatManager(tl *tldex.Client, minWait func(videoID string) time.Duration, log *slog.Logger) *chat.Manager {
	youtube := livechat.New()
	cfg := chat.Config{
		Open: func(ctx context.Context, videoID string) (chat.Reader, error) {
			c, err := youtube.Open(ctx, videoID)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		MinWait: minWait,
		Log:     log,
	}
	// A nil *tldex.Client in the interface would look like TLdex is on.
	if tl != nil {
		cfg.TLdex = tl
	}
	return chat.NewManager(cfg)
}

// Start blocks until ctx is done. Closing the store is up to the caller.
func (b *Bot) Start(ctx context.Context) error {
	b.ctx = ctx

	b.Session.AddHandler(b.onReady)
	b.Session.AddHandler(b.onGuildCreate)
	b.Session.AddHandler(b.onGuildDelete)
	unbind := b.Router.Bind(b.Session)
	defer unbind()

	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("bot: connect: %w", err)
	}
	defer func() {
		if err := b.Session.Close(); err != nil {
			b.Log.Warn("failed to close the Discord session", slog.Any("error", err))
		}
	}()

	if err := b.Router.Sync(b.Session); err != nil {
		return fmt.Errorf("bot: sync commands: %w", err)
	}

	go b.purgeLoop(ctx)

	if b.TLdex != nil {
		go func() { _ = b.TLdex.Run(ctx) }()
	}

	go func() { _ = b.Posts.Run(ctx) }()
	if b.Translator != nil {
		go func() { _ = b.Translator.Run(ctx) }()
	}

	relayDone := make(chan struct{})
	if b.Streams != nil {
		go func() { _ = b.Streams.Run(ctx) }()
		go b.fanOutStreams(ctx)
		go func() { _ = b.Live.Run(ctx) }()
		go func() {
			defer close(relayDone)
			_ = b.Relay.Run(ctx)
		}()
	} else {
		close(relayDone)
		b.Log.Warn("no Holodex API key, so stream discovery and relays are off")
	}

	<-ctx.Done()
	b.Log.Info("shutting down")

	<-relayDone
	b.Logs.Close()
	// Messages the sender finishes still add lines for the relay to save.
	b.Sender.Close()
	if b.Relay != nil {
		b.Relay.Close()
	}

	return nil
}

// SettingsChanged tells everything that caches guild settings to reload
// them.
func (b *Bot) SettingsChanged(guildID string) {
	if b.Relay != nil {
		b.Relay.SettingsChanged(guildID)
	}
}

// The tracker has one events channel, and both the relay and the live
// notifier need every event.
func (b *Bot) fanOutStreams(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.Streams.Events():
			for _, out := range []chan stream.Event{b.relayStreams, b.liveStreams} {
				select {
				case out <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (b *Bot) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()

	var lastPurge time.Time
	for {
		b.pruneLines(ctx)
		if time.Since(lastPurge) >= purgeInterval {
			b.purge(ctx)
			lastPurge = time.Now()
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) pruneLines(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	now := time.Now()
	n, err := b.Store.PruneLines(ctx, now.Add(-store.GuildLineRetention), now.Add(-store.ArchiveLineRetention))
	switch {
	case err != nil:
		b.Log.Error("failed to prune relayed lines", slog.Any("error", err))
	case n > 0:
		b.Log.Debug("pruned relayed lines", slog.Int("count", n))
	}
}

// Guilds go first, so hidden streamers only their subscriptions kept can
// go in the same run.
func (b *Bot) purge(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	n, err := b.Store.PurgeGuilds(ctx, time.Now().Add(-store.GuildRetention))
	switch {
	case err != nil:
		b.Log.Error("failed to purge left guilds", slog.Any("error", err))
	case n > 0:
		b.Log.Info("purged left guilds", slog.Int("count", n))
	}

	n, err = b.Streamers.HideUnused(ctx)
	switch {
	case err != nil:
		b.Log.Error("failed to hide unused channels", slog.Any("error", err))
	case n > 0:
		b.Log.Info("hid channels nothing subscribes to", slog.Int("count", n))
	}

	n, err = b.Streamers.Purge(ctx, time.Now().Add(-store.StreamerRetention))
	switch {
	case err != nil:
		b.Log.Error("failed to purge hidden streamers", slog.Any("error", err))
	case n > 0:
		b.Log.Info("purged hidden streamers", slog.Int("count", n))
	}

	if _, err := b.Store.PruneNotices(ctx, time.Now().Add(-store.NoticeRetention)); err != nil {
		b.Log.Error("failed to prune stream notices", slog.Any("error", err))
	}
	if _, err := b.Store.PruneLogs(ctx, time.Now().Add(-store.NoticeRetention)); err != nil {
		b.Log.Error("failed to prune posted logs", slog.Any("error", err))
	}
	if _, err := b.Store.PruneSeen(ctx, time.Now().Add(-store.DedupeRetention)); err != nil {
		b.Log.Error("failed to prune seen posts", slog.Any("error", err))
	}
}

func discordLogger(log *slog.Logger) func(msgL, caller int, format string, a ...any) {
	return func(msgL, _ int, format string, a ...any) {
		level := slog.LevelDebug
		switch msgL {
		case discordgo.LogError:
			level = slog.LevelError
		case discordgo.LogWarning:
			level = slog.LevelWarn
		case discordgo.LogInformational:
			level = slog.LevelInfo
		}

		log.Log(context.Background(), level, fmt.Sprintf(format, a...))
	}
}
