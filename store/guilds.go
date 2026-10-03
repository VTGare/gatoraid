package store

import (
	"encoding/json"
	"time"
)

type Guild struct {
	ID       string
	Settings Settings
	JoinedAt time.Time
	LeftAt   *time.Time
}

func (g *Guild) Active() bool { return g.LeftAt == nil }

// Bump when a Settings change needs DecodeSettings to migrate old values.
const SettingsVersion = 1

// Settings holds a guild's /settings values. They're stored as a single
// JSON document and decoded on top of DefaultSettings, so adding a field
// doesn't need a migration.
type Settings struct {
	Version int `json:"v"`

	ModMessages   bool `json:"mod_messages"`
	Prechat       bool `json:"prechat"`
	ShowChat      bool `json:"show_chat"`
	AutoTranslate bool `json:"auto_translate"`

	// DeepL language code, e.g. EN-US.
	TargetLanguage string `json:"target_language"`

	// Where end-of-stream TL logs go. Empty means the relay channel.
	LogChannelID string `json:"log_channel_id,omitempty"`

	NotifyMembersOnly bool `json:"notify_members_only"`
	RelayFreeChat     bool `json:"relay_free_chat"`
}

func DefaultSettings() Settings {
	return Settings{
		Version:        SettingsVersion,
		ModMessages:    true,
		Prechat:        true,
		ShowChat:       true,
		AutoTranslate:  true,
		TargetLanguage: "EN-US",
	}
}

func DecodeSettings(data []byte) (Settings, error) {
	s := DefaultSettings()
	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}

	s.Version = SettingsVersion
	return s, nil
}

func EncodeSettings(s Settings) ([]byte, error) {
	s.Version = SettingsVersion
	return json.Marshal(s)
}
