package relay

import (
	"context"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Streams that went live longer ago than this aren't announced. After
// downtime, the tracker reports everything that's live at once.
const liveMaxAge = 30 * time.Minute

func (e *Engine) announceLive(ctx context.Context, s stream.Stream) {
	if !s.StartedAt.IsZero() && e.cfg.Now().Sub(s.StartedAt) > liveMaxAge {
		return
	}

	host, _ := e.cfg.Registry.Streamer(s.ChannelID)

	feature := store.FeatureYouTube
	if s.Twitch() {
		feature = store.FeatureTwitch
	}

	for _, sub := range e.cfg.Subs.Match(feature, s.ChannelID) {
		if err := e.notifyLive(ctx, s, host, sub); err != nil {
			e.cfg.Log.Error("failed to announce a stream", slog.String("video_id", s.VideoID),
				slog.String("guild_id", sub.GuildID), slog.Any("error", err))
		}
	}
}

func (e *Engine) notifyLive(ctx context.Context, s stream.Stream, host *store.Streamer, sub *store.Subscription) error {
	g, ok := e.cfg.Guilds.Guild(sub.GuildID)
	if !ok {
		return store.ErrGuildNotFound
	}
	if s.MembersOnly && !g.Settings.NotifyMembersOnly {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	n := store.Notice{GuildID: sub.GuildID, VideoID: s.VideoID, Kind: store.NoticeLive, ChannelID: sub.ChannelID}
	claimed, err := e.cfg.Store.ClaimNotice(ctx, n)
	if err != nil || !claimed {
		return err
	}

	e.cfg.Sender.Send(sender.Message{
		ChannelID: sub.ChannelID,
		Send:      LiveMessage(s, host, sub.RoleID),
	})
	return nil
}

func LiveMessage(s stream.Stream, host *store.Streamer, roleID string) discord.MessageCreate {
	name := StreamerName(&s, host)

	text := name + " is " + StreamLink(&s, "live on "+s.PlatformName()) + "!"
	if s.MembersOnly {
		text = name + " started a " + StreamLink(&s, "members-only stream") + "!"
	}
	return StreamMessage(&s, host, roleID, text)
}
