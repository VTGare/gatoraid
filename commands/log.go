package commands

import (
	"cmp"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/VTGare/gumi/v2"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/tllog"
)

var (
	bareVideoID = regexp.MustCompile(`^[\w-]{11}$`)
	videoInURL  = regexp.MustCompile(`(?:[?&]v=|youtu\.be/|/live/|/shorts/|/embed/)([\w-]{11})(?:[^\w-]|$)`)
)

func logCommand(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "log",
		Description: "Get the relayed lines of a stream as a file",
		Category:    CategoryRelay,
		Defer:       true,
		Options:     []*gumi.Option{gumi.String("video", "Video link or ID").Require()},
		Handler: func(ctx *gumi.Context) error {
			videoID := parseVideoID(ctx.Options.String("video"))
			if videoID == "" {
				return gumi.NewUserError("That's not a YouTube video link or ID.")
			}

			meta := tllog.Meta{VideoID: videoID}
			if b.Holodex != nil {
				if v, err := b.Holodex.Video(ctx.Context(), videoID); err != nil {
					b.Log.Warn("couldn't get a video for /log", slog.String("video_id", videoID), slog.Any("error", err))
				} else {
					meta.Title = v.Title
					meta.Author = cmp.Or(v.Channel.EnglishName, v.Channel.Name)
					meta.AuthorIcon = v.Channel.Photo
					meta.Duration = time.Duration(v.Duration) * time.Second
					// Before a stream starts, available_at is only the schedule.
					if v.Status == holodex.StatusLive || v.Status == holodex.StatusPast {
						meta.Start = v.AvailableAt
					}
				}
			}

			text, n, err := buildLog(b, ctx, meta)
			if err != nil {
				return err
			}
			if n == 0 {
				return gumi.NewUserError("I have no lines from that stream. I keep this server's relays for 7 days and every chat I read for 24 hours.")
			}

			msg := tllog.Message(meta, text, n, Color)
			return ctx.Reply(&gumi.Response{Embeds: msg.Embeds, Files: msg.Files, AllowedMentions: msg.AllowedMentions})
		},
	}
}

// The server's own relays come first. Without any, it's the archive of
// every chat the bot read, which only goes back a day.
func buildLog(b *bot.Bot, ctx *gumi.Context, meta tllog.Meta) (string, int, error) {
	mod := b.Moderation.For(idString(ctx.GuildID()))

	var sources []string
	if ctx.GuildID() != 0 {
		sources = append(sources, ctx.GuildID().String())
	}
	sources = append(sources, "")

	for _, guildID := range sources {
		lines, err := b.Store.VideoLines(ctx.Context(), meta.VideoID, guildID)
		if err != nil {
			return "", 0, err
		}
		if text, n := tllog.Build(meta, lines, mod); n > 0 {
			return text, n, nil
		}
	}

	return "", 0, nil
}

func parseVideoID(input string) string {
	input = strings.TrimSpace(input)
	if bareVideoID.MatchString(input) {
		return input
	}
	if m := videoInURL.FindStringSubmatch(input); m != nil {
		return m[1]
	}
	return ""
}
