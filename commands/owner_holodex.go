package commands

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/VTGare/gumi/v2"
	"github.com/disgoorg/disgo/discord"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/stream"
)

const maxListedStreams = 20

var errNoHolodex = gumi.NewUserError("Stream discovery is off: there's no Holodex API key configured.")

func ownerStreams(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "streams",
		Description: "Show the streams GatorAid is tracking",
		Ephemeral:   true,
		Handler: func(ctx *gumi.Context) error {
			if b.Streams == nil {
				return errNoHolodex
			}

			var live []stream.Stream
			var upcoming, members, freeChat int
			for _, s := range b.Streams.Streams() {
				switch {
				case s.MembersOnly:
					members++
				case s.FreeChat:
					freeChat++
				case s.Status == stream.Live:
					live = append(live, s)
				default:
					upcoming++
				}
			}

			var sb strings.Builder
			for i, s := range live {
				if i == maxListedStreams {
					fmt.Fprintf(&sb, "…and %d more\n", len(live)-i)
					break
				}
				fmt.Fprintf(&sb, "- [%s](<%s>) · %s\n", escapeLink(s.Title), s.URL(), s.ChannelName)
			}
			if len(live) == 0 {
				sb.WriteString("Nobody's live right now.\n")
			}

			return ctx.ReplyEmbed(discord.Embed{
				Title:       fmt.Sprintf("Live (%d)", len(live)),
				Description: sb.String(),
				Color:       Color,
				Footer: &discord.EmbedFooter{
					Text: fmt.Sprintf("Also tracking %d upcoming, %d members-only and %d free chat rooms", upcoming, members, freeChat),
				},
			})
		},
	}
}

func ownerStreamerSync(b *bot.Bot) *gumi.Command {
	return &gumi.Command{
		Name:        "sync",
		Description: "Compare a Holodex org with the registry",
		Ephemeral:   true,
		Defer:       true,
		Options: []*gumi.Option{
			gumi.String("org", "Holodex org, case-sensitive: Hololive, Nijisanji, Independents…").Require(),
			groupOption(b, "group", "Group to put missing channels in, in the generated seed entries"),
		},
		Handler: func(ctx *gumi.Context) error {
			if b.Holodex == nil {
				return errNoHolodex
			}

			org := strings.TrimSpace(ctx.Options.String("org"))
			groupID := ""
			if ctx.Options.Has("group") {
				g, err := resolveGroup(b.Streamers, ctx.Options.String("group"))
				if err != nil {
					return err
				}
				groupID = g.ID
			}

			channels, err := b.Holodex.OrgChannels(ctx.Context(), org)
			if err != nil {
				return err
			}
			if len(channels) == 0 {
				return gumi.Errorf("Holodex has no channels in %q. Org names are case-sensitive, like Hololive or Independents.", org)
			}

			var inactive, missing []*store.Streamer
			var twitchDiffs []string
			for _, ch := range channels {
				st, known := b.Streamers.Streamer(ch.ID)
				switch {
				case known && ch.Inactive:
					inactive = append(inactive, st)
				case known && holodexTwitch(&ch) != "" && holodexTwitch(&ch) != st.Twitch:
					twitchDiffs = append(twitchDiffs, fmt.Sprintf("- %s: %s on Holodex, %s here",
						st.Name, inlineCode(holodexTwitch(&ch)), inlineCode(cmp.Or(st.Twitch, "none"))))
				case !known && !ch.Inactive:
					name := ch.EnglishName
					if name == "" {
						name = ch.Name
					}
					missing = append(missing, &store.Streamer{
						ChannelID: ch.ID, Name: name, ChannelName: ch.Name, Twitter: ch.Twitter, Twitch: holodexTwitch(&ch),
						GroupID: groupID,
					})
				}
			}

			var report strings.Builder
			fmt.Fprintf(&report, "**%s**: %d channels on Holodex. %d in the registry are inactive there, %d active ones aren't in the registry.\n",
				org, len(channels), len(inactive), len(missing))

			if len(inactive) > 0 {
				report.WriteString("\nInactive on Holodex, probably graduated:\n")
				for _, st := range inactive {
					fmt.Fprintf(&report, "- %s (`%s`)\n", st.Name, st.ChannelID)
				}
			}

			if len(twitchDiffs) > 0 {
				report.WriteString("\nTwitch usernames that differ from Holodex:\n")
				report.WriteString(strings.Join(twitchDiffs, "\n") + "\n")
			}

			resp := &gumi.Response{Content: report.String()}
			if len(missing) > 0 {
				out, err := b.Streamers.Export(missing)
				if err != nil {
					return err
				}
				resp.Files = append(resp.Files, discord.NewFile("missing.toml", "", strings.NewReader(out)))
			}

			if len(resp.Content) > maxInlineExport {
				resp.Files = append(resp.Files, discord.NewFile("report.md", "", strings.NewReader(resp.Content)))
				resp.Content = fmt.Sprintf("**%s**: %d inactive in the registry, %d missing. Details attached.", org, len(inactive), len(missing))
			}

			return ctx.Reply(resp)
		},
	}
}

func escapeLink(s string) string {
	return strings.NewReplacer("[", "(", "]", ")").Replace(s)
}
