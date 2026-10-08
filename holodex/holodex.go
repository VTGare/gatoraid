package holodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://holodex.net/api/v2"

// Around 400 channel IDs in one /users/live URL gets a 414 from Holodex.
const channelsPerRequest = 100

// /channels returns at most 50 per page.
const channelPageSize = 50

var ErrNotFound = errors.New("holodex: not found")

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("holodex: %d %s: %s", e.Status, http.StatusText(e.Status), e.Body)
}

type Client struct {
	key  string
	base string
	http *http.Client
}

type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.base = strings.TrimSuffix(u, "/") } }

func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		key:  apiKey,
		base: DefaultBaseURL,
		http: &http.Client{Timeout: 20 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type Status string

const (
	StatusNew      Status = "new"
	StatusUpcoming Status = "upcoming"
	StatusLive     Status = "live"
	StatusPast     Status = "past"
	StatusMissing  Status = "missing"
)

type Video struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	TopicID string `json:"topic_id"`
	Status  Status `json:"status"`
	// Once a stream is live, this is when it actually started. Before that
	// it's the scheduled time. Holodex doesn't send start_actual.
	AvailableAt    time.Time `json:"available_at"`
	StartScheduled time.Time `json:"start_scheduled"`
	// Seconds, once the stream is over.
	Duration    int     `json:"duration"`
	LiveViewers int     `json:"live_viewers"`
	Description string  `json:"description"`
	Channel     Channel `json:"channel"`
	// Channels taking part, from Holodex's collab detection.
	Mentions []Channel `json:"mentions"`
}

type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
	Type        string `json:"type"`
	Org         string `json:"org"`
	Suborg      string `json:"suborg"`
	Photo       string `json:"photo"`
	Twitter     string `json:"twitter"`
	// The Twitch username as the channel's owner typed it, often with capitals.
	Twitch   string `json:"twitch"`
	Inactive bool   `json:"inactive"`
}

// Holodex also returns streams on other channels that mention these ones,
// and leaves out descriptions. Video has them.
func (c *Client) UsersLive(ctx context.Context, channelIDs []string) ([]Video, error) {
	var out []Video
	for start := 0; start < len(channelIDs); start += channelsPerRequest {
		batch := channelIDs[start:min(start+channelsPerRequest, len(channelIDs))]

		var videos []Video
		if err := c.get(ctx, "/users/live", url.Values{"channels": {strings.Join(batch, ",")}}, &videos); err != nil {
			return nil, err
		}
		out = append(out, videos...)
	}

	return out, nil
}

func (c *Client) Video(ctx context.Context, id string) (*Video, error) {
	var v Video
	if err := c.get(ctx, "/videos/"+url.PathEscape(id), nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *Client) Channel(ctx context.Context, id string) (*Channel, error) {
	var ch Channel
	if err := c.get(ctx, "/channels/"+url.PathEscape(id), nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Org names are Holodex's and case-sensitive, like "Hololive" or
// "Independents".
func (c *Client) OrgChannels(ctx context.Context, org string) ([]Channel, error) {
	var out []Channel
	for offset := 0; ; offset += channelPageSize {
		var page []Channel
		q := url.Values{
			"org":    {org},
			"type":   {"vtuber"},
			"limit":  {strconv.Itoa(channelPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		if err := c.get(ctx, "/channels", q, &page); err != nil {
			return nil, err
		}

		out = append(out, page...)
		if len(page) < channelPageSize {
			return out, nil
		}
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, into any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-APIKEY", c.key)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("holodex: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("holodex: %s: decode: %w", path, err)
	}
	return nil
}
