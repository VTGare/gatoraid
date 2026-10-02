// Package notify announces streams going live and new community posts.
package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/subs"
)

const (
	defaultMaxAge = 30 * time.Minute
	storeTimeout  = 10 * time.Second
)

type LiveStore interface {
	Guild(ctx context.Context, guildID string) (*store.Guild, error)
	ClaimNotice(ctx context.Context, n store.Notice) (bool, error)
	SetNoticeMessage(ctx context.Context, n store.Notice, messageID string) error
}

type LiveConfig struct {
	Streams  <-chan stream.Event
	Registry *streamers.Registry
	Subs     *subs.Service
	Store    LiveStore
	Sender   relay.Sender
	// Streams that went live longer ago than this aren't announced. After
	// downtime, the tracker reports everything that's live at once.
	MaxAge time.Duration
	Color  int
	Now    func() time.Time
	Log    *slog.Logger
}

type Live struct {
	cfg LiveConfig
}

func NewLive(cfg LiveConfig) *Live {
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = defaultMaxAge
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Live{cfg: cfg}
}

func (l *Live) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-l.cfg.Streams:
			if ev.Kind == stream.EventLive {
				l.announce(ctx, ev.Stream)
			}
		}
	}
}

func (l *Live) announce(ctx context.Context, s stream.Stream) {
	if !s.StartedAt.IsZero() && l.cfg.Now().Sub(s.StartedAt) > l.cfg.MaxAge {
		return
	}

	host, _ := l.cfg.Registry.Streamer(s.ChannelID)

	for _, sub := range l.cfg.Subs.Match(store.FeatureYouTube, s.ChannelID) {
		if err := l.notify(ctx, s, host, sub); err != nil {
			l.cfg.Log.Error("failed to announce a stream", slog.String("video_id", s.VideoID),
				slog.String("guild_id", sub.GuildID), slog.Any("error", err))
		}
	}
}

func (l *Live) notify(ctx context.Context, s stream.Stream, host *store.Streamer, sub *store.Subscription) error {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	g, err := l.cfg.Store.Guild(ctx, sub.GuildID)
	if err != nil {
		return err
	}
	if s.MembersOnly && !g.Settings.NotifyMembersOnly || s.FreeChat && !g.Settings.NotifyFreeChat {
		return nil
	}

	n := store.Notice{GuildID: sub.GuildID, VideoID: s.VideoID, Kind: store.NoticeLive, ChannelID: sub.ChannelID}
	claimed, err := l.cfg.Store.ClaimNotice(ctx, n)
	if err != nil || !claimed {
		return err
	}

	l.cfg.Sender.Send(sender.Message{
		ChannelID: sub.ChannelID,
		Send:      LiveMessage(s, host, sub.RoleID, l.cfg.Color),
		OnSent: func(m *discordgo.Message) {
			ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
			defer cancel()
			if err := l.cfg.Store.SetNoticeMessage(ctx, n, m.ID); err != nil {
				l.cfg.Log.Warn("failed to save a notification's message", slog.Any("error", err))
			}
		},
	})
	return nil
}

// LiveMessage pings only the role, which has to be in the message text to
// notify anyone.
func LiveMessage(s stream.Stream, host *store.Streamer, roleID string, color int) *discordgo.MessageSend {
	e := &discordgo.MessageEmbed{
		Title:       s.Title,
		URL:         s.URL(),
		Description: "Live now",
		Color:       color,
		Image:       &discordgo.MessageEmbedImage{URL: "https://i.ytimg.com/vi/" + s.VideoID + "/hqdefault.jpg"},
		Author: &discordgo.MessageEmbedAuthor{
			Name: s.ChannelName,
			URL:  "https://www.youtube.com/channel/" + s.ChannelID,
		},
	}
	if host != nil {
		e.Author.Name = host.Name
		e.Author.IconURL = host.AvatarURL
	}
	if s.MembersOnly {
		e.Description = "Members-only stream"
	}
	if !s.StartedAt.IsZero() {
		e.Timestamp = s.StartedAt.Format(time.RFC3339)
	}

	msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{e}, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if roleID != "" {
		msg.Content = "<@&" + roleID + ">"
		msg.AllowedMentions.Roles = []string{roleID}
	}
	return msg
}
