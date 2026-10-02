package chat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/VTGare/gatoraid/holodex/tldex"
	"github.com/VTGare/gatoraid/youtube/livechat"
)

type Comment struct {
	VideoID         string
	ID              string
	AuthorChannelID string
	AuthorName      string
	Text            string
	Time            time.Time
	Owner           bool
	Moderator       bool
	Verified        bool
	Member          bool
	SuperChat       string
	// Hints from TLdex. Only set on comments that came through it.
	TL     bool
	VTuber bool
	Source Source
}

type Source string

const (
	SourceYouTube Source = "youtube"
	SourceTLdex   Source = "tldex"
	// Lines posted on Holodex with MChad. They never appear in YouTube chat.
	SourceMChad Source = "mchad"
)

type EventKind int

const (
	EventComment EventKind = iota
	// TLdex knows when a stream actually started, which Holodex's REST API
	// no longer says.
	EventStarted
	// The chat is over or can't be read. Err says why.
	EventStopped
)

type Event struct {
	Kind      EventKind
	VideoID   string
	Comment   *Comment
	StartedAt time.Time
	Err       error
}

type Reader interface {
	Poll(ctx context.Context) ([]livechat.Message, error)
	Reopen(ctx context.Context) ([]livechat.Message, error)
	Wait() time.Duration
}

type Opener func(ctx context.Context, videoID string) (Reader, error)

type TLdex interface {
	Subscribe(videoID string) <-chan tldex.Update
	Unsubscribe(videoID string)
}

type Config struct {
	Open Opener
	// Optional.
	TLdex TLdex
	Log   *slog.Logger
	// The first retry after an error. It doubles up to MaxBackoff.
	Backoff    time.Duration
	MaxBackoff time.Duration
	// A chat that keeps failing for this long is given up on.
	GiveUpAfter time.Duration
}

type Manager struct {
	cfg    Config
	events chan Event

	mu       sync.Mutex
	sessions map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func NewManager(cfg Config) *Manager {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Backoff == 0 {
		cfg.Backoff = 2 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = time.Minute
	}
	if cfg.GiveUpAfter == 0 {
		cfg.GiveUpAfter = 10 * time.Minute
	}

	return &Manager{cfg: cfg, events: make(chan Event, 1024), sessions: map[string]context.CancelFunc{}}
}

func (m *Manager) Events() <-chan Event { return m.events }

// Start reads a stream's chat until it ends, Stop is called or ctx is done.
// Starting a stream that's already running does nothing.
func (m *Manager) Start(ctx context.Context, videoID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[videoID]; ok {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	m.sessions[videoID] = cancel

	m.wg.Go(func() {
		err := m.run(ctx, videoID)

		m.mu.Lock()
		delete(m.sessions, videoID)
		m.mu.Unlock()
		cancel()

		// A stop asked for by Stop or shutdown isn't news to anyone.
		if !errors.Is(err, context.Canceled) {
			m.send(context.Background(), Event{Kind: EventStopped, VideoID: videoID, Err: err})
		}
	})
}

func (m *Manager) Stop(videoID string) {
	m.mu.Lock()
	cancel, ok := m.sessions[videoID]
	m.mu.Unlock()

	if ok {
		cancel()
	}
}

func (m *Manager) Running() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (m *Manager) Wait() { m.wg.Wait() }

func (m *Manager) run(ctx context.Context, videoID string) error {
	dedup := newDeduper(dedupWindow)

	if m.cfg.TLdex != nil {
		updates := m.cfg.TLdex.Subscribe(videoID)
		defer m.cfg.TLdex.Unsubscribe(videoID)
		go m.relayTLdex(ctx, videoID, updates, dedup)
	}

	chat, err := m.open(ctx, videoID)
	if err != nil {
		return err
	}

	var failingSince time.Time
	backoff := m.cfg.Backoff
	wait := chat.Wait()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}

		msgs, err := chat.Poll(ctx)
		if err != nil && !permanent(err) {
			// Reopening picks up anything posted while the poll failed.
			msgs, err = chat.Reopen(ctx)
		}

		switch {
		case err == nil:
			failingSince, backoff = time.Time{}, m.cfg.Backoff
			wait = chat.Wait()
			for _, msg := range msgs {
				if c := fromYouTube(videoID, msg); dedup.first(c) {
					m.send(ctx, Event{Kind: EventComment, VideoID: videoID, Comment: c})
				}
			}
		case permanent(err), ctx.Err() != nil:
			return err
		default:
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			if time.Since(failingSince) > m.cfg.GiveUpAfter {
				return err
			}
			m.cfg.Log.Warn("chat poll failed", slog.String("video_id", videoID), slog.Any("error", err), slog.Duration("retry_in", backoff))
			wait = backoff
			backoff = min(backoff*2, m.cfg.MaxBackoff)
		}
	}
}

