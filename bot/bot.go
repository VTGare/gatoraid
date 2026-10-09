package bot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"github.com/VTGare/gumi/v2"
	"github.com/VTGare/gumi/v2/middleware"
	"github.com/disgoorg/disgo"
	disgobot "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"

	"github.com/VTGare/gatoraid/chat"
	"github.com/VTGare/gatoraid/guilds"
	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/holodex/tldex"
	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/notify"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
	"github.com/VTGare/gatoraid/tllog"
	"github.com/VTGare/gatoraid/translate"
	"github.com/VTGare/gatoraid/twitch/helix"
	"github.com/VTGare/gatoraid/twitch/irc"
	"github.com/VTGare/gatoraid/youtube/channel"
	"github.com/VTGare/gatoraid/youtube/livechat"
	"github.com/VTGare/gatoraid/youtube/posts"
)

const (
	eventTimeout    = 10 * time.Second
	closeTimeout    = 10 * time.Second
	commandTimeout  = 2 * time.Minute
	pruneInterval   = time.Hour
	purgeInterval   = 24 * time.Hour
	distantChatPoll = 15 * time.Second
)

// Embed color for everything the bot posts.
const Color = 0x4C9A2A

type Bot struct {
	Config    *config.Config
	Log       *slog.Logger
	Store     store.Store
	Guilds    *guilds.State
	Streamers *streamers.Registry
	Subs      *subs.Service

	// Looks up YouTube channels from links and handles.
	Channels *channel.Client

	// Nil without a DeepL API key.
	Translator *translate.Service
	Client     *disgobot.Client
	Router     *gumi.Router

	// Nil without a Holodex API key.
	Holodex *holodex.Client
	Streams *stream.Tracker
	Chats   *chat.Manager

	// Nil unless holodex.tldex is on.
	TLdex *tldex.Client

	// Both nil without Twitch credentials.
	Twitch     *helix.Client
	TwitchChat *irc.Client
	Sender     *sender.Sender

	// Nil without a Holodex API key, since nothing would tell it about
	// streams.
	Relay *relay.Engine
	Logs  *tllog.Writer

	Posts *notify.Posts

	// Start's context, so shutting down cancels commands and event handlers.
	ctx context.Context
}

