package commands

import (
	"github.com/VTGare/gumi"

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
	return b.Router.Register(
		gumi.HelpCommand(gumi.HelpConfig{
			Title:         "GatorAid commands",
			Color:         Color,
			Category:      CategoryGeneral,
			CategoryOrder: []string{CategoryRelay, CategoryNotifications, CategoryModeration, CategoryGeneral},
		}),
		subscriptionCommand(b, relayFeature),
		subscriptionCommand(b, cameosFeature),
		subscriptionCommand(b, gossipFeature),
		streamersCommand(b),
		ownerCommand(b),
	)
}
