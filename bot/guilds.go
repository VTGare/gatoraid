package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/store"
)

// READY lists every guild the bot is in, including unavailable ones, so
// anything missing removed the bot while it was offline.
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	ids := make([]string, 0, len(r.Guilds))
	for _, g := range r.Guilds {
		ids = append(ids, g.ID)
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
		b.notify(s, fmt.Sprintf("Removed from `%s` while offline. Its data is kept for 30 days.", id))
	}
	if len(left) > 0 {
		b.reloadSubs(ctx)
	}
}

// Discord sends GUILD_CREATE for every guild on startup and after outages,
// not just on joins, so only new and restored guilds get a notice.
func (b *Bot) onGuildCreate(s *discordgo.Session, g *discordgo.GuildCreate) {
	if g.Guild == nil || g.Unavailable {
		return
	}

	ctx, cancel := context.WithTimeout(b.ctx, eventTimeout)
	defer cancel()

	_, kind, err := b.Store.JoinGuild(ctx, g.ID)
	if err != nil {
		b.Log.Error("failed to record guild", slog.String("guild_id", g.ID), slog.Any("error", err))
		return
	}

	if kind == store.JoinRestored {
		b.reloadSubs(ctx)
	}

	switch kind {
	case store.JoinNew:
		b.Log.Info("joined guild", guildAttrs(g.Guild)...)
		b.notify(s, fmt.Sprintf("Joined **%s** (`%s`), %d members.", g.Name, g.ID, g.MemberCount))
	case store.JoinRestored:
		b.Log.Info("rejoined guild", guildAttrs(g.Guild)...)
		b.notify(s, fmt.Sprintf("Rejoined **%s** (`%s`), %d members. Settings restored.", g.Name, g.ID, g.MemberCount))
	}
}

// Outages send GUILD_DELETE too, with Unavailable set.
func (b *Bot) onGuildDelete(s *discordgo.Session, g *discordgo.GuildDelete) {
	if g.Guild == nil {
		return
	}

	if g.Unavailable {
		b.Log.Warn("guild unavailable", slog.String("guild_id", g.ID))
		return
	}

	ctx, cancel := context.WithTimeout(b.ctx, eventTimeout)
	defer cancel()

	if err := b.Store.LeaveGuild(ctx, g.ID, time.Now()); err != nil {
		b.Log.Error("failed to record leaving guild", slog.String("guild_id", g.ID), slog.Any("error", err))
		return
	}
	b.reloadSubs(ctx)

	name := g.ID
	if g.BeforeDelete != nil && g.BeforeDelete.Name != "" {
		name = g.BeforeDelete.Name
	}

	b.Log.Info("left guild", slog.String("guild_id", g.ID), slog.String("guild", name))
	b.notify(s, fmt.Sprintf("Left **%s** (`%s`). Its data is kept for 30 days.", name, g.ID))
}

func (b *Bot) reloadSubs(ctx context.Context) {
	if err := b.Subs.Reload(ctx); err != nil {
		b.Log.Error("failed to reload subscriptions", slog.Any("error", err))
	}
}

func (b *Bot) notify(s *discordgo.Session, msg string) {
	ch := b.Config.Discord.LogChannelID
	if ch == "" {
		return
	}

	_, err := s.ChannelMessageSendComplex(ch, &discordgo.MessageSend{
		Content:         msg,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		b.Log.Warn("failed to post to the log channel", slog.String("channel_id", ch), slog.Any("error", err))
	}
}

func guildAttrs(g *discordgo.Guild) []any {
	return []any{
		slog.String("guild_id", g.ID),
		slog.String("guild", g.Name),
		slog.Int("members", g.MemberCount),
	}
}
