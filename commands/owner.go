package commands

import (
	"regexp"
	"strings"

	"github.com/VTGare/gumi"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/store"
)

var channelInURL = regexp.MustCompile(`UC[\w-]{22}`)

// Typing this into an optional text field clears it.
const clearValue = "-"

func ownerCommand(b *bot.Bot) *gumi.Command {
	// Zero permissions hides the command from everyone but administrators;
	// OwnerOnly does the real gatekeeping.
	hidden := int64(0)

	var guilds []string
	if b.Config.Discord.DevGuildID != "" {
		guilds = []string{b.Config.Discord.DevGuildID}
	}

	return &gumi.Command{
		Name:                     "owner",
		Description:              "Bot owner tools",
		Category:                 CategoryGeneral,
		Hidden:                   true,
		Checks:                   []gumi.Check{gumi.OwnerOnly},
		DefaultMemberPermissions: &hidden,
		GuildIDs:                 guilds,
		Subcommands: []*gumi.Command{
			{
				Name:        "streamers",
				Description: "Edit the streamer registry",
				Subcommands: []*gumi.Command{
					ownerStreamerAdd(b),
					ownerStreamerEdit(b),
					ownerStreamerRemove(b),
					ownerStreamerExport(b),
				},
			},
		},
	}
}

func ownerStreamerAdd(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "add",
		Description: "Add a streamer",
		Ephemeral:   true,
		Options: []*gumi.Option{
			gumi.String("channel", "YouTube channel ID or URL").Require(),
			gumi.String("name", "Display name").Require(),
			groupOption(b, "group", "Group"),
			gumi.String("twitter", "Twitter handle"),
			gumi.String("aliases", "Comma-separated nicknames"),
			gumi.String("channel_name", "YouTube channel title, used to spot collabs"),
			gumi.Boolean("free_chat", "They stream in rooms titled free chat"),
		},
		Handler: func(ctx *gumi.Context) error {
			id := channelInURL.FindString(ctx.Options.String("channel"))
			if id == "" {
				return gumi.NewUserError("That doesn't look like a YouTube channel ID or channel URL.")
			}

			if existing, ok := b.Streamers.Streamer(id); ok {
				return gumi.Errorf("%s is already in the registry. Use `/owner streamers edit` instead.", existing.Name)
			}

			st := store.Streamer{
				ChannelID: id,
				Name:      strings.TrimSpace(ctx.Options.String("name")),
				Source:    store.SourceOwner,
			}
			if err := applyStreamerEdits(b, ctx, &st); err != nil {
				return err
			}

			if err := b.Streamers.Save(ctx.Context(), st); err != nil {
				return err
			}

			return replySaved(b, ctx, "Added", id)
		},
	}
}

func ownerStreamerEdit(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "edit",
		Description: "Edit a streamer; only the options you fill in change",
		Ephemeral:   true,
		Options: []*gumi.Option{
			streamerOption(b, "streamer", "Who to edit").Require(),
			gumi.String("name", "Display name"),
			groupOption(b, "group", `Group, or "-" for none`),
			gumi.String("twitter", `Twitter handle, or "-" for none`),
			gumi.String("aliases", `Comma-separated nicknames, replacing the old ones, or "-" for none`),
			gumi.String("channel_name", `YouTube channel title, or "-" for none`),
			gumi.Boolean("free_chat", "They stream in rooms titled free chat"),
		},
		Handler: func(ctx *gumi.Context) error {
			found, err := resolveStreamer(b.Streamers, ctx.Options.String("streamer"))
			if err != nil {
				return err
			}

			st := *found
			st.Aliases = append([]string(nil), found.Aliases...)
			st.Source = store.SourceOwner
			if ctx.Options.Has("name") {
				st.Name = strings.TrimSpace(ctx.Options.String("name"))
			}
			if err := applyStreamerEdits(b, ctx, &st); err != nil {
				return err
			}

			if err := b.Streamers.Save(ctx.Context(), st); err != nil {
				return err
			}

			return replySaved(b, ctx, "Updated", st.ChannelID)
		},
	}
}

