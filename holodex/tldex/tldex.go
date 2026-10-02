// Package tldex follows Holodex's TLdex feed, which relays translations and
// streamer messages from YouTube chats, plus lines posted with MChad.
package tldex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Socket.IO v4 over a plain websocket, without the polling handshake.
const DefaultURL = "wss://holodex.net/api/socket.io/?EIO=4&transport=websocket"

const lang = "en"

type Message struct {
	VideoID string
	// Empty for lines posted with MChad, which never were in YouTube chat.
	ChannelID string
	Name      string
	Text      string
	Time      time.Time
	TL        bool
	VTuber    bool
	Moderator bool
	Source    string
}

type Update struct {
	Message *Message
	// Set once Holodex confirms the subscription, when it knows the start.
	StartedAt time.Time
	Ended     bool
}

type Client struct {
	url    string
	dialer *websocket.Dialer
	log    *slog.Logger
	retry  time.Duration

	mu    sync.Mutex
	conn  *websocket.Conn
	subs  map[string]chan Update
	write sync.Mutex
}

type Option func(*Client)

func WithURL(u string) Option { return func(c *Client) { c.url = u } }

func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// The first wait after a dropped connection. It doubles up to a minute.
func WithRetry(d time.Duration) Option { return func(c *Client) { c.retry = d } }

func New(opts ...Option) *Client {
	c := &Client{
		url:    DefaultURL,
		dialer: &websocket.Dialer{HandshakeTimeout: 15 * time.Second},
		log:    slog.New(slog.DiscardHandler),
		retry:  time.Second,
		subs:   map[string]chan Update{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// The channel closes on Unsubscribe. Updates are dropped if nobody reads
// them.
func (c *Client) Subscribe(videoID string) <-chan Update {
	c.mu.Lock()
	ch, ok := c.subs[videoID]
	if !ok {
		ch = make(chan Update, 256)
		c.subs[videoID] = ch
	}
	conn := c.conn
	c.mu.Unlock()

	if !ok && conn != nil {
		c.emit(conn, "subscribe", videoID)
	}
	return ch
}

func (c *Client) Unsubscribe(videoID string) {
	c.mu.Lock()
	ch, ok := c.subs[videoID]
	delete(c.subs, videoID)
	conn := c.conn
	c.mu.Unlock()

	if !ok {
		return
	}
	close(ch)
	if conn != nil {
		c.emit(conn, "unsubscribe", videoID)
	}
}

// Run resubscribes to everything after each reconnect.
func (c *Client) Run(ctx context.Context) error {
	wait := c.retry
	for {
		connected, err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			wait = c.retry
		}
		c.log.Warn("TLdex connection lost", slog.Any("error", err), slog.Duration("retry_in", wait))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
}

// session runs one connection and reports whether it got as far as the
// Socket.IO handshake.
func (c *Client) session(ctx context.Context) (bool, error) {
	conn, _, err := c.dialer.DialContext(ctx, c.url, http.Header{"Origin": {"https://holodex.net"}})
	if err != nil {
		return false, err
	}

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	// The server pings every pingInterval. A missing ping means the
	// connection is dead even if TCP hasn't noticed.
	timeout := 60 * time.Second
	connected := false

	for {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return connected, err
		}

		packet := string(data)
		switch {
		case strings.HasPrefix(packet, "0"):
			var open struct {
				PingInterval int `json:"pingInterval"`
				PingTimeout  int `json:"pingTimeout"`
			}
			if json.Unmarshal(data[1:], &open) == nil && open.PingInterval > 0 {
				timeout = time.Duration(open.PingInterval+open.PingTimeout) * time.Millisecond
			}
			if err := c.send(conn, "40"); err != nil {
				return connected, err
			}

		case packet == "2":
			if err := c.send(conn, "3"); err != nil {
				return connected, err
			}

		case strings.HasPrefix(packet, "40"):
			connected = true
			c.mu.Lock()
			c.conn = conn
			videos := make([]string, 0, len(c.subs))
			for id := range c.subs {
				videos = append(videos, id)
			}
			c.mu.Unlock()

			for _, id := range videos {
				c.emit(conn, "subscribe", id)
			}

		case strings.HasPrefix(packet, "41"), strings.HasPrefix(packet, "44"):
			return connected, fmt.Errorf("tldex: server closed the namespace: %s", packet)

		case strings.HasPrefix(packet, "42"):
			c.handleEvent(data[2:])
		}
	}
}

func (c *Client) handleEvent(data []byte) {
	var event []json.RawMessage
	if json.Unmarshal(data, &event) != nil || len(event) < 2 {
		return
	}

	var name string
	if json.Unmarshal(event[0], &name) != nil {
		return
	}

	switch name {
	case "subscribeSuccess":
		var ok struct {
			ID          string    `json:"id"`
			StartActual time.Time `json:"start_actual"`
		}
		if json.Unmarshal(event[1], &ok) == nil {
			c.deliver(ok.ID, Update{StartedAt: ok.StartActual})
		}

	case "subscribeError":
		var bad struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(event[1], &bad)
		c.log.Debug("TLdex subscription failed", slog.String("video_id", bad.ID), slog.String("message", bad.Message))

	default:
		videoID, l, ok := strings.Cut(name, "/")
		if !ok || l != lang {
			return
		}
		if u, ok := parseUpdate(videoID, event[1]); ok {
			c.deliver(videoID, u)
		}
	}
}

func parseUpdate(videoID string, data json.RawMessage) (Update, bool) {
	var raw struct {
		Type        string `json:"type"`
		Name        string `json:"name"`
		ChannelID   string `json:"channel_id"`
		Message     string `json:"message"`
		Timestamp   int64  `json:"timestamp"`
		IsTL        bool   `json:"is_tl"`
		IsVTuber    bool   `json:"is_vtuber"`
		IsModerator bool   `json:"is_moderator"`
		Source      string `json:"source"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return Update{}, false
	}

	if raw.Type == "end" {
		return Update{Ended: true}, true
	}
	if raw.Name == "" || raw.Message == "" {
		return Update{}, false
	}

	return Update{Message: &Message{
		VideoID:   videoID,
		ChannelID: raw.ChannelID,
		Name:      raw.Name,
		Text:      raw.Message,
		Time:      time.UnixMilli(raw.Timestamp),
		TL:        raw.IsTL || raw.Source == "MChad",
		VTuber:    raw.IsVTuber,
		Moderator: raw.IsModerator,
		Source:    raw.Source,
	}}, true
}

func (c *Client) deliver(videoID string, u Update) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch, ok := c.subs[videoID]
	if !ok {
		return
	}
	select {
	case ch <- u:
	default:
		c.log.Warn("dropping a TLdex update nobody read", slog.String("video_id", videoID))
	}
}

func (c *Client) emit(conn *websocket.Conn, event, videoID string) {
	payload, _ := json.Marshal([]any{event, map[string]string{"video_id": videoID, "lang": lang}})
	if err := c.send(conn, "42"+string(payload)); err != nil {
		c.log.Debug("TLdex write failed", slog.String("event", event), slog.Any("error", err))
	}
}

var errClosed = errors.New("tldex: connection closed")

func (c *Client) send(conn *websocket.Conn, packet string) error {
	if conn == nil {
		return errClosed
	}
	c.write.Lock()
	defer c.write.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, []byte(packet))
}