func New(cfg *config.Config, log *slog.Logger, st store.Store) (*Bot, error) {
	discordLog := log.With("component", "disgo")
	c, err := disgo.New(
		cfg.Discord.Token,
		disgobot.WithLogger(discordLog),
		disgobot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds)),
		disgobot.WithEventManagerConfigOpts(disgobot.WithAsyncEventsEnabled()),
	)
	if err != nil {
		return nil, fmt.Errorf("bot: create client: %w", err)
	}

	c.Gateway = gateway.New(
		cfg.Discord.Token, c.EventManager.HandleGatewayEvent,
		gateway.WithIntents(gateway.IntentGuilds),
		gateway.WithLogger(discordLog),
		gateway.WithOS(runtime.GOOS),
		gateway.WithBrowser(disgo.Name),
		gateway.WithDevice(disgo.Name),
	)

	b := &Bot{
		Config:    cfg,
		Log:       log,
		Store:     st,
		Guilds:    guilds.New(st),
		Streamers: streamers.New(st),
		Client:    c,
		ctx:       context.Background(),
	}
	b.Subs = subs.New(st, b.Streamers)
	b.Channels = channel.New()
	if cfg.DeepL.APIKey != "" {
		b.Translator = translate.NewService(translate.NewDeepL(cfg.DeepL.APIKey),
			int64(cfg.DeepL.MonthlyCharacterBudget), log.With("component", "deepl"))
	}

	b.Router = gumi.New(gumi.Config{
		DisablePrefixCommands: true,
		OwnerIDs:              cfg.Discord.Owners(),
		DevGuildID:            config.ID(cfg.Discord.DevGuildID),
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

	if cfg.Twitch.Enabled() {
		b.Twitch = helix.New(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)
		b.TwitchChat = irc.New(irc.WithLogger(log.With("component", "twitch")))
	}

	if cfg.Holodex.APIKey != "" {
		b.Holodex = holodex.New(cfg.Holodex.APIKey)
		streamCfg := stream.Config{
			Source:   b.Holodex,
			Channels: b.Streamers.ChannelIDs,
			Classifier: stream.Classifier{FreeChatStreams: func(id string) bool {
				st, ok := b.Streamers.Streamer(id)
				return ok && st.FreeChatStreams
			}},
			PrechatLead: time.Duration(cfg.Relay.PrechatHours) * time.Hour,
			Log:         log.With("component", "streams"),
			OnAvatars: func(ctx context.Context, avatars map[string]string) {
				if err := b.Streamers.UpdateAvatars(ctx, avatars); err != nil {
					log.Warn("failed to update avatars", slog.Any("error", err))
				}
			},
		}

		if b.Twitch != nil {
			streamCfg.Twitch = b.Twitch
			streamCfg.TwitchChannels = b.Streamers.TwitchChannels
		}

		b.Streams = stream.NewTracker(streamCfg)
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

	b.Chats = NewChatManager(b.TLdex, b.TwitchChat, minWait, log.With("component", "chat"))

	b.Sender = sender.New(sender.Config{Poster: c.Rest, Log: log.With("component", "sender")})

	b.Logs = tllog.NewWriter(tllog.Config{
		Store:    st,
		Guilds:   b.Guilds,
		Sender:   b.Sender,
		Streamer: b.Streamers.Streamer,
		Color:    Color,
		Log:      log.With("component", "tllog"),
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
		relayCfg := relay.Config{
			Streams:  b.Streams.Events(),
			Chats:    b.Chats,
			Registry: b.Streamers,
			Subs:     b.Subs,
			Guilds:   b.Guilds,
			Store:    st,
			Sender:   b.Sender,
			Formatter: &relay.Formatter{
				Emoji:   cfg.Emoji,
				Lineage: b.Streamers.Lineage,
			},
			OnEnded: b.Logs.StreamEnded,
			Log:     log.With("component", "relay"),
		}
		// A nil *Service in the interface would look like translation is on.
		if b.Translator != nil {
			relayCfg.Translator = b.Translator
		}
		b.Relay = relay.NewEngine(relayCfg)
	}

	return b, nil
}

// NewChatManager reads YouTube chat, merged with TLdex when tl isn't nil,
// and Twitch chat when twitch isn't nil. minWait is optional.
func NewChatManager(tl *tldex.Client, twitch *irc.Client, minWait func(videoID string) time.Duration, log *slog.Logger) *chat.Manager {
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

	// Nil clients in the interfaces would look like they're on.
	if tl != nil {
		cfg.TLdex = tl
	}

	if twitch != nil {
		cfg.Twitch = twitch
	}

	return chat.NewManager(cfg)
}

// Start blocks until ctx is done. Closing the store is up to the caller.
func (b *Bot) Start(ctx context.Context) error {
	b.ctx = ctx

	listeners := b.listeners()
	b.Client.AddEventListeners(listeners...)
	defer b.Client.RemoveEventListeners(listeners...)

	if err := b.Client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("bot: connect: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		b.Client.Close(ctx)
	}()

	if err := b.Router.Sync(b.Client); err != nil {
		return fmt.Errorf("bot: sync commands: %w", err)
	}

	go b.purgeLoop(ctx)

	if b.TLdex != nil {
		go func() { _ = b.TLdex.Run(ctx) }()
	}

	if b.TwitchChat != nil {
		go func() { _ = b.TwitchChat.Run(ctx) }()
	}

	go func() { _ = b.Posts.Run(ctx) }()
	if b.Translator != nil {
		go func() { _ = b.Translator.Run(ctx) }()
	}

	relayDone := make(chan struct{})
	if b.Streams != nil {
		go func() { _ = b.Streams.Run(ctx) }()
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

func (b *Bot) listeners() []disgobot.EventListener {
	return []disgobot.EventListener{
		disgobot.NewListenerFunc(b.onReady),
		disgobot.NewListenerFunc(func(e *events.GuildReady) { b.guildCreated(e.Guild) }),
		disgobot.NewListenerFunc(func(e *events.GuildAvailable) { b.guildCreated(e.Guild) }),
		disgobot.NewListenerFunc(func(e *events.GuildJoin) { b.guildCreated(e.Guild) }),
		disgobot.NewListenerFunc(func(e *events.GuildUnavailable) {
			b.Log.Warn("guild unavailable", slog.String("guild_id", e.GuildID.String()))
		}),
		disgobot.NewListenerFunc(func(e *events.GuildLeave) { b.guildLeft(e.GuildID.String(), e.Guild.Name) }),
		b.Router,
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

	n, err := b.Guilds.Purge(ctx, time.Now().Add(-store.GuildRetention))
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
