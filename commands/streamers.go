package commands

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/VTGare/gumi/v2"
	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
)

// Leaves room under Discord's 4096 character embed description limit.
const maxListLength = 4000

func streamersCommand(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "streamers",
		Description: "Browse the streamers GatorAid knows about",
		Category:    CategoryGeneral,
		Subcommands: []*gumi.Command{
			{
				Name:        "list",
				Description: "List streamer groups, or the streamers in one",
				Options:     []*gumi.Option{groupOption(b, "group", "The group to list")},
				Handler: func(ctx *gumi.Context) error {
					if !ctx.Options.Has("group") {
						return ctx.ReplyEmbed(groupOverview(b.Streamers))
					}

					g, err := resolveGroup(b.Streamers, ctx.Options.String("group"))
					if err != nil {
						return err
					}
					return ctx.ReplyEmbed(groupMembers(b.Streamers, g))
				},
			},
			{
				Name:        "info",
				Description: "Show what GatorAid knows about a streamer",
				Options:     []*gumi.Option{streamerOption(b, "streamer", "Name, alias or channel ID").Require()},
				Handler: func(ctx *gumi.Context) error {
					st, err := resolveStreamer(b.Streamers, ctx.Options.String("streamer"))
					if err != nil {
						return err
					}
					return ctx.ReplyEmbed(streamerEmbed(b.Streamers, st))
				},
			},
		},
	}
}

func streamerOption(b *bot.Bot, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		var choices []gumi.Choice
		for _, st := range b.Streamers.Search(ctx.Value, 25) {
			label := st.Name
			if g, ok := b.Streamers.Group(st.GroupID); ok {
				label += " · " + g.Name
			}
			// Picking a suggestion sends the channel ID, so names shared by
			// several streamers stay unambiguous.
			choices = append(choices, gumi.Choice{Name: label, Value: st.ChannelID})
		}
		return choices, nil
	})
}

func groupOption(b *bot.Bot, name, description string) *gumi.Option {
	return gumi.String(name, description).WithAutocomplete(func(ctx *gumi.AutocompleteContext) ([]gumi.Choice, error) {
		var choices []gumi.Choice
		for _, g := range b.Streamers.SearchGroups(ctx.Value, 25) {
			choices = append(choices, gumi.Choice{Name: g.Name, Value: g.ID})
		}
		return choices, nil
	})
}

func resolveStreamer(reg *streamers.Registry, query string) (*store.Streamer, error) {
	st, err := reg.Resolve(query)

	var amb *streamers.AmbiguousError
	switch {
	case errors.As(err, &amb):
		names := make([]string, 0, len(amb.Candidates))
		for _, c := range amb.Candidates[:min(10, len(amb.Candidates))] {
			names = append(names, c.Name)
		}
		return nil, gumi.Errorf("%q could be %s. Pick one from the suggestions.", query, strings.Join(names, ", "))
	case errors.Is(err, streamers.ErrNotFound):
		return nil, gumi.Errorf("I don't know a streamer called %q. Try `/streamers list`.", query)
	}

	return st, err
}

func resolveGroup(reg *streamers.Registry, query string) (*store.Group, error) {
	query = strings.TrimSpace(query)
	if g, ok := reg.Group(query); ok {
		return g, nil
	}

	for _, g := range reg.Groups() {
		if strings.EqualFold(g.Name, query) {
			return g, nil
		}
	}

	return nil, gumi.Errorf("There's no group called %q. Try `/streamers list`.", query)
}

func groupOverview(reg *streamers.Registry) discord.Embed {
	var sb strings.Builder
	for _, g := range reg.Groups() {
		if g.ParentID != "" {
			continue
		}

		fmt.Fprintf(&sb, "- **%s** (%d)\n", g.Name, len(reg.Members(g.ID)))
		for _, sub := range reg.Subgroups(g.ID) {
			fmt.Fprintf(&sb, "  - %s (%d)\n", sub.Name, len(reg.Members(sub.ID)))
		}
	}

	return discord.Embed{
		Title:       "Streamer groups",
		Description: sb.String(),
		Color:       Color,
		Footer:      &discord.EmbedFooter{Text: "/streamers list group:<name> shows who's in a group"},
	}
}

