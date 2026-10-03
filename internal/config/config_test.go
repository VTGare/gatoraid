package config_test

import (
	"os"
	"path/filepath"

	"github.com/VTGare/gatoraid/internal/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config", func() {
	writeFile := func(content string) string {
		path := filepath.Join(GinkgoT().TempDir(), "config.json")
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
		return path
	}

	It("loads everything from the environment alone", func() {
		cfg, err := config.Load("", []string{
			"GATORAID_DISCORD_TOKEN=t",
			"GATORAID_DISCORD_OWNER_IDS=1,2",
			"GATORAID_DISCORD_DEV_GUILD_ID=3",
			"GATORAID_DISCORD_OWNER_GUILD_ID=5",
			"GATORAID_DISCORD_LOG_CHANNEL_ID=4",
			"GATORAID_DATABASE_PATH=data/bot.db",
			"GATORAID_HOLODEX_API_KEY=h",
			"GATORAID_HOLODEX_TLDEX=true",
			"GATORAID_DEEPL_API_KEY=d:fx",
			"GATORAID_DEEPL_MONTHLY_CHARACTER_BUDGET=1000",
			"GATORAID_LIMITS_USER_CHANNELS=5",
			"GATORAID_RELAY_PRECHAT_HOURS=48",
			"GATORAID_LOG_LEVEL=debug",
			"GATORAID_LOG_FORMAT=text",
			"GATORAID_EMOJIS=deepl=<:deepL:1>,hololive=<:holo:2>",
			"UNRELATED=x",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Discord).To(Equal(config.Discord{
			Token: "t", OwnerIDs: []string{"1", "2"}, DevGuildID: "3", OwnerGuildID: "5", LogChannelID: "4",
		}))
		Expect(cfg.OwnerGuild()).To(Equal("5"))
		Expect(cfg.Database.Path).To(Equal("data/bot.db"))
		Expect(cfg.Holodex).To(Equal(config.Holodex{APIKey: "h", TLdex: true}))
		Expect(cfg.DeepL).To(Equal(config.DeepL{APIKey: "d:fx", MonthlyCharacterBudget: 1000}))
		Expect(cfg.Limits.UserChannels).To(Equal(5))
		Expect(cfg.Relay.PrechatHours).To(HaveValue(Equal(48)))
		Expect(cfg.Log).To(Equal(config.Log{Level: "debug", Format: "text"}))
		Expect(cfg.Emojis).To(Equal(map[string]string{"deepl": "<:deepL:1>", "hololive": "<:holo:2>"}))
	})

	It("falls back to the file for anything the environment leaves unset", func() {
		path := writeFile(`{
			"discord": {"token": "file-token", "owner_ids": ["1"]},
			"holodex": {"api_key": "file-key", "tldex": true},
			"log": {"format": "text"},
			"emojis": {"deepl": "<:deepL:1>"}
		}`)

		cfg, err := config.Load(path, []string{
			"GATORAID_DISCORD_TOKEN=env-token",
			"GATORAID_LOG_LEVEL=warn",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Discord.Token).To(Equal("env-token"))
		Expect(cfg.Discord.OwnerIDs).To(Equal([]string{"1"}))
		Expect(cfg.Holodex).To(Equal(config.Holodex{APIKey: "file-key", TLdex: true}))
		Expect(cfg.Log).To(Equal(config.Log{Level: "warn", Format: "text"}))
		Expect(cfg.Emoji("deepl", "x")).To(Equal("<:deepL:1>"))
		Expect(cfg.Emoji("youtube", "▶️")).To(Equal("▶️"))
	})

	It("lets the environment turn a file's boolean off", func() {
		path := writeFile(`{"discord": {"token": "t"}, "holodex": {"tldex": true}}`)

		cfg, err := config.Load(path, []string{"GATORAID_HOLODEX_TLDEX=false"})

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Holodex.TLdex).To(BeFalse())
	})

	It("lets the environment override the file's prechat hours", func() {
		path := writeFile(`{"discord": {"token": "t"}, "relay": {"prechat_hours": 48}}`)

		cfg, err := config.Load(path, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Relay.PrechatHours).To(HaveValue(Equal(48)))

		cfg, err = config.Load(path, []string{"GATORAID_RELAY_PRECHAT_HOURS=12"})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Relay.PrechatHours).To(HaveValue(Equal(12)))
	})

	It("fills in defaults", func() {
		cfg, err := config.Load("", []string{"GATORAID_DISCORD_TOKEN=t"})

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Database.Path).To(Equal(config.DefaultDatabasePath))
		Expect(cfg.DeepL.MonthlyCharacterBudget).To(Equal(config.DefaultDeepLBudget))
		Expect(cfg.Limits.UserChannels).To(Equal(config.DefaultUserChannels))
		Expect(cfg.Relay.PrechatHours).To(HaveValue(Equal(config.DefaultPrechatHours)))
		Expect(cfg.Log).To(Equal(config.Log{Level: "info", Format: "json"}))
	})

	DescribeTable("rejects invalid configs",
		func(file string, environ []string, msg string) {
			path := ""
			if file != "" {
				path = writeFile(file)
			}

			_, err := config.Load(path, environ)
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("missing token", "", nil, "Discord token is required"),
		Entry("unknown file key", `{"discord": {"token": "t", "prefix": "!"}}`, nil, `unknown field "prefix"`),
		Entry("malformed file", `{"discord": `, nil, "config:"),
		Entry("bad env integer", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_DEEPL_MONTHLY_CHARACTER_BUDGET=lots"}, `parse error on field "MonthlyCharacterBudget"`),
		Entry("bad log level", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_LOG_LEVEL=loud"}, "log level"),
		Entry("bad log format", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_LOG_FORMAT=xml"}, "log format"),
		Entry("negative budget", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_DEEPL_MONTHLY_CHARACTER_BUDGET=-1"}, "cannot be negative"),
		Entry("zero prechat hours in the file", `{"discord": {"token": "t"}, "relay": {"prechat_hours": 0}}`, nil, "prechat hours must be at least 1"),
		Entry("zero prechat hours", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_RELAY_PRECHAT_HOURS=0"}, "prechat hours must be at least 1"),
		Entry("negative prechat hours", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_RELAY_PRECHAT_HOURS=-1"}, "prechat hours must be at least 1"),
		Entry("negative user channel limit", "", []string{"GATORAID_DISCORD_TOKEN=t", "GATORAID_LIMITS_USER_CHANNELS=-1"}, "user channel limit cannot be negative"),
	)

	It("reports a missing file that was asked for", func() {
		_, err := config.Load(filepath.Join(GinkgoT().TempDir(), "nope.json"), []string{"GATORAID_DISCORD_TOKEN=t"})
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	Describe("choosing the file", func() {
		It("prefers an explicit path, then GATORAID_CONFIG", func() {
			Expect(config.Path("flag.json", []string{"GATORAID_CONFIG=env.json"})).To(Equal("flag.json"))
			Expect(config.Path("", []string{"GATORAID_CONFIG=env.json"})).To(Equal("env.json"))
		})

		It("uses config.json only when it exists", func() {
			GinkgoT().Chdir(GinkgoT().TempDir())
			Expect(config.Path("", nil)).To(BeEmpty())

			Expect(os.WriteFile(config.DefaultPath, []byte(`{}`), 0o600)).To(Succeed())
			Expect(config.Path("", nil)).To(Equal(config.DefaultPath))
		})
	})

	It("puts /owner in the dev guild unless an owner guild is set", func() {
		Expect((&config.Config{Discord: config.Discord{DevGuildID: "dev"}}).OwnerGuild()).To(Equal("dev"))
		Expect((&config.Config{Discord: config.Discord{DevGuildID: "dev", OwnerGuildID: "own"}}).OwnerGuild()).To(Equal("own"))
		Expect((&config.Config{}).OwnerGuild()).To(BeEmpty())
	})
})
