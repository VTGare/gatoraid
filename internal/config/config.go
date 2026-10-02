package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

const EnvPrefix = "GATORAID_"

const EnvConfigPath = EnvPrefix + "CONFIG"

const (
	DefaultPath         = "config.json"
	DefaultDatabasePath = "gatoraid.db"
	// The DeepL API Free monthly limit.
	DefaultDeepLBudget = 500_000
	DefaultLogLevel    = "info"
	DefaultLogFormat   = "json"
)

// Variable names are the prefix, the section and the env tag put together,
// like GATORAID_HOLODEX_API_KEY.
type Config struct {
	Discord  Discord  `json:"discord" envPrefix:"DISCORD_"`
	Database Database `json:"database" envPrefix:"DATABASE_"`
	Holodex  Holodex  `json:"holodex" envPrefix:"HOLODEX_"`
	DeepL    DeepL    `json:"deepl" envPrefix:"DEEPL_"`
	Log      Log      `json:"log" envPrefix:"LOG_"`
	// Emojis maps keys like "deepl" or "youtube" to custom emoji markup
	// ("<:deepL:123>"). Missing keys use plain Unicode emojis. From the
	// environment it's GATORAID_EMOJIS="deepl=<:deepL:1>,youtube=<:YouTube:2>",
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

	return errors.Join(errs...)
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
