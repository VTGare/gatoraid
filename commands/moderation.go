package commands

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VTGare/gumi"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/youtube/channel"
)

const (
	maxPattern     = 100
	maxReason      = 200
	maxFieldLength = 1024
)

var moderatorChecks = []gumi.Check{
	gumi.GuildOnly,
	gumi.HasPermissions(discordgo.PermissionManageMessages),
}

func moderatorPermissions() *int64 {
	p := int64(discordgo.PermissionManageMessages)
	return &p
}

func blacklistCommand(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:                     "blacklist",
		Description:              "Stop relaying someone's messages in this server",
		Category:                 CategoryModeration,
		Checks:                   moderatorChecks,
		DefaultMemberPermissions: moderatorPermissions(),
		Contexts:                 []discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Subcommands: []*gumi.Command{
			{
				Name:        "add",
				Description: "Blacklist a YouTube channel",
				Ephemeral:   true,
				Defer:       true,
				Options: []*gumi.Option{
					recentAuthorOption(b, "channel", "Channel link, @handle or channel ID, or someone recently relayed").Require(),
					gumi.String("reason", "Why, for the list").WithLength(1, maxReason),
				},
				Handler: func(ctx *gumi.Context) error { return blacklistAdd(b, ctx) },
			},
			{
				Name:        "remove",
				Description: "Take a channel off the blacklist; default is the last one added",
				Ephemeral:   true,
				Options:     []*gumi.Option{blacklistedOption(b, "channel", "Who to take off")},
				Handler:     func(ctx *gumi.Context) error { return blacklistRemove(b, ctx) },
			},
			{
				Name:        "list",
				Description: "Get the blacklist as a file",
				Ephemeral:   true,
				Handler:     func(ctx *gumi.Context) error { return blacklistList(b, ctx) },
			},
		},
	}
}

func blacklistAuthorCommand(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:                     "Blacklist author",
		Type:                     gumi.MessageContext,
		Category:                 CategoryModeration,
		Checks:                   moderatorChecks,
		DefaultMemberPermissions: moderatorPermissions(),
		Contexts:                 []discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Ephemeral:                true,
		Handler: func(ctx *gumi.Context) error {
			line, err := b.Store.LineByMessage(ctx.Context(), ctx.GuildID(), ctx.TargetMessage.ID)
			if errors.Is(err, store.ErrLineNotFound) {
				return gumi.NewUserError("That's not a line I relayed in this server in the last week.")
			}
			if err != nil {
				return err
			}

			msg, err := addToBlacklist(b, ctx, store.BlacklistEntry{
				GuildID:   ctx.GuildID(),
				ChannelID: line.AuthorChannelID,
				Name:      line.AuthorName,
				AddedBy:   ctx.AuthorID(),
			})
			if err != nil {
				return err
			}

			if line.Kind == store.LineOwner {
				msg += " Streamers' lines in their own chat are always relayed, so this only stops their cameos and gossip."
			}
			return ctx.ReplyText(msg)
		},
	}
}

func blacklistAdd(b *bot.Bot, ctx *gumi.Context) error {
	input := ctx.Options.String("channel")

	ch, err := b.Channels.Resolve(ctx.Context(), input)
	switch {
	case errors.Is(err, channel.ErrInvalid):
		return gumi.NewUserError("That's not a YouTube channel. Paste a channel link, an @handle or a channel ID, or pick someone from the suggestions.")
	case errors.Is(err, channel.ErrNotFound):
		return gumi.Errorf("There's no YouTube channel at %s.", inlineCode(input))
	case err != nil:
		return gumi.WrapUserError("I couldn't reach YouTube to look that channel up. Try again in a bit.", err)
	}

	// Chat shows @handles, so that's the name people will recognise.
	name := ch.Handle
	if name == "" {
		name = ch.Name
	}

	msg, err := addToBlacklist(b, ctx, store.BlacklistEntry{
		GuildID:   ctx.GuildID(),
		ChannelID: ch.ID,
		Name:      name,
		Reason:    strings.TrimSpace(ctx.Options.String("reason")),
		AddedBy:   ctx.AuthorID(),
	})
	if err != nil {
		return err
	}

	return ctx.ReplyText(msg)
}

func addToBlacklist(b *bot.Bot, ctx *gumi.Context, e store.BlacklistEntry) (string, error) {
	who := blacklistName(e)

	if b.Moderation.Blacklisted(e.GuildID, e.ChannelID) {
		return "", gumi.Errorf("%s is already blacklisted.", who)
	}

	if _, err := b.Moderation.AddToBlacklist(ctx.Context(), e); err != nil {
		if errors.Is(err, store.ErrGuildNotFound) {
			return "", gumi.NewUserError("I haven't finished setting up this server yet. Try again in a minute.")
		}
		return "", err
	}

	return fmt.Sprintf("Blacklisted %s. Their messages won't be relayed here. `/blacklist remove` undoes it.", who), nil
}

