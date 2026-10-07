package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"github.com/VTGare/gatoraid/internal/config"

	"github.com/VTGare/gatoraid/store"
)

// READY lists every guild the bot is in, including unavailable ones, so
// anything missing removed the bot while it was offline.
func (b *Bot) onReady(r *events.Ready) {
	ids := make([]string, 0, len(r.Guilds))
	for _, g := range r.Guilds {
		ids = append(ids, g.ID.String())
	}

	b.Log.Info("connected to Discord",
		slog.String("user", r.User.Username),
		slog.Int("guilds", len(ids)))

	ctx, cancel := context.WithTimeout(b.ctx, eventTimeout)
	defer cancel()

	left, err := b.Store.ReconcileGuilds(ctx, ids, time.Now())
	if err != nil {
		b.Log.Error("failed to reconcile guilds", slog.Any("error", err))
		return
	}

	for _, id := range left {
		b.Log.Info("left guild while offline", slog.String("guild_id", id))
		b.notify(fmt.Sprintf("Removed from `%s` while offline. Its data is kept for 30 days.", id))
	}
	if len(left) > 0 {
		b.reloadSubs(ctx)
	}
}

// Discord sends GUILD_CREATE for every guild on startup and after outages,
// not just on joins, so only new and restored guilds get a notice.
func (b *Bot) guildCreated(gg discord.GatewayGuild) {
	g := gg.Guild
	if gg.Unavailable {
		return
	}

	ctx, cancel := context.WithTimeout(b.ctx, eventTimeout)
	defer cancel()

	_, kind, err := b.Store.JoinGuild(ctx, g.ID.String())
	if err != nil {
		b.Log.Error("failed to record guild", slog.String("guild_id", g.ID.String()), slog.Any("error", err))
		return
	}

	if kind == store.JoinRestored {
		b.reloadSubs(ctx)
	}

	switch kind {
	case store.JoinNew:
		b.Log.Info("joined guild", guildAttrs(g)...)
		b.notify(fmt.Sprintf("Joined **%s** (`%s`), %d members.", g.Name, g.ID, g.MemberCount))
	case store.JoinRestored:
		b.Log.Info("rejoined guild", guildAttrs(g)...)
		b.notify(fmt.Sprintf("Rejoined **%s** (`%s`), %d members. Settings restored.", g.Name, g.ID, g.MemberCount))
	}
}

// name is from the cache, so it's empty if the guild never got there.
func (b *Bot) guildLeft(id, name string) {
	ctx, cancel := context.WithTimeout(b.ctx, eventTimeout)
	defer cancel()

	if err := b.Store.LeaveGuild(ctx, id, time.Now()); err != nil {
		b.Log.Error("failed to record leaving guild", slog.String("guild_id", id), slog.Any("error", err))
		return
	}
	b.reloadSubs(ctx)

	if name == "" {
		name = id
	}

	b.Log.Info("left guild", slog.String("guild_id", id), slog.String("guild", name))
	b.notify(fmt.Sprintf("Left **%s** (`%s`). Its data is kept for 30 days.", name, id))
}

func (b *Bot) reloadSubs(ctx context.Context) {
	if err := b.Subs.Reload(ctx); err != nil {
		b.Log.Error("failed to reload subscriptions", slog.Any("error", err))
	}
}

func (b *Bot) notify(msg string) {
	ch := config.ID(b.Config.Discord.LogChannelID)
	if ch == 0 {
		return
	}

	_, err := b.Client.Rest.CreateMessage(ch, discord.MessageCreate{
		Content:         msg,
		AllowedMentions: &discord.AllowedMentions{},
	})
	if err != nil {
		b.Log.Warn("failed to post to the log channel", slog.String("channel_id", ch.String()), slog.Any("error", err))
	}
}

func guildAttrs(g discord.Guild) []any {
	return []any{
		slog.String("guild_id", g.ID.String()),
		slog.String("guild", g.Name),
		slog.Int("members", g.MemberCount),
	}
}
