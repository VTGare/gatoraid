package commands

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VTGare/gumi/v2"
	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/twitch"
	"github.com/VTGare/gatoraid/youtube/channel"
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
	// Takes YouTube channels outside the registry too. Cameos and gossip
	// depend on who counts as a VTuber, so they stay with the registry.
	anyChannel bool
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
		anyChannel:  true,
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
		description: "Notifications when streamers go live on YouTube",
		add:         "Notify when a streamer, a group or anyone goes live on YouTube",
		doing:       "posting YouTube live notifications for %s",
		noun:        "YouTube live notification",
		title:       "YouTube live notifications",
		pings:       "Notifications",
		anyChannel:  true,
		roleHelp:    "Role to ping",
	}
	twitchFeature = feature{
		Feature:     store.FeatureTwitch,
		command:     "notify twitch",
		description: "Notifications when streamers go live on Twitch",
		add:         "Notify when a streamer, a group or anyone goes live on Twitch",
		doing:       "posting Twitch live notifications for %s",
		noun:        "Twitch live notification",
		title:       "Twitch live notifications",
		pings:       "Notifications",
		anyChannel:  true,
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
		anyChannel:  true,
		roleHelp:    "Role to ping",
	}
)

// Channels the bot can post lines and notices in.
var textChannels = []discord.ChannelType{
	discord.ChannelTypeGuildText,
	discord.ChannelTypeGuildNews,
	discord.ChannelTypeGuildPublicThread,
	discord.ChannelTypeGuildPrivateThread,
	discord.ChannelTypeGuildNewsThread,
}

func subscriptionCommand(b *bot.Bot, f feature) *gumi.Command {
	cmd := managerCommand(b, f.command, f.description)
	cmd.Subcommands = subscriptionSubcommands(b, f)
	return cmd
}

func notifyCommand(b *bot.Bot) *gumi.Command {
	cmd := managerCommand(b, "notify", "Notifications for live streams and community posts")
	cmd.Category = CategoryNotifications
	cmd.Subcommands = []*gumi.Command{
		{Name: "youtube", Description: youtubeFeature.description, Subcommands: subscriptionSubcommands(b, youtubeFeature)},
		{Name: "twitch", Description: twitchFeature.description, Subcommands: subscriptionSubcommands(b, twitchFeature)},
		{Name: "posts", Description: postsFeature.description, Subcommands: subscriptionSubcommands(b, postsFeature)},
	}
	return cmd
}