func ownerStreamerRemove(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "remove",
		Description: "Remove a streamer",
		Ephemeral:   true,
		Options:     []*gumi.Option{streamerOption(b, "streamer", "Who to remove").Require()},
		Handler: func(ctx *gumi.Context) error {
			st, err := resolveStreamer(b.Streamers, ctx.Options.String("streamer"))
			if err != nil {
				return err
			}

			if err := b.Streamers.Delete(ctx.Context(), st.ChannelID); err != nil {
				return err
			}

			msg := "Removed " + st.Name + "."
			if st.Source == store.SourceSeed {
				msg += " They're in the seed files, so they'll be back on the next restart unless you take them out there too."
			}
			return ctx.ReplyText(msg)
		},
	}
}

// Discord messages max out at 2000 characters; longer exports become a file.
const maxInlineExport = 1900

func ownerStreamerExport(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "export",
		Description: "Print owner-added streamers as seed entries to paste into the seed files",
		Ephemeral:   true,
		Options:     []*gumi.Option{streamerOption(b, "streamer", "Just this streamer; default is every owner entry")},
		Handler: func(ctx *gumi.Context) error {
			var sts []*store.Streamer
			if ctx.Options.Has("streamer") {
				st, err := resolveStreamer(b.Streamers, ctx.Options.String("streamer"))
				if err != nil {
					return err
				}
				sts = append(sts, st)
			} else {
				for _, st := range b.Streamers.Streamers() {
					if st.Source == store.SourceOwner {
						sts = append(sts, st)
					}
				}
			}

			if len(sts) == 0 {
				return ctx.ReplyText("Nothing to export: no streamers were added or edited with owner commands.")
			}

			out, err := b.Streamers.Export(sts)
			if err != nil {
				return err
			}

			note := "Paste into the seed files. Once deployed, matching entries go back to being managed by the seed."
			if len(out) <= maxInlineExport {
				return ctx.ReplyText(note + "\n```toml\n" + out + "```")
			}

			return ctx.Reply(&gumi.Response{
				Content: note,
				Files:   []*discordgo.File{{Name: "streamers.toml", ContentType: "text/plain", Reader: strings.NewReader(out)}},
			})
		},
	}
}

// applyStreamerEdits copies the optional fields that were filled in.
func applyStreamerEdits(b *bot.Bot, ctx *gumi.Context, st *store.Streamer) error {
	opts := ctx.Options

	if st.Name == "" {
		return gumi.NewUserError("The name can't be empty.")
	}

	if opts.Has("group") {
		switch v := strings.TrimSpace(opts.String("group")); v {
		case clearValue:
			st.GroupID = ""
		default:
			g, err := resolveGroup(b.Streamers, v)
			if err != nil {
				return err
			}
			st.GroupID = g.ID
		}
	}

	text := map[string]*string{
		"twitter":      &st.Twitter,
		"channel_name": &st.ChannelName,
	}
	for name, field := range text {
		if !opts.Has(name) {
			continue
		}
		v := strings.TrimSpace(opts.String(name))
		if v == clearValue {
			v = ""
		}
		*field = v
	}
	st.Twitter = strings.TrimPrefix(st.Twitter, "@")

	if opts.Has("aliases") {
		st.Aliases = nil
		if v := opts.String("aliases"); strings.TrimSpace(v) != clearValue {
			st.Aliases = splitAliases(v)
		}
	}

	if opts.Has("free_chat") {
		st.FreeChatStreams = opts.Bool("free_chat")
	}

	return nil
}

func replySaved(b *bot.Bot, ctx *gumi.Context, verb, channelID string) error {
	st, ok := b.Streamers.Streamer(channelID)
	if !ok {
		return ctx.ReplyText(verb + ".")
	}

	return ctx.Reply(&gumi.Response{
		Content: verb + " " + st.Name + ".",
		Embeds:  []*discordgo.MessageEmbed{streamerEmbed(b.Streamers, st)},
	})
}
