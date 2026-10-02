package store

import (
	"context"
	"time"
)

const (
	GuildLineRetention   = 7 * 24 * time.Hour
	ArchiveLineRetention = 24 * time.Hour
)

type LineKind string

const (
	LineOwner  LineKind = "owner"
	LineTL     LineKind = "tl"
	LineVTuber LineKind = "vtuber"
	LineMod    LineKind = "mod"
	LineCameo  LineKind = "cameo"
	LineGossip LineKind = "gossip"
)

type Line struct {
	VideoID string
	// Empty for the bot-wide archive.
	GuildID         string
	ChannelID       string
	MessageID       string
	AuthorChannelID string
	AuthorName      string
	Body            string
	Kind            LineKind
	SaidAt          time.Time
}

type LineStore interface {
	SaveLines(ctx context.Context, lines []Line) error
	VideoLines(ctx context.Context, videoID, guildID string) ([]Line, error)
	// Guild lines and archive lines have separate cutoffs.
	PruneLines(ctx context.Context, guildBefore, archiveBefore time.Time) (int, error)
}