// Visible to everyone, so people with a Manager role but no Manage Server
// permission can find them. The check does the gatekeeping.
func managerCommand(b *bot.Bot, name, description string) *gumi.Command {
	return &gumi.Command{
		Name:        name,
		Description: description,
		Category:    CategoryRelay,
		Checks:      []gumi.Check{permCheck(b.Store, manager)},
		Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func subscriptionSubcommands(b *bot.Bot, f feature) []*gumi.Command {
	targetHelp := "Streamer, group or all"
	switch {
	case f.streamersOnly:
		targetHelp = "Streamer"
	case f.anyChannel:
		targetHelp = "Streamer, group, all, or a YouTube channel link or @handle"
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
	query := strings.TrimSpace(ctx.Options.String("target"))

	var (
		target store.Target
		err    error
	)
	if f.anyChannel && outsideRegistry(b, query) {
		target, err = addUserChannel(b, ctx, query)
	} else {
		target, err = resolveTarget(b, f, query)
	}
	if err != nil {
		return err
	}

	sub := store.Subscription{
		GuildID:   ctx.GuildID().String(),
		Feature:   f.Feature,
		Target:    target,
		ChannelID: idString(ctx.Options.ID("channel")),
		RoleID:    idString(ctx.Options.ID("role")),
		CreatedBy: ctx.AuthorID().String(),
	}
	if sub.ChannelID == "" {
		sub.ChannelID = ctx.ChannelID().String()
	}
	if sub.RoleID == ctx.GuildID().String() {
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
		msg += "\n" + f.pings + " ping <@&" + sub.RoleID + ">."
	} else if !created && f.pings != "" {
		msg += "\n" + f.pings + " don't ping anyone."
	}

	return success(ctx, msg)
}

func subscriptionRemove(b *bot.Bot, f feature, ctx *gumi.Context) error {
	target, err := resolveSubscribed(b, f, ctx.Options.String("target"))
	if err != nil {
		return err
	}

	channelID := idString(ctx.Options.ID("channel"))
	if channelID == "" {
		channelID = ctx.ChannelID().String()
	}

	doing := fmt.Sprintf(f.doing, describeTarget(b, target))
	err = b.Subs.Remove(ctx.Context(), ctx.GuildID().String(), f.Feature, target, channelID)
	if errors.Is(err, store.ErrSubscriptionNotFound) {
		return gumi.Errorf("I'm not %s in <#%s>.", doing, channelID)
	}
	if err != nil {
		return err
	}

	return success(ctx, "Stopped "+doing+" in <#"+channelID+">.")
}

func subscriptionClear(b *bot.Bot, f feature, ctx *gumi.Context) error {
	channelID := idString(ctx.Options.ID("channel"))
	if channelID == "" {
		channelID = ctx.ChannelID().String()
	}

	n, err := b.Subs.Clear(ctx.Context(), ctx.GuildID().String(), f.Feature, channelID)
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

	return success(ctx, fmt.Sprintf("Removed %d %s from <#%s>.", n, noun, channelID))
}

func subscriptionList(b *bot.Bot, f feature, ctx *gumi.Context) error {
	list, err := b.Subs.Guild(ctx.Context(), ctx.GuildID().String(), f.Feature)
	if err != nil {
		return err
	}

	e := discord.Embed{Title: f.title, Color: Color}
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

// success confirms a change in a small embed, which stands apart from
// relayed lines in the same channel. Mentions in it never ping anyone.
func success(ctx *gumi.Context, msg string) error {
	return ctx.Reply(&gumi.Response{
		Embeds:          []discord.Embed{{Description: "✅ " + msg, Color: Color}},
		AllowedMentions: &discord.AllowedMentions{},
	})
}

// A channel link, handle or ID that isn't a registry streamer's ID. Links
// and handles may still turn out to be one once looked up.
func outsideRegistry(b *bot.Bot, query string) bool {
	if _, ok := b.Streamers.Streamer(query); ok {
		return false
	}
	_, err := channel.Path(query)
	return err == nil
}

// addUserChannel finds a channel on YouTube, checks that Holodex tracks it,
// since that's how the bot learns about its streams, and adds it to the
// registry as a user channel. Registry streamers are used as they are.
func addUserChannel(b *bot.Bot, ctx *gumi.Context, query string) (store.Target, error) {
	// YouTube and Holodex can take longer than Discord waits for a reply.
	if err := ctx.Defer(); err != nil {
		return store.Target{}, err
	}

	ch, err := b.Channels.Resolve(ctx.Context(), query)
	switch {
	case errors.Is(err, channel.ErrNotFound), errors.Is(err, channel.ErrInvalid):
		return store.Target{}, gumi.Errorf("There's no YouTube channel at %s.", inlineCode(query))
	case err != nil:
		return store.Target{}, gumi.WrapUserError("I couldn't reach YouTube to look that channel up. Try again in a bit.", err)
	}

	target := store.Target{Kind: store.TargetChannel, ID: ch.ID}
	if st, ok := b.Streamers.Streamer(ch.ID); ok && st.Curated() {
		return target, nil
	}

	added, err := userChannels(ctx.Context(), b, ctx.GuildID().String())
	if err != nil {
		return store.Target{}, err
	}
	if limit := b.Config.Limits.UserChannels; !added[ch.ID] && len(added) >= limit {
		return store.Target{}, gumi.Errorf("This server has reached its limit for channels from outside the streamer "+
			"list (%d). Remove one first.", limit)
	}

	if _, ok := b.Streamers.Streamer(ch.ID); ok {
		return target, nil
	}

	if b.Holodex == nil {
		return store.Target{}, gumi.NewUserError("I can only follow channels Holodex tracks, and this bot isn't connected to Holodex.")
	}
	hc, err := b.Holodex.Channel(ctx.Context(), ch.ID)
	switch {
	case errors.Is(err, holodex.ErrNotFound):
		return store.Target{}, gumi.Errorf("Holodex doesn't track **%s**, so I can't tell when it goes live. "+
			"The channel's owner can ask Holodex to add it at <https://holodex.net/addChannel>.", relay.EscapeMarkdown(ch.Name))
	case err != nil:
		return store.Target{}, gumi.WrapUserError("I couldn't reach Holodex to check that channel. Try again in a bit.", err)
	case hc.Inactive:
		return store.Target{}, gumi.Errorf("Holodex lists **%s** as inactive.", relay.EscapeMarkdown(ch.Name))
	}

	name := cmp.Or(hc.EnglishName, ch.Name, hc.Name)
	err = b.Streamers.Save(ctx.Context(), store.Streamer{
		ChannelID:    ch.ID,
		Name:         name,
		ChannelName:  ch.Name,
		Twitter:      hc.Twitter,
		Twitch:       holodexTwitch(hc),
		AvatarURL:    cmp.Or(ch.AvatarURL, hc.Photo),
		Source:       store.SourceUser,
		AddedByGuild: ctx.GuildID().String(),
	})
	return target, err
}

// userChannels are the channels outside the registry this server
// subscribes to in any way.
func userChannels(ctx context.Context, b *bot.Bot, guildID string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, f := range []store.Feature{store.FeatureRelay, store.FeatureYouTube, store.FeatureTwitch, store.FeaturePosts} {
		subs, err := b.Subs.Guild(ctx, guildID, f)
		if err != nil {
			return nil, err
		}
		for _, sub := range subs {
			if st, ok := b.Streamers.Lookup(sub.Target.ID); ok && sub.Target.Kind == store.TargetChannel && !st.Curated() {
				out[sub.Target.ID] = true
			}
		}
	}
	return out, nil
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
		list, err := b.Subs.Guild(ctx.Context(), ctx.GuildID().String(), f.Feature)
		if err != nil {
			return nil, err
		}

		channelID := idString(ctx.Options.ID("channel"))
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

// Holodex keeps Twitch usernames as their owners typed them, often with
// capitals. Ones that still aren't valid are left out.
func holodexTwitch(ch *holodex.Channel) string {
	username := strings.ToLower(strings.TrimSpace(ch.Twitch))
	if !twitch.ValidUsername(username) {
		return ""
	}

	return username
}