func blacklistRemove(b *bot.Bot, ctx *gumi.Context) error {
	channelID := ""
	if ctx.Options.Has("channel") {
		var err error
		if channelID, err = findBlacklisted(b, ctx); err != nil {
			return err
		}
	}

	e, err := b.Moderation.RemoveFromBlacklist(ctx.Context(), ctx.GuildID(), channelID)
	switch {
	case errors.Is(err, store.ErrNotBlacklisted) && channelID == "":
		return gumi.NewUserError("The blacklist is empty.")
	case errors.Is(err, store.ErrNotBlacklisted):
		return gumi.NewUserError("That channel isn't blacklisted.")
	case err != nil:
		return err
	}

	return ctx.ReplyText("Took " + blacklistName(*e) + " off the blacklist.")
}

// Suggestions send the channel ID. Typed input can also be a name on the
// blacklist, or a link or handle to look up.
func findBlacklisted(b *bot.Bot, ctx *gumi.Context) (string, error) {
	input := strings.TrimSpace(ctx.Options.String("channel"))

	for _, e := range b.Moderation.Blacklist(ctx.GuildID()) {
		if e.ChannelID == input || strings.EqualFold(e.Name, input) {
			return e.ChannelID, nil
		}
	}

	ch, err := b.Channels.Resolve(ctx.Context(), input)
	switch {
	case errors.Is(err, channel.ErrInvalid), errors.Is(err, channel.ErrNotFound):
		return "", gumi.NewUserError("That channel isn't blacklisted.")
	case err != nil:
		return "", gumi.WrapUserError("I couldn't reach YouTube to look that channel up. Try again in a bit.", err)
	}
	return ch.ID, nil
}

func blacklistList(b *bot.Bot, ctx *gumi.Context) error {
	entries := b.Moderation.Blacklist(ctx.GuildID())
	if len(entries) == 0 {
		return ctx.ReplyText("The blacklist is empty.")
	}

	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\n", e.ChannelID, e.Name, e.CreatedAt.UTC().Format(time.DateOnly), e.Reason)
	}

	count := fmt.Sprintf("%d blacklisted channels, newest first.", len(entries))
	if len(entries) == 1 {
		count = "1 blacklisted channel."
	}

	return ctx.Reply(&gumi.Response{
		Content: count,
		Files:   []*discordgo.File{{Name: "blacklist.txt", ContentType: "text/plain", Reader: strings.NewReader(sb.String())}},
	})
}

func blacklistName(e store.BlacklistEntry) string {
	if e.Name == "" {
		return inlineCode(e.ChannelID)
	}
	return "**" + relay.EscapeMarkdown(e.Name) + "**"
}

func recentAuthorOption(b *bot.Bot, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		lines, err := b.Store.RecentAuthors(ctx.Context(), ctx.GuildID(), strings.TrimSpace(ctx.Value), 25)
		if err != nil {
			return nil, err
		}

		choices := make([]gumi.Choice, 0, len(lines))
		for _, l := range lines {
			choices = append(choices, gumi.Choice{Name: l.AuthorName + " · " + l.AuthorChannelID, Value: l.AuthorChannelID})
		}
		return choices, nil
	})
}

func blacklistedOption(b *bot.Bot, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		q := strings.ToLower(strings.TrimSpace(ctx.Value))

		var choices []gumi.Choice
		for _, e := range b.Moderation.Blacklist(ctx.GuildID()) {
			label := e.ChannelID
			if e.Name != "" {
				label = e.Name + " · " + e.ChannelID
			}
			if q != "" && !strings.Contains(strings.ToLower(label), q) {
				continue
			}

			choices = append(choices, gumi.Choice{Name: label, Value: e.ChannelID})
			if len(choices) == 25 {
				break
			}
		}
		return choices, nil
	})
}

func filterCommand(b *bot.Bot) *gumi.Command {
	kind := func() *gumi.Option {
		return gumi.String("type", "Banned words drop lines, wanted prefixes mark translations").WithChoices(
			gumi.Choice{Name: "banned: drop lines containing it", Value: string(store.FilterBanned)},
			gumi.Choice{Name: "wanted: lines starting with it are translations", Value: string(store.FilterWanted)},
		)
	}

	return &gumi.Command{
		Name:                     "filter",
		Description:              "Drop lines with banned words, or mark lines with a prefix as translations",
		Category:                 CategoryModeration,
		Checks:                   moderatorChecks,
		DefaultMemberPermissions: moderatorPermissions(),
		Contexts:                 []discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Subcommands: []*gumi.Command{
			{
				Name:        "add",
				Description: "Add a banned word or a wanted prefix",
				Ephemeral:   true,
				Options: []*gumi.Option{
					kind().Require(),
					gumi.String("pattern", "Text to match, ignoring case").WithLength(1, maxPattern).Require(),
				},
				Handler: func(ctx *gumi.Context) error { return filterAdd(b, ctx) },
			},
			{
				Name:        "remove",
				Description: "Remove a filter",
				Ephemeral:   true,
				Options: []*gumi.Option{
					kind().Require(),
					filterOption(b, "pattern", "Which one").Require(),
				},
				Handler: func(ctx *gumi.Context) error { return filterRemove(b, ctx) },
			},
			{
				Name:        "list",
				Description: "Show this server's filters",
				Ephemeral:   true,
				Handler:     func(ctx *gumi.Context) error { return filterList(b, ctx) },
			},
		},
	}
}

