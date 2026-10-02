package stream

import (
	"strings"
	"time"

	"github.com/VTGare/gatoraid/holodex"
)

type Status int

const (
	Upcoming Status = iota
	Live
)

func (s Status) String() string {
	if s == Live {
		return "live"
	}
	return "upcoming"
}

type Stream struct {
	VideoID     string
	ChannelID   string
	ChannelName string
	Title       string
	TopicID     string
	Status      Status
	ScheduledAt time.Time
	// Set once the stream is live.
	StartedAt   time.Time
	MembersOnly bool
	FreeChat    bool
	// Channels Holodex detected in the stream, for collabs.
	Mentions []string
}

func (s *Stream) URL() string { return "https://youtu.be/" + s.VideoID }

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
		ChannelID:   v.Channel.ID,
		ChannelName: v.Channel.Name,
		Title:       v.Title,
		TopicID:     v.TopicID,
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
