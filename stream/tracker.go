package stream

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/holodex"
	"github.com/VTGare/gatoraid/twitch/helix"
)

const (
	DefaultInterval = 30 * time.Second

	// Upcoming rooms more than 6 hours past their scheduled start are
	// usually abandoned, so they aren't relayed.
	PrechatGrace = 6 * time.Hour

	// Twitch sometimes leaves a live stream out of one response, so a
	// stream only ends after this many polls in a row without it.
	twitchMisses = 2

	twitchThumbWidth  = 1280
	twitchThumbHeight = 720
)

type EventKind int

const (
	// An upcoming stream turned up. Sent again when a distant stream stops
	// being distant.
	EventPrechat EventKind = iota
	EventLive
	EventEnded
)

type Event struct {
	Kind   EventKind
	Stream Stream
	// Only set for EventEnded. False when an upcoming stream was cancelled
	// or stopped being tracked.
	WasLive bool
}

type Source interface {
	UsersLive(ctx context.Context, channelIDs []string) ([]holodex.Video, error)
	Video(ctx context.Context, id string) (*holodex.Video, error)
}

type TwitchSource interface {
	Streams(ctx context.Context, usernames []string) ([]helix.Stream, error)
}

type Config struct {
	Source Source
	// Called before every poll, so newly added channels get picked up.
	Channels func() []string
	// Optional. Holodex lists Twitch streams minutes late, so they come
	// from Twitch.
	Twitch TwitchSource
	// Twitch usernames to the YouTube channel IDs of their streamers. Called
	// before every poll.
	TwitchChannels func() map[string]string
	Classifier     Classifier
	Interval       time.Duration
	// Upcoming streams further off than this are distant.
	PrechatLead time.Duration
	Log         *slog.Logger
	OnAvatars   func(ctx context.Context, avatars map[string]string)
	Now         func() time.Time
}

type tracked struct {
	Stream
	prechatSent bool
	nearSent    bool
	// Twitch polls in a row the stream was missing from.
	missed int
}

// On startup the tracker reports everything that's already live or
// upcoming, so consumers have to dedupe notifications across restarts.
type Tracker struct {
	cfg    Config
	events chan Event

	mu    sync.RWMutex
	state map[string]*tracked
}

func NewTracker(cfg Config) *Tracker {
	if cfg.Interval == 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	return &Tracker{
		cfg:    cfg,
		events: make(chan Event, 256),
		state:  map[string]*tracked{},
	}
}

func (t *Tracker) Events() <-chan Event { return t.events }

// In Urgency order.
func (t *Tracker) Streams() []Stream {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]Stream, 0, len(t.state))
	for _, tr := range t.state {
		out = append(out, tr.Stream)
	}
	slices.SortFunc(out, func(a, b Stream) int { return Urgency(&a, &b) })
	return out
}

// Distant is as of the last poll. Unknown streams aren't distant.
func (t *Tracker) Distant(videoID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	tr, ok := t.state[videoID]
	return ok && tr.Distant
}

