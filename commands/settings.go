package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/VTGare/gumi"
	"github.com/bwmarrin/discordgo"

	"github.com/VTGare/gatoraid/bot"
	"github.com/VTGare/gatoraid/perms"
	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/translate"
)

const settingsTimeout = 10 * time.Second

type toggle struct {
	name, section string
	label, hint   string
	field         func(*store.Settings) *bool
}

var toggles = []toggle{
	{"mods", "relay", "Mod messages", "Relay what chat moderators say",
		func(s *store.Settings) *bool { return &s.ModMessages }},
	{"prechat", "relay", "Waiting rooms", "Relay the waiting room before the stream starts",
		func(s *store.Settings) *bool { return &s.Prechat }},
	{"chatlink", "relay", "Chat link", "Add a link to the chat when a channel relays several streamers",
		func(s *store.Settings) *bool { return &s.ShowChat }},
	{"translate", "relay", "Auto-translate", "Translate what VTubers say when it isn't in the server's language",
		func(s *store.Settings) *bool { return &s.AutoTranslate }},
	{"membersonly", "streams", "Notify members-only streams", "Live notifications for streams only members can watch",
		func(s *store.Settings) *bool { return &s.NotifyMembersOnly }},
	{"relayfreechat", "streams", "Relay free chat rooms", "Relay the chat of rooms titled free chat",
		func(s *store.Settings) *bool { return &s.RelayFreeChat }},
}

type section struct {
	key, title, summary string
}

var sections = []section{
	{"relay", "Relay", "Mod messages, waiting rooms, chat link, auto-translate"},
	{"translation", "Translation", "Which language VTuber lines are translated into"},
	{"logs", "Logs", "Where stream logs go"},
	{"streams", "Streams", "Members-only streams and free chat rooms"},
	{"permissions", "Permissions", "Roles that can manage the bot"},
}

func settingsCommand(b *bot.Bot) *gumi.Command {
	cmd := &gumi.Command{
		Name:        "settings",
		Description: "See and change how GatorAid works in this server",
		Category:    CategoryGeneral,
		Ephemeral:   true,
		Checks:      []gumi.Check{gumi.GuildOnly},
		Contexts:    []discordgo.InteractionContextType{discordgo.InteractionContextGuild},
	}
	p := &settingsPanel{b: b, cmd: cmd}
	cmd.Handler = p.open
	cmd.Components = p.handle
	return cmd
}

// settingsPanel is the /settings message: an overview and a page per
// section. Its state lives in the component IDs (opener, action, argument),
// so it keeps working across restarts.
type settingsPanel struct {
	b   *bot.Bot
	cmd *gumi.Command
}

func (p *settingsPanel) open(ctx *gumi.Context) error {
	v, err := p.view(ctx.Context(), ctx.Session, ctx.GuildID(), ctx.AuthorID())
	if err != nil {
		return err
	}
	return ctx.Reply(v.render("home"))
}

// Anyone can look around. Changing things takes the Manager level.
func (p *settingsPanel) handle(ctx *gumi.ComponentContext) error {
	owner, action, arg := ctx.Arg(0), ctx.Arg(1), ctx.Arg(2)

	if ctx.UserID() != owner {
		return ctx.Reply(gumi.Text("This panel is someone else's. Run `/settings` for your own.").Private())
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), settingsTimeout)
	defer cancel()

	section := arg
	switch action {
	case "go":
	case "nav":
		section = first(ctx.Values())
	default:
		var roles []string
		if m := ctx.Interaction.Member; m != nil {
			roles = m.Roles
		}
		ok, err := perms.Allowed(reqCtx, p.b.Store, ctx.GuildID(), perms.Manager, ctx.Permissions(), roles)
		if err != nil {
			return err
		}
		if !ok {
			return ctx.Reply(gumi.Text(perms.Denied(perms.Manager)).Private())
		}

		if action == "otherlang" {
			return ctx.Modal(gumi.ComponentID(p.cmd, owner, "langcode"), "Translation language", discordgo.TextInput{
				CustomID: "code", Label: "DeepL language code, like FI or PT-BR", Style: discordgo.TextInputShort,
				Required: true, MinLength: 2, MaxLength: 10,
			})
		}

		if section, err = p.apply(reqCtx, ctx, action, arg); err != nil {
			return err
		}
	}

	v, err := p.view(reqCtx, ctx.Session, ctx.GuildID(), owner)
	if err != nil {
		return err
	}
	return ctx.Update(v.render(section))
}

