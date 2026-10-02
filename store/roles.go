package store

import "context"

type RoleKind string

const (
	// Manages subscriptions and settings.
	RoleManager RoleKind = "manager"
	// Manages the blacklist and filters.
	RoleBlacklister RoleKind = "blacklister"
)

type RoleStore interface {
	// The guild's bot roles of one kind.
	GuildRoles(ctx context.Context, guildID string, kind RoleKind) ([]string, error)
	// Replaces the guild's roles of one kind. ErrGuildNotFound if the guild
	// was never recorded.
	SetGuildRoles(ctx context.Context, guildID string, kind RoleKind, roleIDs []string) error
}
