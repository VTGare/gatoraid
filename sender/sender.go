// Package sender posts messages to Discord through one queue per channel,
// so a busy or broken channel doesn't hold up the others.
//
// DisGo already waits out rate limits and retries 429s. The sender retries
// server errors and stops posting to channels that refuse the bot for a
// while, because Discord bans IPs that send too many invalid requests.
package sender

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const (
	defaultQueueSize   = 100
	defaultCooldown    = 10 * time.Minute
	defaultIdleTimeout = time.Minute
	defaultRetries     = 3
	defaultBackoff     = time.Second
)

type Poster interface {
	CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
}

type Message struct {
	ChannelID string
	Send      discord.MessageCreate
	// Called with the posted message, from the channel's goroutine.
	OnSent func(*discord.Message)
}

type Config struct {
	Poster Poster
	Log    *slog.Logger
	// Messages waiting per channel. More are dropped.
	QueueSize int
	// How long a channel that refused the bot is skipped.
	Cooldown time.Duration
	// Channel goroutines exit after this long without messages.
	IdleTimeout time.Duration
	// Zero means the default. Negative means none.
	Retries int
	// Doubles after every retry.
	Backoff time.Duration
	// Called when a channel refuses the bot, before its cooldown starts.
	OnRefused func(channelID string, err error)
}

type Sender struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	queues  map[string]chan Message
	refused map[string]time.Time
}

func New(cfg Config) *Sender {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultCooldown
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	} else if cfg.Retries == 0 {
		cfg.Retries = defaultRetries
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultBackoff
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Sender{
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		queues:  map[string]chan Message{},
		refused: map[string]time.Time{},
	}
}

// Send queues the message and reports whether it was accepted. Messages to
// channels on cooldown, to full queues and after Close are dropped.
func (s *Sender) Send(msg Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx.Err() != nil {
		return false
	}

	if until, ok := s.refused[msg.ChannelID]; ok {
		if time.Now().Before(until) {
			return false
		}
		delete(s.refused, msg.ChannelID)
	}

	q, ok := s.queues[msg.ChannelID]
	if !ok {
		q = make(chan Message, s.cfg.QueueSize)
		s.queues[msg.ChannelID] = q
		s.wg.Add(1)
		go s.work(msg.ChannelID, q)
	}

	select {
	case q <- msg:
		return true
	default:
		s.cfg.Log.Warn("send queue full, dropping a message", slog.String("channel_id", msg.ChannelID))
		return false
	}
}

// Close drops queued messages and waits for messages being sent.
func (s *Sender) Close() {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Sender) work(channelID string, q chan Message) {
	defer s.wg.Done()

	idle := time.NewTimer(s.cfg.IdleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-q:
			s.post(msg)
			idle.Reset(s.cfg.IdleTimeout)
		case <-idle.C:
			// Send enqueues under the lock, so an empty queue here stays
			// empty until it's gone from the map.
			s.mu.Lock()
			if len(q) == 0 {
				delete(s.queues, channelID)
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			idle.Reset(s.cfg.IdleTimeout)
		}
	}
}

func (s *Sender) post(msg Message) {
	log := s.cfg.Log.With(slog.String("channel_id", msg.ChannelID))
	backoff := s.cfg.Backoff

	channelID, err := snowflake.Parse(msg.ChannelID)
	if err != nil {
		log.Error("can't send to an invalid channel ID", slog.Any("error", err))
		return
	}

	for attempt := 0; ; attempt++ {
		rewind(msg.Send)
		sent, err := s.cfg.Poster.CreateMessage(channelID, msg.Send, rest.WithCtx(s.ctx))
		if err == nil {
			if msg.OnSent != nil {
				msg.OnSent(sent)
			}
			return
		}

		switch {
		case s.ctx.Err() != nil:
			return
		case refused(err):
			s.refuse(msg.ChannelID, err)
			log.Warn("can't post in channel, pausing it", slog.Duration("for", s.cfg.Cooldown), slog.Any("error", err))
			return
		case !retryable(err) || attempt >= s.cfg.Retries:
			log.Error("failed to send a message", slog.Int("attempts", attempt+1), slog.Any("error", err))
			return
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
			backoff *= 2
		}
	}
}

// Drops what's queued for the channel too, since it would fail the same
// way.
func (s *Sender) refuse(channelID string, err error) {
	s.mu.Lock()
	s.refused[channelID] = time.Now().Add(s.cfg.Cooldown)
	if q, ok := s.queues[channelID]; ok {
		for len(q) > 0 {
			<-q
		}
	}
	s.mu.Unlock()

	if s.cfg.OnRefused != nil {
		s.cfg.OnRefused(channelID, err)
	}
}

// A failed attempt may have read attachments partway, and a retry would
// upload what's left.
func rewind(m discord.MessageCreate) {
	for _, f := range m.Files {
		if s, ok := f.Reader.(io.Seeker); ok {
			_, _ = s.Seek(0, io.SeekStart)
		}
	}
}

func refused(err error) bool {
	var restErr *rest.Error
	if !errors.As(err, &restErr) || restErr.Response == nil {
		return false
	}
	return restErr.Response.StatusCode == http.StatusForbidden || restErr.Response.StatusCode == http.StatusNotFound
}

// Other 4xx errors are the message's fault, so sending it again won't
// help.
func retryable(err error) bool {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode >= http.StatusInternalServerError
	}
	return true
}