// open retries until the chat opens, it's clear it never will, or it has
// failed for GiveUpAfter.
func (m *Manager) open(ctx context.Context, videoID string) (Reader, error) {
	start := time.Now()
	backoff := m.cfg.Backoff

	for {
		chat, err := m.cfg.Open(ctx, videoID)
		if err == nil || permanent(err) || ctx.Err() != nil || time.Since(start) > m.cfg.GiveUpAfter {
			return chat, err
		}

		m.cfg.Log.Warn("couldn't open chat", slog.String("video_id", videoID), slog.Any("error", err), slog.Duration("retry_in", backoff))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, m.cfg.MaxBackoff)
	}
}

func (m *Manager) relayTLdex(ctx context.Context, videoID string, updates <-chan tldex.Update, dedup *deduper) {
	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-updates:
			if !ok {
				return
			}

			switch {
			case !u.StartedAt.IsZero():
				m.send(ctx, Event{Kind: EventStarted, VideoID: videoID, StartedAt: u.StartedAt})
			case u.Message != nil:
				if c := fromTLdex(u.Message); dedup.first(c) {
					m.send(ctx, Event{Kind: EventComment, VideoID: videoID, Comment: c})
				}
			}
			// TLdex's end notice isn't used. YouTube chat ending is the
			// signal, and it covers streams without TLdex too.
		}
	}
}

func (m *Manager) send(ctx context.Context, e Event) {
	select {
	case m.events <- e:
	case <-ctx.Done():
	}
}

// Errors that retrying won't fix.
func permanent(err error) bool {
	return errors.Is(err, livechat.ErrEnded) || errors.Is(err, livechat.ErrDisabled) ||
		errors.Is(err, livechat.ErrUnavailable) || errors.Is(err, livechat.ErrNotFound)
}

func fromYouTube(videoID string, m livechat.Message) *Comment {
	return &Comment{
		VideoID:         videoID,
		ID:              m.ID,
		AuthorChannelID: m.AuthorChannelID,
		AuthorName:      m.AuthorName,
		Text:            m.Text,
		Time:            m.Time,
		Owner:           m.Owner,
		Moderator:       m.Moderator,
		Verified:        m.Verified,
		Member:          m.Member,
		SuperChat:       m.SuperChat,
		Source:          SourceYouTube,
	}
}

func fromTLdex(m *tldex.Message) *Comment {
	source := SourceTLdex
	if m.ChannelID == "" || strings.EqualFold(m.Source, "MChad") {
		source = SourceMChad
	}

	return &Comment{
		VideoID:         m.VideoID,
		ID:              "tldex:" + m.ChannelID + ":" + m.Time.Format(time.RFC3339Nano),
		AuthorChannelID: m.ChannelID,
		AuthorName:      m.Name,
		Text:            m.Text,
		Time:            m.Time,
		Moderator:       m.Moderator,
		TL:              m.TL,
		VTuber:          m.VTuber,
		Source:          source,
	}
}
