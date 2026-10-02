package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/VTGare/gumi"
	"github.com/VTGare/gumi/middleware"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/store"
)

const (
	eventTimeout   = 10 * time.Second
	commandTimeout = 2 * time.Minute
	purgeInterval  = 24 * time.Hour
)

type Bot struct {
	Config  *config.Config
	Log     *slog.Logger
	Store   store.Store
	Session *discordgo.Session
	Router  *gumi.Router

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
		Config:  cfg,
		Log:     log,
		Store:   st,
		Session: s,
		ctx:     context.Background(),
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

	return b, nil
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

	<-ctx.Done()
	b.Log.Info("shutting down")

	return nil
}

func (b *Bot) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()

	for {
		b.purgeGuilds(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) purgeGuilds(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	n, err := b.Store.PurgeGuilds(ctx, time.Now().Add(-store.GuildRetention))
	switch {
	case err != nil:
		b.Log.Error("failed to purge left guilds", slog.Any("error", err))
	case n > 0:
		b.Log.Info("purged left guilds", slog.Int("count", n))
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
