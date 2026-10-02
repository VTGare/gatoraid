package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotBlacklisted = errors.New("not blacklisted")
	ErrFilterNotFound = errors.New("filter not found")
)

type BlacklistEntry struct {
	GuildID string
	// YouTube channel ID.
	ChannelID string
	Name      string
	Reason    string
	AddedBy   string
	CreatedAt time.Time
}

type FilterKind string

const (
	// Lines containing the pattern aren't relayed.
	FilterBanned FilterKind = "banned"
	// Lines starting with the pattern count as translations.
	FilterWanted FilterKind = "wanted"
)

type Filter struct {
	GuildID   string
	Kind      FilterKind
	Pattern   string
	CreatedAt time.Time
}

type ModerationStore interface {
	// Every guild's entries, oldest first.
	AllBlacklists(ctx context.Context) ([]BlacklistEntry, error)
	AllFilters(ctx context.Context) ([]Filter, error)

	// Reports whether the channel was new to the guild's blacklist. An
	// existing entry is left as it is. ErrGuildNotFound if the guild was
	// never recorded.
	AddToBlacklist(ctx context.Context, e BlacklistEntry) (bool, error)
	// An empty channelID removes the newest entry. Returns what was removed.
	RemoveFromBlacklist(ctx context.Context, guildID, channelID string) (*BlacklistEntry, error)

	// Reports whether the filter was new.
	AddFilter(ctx context.Context, f Filter) (bool, error)
	RemoveFilter(ctx context.Context, guildID string, kind FilterKind, pattern string) error
}
