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
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/youtube/livechat"
)

const (
	eventTimeout   = 10 * time.Second
	commandTimeout = 2 * time.Minute
	purgeInterval  = 24 * time.Hour
)

type Bot struct {
	Config    *config.Config
	Log       *slog.Logger
	Store     store.Store
	Streamers *streamers.Registry
	Subs      *subs.Service
	Session   *discordgo.Session
	Router    *gumi.Router
	// Nil without a Holodex API key.
	Holodex *holodex.Client
	Streams *stream.Tracker
	Chats   *chat.Manager
	// Nil unless holodex.tldex is on.
	TLdex *tldex.Client

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
			Log: log.With("component", "streams"),
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
	b.Chats = NewChatManager(b.TLdex, log.With("component", "chat"))

	return b, nil
}

// NewChatManager reads YouTube chat, merged with TLdex when tl isn't nil.
func NewChatManager(tl *tldex.Client, log *slog.Logger) *chat.Manager {
	youtube := livechat.New()
	cfg := chat.Config{
		Open: func(ctx context.Context, videoID string) (chat.Reader, error) {
			c, err := youtube.Open(ctx, videoID)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		Log: log,
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

	if b.Streams != nil {
		go func() { _ = b.Streams.Run(ctx) }()
		go b.watchStreams(ctx)
	} else {
		b.Log.Warn("no Holodex API key, so stream discovery is off")
	}

	if b.TLdex != nil {
		go func() { _ = b.TLdex.Run(ctx) }()
	}
	go b.watchChats(ctx)

	<-ctx.Done()
	b.Log.Info("shutting down")

	return nil
}

func (b *Bot) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()

	for {
		b.purge(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Relays and notifications will hook in here. Until then the events are
// only logged.
func (b *Bot) watchStreams(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.Streams.Events():
			b.Log.Info("stream "+e.Kind.String(),
				slog.String("video_id", e.Stream.VideoID),
				slog.String("channel", e.Stream.ChannelName),
				slog.String("title", e.Stream.Title),
				slog.Bool("members_only", e.Stream.MembersOnly),
				slog.Bool("free_chat", e.Stream.FreeChat))
		}
	}
}

// Relays will start sessions and read comments here. Until then only
// stopped sessions are logged.
func (b *Bot) watchChats(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.Chats.Events():
			if e.Kind == chat.EventStopped {
				b.Log.Info("chat stopped", slog.String("video_id", e.VideoID), slog.Any("reason", e.Err))
			}
		}
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

	n, err = b.Streamers.Purge(ctx, time.Now().Add(-store.StreamerRetention))
	switch {
	case err != nil:
		b.Log.Error("failed to purge hidden streamers", slog.Any("error", err))
	case n > 0:
		b.Log.Info("purged hidden streamers", slog.Int("count", n))
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
