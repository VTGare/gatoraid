package commands

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VTGare/gumi"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/store"
)

var bareChannelID = regexp.MustCompile(`^UC[\w-]{22}$`)

// Autocomplete values for targets that aren't a single channel.
const (
	allValue    = "all"
	groupPrefix = "group:"
)

type feature struct {
	store.Feature
	// The command's full path, e.g. "relay" or "notify youtube".
	command     string
	description string
	add         string
	// What the bot does with a target, e.g. "relaying %s".
	doing string
	// "relay" as in "2 relay subscriptions".
	noun  string
	title string
	// Gossip matches a streamer's aliases, so it takes single streamers only.
	streamersOnly bool
	// What pings the role, e.g. "Relay notices". Empty for features
	// without a role.
	pings    string
	roleHelp string
}

var (
	relayFeature = feature{
		Feature:     store.FeatureRelay,
		command:     "relay",
		description: "Relay translations, the streamer and other VTubers from a stream's chat",
		add:         "Start relaying a streamer, a group or everyone",
		doing:       "relaying %s",
		noun:        "relay",
		title:       "Relays",
		pings:       "Relay notices",
		roleHelp:    "Role to ping when a relay starts",
	}
	cameosFeature = feature{
		Feature:     store.FeatureCameos,
		command:     "cameos",
		description: "Post what a streamer says in other streamers' chats",
		add:         "Start posting cameos by a streamer, a group or everyone",
		doing:       "posting cameos by %s",
		noun:        "cameos",
		title:       "Cameos",
	}
	gossipFeature = feature{
		Feature:       store.FeatureGossip,
		command:       "gossip",
		description:   "Post translations and VTuber messages that mention a streamer in other chats",
		add:           "Start posting messages about a streamer",
		doing:         "posting messages about %s",
		noun:          "gossip",
		title:         "Gossip",
		streamersOnly: true,
	}
	youtubeFeature = feature{
		Feature:     store.FeatureYouTube,
		command:     "notify youtube",
		description: "Notifications when streamers go live",
		add:         "Notify when a streamer, a group or anyone goes live",
		doing:       "posting live notifications for %s",
		noun:        "live notification",
		title:       "Live notifications",
		pings:       "Notifications",
		roleHelp:    "Role to ping",
	}
	postsFeature = feature{
		Feature:     store.FeaturePosts,
		command:     "notify posts",
		description: "Notifications for community posts",
		add:         "Notify when a streamer, a group or anyone makes a post",
		doing:       "posting new posts by %s",
		noun:        "post notification",
		title:       "Post notifications",
		pings:       "Notifications",
		roleHelp:    "Role to ping",
	}
)

// Channels the bot can post lines and notices in.
var textChannels = []discordgo.ChannelType{
	discordgo.ChannelTypeGuildText,
	discordgo.ChannelTypeGuildNews,
	discordgo.ChannelTypeGuildPublicThread,
	discordgo.ChannelTypeGuildPrivateThread,
	discordgo.ChannelTypeGuildNewsThread,
}

func subscriptionCommand(b *bot.Bot, f feature) *gumi.Command {
	cmd := managerCommand(f.command, f.description)
	cmd.Subcommands = subscriptionSubcommands(b, f)
	return cmd
}

func notifyCommand(b *bot.Bot) *gumi.Command {
	cmd := managerCommand("notify", "Notifications for live streams and community posts")
	cmd.Category = CategoryNotifications
	cmd.Subcommands = []*gumi.Command{
		{Name: "youtube", Description: youtubeFeature.description, Subcommands: subscriptionSubcommands(b, youtubeFeature)},
		{Name: "posts", Description: postsFeature.description, Subcommands: subscriptionSubcommands(b, postsFeature)},
	}
	return cmd
}

func managerCommand(name, description string) *gumi.Command {
	manage := int64(discordgo.PermissionManageGuild)
	return &gumi.Command{
		Name:                     name,
		Description:              description,
		Category:                 CategoryRelay,
		Checks:                   []gumi.Check{gumi.GuildOnly, gumi.HasPermissions(manage)},
		DefaultMemberPermissions: &manage,
		Contexts:                 []discordgo.InteractionContextType{discordgo.InteractionContextGuild},
	}
}

func subscriptionSubcommands(b *bot.Bot, f feature) []*gumi.Command {
	targetHelp := "Streamer, group or all"
	if f.streamersOnly {
		targetHelp = "Streamer"
	}

	addOptions := []*gumi.Option{
		targetOption(b, f, targetHelp).Require(),
		gumi.Channel("channel", "Where to post; default is this channel").WithChannelTypes(textChannels...),
	}
	if f.pings != "" {
		addOptions = append(addOptions, gumi.Role("role", f.roleHelp))
	}

	return []*gumi.Command{
		{
			Name:        "add",
			Description: f.add,
			Options:     addOptions,
			Handler:     func(ctx *gumi.Context) error { return subscriptionAdd(b, f, ctx) },
		},
		{
			Name:        "remove",
			Description: "Stop one",
			Options: []*gumi.Option{
				subscribedOption(b, f, "target", targetHelp).Require(),
				gumi.Channel("channel", "Which channel; default is this one").WithChannelTypes(textChannels...),
			},
			Handler: func(ctx *gumi.Context) error { return subscriptionRemove(b, f, ctx) },
		},
		{
			Name:        "clear",
			Description: "Stop everything in a channel",
			Options: []*gumi.Option{
				gumi.Channel("channel", "Which channel; default is this one").WithChannelTypes(textChannels...),
			},
			Handler: func(ctx *gumi.Context) error { return subscriptionClear(b, f, ctx) },
		},
		{
			Name:        "list",
			Description: "Show this server's " + strings.ToLower(f.title),
			Handler:     func(ctx *gumi.Context) error { return subscriptionList(b, f, ctx) },
		},
	}
}

