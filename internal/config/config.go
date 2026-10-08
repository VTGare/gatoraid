package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/disgoorg/snowflake/v2"
)

const EnvPrefix = "GATORAID_"

const EnvConfigPath = EnvPrefix + "CONFIG"

const (
	DefaultPath         = "config.json"
	DefaultDatabasePath = "gatoraid.db"
	// The DeepL API Free monthly limit.
	DefaultDeepLBudget  = 500_000
	DefaultUserChannels = 25
	DefaultPrechatHours = 24
	DefaultLogLevel     = "info"
	DefaultLogFormat    = "json"
)

// Variable names are the prefix, the section and the env tag put together,
// like GATORAID_HOLODEX_API_KEY.
type Config struct {
	Discord  Discord  `json:"discord" envPrefix:"DISCORD_"`
	Database Database `json:"database" envPrefix:"DATABASE_"`
	Holodex  Holodex  `json:"holodex" envPrefix:"HOLODEX_"`
	DeepL    DeepL    `json:"deepl" envPrefix:"DEEPL_"`
	Twitch   Twitch   `json:"twitch" envPrefix:"TWITCH_"`
	Limits   Limits   `json:"limits" envPrefix:"LIMITS_"`
	Relay    Relay    `json:"relay" envPrefix:"RELAY_"`
	Log      Log      `json:"log" envPrefix:"LOG_"`
	// Emojis maps keys like "deepl" or "hololive" to custom emoji markup
	// ("<:deepL:123>"). Missing keys use plain Unicode emojis. From the
	// environment it's GATORAID_EMOJIS="deepl=<:deepL:1>,hololive=<:holo:2>",
	// which replaces the file's map instead of merging with it.
	Emojis map[string]string `json:"emojis" env:"EMOJIS" envKeyValSeparator:"="`
}

type Discord struct {
	Token string `json:"token" env:"TOKEN"`
	// Comma-separated in the environment.
	OwnerIDs []string `json:"owner_ids" env:"OWNER_IDS"`
	// Registers all commands in this one guild instead of globally, for
	// development.
	DevGuildID string `json:"dev_guild_id" env:"DEV_GUILD_ID"`
	// The only guild /owner is registered in. Defaults to DevGuildID.
	OwnerGuildID string `json:"owner_guild_id" env:"OWNER_GUILD_ID"`
	// Gets guild join and leave notices.
	LogChannelID string `json:"log_channel_id" env:"LOG_CHANNEL_ID"`
}

type Database struct {
	Path string `json:"path" env:"PATH"`
}

type Holodex struct {
	APIKey string `json:"api_key" env:"API_KEY"`
	// Also pull lines from Holodex's TLdex feed into live relays.
	TLdex bool `json:"tldex" env:"TLDEX"`
}

type DeepL struct {
	// Empty turns translation off.
	APIKey                 string `json:"api_key" env:"API_KEY"`
	MonthlyCharacterBudget int    `json:"monthly_character_budget" env:"MONTHLY_CHARACTER_BUDGET"`
}

// An app from the Twitch developer console. Both empty turns Twitch off.
type Twitch struct {
	ClientID     string `json:"client_id" env:"CLIENT_ID"`
	ClientSecret string `json:"client_secret" env:"CLIENT_SECRET"`
}

func (t Twitch) Enabled() bool { return t.ClientID != "" && t.ClientSecret != "" }

type Limits struct {
	// YouTube channels outside the streamer registry one guild can add.
	UserChannels int `json:"user_channels" env:"USER_CHANNELS"`
}

type Relay struct {
	// Waiting rooms further off than this many hours are relayed quietly.
	// Their chat is read every 15 seconds and the relay notice waits until
	// they're this close.
	PrechatHours int `json:"prechat_hours" env:"PRECHAT_HOURS"`
}

type Log struct {
	Level  string `json:"level" env:"LEVEL"`
	Format string `json:"format" env:"FORMAT"`
}