// apply makes one change and returns the section to show next.
func (p *settingsPanel) apply(ctx context.Context, cc *gumi.ComponentContext, action, arg string) (string, error) {
	guildID := cc.GuildID()

	if action == "roles" {
		kind := store.RoleKind(arg)
		if kind != store.RoleManager && kind != store.RoleBlacklister {
			return "home", nil
		}
		return "permissions", p.b.Store.SetGuildRoles(ctx, guildID, kind, cc.Values())
	}

	g, err := p.b.Store.Guild(ctx, guildID)
	if err != nil {
		return "", err
	}
	settings := g.Settings

	section := "home"
	switch action {
	case "toggle":
		for _, t := range toggles {
			if t.name == arg {
				f := t.field(&settings)
				*f = !*f
				section = t.section
			}
		}
	case "lang":
		if code := first(cc.Values()); code != "" {
			settings.TargetLanguage = code
		}
		section = "translation"
	case "langcode":
		code := strings.ToUpper(strings.TrimSpace(cc.TextInput("code")))
		if !p.knownLanguage(ctx, code) {
			return "", gumi.Errorf("DeepL can't translate into %s. The codes are listed at "+
				"<https://developers.deepl.com/docs/getting-started/supported-languages>.", inlineCode(code))
		}
		settings.TargetLanguage = code
		section = "translation"
	case "logchannel":
		settings.LogChannelID = first(cc.Values())
		section = "logs"
	case "logclear":
		settings.LogChannelID = ""
		section = "logs"
	}

	if err := p.b.Store.UpdateGuildSettings(ctx, guildID, settings); err != nil {
		return "", err
	}
	p.b.SettingsChanged(guildID)
	return section, nil
}

// DeepL's own list when it can be reached, else the common ones.
func (p *settingsPanel) knownLanguage(ctx context.Context, code string) bool {
	if p.b.Translator != nil {
		if langs, err := p.b.Translator.Languages(ctx); err == nil {
			return slices.ContainsFunc(langs, func(l translate.Language) bool { return l.Code == code })
		}
	}
	return slices.ContainsFunc(translate.Common, func(l translate.Language) bool { return l.Code == code })
}

func (p *settingsPanel) view(ctx context.Context, s *discordgo.Session, guildID, owner string) (*panelView, error) {
	g, err := p.b.Store.Guild(ctx, guildID)
	if err != nil {
		return nil, err
	}

	v := &panelView{
		cmd: p.cmd, owner: owner, settings: g.Settings, counts: map[store.Feature]int{},
		translation: p.b.Translator != nil,
	}
	for _, f := range []store.Feature{store.FeatureRelay, store.FeatureCameos, store.FeatureGossip, store.FeatureYouTube, store.FeaturePosts} {
		subs, err := p.b.Subs.Guild(ctx, guildID, f)
		if err != nil {
			return nil, err
		}
		v.counts[f] = len(subs)
	}
	added, err := userChannels(ctx, p.b, guildID)
	if err != nil {
		return nil, err
	}
	v.outside = len(added)
	v.blacklisted = len(p.b.Moderation.Blacklist(guildID))
	v.filters = len(p.b.Moderation.Filters(guildID))

	if v.managers, err = p.b.Store.GuildRoles(ctx, guildID, store.RoleManager); err != nil {
		return nil, err
	}
	if v.blacklisters, err = p.b.Store.GuildRoles(ctx, guildID, store.RoleBlacklister); err != nil {
		return nil, err
	}

	// Only the state cache, so opening the panel never waits on Discord.
	if s.State != nil {
		if dg, err := s.State.Guild(guildID); err == nil {
			v.name, v.icon = dg.Name, dg.IconURL("128")
		}
	}

	return v, nil
}

type panelView struct {
	cmd      *gumi.Command
	owner    string
	settings store.Settings
	counts   map[store.Feature]int
	// Channels from outside the streamer registry.
	outside     int
	blacklisted int
	filters     int
	managers    []string
	// Blacklister roles.
	blacklisters []string
	name, icon   string
	// The bot has a DeepL key.
	translation bool
}

