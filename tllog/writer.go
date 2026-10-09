package tllog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/guilds"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Long enough for the last lines to be sent and saved.
const defaultDelay = time.Minute

const storeTimeout = 30 * time.Second

type Store interface {
	VideoLines(ctx context.Context, videoID, guildID string) ([]store.Line, error)
	VideoChannels(ctx context.Context, videoID string) ([]store.VideoChannel, error)
	ClaimLog(ctx context.Context, guildID, videoID, channelID string) (bool, error)
}

type Config struct {
	Store  Store
	Guilds *guilds.State
	Sender relay.Sender
	// How long after a stream ends its logs are posted.
	Delay time.Duration
	// Looks up the streamer for the message. Optional.
	Streamer func(channelID string) (*store.Streamer, bool)
	Color    int
	Now      func() time.Time
	Log      *slog.Logger
}

// Writer posts a log to every guild that relayed a stream once it ends:
// to the guild's log channel, or else to each channel it was relayed in.
type Writer struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWriter(cfg Config) *Writer {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Delay <= 0 {
		cfg.Delay = defaultDelay
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Writer{cfg: cfg, ctx: ctx, cancel: cancel}
}

// StreamEnded schedules the stream's logs. Logs still waiting when Close
// is called aren't posted.
func (w *Writer) StreamEnded(s stream.Stream) {
	if w.ctx.Err() != nil {
		return
	}

	meta := Meta{VideoID: s.VideoID, Link: s.URL(), Title: s.Title, Start: s.StartedAt, Author: s.ChannelName}
	if !s.StartedAt.IsZero() {
		meta.Duration = w.cfg.Now().Sub(s.StartedAt)
	}
	if w.cfg.Streamer != nil {
		if st, ok := w.cfg.Streamer(s.ChannelID); ok {
			meta.Author, meta.AuthorIcon = st.Name, st.AvatarURL
		}
	}

	w.wg.Go(func() {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.cfg.Delay):
		}

		ctx, cancel := context.WithTimeout(w.ctx, storeTimeout)
		defer cancel()
		if err := w.post(ctx, meta); err != nil {
			w.cfg.Log.Error("failed to post TL logs", slog.String("video_id", s.VideoID), slog.Any("error", err))
		}
	})
}

func (w *Writer) Close() {
	w.cancel()
	w.wg.Wait()
}

func (w *Writer) post(ctx context.Context, meta Meta) error {
	channels, err := w.cfg.Store.VideoChannels(ctx, meta.VideoID)
	if err != nil {
		return err
	}

	byGuild := map[string][]string{}
	var guilds []string
	for _, vc := range channels {
		if _, ok := byGuild[vc.GuildID]; !ok {
			guilds = append(guilds, vc.GuildID)
		}
		byGuild[vc.GuildID] = append(byGuild[vc.GuildID], vc.ChannelID)
	}

	for _, guildID := range guilds {
		if err := w.postGuild(ctx, meta, guildID, byGuild[guildID]); err != nil {
			w.cfg.Log.Error("failed to post a TL log", slog.String("video_id", meta.VideoID),
				slog.String("guild_id", guildID), slog.Any("error", err))
		}
	}

	return nil
}

func (w *Writer) postGuild(ctx context.Context, meta Meta, guildID string, relayChannels []string) error {
	g, ok := w.cfg.Guilds.Guild(guildID)
	if !ok {
		return store.ErrGuildNotFound
	}
	if !g.Active() {
		return nil
	}

	lines, err := w.cfg.Store.VideoLines(ctx, meta.VideoID, guildID)
	if err != nil {
		return err
	}
	mod := w.cfg.Guilds.Moderation(guildID)

	if logChannel := g.Settings.LogChannelID; logChannel != "" {
		return w.send(ctx, meta, guildID, logChannel, lines, mod)
	}

	for _, channelID := range relayChannels {
		var inChannel []store.Line
		for _, l := range lines {
			if l.ChannelID == channelID {
				inChannel = append(inChannel, l)
			}
		}
		if err := w.send(ctx, meta, guildID, channelID, inChannel, mod); err != nil {
			return err
		}
	}

	return nil
}

func (w *Writer) send(ctx context.Context, meta Meta, guildID, channelID string, lines []store.Line, mod *guilds.Moderation) error {
	text, n := Build(meta, lines, mod)
	if n == 0 {
		return nil
	}

	claimed, err := w.cfg.Store.ClaimLog(ctx, guildID, meta.VideoID, channelID)
	if err != nil || !claimed {
		return err
	}

	w.cfg.Sender.Send(sender.Message{ChannelID: channelID, Send: Message(meta, text, n, w.cfg.Color)})
	return nil
}

// Message is a short summary with the log attached as a .txt file.
func Message(meta Meta, text string, lines int, color int) discord.MessageCreate {
	title := meta.Title
	if title == "" {
		title = meta.VideoID
	}

	desc := []string{"Stream log"}
	if meta.Duration >= time.Minute {
		desc = append(desc, length(meta.Duration))
	}
	if lines == 1 {
		desc = append(desc, "1 line")
	} else {
		desc = append(desc, fmt.Sprintf("%d lines", lines))
	}

	e := discord.Embed{
		Title:       title,
		URL:         meta.URL(),
		Description: strings.Join(desc, " · "),
		Color:       color,
	}
	if meta.Author != "" {
		e.Author = &discord.EmbedAuthor{Name: meta.Author, IconURL: meta.AuthorIcon}
	}

	return discord.MessageCreate{
		Embeds:          []discord.Embed{e},
		Files:           []*discord.File{discord.NewFile(fileName(meta.VideoID), "", strings.NewReader(text))},
		AllowedMentions: &discord.AllowedMentions{},
	}
}

// Like "2 h 14 min" or "45 min".
func length(d time.Duration) string {
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	if h == 0 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func fileName(videoID string) string {
	return strings.ReplaceAll(videoID, ":", "-") + ".txt"
}
