package stream

import (
	"cmp"
	"strings"
	"time"

	"github.com/VTGare/gatoraid/holodex"
)

type Platform string

const (
	YouTube Platform = "youtube"
	Twitch  Platform = "twitch"
)

type Status int

const (
	Upcoming Status = iota
	Live
)

type Stream struct {
	// Twitch streams use "twitch:" and Twitch's stream ID, so the two never
	// clash.
	VideoID  string
	Platform Platform
	// The YouTube channel ID, which is how the registry knows streamers,
	// for Twitch streams too.
	ChannelID   string
	ChannelName string
	Title       string
	Status      Status
	ScheduledAt time.Time
	// Set once the stream is live.
	StartedAt   time.Time
	MembersOnly bool
	FreeChat    bool
	// The tracker sets it for upcoming streams further off than its
	// PrechatLead or with no scheduled time. Their chat is read slowly and
	// gets no relay notice.
	Distant bool
	// Channels Holodex detected in the stream, for collabs.
	Mentions []string
	// Only set for Twitch streams.
	TwitchUsername string
	Thumbnail      string
}

func (s *Stream) Twitch() bool { return s.Platform == Twitch }

func (s *Stream) URL() string {
	if s.Twitch() {
		return "https://www.twitch.tv/" + s.TwitchUsername
	}
	return "https://youtu.be/" + s.VideoID
}

func (s *Stream) ChannelURL() string {
	if s.Twitch() {
		return "https://www.twitch.tv/" + s.TwitchUsername
	}
	return "https://www.youtube.com/channel/" + s.ChannelID
}

func (s *Stream) ThumbnailURL() string {
	if s.Thumbnail != "" {
		return s.Thumbnail
	}
	return "https://i.ytimg.com/vi/" + s.VideoID + "/hqdefault.jpg"
}

func (s *Stream) PlatformName() string {
	if s.Twitch() {
		return "Twitch"
	}
	return "YouTube"
}

// Urgency puts live streams first, then near waiting rooms, then distant
// ones, each by scheduled time. Chats open a few at a time, and after a
// restart this order decides which ones wait.
func Urgency(a, b *Stream) int {
	rank := func(s *Stream) int {
		switch {
		case s.Status == Live:
			return 0
		case !s.Distant:
			return 1
		default:
			return 2
		}
	}
	if c := cmp.Compare(rank(a), rank(b)); c != 0 {
		return c
	}

	switch {
	case a.ScheduledAt.IsZero() != b.ScheduledAt.IsZero():
		if a.ScheduledAt.IsZero() {
			return 1
		}
		return -1
	default:
		return a.ScheduledAt.Compare(b.ScheduledAt)
	}
}

// Phrases rather than "member", which would also match "remember".
var membersOnlyPhrases = []string{
	"members only", "member only", "members-only", "member's only", "membership only",
	"【members", "[members", "(members", "【membership", "[membership",
	"メン限", "メンバー限定", "メンバーシップ限定",
}

var freeChatPhrases = []string{"free chat", "freechat", "freeechat", "free talk", "フリーチャット"}

type Classifier struct {
	// Channels whose rooms titled "free chat" are real streams.
	FreeChatStreams func(channelID string) bool
}

func (c Classifier) Classify(v holodex.Video) Stream {
	s := Stream{
		VideoID:     v.ID,
		Platform:    YouTube,
		ChannelID:   v.Channel.ID,
		ChannelName: v.Channel.Name,
		Title:       v.Title,
		Status:      Upcoming,
		ScheduledAt: v.StartScheduled,
	}

	if v.Status == holodex.StatusLive {
		s.Status = Live
		s.StartedAt = v.AvailableAt
	}

	for _, m := range v.Mentions {
		s.Mentions = append(s.Mentions, m.ID)
	}

	title := strings.ToLower(v.Title)
	s.MembersOnly = strings.EqualFold(v.TopicID, "membersonly") || containsAny(title, membersOnlyPhrases)

	// A free chat room that goes live is a stream after all. Holodex's own
	// topic beats the per-channel exception, which is only about titles.
	if s.Status != Live {
		exempt := c.FreeChatStreams != nil && c.FreeChatStreams(s.ChannelID)
		s.FreeChat = strings.EqualFold(v.TopicID, "FreeChat") || (!exempt && containsAny(title, freeChatPhrases))
	}

	return s
}

func containsAny(s string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
