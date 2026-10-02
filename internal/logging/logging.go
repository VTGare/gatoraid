package logging

import (
	"io"
	"log/slog"
	"strings"

	"github.com/VTGare/gatoraid/internal/config"
)

func New(w io.Writer, cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level(cfg.Level)}

	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}

	return slog.New(slog.NewJSONHandler(w, opts))
}

func level(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(s))); err != nil {
		return slog.LevelInfo
	}

	return l
}