func (v *panelView) id(action string, arg ...string) string {
	return gumi.ComponentID(v.cmd, append([]string{v.owner, action}, arg...)...)
}

func (v *panelView) render(key string) *gumi.Response {
	e := &discordgo.MessageEmbed{Color: Color}
	if v.name != "" {
		e.Author = &discordgo.MessageEmbedAuthor{Name: v.name, IconURL: v.icon}
	}

	back := button("Back to settings", discordgo.SecondaryButton, v.id("go", "home"))
	var rows []discordgo.MessageComponent

	switch key {
	case "relay", "streams":
		e.Title = sectionTitle(key)
		var lines []string
		var buttons []discordgo.MessageComponent
		for _, t := range toggles {
			if t.section != key {
				continue
			}
			on := *t.field(&v.settings)
			lines = append(lines, settingLine(t.label, onOff(on), t.hint))
			buttons = append(buttons, v.toggleButton(t, on))
		}
		e.Description = strings.Join(lines, "\n\n")
		rows = buttonRows(append(buttons, back)...)
	case "translation":
		e.Title = "Translation"
		e.Description = "Lines VTubers write in other languages are translated into this one when auto-translate is on.\n\n" +
			settingLine("Language", translate.Name(v.settings.TargetLanguage), "Translations come from DeepL")
		if !v.translation {
			e.Description += "\n\n-# DeepL isn't set up for this bot, so nothing is translated for now."
		}
		rows = append([]discordgo.MessageComponent{v.languageSelect()},
			buttonRows(button("Other language…", discordgo.SecondaryButton, v.id("otherlang")), back)...)
	case "logs":
		e.Title = "Logs"
		e.Description = "When a stream ends, its log goes here as a text file.\n\n" +
			settingLine("Log channel", v.logChannel(), "Without one, each relay channel gets its own log")
		rows = []discordgo.MessageComponent{v.channelSelect()}
		var buttons []discordgo.MessageComponent
		if v.settings.LogChannelID != "" {
			buttons = append(buttons, button("Post in the relay channels", discordgo.SecondaryButton, v.id("logclear")))
		}
		rows = append(rows, buttonRows(append(buttons, back)...)...)
	case "permissions":
		e.Title = "Permissions"
		e.Description = "Manage Server can always change settings and subscriptions, and Manage Messages can always " +
			"edit the blacklist and filters. These roles can too.\n\n" +
			settingLine("Managers", roleList(v.managers), "Subscriptions, settings, the blacklist and filters") + "\n\n" +
			settingLine("Blacklisters", roleList(v.blacklisters), "The blacklist and filters")
		rows = []discordgo.MessageComponent{
			v.roleSelect(store.RoleManager, "Manager roles…", v.managers),
			v.roleSelect(store.RoleBlacklister, "Blacklister roles…", v.blacklisters),
		}
		rows = append(rows, buttonRows(back)...)
	default:
		v.renderHome(e)
		rows = []discordgo.MessageComponent{v.navSelect()}
	}

	return &gumi.Response{Embeds: []*discordgo.MessageEmbed{e}, Components: compact(rows)}
}

func (v *panelView) renderHome(e *discordgo.MessageEmbed) {
	s := v.settings
	c := v.counts

	e.Title = "Settings"
	e.Description = fmt.Sprintf("**%d** relays · **%d** cameos · **%d** gossip · **%d** live and **%d** post notifications\n"+
		"**%d** channels from outside the streamer list · **%d** blacklisted · **%d** filters\n"+
		"-# Change those with `/relay`, `/cameos`, `/gossip`, `/notify`, `/blacklist` and `/filter`",
		c[store.FeatureRelay], c[store.FeatureCameos], c[store.FeatureGossip], c[store.FeatureYouTube], c[store.FeaturePosts],
		v.outside, v.blacklisted, v.filters)

	e.Fields = []*discordgo.MessageEmbedField{
		{Name: "Relay", Value: fmt.Sprintf("Mod messages **%s** · Waiting rooms **%s** · Chat link **%s** · Auto-translate **%s**",
			onOff(s.ModMessages), onOff(s.Prechat), onOff(s.ShowChat), onOff(s.AutoTranslate))},
		{Name: "Translation", Value: "Into **" + translate.Name(s.TargetLanguage) + "**"},
		{Name: "Logs", Value: v.logChannel()},
		{Name: "Streams", Value: fmt.Sprintf("Notify members-only **%s** · Relay free chat **%s**",
			onOff(s.NotifyMembersOnly), onOff(s.RelayFreeChat))},
		{Name: "Permissions", Value: "Managers " + roleList(v.managers) + " · Blacklisters " + roleList(v.blacklisters)},
	}
	e.Footer = &discordgo.MessageEmbedFooter{Text: "Changing settings needs Manage Server or a Manager role"}
	if v.icon != "" {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: v.icon}
	}
}

