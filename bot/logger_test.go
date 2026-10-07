package bot

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type levels struct{ got []slog.Level }

func (l *levels) Enabled(context.Context, slog.Level) bool { return true }
func (l *levels) WithAttrs([]slog.Attr) slog.Handler      { return l }
func (l *levels) WithGroup(string) slog.Handler           { return l }

func (l *levels) Handle(_ context.Context, r slog.Record) error {
	l.got = append(l.got, r.Level)
	return nil
}

var _ = Describe("discordLogger", func() {
	It("maps discordgo levels and keeps unknown events at debug", func() {
		h := &levels{}
		logf := discordLogger(slog.New(h))

		logf(discordgo.LogError, 0, "error reading from gateway")
		logf(discordgo.LogWarning, 0, "error reading from gateway")
		logf(discordgo.LogWarning, 0, "unknown event: Op: %d, Seq: %d, Type: %s, Data: %s", 0, 8, "VOICE_CHANNEL_STATUS_UPDATE", "{}")
		logf(discordgo.LogInformational, 0, "connected")
		logf(discordgo.LogDebug, 0, "heartbeat")

		Expect(h.got).To(Equal([]slog.Level{
			slog.LevelError, slog.LevelWarn, slog.LevelDebug, slog.LevelInfo, slog.LevelDebug,
		}))
	})
})
