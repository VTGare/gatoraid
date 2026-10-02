package store

import (
	"context"
	"errors"
	"time"
)

var ErrLineNotFound = errors.New("line not found")

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

type VideoChannel struct {
	GuildID   string
	ChannelID string
}

type LineStore interface {
	SaveLines(ctx context.Context, lines []Line) error
	VideoLines(ctx context.Context, videoID, guildID string) ([]Line, error)
	// The guild line posted as this Discord message.
	LineByMessage(ctx context.Context, guildID, messageID string) (*Line, error)
	// Authors of the guild's lines whose name contains query, most recent
	// first, one line each.
	RecentAuthors(ctx context.Context, guildID, query string, limit int) ([]Line, error)
	// The Discord channels each guild relayed the video's chat into. Cameo
	// and gossip lines don't count.
	VideoChannels(ctx context.Context, videoID string) ([]VideoChannel, error)
	// Guild lines and archive lines have separate cutoffs.
	PruneLines(ctx context.Context, guildBefore, archiveBefore time.Time) (int, error)
}
