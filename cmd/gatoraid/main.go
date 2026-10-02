package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/commands"
	"github.com/VTGare/gatoraid/internal/config"
	"github.com/VTGare/gatoraid/internal/logging"
	"github.com/VTGare/gatoraid/store/sqlite"
	"github.com/VTGare/gatoraid/streamers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "JSON config file; defaults to $"+config.EnvConfigPath+", then ./config.json if present")
	flag.Parse()

	environ := os.Environ()
	cfg, err := config.Load(config.Path(*configPath, environ), environ)
	if err != nil {
		return err
	}

	log := logging.New(os.Stderr, cfg.Log)
	slog.SetDefault(log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	openCtx, cancelOpen := context.WithTimeout(ctx, time.Minute)
	st, err := sqlite.Open(openCtx, cfg.Database.Path)
	cancelOpen()
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("failed to close the database", slog.Any("error", err))
		}
	}()

	b, err := bot.New(cfg, log, st)
	if err != nil {
		return err
	}

	seed, err := streamers.LoadSeed()
	if err != nil {
		return err
	}

	res, err := b.Streamers.Sync(ctx, seed)
	if err != nil {
		return fmt.Errorf("sync streamer seed: %w", err)
	}
	log.Info("synced streamer seed",
		slog.Int("streamers", len(b.Streamers.Streamers())),
		slog.Int("added", res.Added),
		slog.Int("updated", res.Updated),
		slog.Int("removed", res.Removed),
		slog.Int("kept_owner_edits", res.Kept),
		slog.Int("returned_to_seed", res.Returned))

	if err := commands.Register(b); err != nil {
		return err
	}

	log.Info("starting GatorAid", slog.String("database", cfg.Database.Path))
	return b.Start(ctx)
}
