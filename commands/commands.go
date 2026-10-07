package commands

import (
	"github.com/VTGare/gumi/v2"
	"github.com/disgoorg/snowflake/v2"

	"github.com/VTGare/gatoraid/bot"
)

const Color = bot.Color

const (
	CategoryRelay         = "Relay"
	CategoryNotifications = "Notifications"
	CategoryModeration    = "Moderation"
	CategoryGeneral       = "General"
)

func Register(b *bot.Bot) error {
	cmds := []*gumi.Command{
		gumi.HelpCommand(gumi.HelpConfig{
			Title:         "GatorAid commands",
			Color:         Color,
			Category:      CategoryGeneral,
			CategoryOrder: []string{CategoryRelay, CategoryNotifications, CategoryModeration, CategoryGeneral},
		}),
		subscriptionCommand(b, relayFeature),
		subscriptionCommand(b, cameosFeature),
		subscriptionCommand(b, gossipFeature),
		notifyCommand(b),
		logCommand(b),
		settingsCommand(b),
		blacklistCommand(b),
		blacklistAuthorCommand(b),
		filterCommand(b),
		streamersCommand(b),
	}
	if guild := b.Config.OwnerGuild(); guild != "" {
		cmds = append(cmds, ownerCommand(b, guild))
	}
	return b.Router.Register(cmds...)
}

// The store keeps Discord IDs as strings, with "" for none.
func idString(id snowflake.ID) string {
	if id == 0 {
		return ""
	}
	return id.String()
}