func (t *Tracker) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()

	for {
		if err := t.Poll(ctx); err != nil && ctx.Err() == nil {
			t.cfg.Log.Warn("stream poll failed", slog.Any("error", err))
		}
		if err := t.PollTwitch(ctx); err != nil && ctx.Err() == nil {
			t.cfg.Log.Warn("Twitch stream poll failed", slog.Any("error", err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// A failed poll leaves the tracked streams as they were.
func (t *Tracker) Poll(ctx context.Context) error {
	channels := t.cfg.Channels()
	videos, err := t.cfg.Source.UsersLive(ctx, channels)
	if err != nil {
		return err
	}

	requested := make(map[string]bool, len(channels))
	for _, id := range channels {
		requested[id] = true
	}

	now := t.cfg.Now()
	seen := map[string]bool{}
	avatars := map[string]string{}
	var events []Event

	t.mu.Lock()
	for _, v := range videos {
		// Holodex also lists placeholders for streams elsewhere, like Twitch.
		if v.Type != "stream" || seen[v.ID] {
			continue
		}
		seen[v.ID] = true

		if requested[v.Channel.ID] && v.Channel.Photo != "" {
			avatars[v.Channel.ID] = v.Channel.Photo
		}

		s := t.cfg.Classifier.Classify(v)
		s.Distant = t.distant(&s, now)
		prev := t.state[v.ID]
		tr := &tracked{Stream: s}
		if prev != nil {
			tr.prechatSent = prev.prechatSent
			tr.nearSent = prev.nearSent
		}
		t.state[v.ID] = tr

		switch {
		case s.Status == Live && (prev == nil || prev.Status != Live):
			events = append(events, Event{Kind: EventLive, Stream: s})
		case s.Status == Upcoming && !tr.prechatSent && !overdue(&s, now):
			tr.prechatSent = true
			tr.nearSent = !s.Distant
			events = append(events, Event{Kind: EventPrechat, Stream: s})
		case s.Status == Upcoming && tr.prechatSent && !tr.nearSent && !s.Distant:
			tr.nearSent = true
			events = append(events, Event{Kind: EventPrechat, Stream: s})
		}
	}

	var gone []*tracked
	for id, tr := range t.state {
		if !seen[id] && !tr.Twitch() {
			gone = append(gone, tr)
		}
	}
	t.mu.Unlock()

	// Streams drop out of /users/live when they end, but also when Holodex
	// hiccups, so check each one before calling it over.
	for _, tr := range gone {
		ended, err := t.confirmEnded(ctx, tr, requested)
		if err != nil {
			t.cfg.Log.Warn("couldn't check a missing stream", slog.String("video_id", tr.VideoID), slog.Any("error", err))
			continue
		}
		if !ended {
			continue
		}

		t.mu.Lock()
		delete(t.state, tr.VideoID)
		t.mu.Unlock()
		events = append(events, Event{Kind: EventEnded, Stream: tr.Stream, WasLive: tr.Status == Live})
	}

	if t.cfg.OnAvatars != nil && len(avatars) > 0 {
		t.cfg.OnAvatars(ctx, avatars)
	}

	return t.send(ctx, events)
}

// PollTwitch does nothing without a Twitch source. A failed poll leaves
// the tracked streams as they were.
func (t *Tracker) PollTwitch(ctx context.Context) error {
	if t.cfg.Twitch == nil {
		return nil
	}

	channels := t.cfg.TwitchChannels()
	usernames := slices.Sorted(maps.Keys(channels))
	streams, err := t.cfg.Twitch.Streams(ctx, usernames)
	if err != nil {
		return err
	}

	seen := map[string]bool{}
	var events []Event

	t.mu.Lock()
	for _, ts := range streams {
		username := strings.ToLower(ts.Username)
		channelID, ok := channels[username]
		if ts.Type != "live" || !ok {
			continue
		}

		s := Stream{
			VideoID:        "twitch:" + ts.ID,
			Platform:       Twitch,
			ChannelID:      channelID,
			ChannelName:    ts.DisplayName,
			Title:          ts.Title,
			Status:         Live,
			ScheduledAt:    ts.StartedAt,
			StartedAt:      ts.StartedAt,
			TwitchUsername: username,
			Thumbnail:      helix.Thumbnail(ts.ThumbnailURL, twitchThumbWidth, twitchThumbHeight),
		}
		// Discord caches embed images by URL, and Twitch keeps one preview
		// URL per channel.
		if s.Thumbnail != "" {
			s.Thumbnail += "?s=" + strconv.FormatInt(ts.StartedAt.Unix(), 10)
		}
		seen[s.VideoID] = true

		if _, ok := t.state[s.VideoID]; !ok {
			events = append(events, Event{Kind: EventLive, Stream: s})
		}
		t.state[s.VideoID] = &tracked{Stream: s}
	}

	for id, tr := range t.state {
		if !tr.Twitch() || seen[id] {
			continue
		}
		tr.missed++
		if _, followed := channels[tr.TwitchUsername]; followed && tr.missed < twitchMisses {
			continue
		}
		delete(t.state, id)
		events = append(events, Event{Kind: EventEnded, Stream: tr.Stream, WasLive: true})
	}
	t.mu.Unlock()

	return t.send(ctx, events)
}

func (t *Tracker) send(ctx context.Context, events []Event) error {
	// Ended streams go first, since they free chats up.
	slices.SortStableFunc(events, func(a, b Event) int {
		if (a.Kind == EventEnded) != (b.Kind == EventEnded) {
			if a.Kind == EventEnded {
				return -1
			}
			return 1
		}
		return Urgency(&a.Stream, &b.Stream)
	})

	for _, e := range events {
		select {
		case t.events <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (t *Tracker) confirmEnded(ctx context.Context, tr *tracked, requested map[string]bool) (bool, error) {
	v, err := t.cfg.Source.Video(ctx, tr.VideoID)
	switch {
	case errors.Is(err, holodex.ErrNotFound):
		return true, nil
	case err != nil:
		return false, err
	}

	switch v.Status {
	case holodex.StatusLive, holodex.StatusUpcoming:
		// Still on, so it only vanished from the list. Keep it unless we've
		// stopped tracking the channel.
		return !requested[tr.ChannelID] && !mentionsAny(tr.Mentions, requested), nil
	default:
		return true, nil
	}
}

func (t *Tracker) distant(s *Stream, now time.Time) bool {
	if s.Status != Upcoming {
		return false
	}
	return s.ScheduledAt.IsZero() || s.ScheduledAt.Sub(now) > t.cfg.PrechatLead
}

func overdue(s *Stream, now time.Time) bool {
	return !s.ScheduledAt.IsZero() && s.ScheduledAt.Before(now.Add(-PrechatGrace))
}

func mentionsAny(mentions []string, requested map[string]bool) bool {
	return slices.ContainsFunc(mentions, func(id string) bool { return requested[id] })
}
