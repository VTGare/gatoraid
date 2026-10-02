package store

import (
	"context"
	"errors"
	"time"
)

type Store interface {
	GuildStore
	StreamerStore
	SubscriptionStore
	NoticeStore
	LineStore
	LogStore
	ModerationStore
	Close() error
}

// How long a guild's data is kept after the bot leaves, in case it gets
// invited back.
const GuildRetention = 30 * 24 * time.Hour

var ErrGuildNotFound = errors.New("guild not found")

type JoinKind int

const (
	// The guild was already active, which is what startup and outage
	// recovery look like.
	JoinExisting JoinKind = iota
	JoinNew
	JoinRestored
)

func (k JoinKind) String() string {
	switch k {
	case JoinNew:
		return "new"
	case JoinRestored:
		return "restored"
	default:
		return "existing"
	}
}

type GuildStore interface {
	// Includes guilds the bot has left.
	Guild(ctx context.Context, guildID string) (*Guild, error)

	// Creates the guild with default settings, or restores it if the bot
	// had left.
	JoinGuild(ctx context.Context, guildID string) (*Guild, JoinKind, error)

	// The data stays until PurgeGuilds removes it.
	LeaveGuild(ctx context.Context, guildID string, at time.Time) error

	// Marks active guilds missing from present as left and returns their
	// IDs. Catches guilds that removed the bot while it was offline.
	ReconcileGuilds(ctx context.Context, present []string, at time.Time) ([]string, error)

	UpdateGuildSettings(ctx context.Context, guildID string, s Settings) error

	PurgeGuilds(ctx context.Context, leftBefore time.Time) (int, error)
}
