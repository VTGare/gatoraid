package commands_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/disgoorg/disgo/discord"

	dt "github.com/VTGare/gumi/v2/gumitest"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
	"github.com/VTGare/gatoraid/translate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("/settings", func() {
	var (
		h   *harness
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		h = newHarness(&streamers.Seed{})
		_, _, err := h.b.Store.JoinGuild(ctx, testGuild)
		Expect(err).NotTo(HaveOccurred())
	})

	id := func(action string, arg ...string) string {
		s := "rt:settings:" + user.String() + ":" + action
		for _, a := range arg {
			s += ":" + a
		}
		return s
	}

	click := func(i *dt.Interaction) map[string]any {
		GinkgoHelper()
		data := h.run(i)
		Expect(data).NotTo(BeNil())
		return data
	}

	asManager := func(i *dt.Interaction) *dt.Interaction {
		return i.WithPermissions(discord.PermissionManageGuild)
	}

	settings := func() store.Settings {
		GinkgoHelper()
		g, err := h.b.Store.Guild(ctx, testGuild)
		Expect(err).NotTo(HaveOccurred())
		return g.Settings
	}

	It("opens an overview anyone can read", func() {
		_, err := h.b.Moderation.AddFilter(ctx, store.Filter{GuildID: testGuild, Kind: store.FilterBanned, Pattern: "x"})
		Expect(err).NotTo(HaveOccurred())

		data := click(dt.Command(user, "settings"))

		Expect(ephemeral(data)).To(BeTrue())
		e := embed(data)
		Expect(e["title"]).To(Equal("Settings"))
		Expect(e["description"]).To(ContainSubstring("**0** channels from outside the streamer list · **0** blacklisted · **1** filters"))
		Expect(fmt.Sprint(e["fields"])).To(ContainSubstring("Into **English (American)**"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring(id("nav")))
	})

	It("lets anyone browse but only managers change things", func() {
		data := click(dt.Select(user, id("nav"), "relay"))
		Expect(embed(data)["title"]).To(Equal("Relay"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Waiting rooms: On"))

		data = click(dt.Button(user, id("toggle", "prechat")))
		Expect(data["content"]).To(ContainSubstring("You need Manage Server, or a Manager role"))
		Expect(settings().Prechat).To(BeTrue())

		data = click(asManager(dt.Button(user, id("toggle", "prechat"))))
		Expect(embed(data)["title"]).To(Equal("Relay"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Waiting rooms: Off"))
		Expect(settings().Prechat).To(BeFalse())
	})

	It("only answers the person who opened it", func() {
		data := click(dt.Select(9, id("nav"), "relay"))
		Expect(data["content"]).To(ContainSubstring("someone else's"))
	})

	It("toggles YouTube and Twitch mod messages separately", func() {
		data := click(asManager(dt.Button(user, id("toggle", "twitchmods"))))

		Expect(embed(data)["title"]).To(Equal("Relay"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Twitch mod messages: Off"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("YouTube mod messages: On"))
		Expect(settings().TwitchModMessages).To(BeFalse())
		Expect(settings().YouTubeModMessages).To(BeTrue())
	})

	It("turns Twitch relays off and on", func() {
		Expect(settings().RelayTwitch).To(BeTrue())

		data := click(asManager(dt.Button(user, id("toggle", "relaytwitch"))))

		Expect(embed(data)["title"]).To(Equal("Streams"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Relay Twitch chats: Off"))
		Expect(settings().RelayTwitch).To(BeFalse())
	})

	It("lets Manager roles change settings", func() {
		Expect(h.b.Store.SetGuildRoles(ctx, testGuild, store.RoleManager, []string{"11"})).To(Succeed())

		click(dt.Button(user, id("toggle", "relayfreechat")).WithRoles(11))
		Expect(settings().RelayFreeChat).To(BeTrue())
	})

	It("sets the language and the log channel", func() {
		data := click(asManager(dt.Select(user, id("lang"), "JA")))
		Expect(embed(data)["description"]).To(ContainSubstring("**Language** Japanese"))
		Expect(settings().TargetLanguage).To(Equal("JA"))

		data = click(asManager(dt.SelectOf(discord.ComponentTypeChannelSelectMenu, user, id("logchannel"), "3002")))
		Expect(embed(data)["description"]).To(ContainSubstring("<#3002>"))
		Expect(settings().LogChannelID).To(Equal("3002"))

		click(asManager(dt.Button(user, id("logclear"))))
		Expect(settings().LogChannelID).To(BeEmpty())
	})

	It("sets and clears bot roles", func() {
		data := click(asManager(dt.SelectOf(discord.ComponentTypeRoleSelectMenu, user, id("roles", "manager"), "21", "22")))
		Expect(embed(data)["description"]).To(ContainSubstring("**Managers** <@&21> <@&22>"))

		roles, err := h.b.Store.GuildRoles(ctx, testGuild, store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(Equal([]string{"21", "22"}))

		click(asManager(dt.SelectOf(discord.ComponentTypeRoleSelectMenu, user, id("roles", "manager"))))
		roles, err = h.b.Store.GuildRoles(ctx, testGuild, store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(BeEmpty())
	})

	It("takes other DeepL languages through a modal", func() {
		deepl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"language":"FI","name":"Finnish"},{"language":"EN-US","name":"English (American)"}]`))
		}))
		DeferCleanup(deepl.Close)
		h.b.Translator = translate.NewService(translate.NewDeepL("key", translate.WithBaseURL(deepl.URL)), 1000, nil)

		data := click(asManager(dt.Button(user, id("otherlang"))))
		Expect(data["custom_id"]).To(Equal(id("langcode")))

		click(asManager(dt.ModalSubmit(user, id("langcode"), map[string]string{"code": " fi "})))
		Expect(settings().TargetLanguage).To(Equal("FI"))

		data = click(asManager(dt.ModalSubmit(user, id("langcode"), map[string]string{"code": "xx"})))
		Expect(data["content"]).To(ContainSubstring("DeepL can't translate into `XX`"))
		Expect(settings().TargetLanguage).To(Equal("FI"))
	})

	It("knows the common languages without DeepL", func() {
		click(asManager(dt.ModalSubmit(user, id("langcode"), map[string]string{"code": "pt-br"})))
		Expect(settings().TargetLanguage).To(Equal("PT-BR"))

		data := click(dt.Select(user, id("nav"), "translation"))
		Expect(embed(data)["description"]).To(ContainSubstring("DeepL isn't set up for this bot"))
	})
})