// Path picks the JSON file: the explicit path (the flag) wins, then
// GATORAID_CONFIG, then config.json if it exists. "" means no file.
func Path(explicit string, environ []string) string {
	if explicit != "" {
		return explicit
	}

	if p := lookup(environ, EnvConfigPath); p != "" {
		return p
	}

	if _, err := os.Stat(DefaultPath); err == nil {
		return DefaultPath
	}

	return ""
}

// Load applies the GATORAID_* variables in environ on top of the JSON file
// at path, if any. Unknown keys in the file are an error.
func Load(path string, environ []string) (*Config, error) {
	var cfg Config

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}

		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}

	if err := env.ParseWithOptions(&cfg, env.Options{
		Prefix:      EnvPrefix,
		Environment: env.ToMap(environ),
	}); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []error

	if c.Discord.Token == "" {
		errs = append(errs, fmt.Errorf("config: a Discord token is required (%sDISCORD_TOKEN or discord.token)", EnvPrefix))
	}

	ids := map[string]string{
		"dev_guild_id": c.Discord.DevGuildID, "owner_guild_id": c.Discord.OwnerGuildID, "log_channel_id": c.Discord.LogChannelID,
	}
	for i, id := range c.Discord.OwnerIDs {
		ids[fmt.Sprintf("owner_ids[%d]", i)] = id
	}
	for _, name := range slices.Sorted(maps.Keys(ids)) {
		if id := ids[name]; id != "" {
			if _, err := snowflake.Parse(id); err != nil {
				errs = append(errs, fmt.Errorf("config: discord.%s %q is not a Discord ID", name, id))
			}
		}
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("config: log level %q must be debug, info, warn or error", c.Log.Level))
	}

	switch c.Log.Format {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("config: log format %q must be json or text", c.Log.Format))
	}

	if c.DeepL.MonthlyCharacterBudget < 0 {
		errs = append(errs, errors.New("config: the DeepL monthly character budget cannot be negative"))
	}

	if (c.Twitch.ClientID == "") != (c.Twitch.ClientSecret == "") {
		errs = append(errs, errors.New("config: Twitch needs both a client ID and a client secret"))
	}

	if c.Limits.UserChannels < 0 {
		errs = append(errs, errors.New("config: the user channel limit cannot be negative"))
	}

	if c.Relay.PrechatHours < 0 {
		errs = append(errs, errors.New("config: the prechat hours cannot be negative"))
	}

	return errors.Join(errs...)
}

// OwnerGuild is where /owner goes. Empty means nowhere: owner tools are
// never registered globally.
// Unset IDs come back as 0. Validate rejects the ones that don't parse.
func ID(s string) snowflake.ID {
	id, _ := snowflake.Parse(s)
	return id
}

func (d Discord) Owners() []snowflake.ID {
	ids := make([]snowflake.ID, 0, len(d.OwnerIDs))
	for _, s := range d.OwnerIDs {
		ids = append(ids, ID(s))
	}
	return ids
}

func (c *Config) OwnerGuild() string {
	if c.Discord.OwnerGuildID != "" {
		return c.Discord.OwnerGuildID
	}
	return c.Discord.DevGuildID
}

func (c *Config) Emoji(key, fallback string) string {
	if e, ok := c.Emojis[key]; ok && e != "" {
		return e
	}

	return fallback
}

func (c *Config) applyDefaults() {
	if c.Database.Path == "" {
		c.Database.Path = DefaultDatabasePath
	}

	if c.DeepL.MonthlyCharacterBudget == 0 {
		c.DeepL.MonthlyCharacterBudget = DefaultDeepLBudget
	}

	if c.Limits.UserChannels == 0 {
		c.Limits.UserChannels = DefaultUserChannels
	}

	if c.Relay.PrechatHours == 0 {
		c.Relay.PrechatHours = DefaultPrechatHours
	}

	if c.Log.Level == "" {
		c.Log.Level = DefaultLogLevel
	}

	if c.Log.Format == "" {
		c.Log.Format = DefaultLogFormat
	}
}

func lookup(environ []string, key string) string {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}

	return ""
}
