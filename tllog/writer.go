package tllog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/sender"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

// Long enough for the last lines to be sent and saved.
const defaultDelay = time.Minute

const storeTimeout = 30 * time.Second

type Store interface {
	Guild(ctx context.Context, guildID string) (*store.Guild, error)
	VideoLines(ctx context.Context, videoID, guildID string) ([]store.Line, error)
	VideoChannels(ctx context.Context, videoID string) ([]store.VideoChannel, error)
	ClaimLog(ctx context.Context, guildID, videoID, channelID string) (bool, error)
}

type Config struct {
	Store  Store
	Sender relay.Sender
	// A guild's blacklist and filters. Optional.
	Moderation func(guildID string) *relay.Moderation
	// How long after a stream ends its logs are posted.
	Delay time.Duration
	Log   *slog.Logger
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
	if cfg.Moderation == nil {
		cfg.Moderation = func(string) *relay.Moderation { return nil }
	}
	if cfg.Delay <= 0 {
		cfg.Delay = defaultDelay
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

	w.wg.Go(func() {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.cfg.Delay):
		}

		ctx, cancel := context.WithTimeout(w.ctx, storeTimeout)
		defer cancel()
		if err := w.post(ctx, s); err != nil {
			w.cfg.Log.Error("failed to post TL logs", slog.String("video_id", s.VideoID), slog.Any("error", err))
		}
	})
}

func (w *Writer) Close() {
	w.cancel()
	w.wg.Wait()
}

func (w *Writer) post(ctx context.Context, s stream.Stream) error {
	channels, err := w.cfg.Store.VideoChannels(ctx, s.VideoID)
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

	meta := Meta{VideoID: s.VideoID, Title: s.Title, Start: s.StartedAt}
	for _, guildID := range guilds {
		if err := w.postGuild(ctx, meta, guildID, byGuild[guildID]); err != nil {
			w.cfg.Log.Error("failed to post a TL log", slog.String("video_id", s.VideoID),
				slog.String("guild_id", guildID), slog.Any("error", err))
		}
	}

	return nil
}

func (w *Writer) postGuild(ctx context.Context, meta Meta, guildID string, relayChannels []string) error {
	g, err := w.cfg.Store.Guild(ctx, guildID)
	if err != nil {
		return err
	}
	if !g.Active() {
		return nil
	}

	lines, err := w.cfg.Store.VideoLines(ctx, meta.VideoID, guildID)
	if err != nil {
		return err
	}
	mod := w.cfg.Moderation(guildID)

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

func (w *Writer) send(ctx context.Context, meta Meta, guildID, channelID string, lines []store.Line, mod *relay.Moderation) error {
	text, ok := Build(meta, lines, mod)
	if !ok {
		return nil
	}

	claimed, err := w.cfg.Store.ClaimLog(ctx, guildID, meta.VideoID, channelID)
	if err != nil || !claimed {
		return err
	}

	w.cfg.Sender.Send(sender.Message{ChannelID: channelID, Send: Message(meta, text)})
	return nil
}

// Message is the log as a .txt attachment.
func Message(meta Meta, text string) *discordgo.MessageSend {
	title := meta.Title
	if title == "" {
		title = meta.VideoID
	}

	return &discordgo.MessageSend{
		Content:         "Log for [" + relay.EscapeMarkdown(title) + "](<" + meta.URL() + ">)",
		Files:           []*discordgo.File{{Name: meta.VideoID + ".txt", ContentType: "text/plain", Reader: strings.NewReader(text)}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}
