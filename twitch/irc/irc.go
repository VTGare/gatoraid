// Package irc reads Twitch chat over IRC on a websocket. It logs in
// anonymously as justinfan<digits>, which Twitch lets read any channel.
package irc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/VTGare/gatoraid/twitch"
)

const DefaultURL = "wss://irc-ws.chat.twitch.tv:443"

const (
	// Twitch allows 20 JOINs every 10 seconds.
	defaultJoinEvery = 500 * time.Millisecond
	pingEvery        = time.Minute
	// Our PINGs get a PONG, so a connection that's quiet for longer is dead.
	readTimeout  = pingEvery + 30*time.Second
	writeTimeout = 10 * time.Second
)

type Message struct {
	ID string
	// The username of the channel the line was said in.
	Channel     string
	UserID      string
	Username    string
	DisplayName string
	Text        string
	Time        time.Time
	Broadcaster bool
	Moderator   bool
	Subscriber  bool
	// Twitch partners have a verified badge.
	Partner bool
	Bits    int
	// Names of the Twitch emotes in Text. Emotes from extensions like 7TV
	// aren't marked and look like words.
	Emotes []string
}

type Client struct {
	url       string
	nick      string
	dialer    *websocket.Dialer
	log       *slog.Logger
	retry     time.Duration
	joinEvery time.Duration

	mu   sync.Mutex
	subs map[string]map[chan *Message]struct{}
	conn *session
}

type Option func(*Client)

func WithURL(u string) Option { return func(c *Client) { c.url = u } }

func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// The first wait after a dropped connection. It doubles up to a minute.
func WithRetry(d time.Duration) Option { return func(c *Client) { c.retry = d } }

func WithJoinEvery(d time.Duration) Option { return func(c *Client) { c.joinEvery = d } }

func New(opts ...Option) *Client {
	c := &Client{
		url:       DefaultURL,
		nick:      "justinfan" + strconv.Itoa(10000+rand.IntN(90000)),
		dialer:    &websocket.Dialer{HandshakeTimeout: 15 * time.Second},
		log:       slog.New(slog.DiscardHandler),
		retry:     time.Second,
		joinEvery: defaultJoinEvery,
		subs:      map[string]map[chan *Message]struct{}{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Subscribe follows a channel's chat until stop is called, which closes
// lines. Lines nobody reads are dropped. A malformed username gets nothing.
func (c *Client) Subscribe(username string) (lines <-chan *Message, stop func()) {
	username = strings.ToLower(username)
	ch := make(chan *Message, 256)
	if !twitch.ValidUsername(username) {
		c.log.Warn("not joining a malformed Twitch username", slog.String("username", username))
		return ch, func() {}
	}

	c.mu.Lock()
	first := len(c.subs[username]) == 0
	if first {
		c.subs[username] = map[chan *Message]struct{}{}
	}
	c.subs[username][ch] = struct{}{}
	if first && c.conn != nil {
		c.conn.queueJoin(username)
	}
	c.mu.Unlock()

	var once sync.Once
	return ch, func() { once.Do(func() { c.unsubscribe(username, ch) }) }
}

func (c *Client) unsubscribe(username string, ch chan *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.subs[username], ch)
	// Closed under the lock, so deliver never sends on a closed channel.
	close(ch)

	if len(c.subs[username]) == 0 {
		delete(c.subs, username)
		if c.conn != nil {
			c.conn.part(username)
		}
	}
}

func (c *Client) subscribed(username string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs[username]) > 0
}

// Run reconnects until ctx is done and rejoins every channel each time.
func (c *Client) Run(ctx context.Context) error {
	wait := c.retry
	for {
		welcomed, err := c.runSession(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if welcomed {
			wait = c.retry
		}
		c.log.Warn("Twitch chat connection lost", slog.Any("error", err), slog.Duration("retry_in", wait))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
}

// runSession runs one connection and reports whether Twitch welcomed it.
func (c *Client) runSession(ctx context.Context) (bool, error) {
	ws, _, err := c.dialer.DialContext(ctx, c.url, http.Header{})
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithCancel(ctx)
	s := &session{client: c, ws: ws, wake: make(chan struct{}, 1)}
	stop := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer func() {
		cancel()
		stop()
		c.mu.Lock()
		if c.conn == s {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = ws.Close()
	}()

	for _, l := range []string{"CAP REQ :twitch.tv/tags twitch.tv/commands", "PASS SCHMOOPIIE", "NICK " + c.nick} {
		if err := s.send(l); err != nil {
			return false, err
		}
	}

	welcomed := false
	for {
		_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return welcomed, err
		}

		for raw := range strings.SplitSeq(string(data), "\r\n") {
			if raw == "" {
				continue
			}
			l, ok := parseLine(raw)
			if !ok {
				continue
			}

			switch l.command {
			case "001":
				welcomed = true
				c.mu.Lock()
				c.conn = s
				for username := range c.subs {
					s.queueJoin(username)
				}
				c.mu.Unlock()
				go s.joinLoop(ctx)
				go s.pingLoop(ctx)

			case "PING":
				if err := s.send("PONG :" + l.param(0)); err != nil {
					return welcomed, err
				}

			case "RECONNECT":
				return welcomed, errors.New("twitch: server asked to reconnect")

			case "NOTICE":
				if err := c.notice(l); err != nil {
					return welcomed, err
				}

			case "PRIVMSG":
				if m, ok := parseMessage(l); ok {
					c.deliver(m)
				}
			}
		}
	}
}

// Twitch says nothing to anonymous users about channels that don't
// exist. Login failures are the notices without a msg-id.
func (c *Client) notice(l line) error {
	if l.tags["msg-id"] == "" && strings.Contains(strings.ToLower(l.param(1)), "login") {
		return fmt.Errorf("twitch: %s", l.param(1))
	}
	return nil
}

func (c *Client) deliver(m *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for ch := range c.subs[m.Channel] {
		select {
		case ch <- m:
		default:
			c.log.Warn("dropping a Twitch chat line nobody read", slog.String("channel", m.Channel))
		}
	}
}

type session struct {
	client *Client
	ws     *websocket.Conn
	write  sync.Mutex

	// Joins wait their turn, so a reconnect with many channels stays under
	// Twitch's join limit.
	mu      sync.Mutex
	pending []string
	wake    chan struct{}
}

func (s *session) queueJoin(username string) {
	s.mu.Lock()
	s.pending = append(s.pending, username)
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *session) part(username string) {
	s.mu.Lock()
	queued := len(s.pending)
	s.pending = slices.DeleteFunc(s.pending, func(u string) bool { return u == username })
	joined := len(s.pending) == queued
	s.mu.Unlock()

	// Callers hold the client's lock, so PART goes out from another goroutine.
	if joined {
		go func() { _ = s.send("PART #" + username) }()
	}
}

func (s *session) joinLoop(ctx context.Context) {
	for {
		s.mu.Lock()
		var username string
		if len(s.pending) > 0 {
			username, s.pending = s.pending[0], s.pending[1:]
		}
		s.mu.Unlock()

		if username == "" {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}

		// It may have been unsubscribed while it waited.
		if !s.client.subscribed(username) {
			continue
		}
		if err := s.send("JOIN #" + username); err != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.client.joinEvery):
		}
	}
}

func (s *session) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.send("PING :gatoraid") != nil {
				return
			}
		}
	}
}

func (s *session) send(l string) error {
	s.write.Lock()
	defer s.write.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return s.ws.WriteMessage(websocket.TextMessage, []byte(l+"\r\n"))
}
