package commands_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/bwmarrin/discordgo"

	dt "github.com/VTGare/gatoraid/internal/discordtest"
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
		_, _, err := h.b.Store.JoinGuild(ctx, "guild")
		Expect(err).NotTo(HaveOccurred())
	})

	id := func(action string, arg ...string) string {
		s := "rt:settings:" + user + ":" + action
		for _, a := range arg {
			s += ":" + a
		}
		return s
	}

	click := func(i *discordgo.InteractionCreate) map[string]any {
		GinkgoHelper()
		data := h.run(i)
		Expect(data).NotTo(BeNil())
		return data
	}

	asManager := func(i *discordgo.InteractionCreate) *discordgo.InteractionCreate {
		return dt.WithPermissions(i, discordgo.PermissionManageGuild)
	}

	settings := func() store.Settings {
		GinkgoHelper()
		g, err := h.b.Store.Guild(ctx, "guild")
		Expect(err).NotTo(HaveOccurred())
		return g.Settings
	}

	It("opens an overview anyone can read", func() {
		_, err := h.b.Moderation.AddFilter(ctx, store.Filter{GuildID: "guild", Kind: store.FilterBanned, Pattern: "x"})
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
		data := click(dt.Component(user, id("nav"), "relay"))
		Expect(embed(data)["title"]).To(Equal("Relay"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Waiting rooms: On"))

		data = click(dt.Component(user, id("toggle", "prechat")))
		Expect(data["content"]).To(ContainSubstring("You need Manage Server, or a Manager role"))
		Expect(settings().Prechat).To(BeTrue())

		data = click(asManager(dt.Component(user, id("toggle", "prechat"))))
		Expect(embed(data)["title"]).To(Equal("Relay"))
		Expect(fmt.Sprint(data["components"])).To(ContainSubstring("Waiting rooms: Off"))
		Expect(settings().Prechat).To(BeFalse())
	})

	It("only answers the person who opened it", func() {
		data := click(dt.Component("someone else", id("nav"), "relay"))
		Expect(data["content"]).To(ContainSubstring("someone else's"))
	})

	It("lets Manager roles change settings", func() {
		Expect(h.b.Store.SetGuildRoles(ctx, "guild", store.RoleManager, []string{"mgr"})).To(Succeed())

		click(dt.WithRoles(dt.Component(user, id("toggle", "relayfreechat")), "mgr"))
		Expect(settings().RelayFreeChat).To(BeTrue())
	})

	It("sets the language and the log channel", func() {
		data := click(asManager(dt.Component(user, id("lang"), "JA")))
		Expect(embed(data)["description"]).To(ContainSubstring("**Language** Japanese"))
		Expect(settings().TargetLanguage).To(Equal("JA"))

		data = click(asManager(dt.Component(user, id("logchannel"), "logs")))
		Expect(embed(data)["description"]).To(ContainSubstring("<#logs>"))
		Expect(settings().LogChannelID).To(Equal("logs"))

		click(asManager(dt.Component(user, id("logclear"))))
		Expect(settings().LogChannelID).To(BeEmpty())
	})

	It("sets and clears bot roles", func() {
		data := click(asManager(dt.Component(user, id("roles", "manager"), "r1", "r2")))
		Expect(embed(data)["description"]).To(ContainSubstring("**Managers** <@&r1> <@&r2>"))

		roles, err := h.b.Store.GuildRoles(ctx, "guild", store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(Equal([]string{"r1", "r2"}))

		click(asManager(dt.Component(user, id("roles", "manager"), []string{}...)))
		roles, err = h.b.Store.GuildRoles(ctx, "guild", store.RoleManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(BeEmpty())
	})

	It("takes other DeepL languages through a modal", func() {
		deepl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"language":"FI","name":"Finnish"},{"language":"EN-US","name":"English (American)"}]`))
		}))
		DeferCleanup(deepl.Close)
		h.b.Translator = translate.NewService(translate.NewDeepL("key", translate.WithBaseURL(deepl.URL)), 1000, nil)

		data := click(asManager(dt.Component(user, id("otherlang"))))
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

		data := click(dt.Component(user, id("nav"), "translation"))
		Expect(embed(data)["description"]).To(ContainSubstring("DeepL isn't set up for this bot"))
	})
})