func filterAdd(b *bot.Bot, ctx *gumi.Context) error {
	f := store.Filter{
		GuildID: ctx.GuildID(),
		Kind:    store.FilterKind(ctx.Options.String("type")),
		Pattern: normalizePattern(ctx.Options.String("pattern")),
	}
	if f.Pattern == "" {
		return gumi.NewUserError("The pattern can't be blank.")
	}

	created, err := b.Moderation.AddFilter(ctx.Context(), f)
	switch {
	case errors.Is(err, store.ErrGuildNotFound):
		return gumi.NewUserError("I haven't finished setting up this server yet. Try again in a minute.")
	case err != nil:
		return err
	case !created:
		return gumi.Errorf("%s is already a %s filter.", inlineCode(f.Pattern), f.Kind)
	}

	if f.Kind == store.FilterBanned {
		return ctx.ReplyText("Lines containing " + inlineCode(f.Pattern) + " won't be relayed, except the streamer's own.")
	}
	return ctx.ReplyText("Lines starting with " + inlineCode(f.Pattern) + " now count as translations.")
}

func filterRemove(b *bot.Bot, ctx *gumi.Context) error {
	kind := store.FilterKind(ctx.Options.String("type"))
	pattern := normalizePattern(ctx.Options.String("pattern"))

	err := b.Moderation.RemoveFilter(ctx.Context(), ctx.GuildID(), kind, pattern)
	if errors.Is(err, store.ErrFilterNotFound) {
		return gumi.Errorf("%s isn't a %s filter.", inlineCode(pattern), kind)
	}
	if err != nil {
		return err
	}

	return ctx.ReplyText(fmt.Sprintf("Removed the %s filter %s.", kind, inlineCode(pattern)))
}

// Discord caps embed fields at 1024 characters, so long lists go out as a
// file instead.
func filterList(b *bot.Bot, ctx *gumi.Context) error {
	sections := []struct {
		kind     store.FilterKind
		title    string
		patterns []string
	}{
		{store.FilterBanned, "Banned: lines containing these aren't relayed", nil},
		{store.FilterWanted, "Wanted: lines starting with these are translations", nil},
	}

	empty := true
	for _, f := range b.Moderation.Filters(ctx.GuildID()) {
		for i := range sections {
			if sections[i].kind == f.Kind {
				sections[i].patterns = append(sections[i].patterns, f.Pattern)
				empty = false
			}
		}
	}

	e := &discordgo.MessageEmbed{Title: "Filters", Color: Color}
	if empty {
		e.Description = "No filters yet. Add one with `/filter add`."
		return ctx.ReplyEmbed(e)
	}

	var file strings.Builder
	fits := true
	for _, sec := range sections {
		if len(sec.patterns) == 0 {
			continue
		}

		codes := make([]string, len(sec.patterns))
		for i, p := range sec.patterns {
			codes[i] = inlineCode(p)
		}
		value := strings.Join(codes, ", ")
		fits = fits && len(value) <= maxFieldLength
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: sec.title, Value: value})

		fmt.Fprintf(&file, "%s\n%s\n\n", sec.title, strings.Join(sec.patterns, "\n"))
	}

	if fits {
		return ctx.ReplyEmbed(e)
	}

	return ctx.Reply(&gumi.Response{
		Content: "Too many filters to show here, so here's a file.",
		Files:   []*discordgo.File{{Name: "filters.txt", ContentType: "text/plain", Reader: strings.NewReader(file.String())}},
	})
}

func filterOption(b *bot.Bot, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		kind := store.FilterKind(ctx.Options.String("type"))
		q := normalizePattern(ctx.Value)

		var choices []gumi.Choice
		for _, f := range b.Moderation.Filters(ctx.GuildID()) {
			if (kind != "" && f.Kind != kind) || !strings.Contains(f.Pattern, q) {
				continue
			}
			choices = append(choices, gumi.Choice{Name: f.Pattern, Value: f.Pattern})
			if len(choices) == 25 {
				break
			}
		}
		return choices, nil
	})
}

// Matching ignores case, so patterns are kept lowercase.
func normalizePattern(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Inline code can't hold backticks.
func inlineCode(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}
