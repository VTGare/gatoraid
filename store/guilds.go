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

func (g Guild) Active() bool { return g.LeftAt == nil }

// Settings holds a guild's /settings values. They're stored as a single
// JSON document and decoded on top of DefaultSettings, so adding a field
// doesn't need a migration.
type Settings struct {
	// Saved settings use this key, so it can't take the field's name.
	YouTubeModMessages bool `json:"mod_messages"`
	TwitchModMessages  bool `json:"twitch_mod_messages"`
	Prechat            bool `json:"prechat"`
	ShowChat           bool `json:"show_chat"`
	AutoTranslate      bool `json:"auto_translate"`

	// DeepL language code, e.g. EN-US.
	TargetLanguage string `json:"target_language"`

	// Where end-of-stream TL logs go. Empty means the relay channel.
	LogChannelID string `json:"log_channel_id,omitempty"`

	NotifyMembersOnly bool `json:"notify_members_only"`
	RelayFreeChat     bool `json:"relay_free_chat"`

	// Relays of a streamer include their Twitch chat.
	RelayTwitch bool `json:"relay_twitch"`
}

func (s *Settings) ModMessages(twitch bool) bool {
	if twitch {
		return s.TwitchModMessages
	}

	return s.YouTubeModMessages
}

func DefaultSettings() Settings {
	return Settings{
		YouTubeModMessages: true,
		TwitchModMessages:  true,
		Prechat:            true,
		ShowChat:           true,
		AutoTranslate:      true,
		TargetLanguage:     "EN-US",
		RelayTwitch:        true,
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

	return s, nil
}

func EncodeSettings(s Settings) ([]byte, error) {
	return json.Marshal(s)
}