func (v *panelView) logChannel() string {
	if v.settings.LogChannelID == "" {
		return "None, logs go to the relay channels"
	}
	return "<#" + v.settings.LogChannelID + ">"
}

func (v *panelView) toggleButton(t toggle, on bool) discordgo.MessageComponent {
	if on {
		return button(t.label+": On", discordgo.SuccessButton, v.id("toggle", t.name))
	}
	return button(t.label+": Off", discordgo.SecondaryButton, v.id("toggle", t.name))
}

func (v *panelView) navSelect() discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(sections))
	for _, s := range sections {
		options = append(options, discordgo.SelectMenuOption{Label: s.title, Value: s.key, Description: s.summary})
	}
	return selectRow(discordgo.SelectMenu{
		MenuType: discordgo.StringSelectMenu, CustomID: v.id("nav"), Placeholder: "Change a section…", Options: options,
	})
}

func (v *panelView) languageSelect() discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(translate.Common))
	for _, l := range translate.Common {
		options = append(options, discordgo.SelectMenuOption{
			Label: l.Name, Value: l.Code, Default: l.Code == v.settings.TargetLanguage,
		})
	}
	return selectRow(discordgo.SelectMenu{
		MenuType: discordgo.StringSelectMenu, CustomID: v.id("lang"), Placeholder: "Translate into…", Options: options,
	})
}

func (v *panelView) channelSelect() discordgo.MessageComponent {
	menu := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     v.id("logchannel"),
		Placeholder:  "Log channel…",
		ChannelTypes: textChannels,
	}
	if id := v.settings.LogChannelID; id != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: id, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return selectRow(menu)
}

// MinValues of zero lets people clear every role.
func (v *panelView) roleSelect(kind store.RoleKind, placeholder string, current []string) discordgo.MessageComponent {
	zero := 0
	menu := discordgo.SelectMenu{
		MenuType:    discordgo.RoleSelectMenu,
		CustomID:    v.id("roles", string(kind)),
		Placeholder: placeholder,
		MinValues:   &zero,
		MaxValues:   25,
	}
	for _, id := range current {
		menu.DefaultValues = append(menu.DefaultValues, discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueRole})
	}
	return selectRow(menu)
}

func sectionTitle(key string) string {
	for _, s := range sections {
		if s.key == key {
			return s.title
		}
	}
	return key
}

func settingLine(name, value, hint string) string {
	return fmt.Sprintf("**%s** %s\n-# %s", name, value, hint)
}

func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

func roleList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	mentions := make([]string, len(ids))
	for i, id := range ids {
		mentions[i] = "<@&" + id + ">"
	}
	return strings.Join(mentions, " ")
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func button(label string, style discordgo.ButtonStyle, id string) discordgo.MessageComponent {
	if id == "" {
		return nil
	}
	return discordgo.Button{Label: label, Style: style, CustomID: id}
}

func selectRow(menu discordgo.SelectMenu) discordgo.MessageComponent {
	if menu.CustomID == "" {
		return nil
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

// buttonRows packs buttons into rows of Discord's maximum of five.
func buttonRows(buttons ...discordgo.MessageComponent) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	row := make([]discordgo.MessageComponent, 0, 5)
	for _, b := range buttons {
		if b == nil {
			continue
		}
		if len(row) == 5 {
			rows = append(rows, discordgo.ActionsRow{Components: row})
			row = make([]discordgo.MessageComponent, 0, 5)
		}
		row = append(row, b)
	}
	if len(row) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: row})
	}
	return rows
}

func compact(rows []discordgo.MessageComponent) []discordgo.MessageComponent {
	out := make([]discordgo.MessageComponent, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}