func subscriptionAdd(b *bot.Bot, f feature, ctx *gumi.Context) error {
	target, err := resolveTarget(b, f, ctx.Options.String("target"))
	if err != nil {
		return err
	}

	sub := store.Subscription{
		GuildID:   ctx.GuildID(),
		Feature:   f.Feature,
		Target:    target,
		ChannelID: ctx.Options.ID("channel"),
		RoleID:    ctx.Options.ID("role"),
		CreatedBy: ctx.AuthorID(),
	}
	if sub.ChannelID == "" {
		sub.ChannelID = ctx.ChannelID()
	}
	if sub.RoleID == ctx.GuildID() {
		return gumi.NewUserError("Pinging @everyone isn't supported. Pick a role.")
	}

	created, err := b.Subs.Add(ctx.Context(), sub)
	if errors.Is(err, store.ErrGuildNotFound) {
		return gumi.NewUserError("I haven't finished setting up this server yet. Try again in a minute.")
	}
	if err != nil {
		return err
	}

	doing := fmt.Sprintf(f.doing, describeTarget(b, target))
	msg := "Now " + doing + " in <#" + sub.ChannelID + ">."
	if !created {
		msg = "Already " + doing + " in <#" + sub.ChannelID + ">."
	}
	if sub.RoleID != "" {
		msg += " " + f.pings + " ping <@&" + sub.RoleID + ">."
	} else if !created && f.pings != "" {
		msg += " " + f.pings + " don't ping anyone."
	}

	return reply(ctx, msg)
}

func subscriptionRemove(b *bot.Bot, f feature, ctx *gumi.Context) error {
	target, err := resolveSubscribed(b, f, ctx.Options.String("target"))
	if err != nil {
		return err
	}

	channelID := ctx.Options.ID("channel")
	if channelID == "" {
		channelID = ctx.ChannelID()
	}

	doing := fmt.Sprintf(f.doing, describeTarget(b, target))
	err = b.Subs.Remove(ctx.Context(), ctx.GuildID(), f.Feature, target, channelID)
	if errors.Is(err, store.ErrSubscriptionNotFound) {
		return gumi.Errorf("I'm not %s in <#%s>.", doing, channelID)
	}
	if err != nil {
		return err
	}

	return reply(ctx, "Stopped "+doing+" in <#"+channelID+">.")
}

func subscriptionClear(b *bot.Bot, f feature, ctx *gumi.Context) error {
	channelID := ctx.Options.ID("channel")
	if channelID == "" {
		channelID = ctx.ChannelID()
	}

	n, err := b.Subs.Clear(ctx.Context(), ctx.GuildID(), f.Feature, channelID)
	if err != nil {
		return err
	}

	noun := f.noun + " subscription"
	if n != 1 {
		noun += "s"
	}
	if n == 0 {
		return gumi.Errorf("<#%s> has no %s.", channelID, noun)
	}

	return reply(ctx, fmt.Sprintf("Removed %d %s from <#%s>.", n, noun, channelID))
}

func subscriptionList(b *bot.Bot, f feature, ctx *gumi.Context) error {
	list, err := b.Subs.Guild(ctx.Context(), ctx.GuildID(), f.Feature)
	if err != nil {
		return err
	}

	e := &discordgo.MessageEmbed{Title: f.title, Color: Color}
	if len(list) == 0 {
		e.Description = fmt.Sprintf("Nothing yet. Add one with `/%s add`.", f.command)
		return ctx.ReplyEmbed(e)
	}

	var (
		sb      strings.Builder
		channel string
		omitted int
	)
	for i, sub := range list {
		var line string
		if sub.ChannelID != channel {
			channel = sub.ChannelID
			line = "\n<#" + channel + ">\n"
		}

		line += "- " + describeTarget(b, sub.Target)
		if sub.RoleID != "" {
			line += " · <@&" + sub.RoleID + ">"
		}
		line += "\n"

		if sb.Len()+len(line) > maxListLength {
			omitted = len(list) - i
			break
		}
		sb.WriteString(line)
	}

	e.Description = strings.TrimSpace(sb.String())
	if omitted > 0 {
		e.Description += fmt.Sprintf("\n…and %d more", omitted)
	}

	return ctx.ReplyEmbed(e)
}