func groupMembers(reg *streamers.Registry, g *store.Group) discord.Embed {
	type section struct {
		title   string
		members []*store.Streamer
	}

	subgroups := reg.Subgroups(g.ID)

	// Members directly in the parent, like official channels, come first.
	var direct []*store.Streamer
	for _, st := range reg.Members(g.ID) {
		if st.GroupID == g.ID {
			direct = append(direct, st)
		}
	}

	sections := []section{{members: direct}}
	if len(subgroups) > 0 && len(direct) > 0 {
		sections[0].title = g.Name
	}
	for _, sub := range subgroups {
		sections = append(sections, section{title: sub.Name, members: reg.Members(sub.ID)})
	}

	var (
		sb      strings.Builder
		omitted int
	)
	for _, sec := range sections {
		if len(sec.members) == 0 {
			continue
		}

		if sec.title != "" {
			fmt.Fprintf(&sb, "**%s** (%d)\n", sec.title, len(sec.members))
		}

		for i, st := range sec.members {
			sep := ", "
			if i == len(sec.members)-1 {
				sep = "\n\n"
			}

			if sb.Len()+len(st.Name)+len(sep) > maxListLength {
				omitted += len(sec.members) - i
				break
			}
			sb.WriteString(st.Name)
			sb.WriteString(sep)
		}
	}

	desc := strings.TrimSpace(sb.String())
	if omitted > 0 {
		desc = strings.TrimSuffix(desc, ",") + fmt.Sprintf("\n…and %d more", omitted)
	}
	if desc == "" {
		desc = "Nobody yet."
	}

	return discord.Embed{
		Title:       fmt.Sprintf("%s (%d)", g.Name, len(reg.Members(g.ID))),
		Description: desc,
		Color:       Color,
	}
}

func streamerEmbed(reg *streamers.Registry, st *store.Streamer) discord.Embed {
	e := discord.Embed{
		Title: st.Name,
		URL:   "https://www.youtube.com/channel/" + st.ChannelID,
		Color: Color,
	}

	if lineage := reg.Lineage(st.GroupID); len(lineage) > 0 {
		names := make([]string, len(lineage))
		for i, g := range lineage {
			names[len(lineage)-1-i] = g.Name
		}
		e.Fields = append(e.Fields, discord.EmbedField{Name: "Group", Value: strings.Join(names, " › "), Inline: new(true)})
	}

	if st.ChannelName != "" {
		e.Fields = append(e.Fields, discord.EmbedField{Name: "Channel", Value: st.ChannelName, Inline: new(true)})
	}

	if st.Twitter != "" {
		e.Fields = append(e.Fields, discord.EmbedField{
			Name: "Twitter", Value: fmt.Sprintf("[@%s](https://x.com/%s)", st.Twitter, st.Twitter), Inline: new(true),
		})
	}

	if st.Twitch != "" {
		e.Fields = append(e.Fields, discord.EmbedField{
			Name: "Twitch", Value: fmt.Sprintf("[%s](https://www.twitch.tv/%s)", st.Twitch, st.Twitch), Inline: new(true),
		})
	}

	if len(st.Aliases) > 0 {
		e.Fields = append(e.Fields, discord.EmbedField{Name: "Aliases", Value: strings.Join(st.Aliases, ", ")})
	}

	if st.AvatarURL != "" {
		e.Thumbnail = &discord.EmbedResource{URL: st.AvatarURL}
	}

	footer := []string{st.ChannelID}
	switch st.Source {
	case store.SourceOwner:
		footer = append(footer, "edited by the bot owner")
	case store.SourceUser:
		footer = append(footer, "added by a server")
	}
	e.Footer = &discord.EmbedFooter{Text: strings.Join(footer, " · ")}

	return e
}

func splitAliases(s string) []string {
	var out []string
	for a := range strings.SplitSeq(s, ",") {
		if a = strings.TrimSpace(a); a != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, a) }) {
			out = append(out, a)
		}
	}
	return out
}
