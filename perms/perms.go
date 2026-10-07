// Package perms decides who may manage the bot in a guild: people with the
// matching Discord permission, or with a role the guild gave that job.
package perms

import (
	"context"
	"slices"

	"github.com/VTGare/gumi/v2"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/store"
)

type Level int

const (
	// Subscriptions and settings.
	Manager Level = iota
	// The blacklist and filters.
	Blacklister
)

// Allowed reports whether a member with these permissions and roles has
// the level. Administrators always do, and managers can blacklist too.
func Allowed(ctx context.Context, st store.RoleStore, guildID string, level Level, permissions discord.Permissions, roles []snowflake.ID) (bool, error) {
	if permissions.Has(discord.PermissionAdministrator) || permissions.Has(discord.PermissionManageGuild) {
		return true, nil
	}
	if level == Blacklister && permissions.Has(discord.PermissionManageMessages) {
		return true, nil
	}

	kinds := []store.RoleKind{store.RoleManager}
	if level == Blacklister {
		kinds = append(kinds, store.RoleBlacklister)
	}

	for _, kind := range kinds {
		ids, err := st.GuildRoles(ctx, guildID, kind)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(roles, func(r snowflake.ID) bool { return slices.Contains(ids, r.String()) }) {
			return true, nil
		}
	}

	return false, nil
}

func Check(st store.RoleStore, level Level) gumi.Check {
	return func(ctx *gumi.Context) error {
		if ctx.GuildID() == 0 {
			return &gumi.CheckError{Check: "guild_only", Message: "This command can only be used in a server."}
		}

		p, err := ctx.Permissions()
		if err != nil {
			return err
		}

		var roles []snowflake.ID
		if m := ctx.Member(); m != nil {
			roles = m.RoleIDs
		}

		ok, err := Allowed(ctx.Context(), st, ctx.GuildID().String(), level, p, roles)
		if err != nil {
			return err
		}
		if !ok {
			return &gumi.CheckError{Check: "perms", Message: Denied(level)}
		}
		return nil
	}
}

func Denied(level Level) string {
	if level == Blacklister {
		return "You need Manage Messages, or a Blacklister or Manager role from `/settings`, to do that."
	}
	return "You need Manage Server, or a Manager role from `/settings`, to do that."
}