// Replies mention channels and roles, which mustn't ping anyone.
func reply(ctx *gumi.Context, msg string) error {
	return ctx.Reply(&gumi.Response{Content: msg, AllowedMentions: &discordgo.MessageAllowedMentions{}})
}

func resolveTarget(b *bot.Bot, f feature, query string) (store.Target, error) {
	query = strings.TrimSpace(query)

	groupID, isGroup := strings.CutPrefix(query, groupPrefix)
	if f.streamersOnly && (isGroup || strings.EqualFold(query, allValue)) {
		return store.Target{}, gumi.Errorf("/%s follows one streamer at a time.", f.command)
	}

	switch {
	case strings.EqualFold(query, allValue):
		return store.Target{Kind: store.TargetAll}, nil
	case isGroup:
		g, err := resolveGroup(b.Streamers, groupID)
		if err != nil {
			return store.Target{}, err
		}
		return store.Target{Kind: store.TargetGroup, ID: g.ID}, nil
	}

	if st, ok := b.Streamers.Streamer(query); ok {
		return store.Target{Kind: store.TargetChannel, ID: st.ChannelID}, nil
	}

	if !f.streamersOnly {
		if g, err := resolveGroup(b.Streamers, query); err == nil {
			return store.Target{Kind: store.TargetGroup, ID: g.ID}, nil
		}
	}

	st, err := resolveStreamer(b.Streamers, query)
	if err != nil {
		return store.Target{}, err
	}
	return store.Target{Kind: store.TargetChannel, ID: st.ChannelID}, nil
}

// Like resolveTarget, but also takes channel IDs the registry hid or never
// had, so subscriptions to removed streamers can still go.
func resolveSubscribed(b *bot.Bot, f feature, query string) (store.Target, error) {
	query = strings.TrimSpace(query)
	if bareChannelID.MatchString(query) {
		return store.Target{Kind: store.TargetChannel, ID: query}, nil
	}
	if id, ok := strings.CutPrefix(query, groupPrefix); ok && !f.streamersOnly {
		return store.Target{Kind: store.TargetGroup, ID: id}, nil
	}
	return resolveTarget(b, f, query)
}

func describeTarget(b *bot.Bot, t store.Target) string {
	switch t.Kind {
	case store.TargetAll:
		return "every streamer"
	case store.TargetGroup:
		if g, ok := b.Streamers.Group(t.ID); ok {
			return "everyone in **" + g.Name + "**"
		}
		return "the removed group `" + t.ID + "`"
	}

	st, ok := b.Streamers.Lookup(t.ID)
	switch {
	case !ok:
		return "`" + t.ID + "`"
	case st.Removed():
		return "**" + st.Name + "** (removed)"
	}
	return "**" + st.Name + "**"
}

func targetLabel(b *bot.Bot, t store.Target) string {
	return strings.ReplaceAll(describeTarget(b, t), "**", "")
}

func targetOption(b *bot.Bot, f feature, description string) *gumi.Option {
	return gumi.String("target", description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		var choices []gumi.Choice
		if !f.streamersOnly {
			q := strings.ToLower(strings.TrimSpace(ctx.Value))
			if strings.HasPrefix(allValue, q) || strings.HasPrefix("every streamer", q) {
				choices = append(choices, gumi.Choice{Name: "Every streamer", Value: allValue})
			}
			for _, g := range b.Streamers.SearchGroups(ctx.Value, 25-len(choices)) {
				choices = append(choices, gumi.Choice{Name: g.Name + " · group", Value: groupPrefix + g.ID})
			}
		}

		for _, st := range b.Streamers.Search(ctx.Value, 25-len(choices)) {
			label := st.Name
			if g, ok := b.Streamers.Group(st.GroupID); ok {
				label += " · " + g.Name
			}
			choices = append(choices, gumi.Choice{Name: label, Value: st.ChannelID})
		}

		return choices, nil
	})
}

// Suggests the server's existing subscriptions, in the chosen channel if
// there is one.
func subscribedOption(b *bot.Bot, f feature, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		list, err := b.Subs.Guild(ctx.Context(), ctx.GuildID(), f.Feature)
		if err != nil {
			return nil, err
		}

		channelID := ctx.Options.ID("channel")
		q := strings.ToLower(strings.TrimSpace(ctx.Value))

		var (
			choices []gumi.Choice
			seen    = map[store.Target]bool{}
		)
		for _, sub := range list {
			if seen[sub.Target] || (channelID != "" && sub.ChannelID != channelID) {
				continue
			}
			seen[sub.Target] = true

			label := capitalize(targetLabel(b, sub.Target))
			if q != "" && !strings.Contains(strings.ToLower(label), q) {
				continue
			}

			value := sub.Target.ID
			switch sub.Target.Kind {
			case store.TargetAll:
				value = allValue
			case store.TargetGroup:
				value = groupPrefix + sub.Target.ID
			}
			choices = append(choices, gumi.Choice{Name: label, Value: value})
			if len(choices) == 25 {
				break
			}
		}

		return choices, nil
	})
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}
