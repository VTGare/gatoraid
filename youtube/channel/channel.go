// Package channel looks up YouTube channels from what people paste: a
// channel ID, an @handle or a channel URL. It reads the channel page, so it
// needs no API key.
package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const DefaultBaseURL = "https://www.youtube.com"

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36"

var (
	ErrNotFound = errors.New("channel: not found")
	// The input isn't a channel ID, @handle or channel URL.
	ErrInvalid = errors.New("channel: not a channel")
)

var channelID = regexp.MustCompile(`^UC[\w-]{22}$`)

type Channel struct {
	ID   string
	Name string
	// With the @. Empty for channels without one.
	Handle    string
	AvatarURL string
}

type Client struct {
	base string
	http *http.Client
}

type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.base = strings.TrimSuffix(u, "/") } }

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func New(opts ...Option) *Client {
	c := &Client{base: DefaultBaseURL, http: &http.Client{Timeout: 10 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Path turns user input into the channel page's path, like /@handle or
// /channel/UC…. /c/ and /user/ URLs work too.
func Path(input string) (string, error) {
	input = strings.TrimSpace(input)

	switch {
	case channelID.MatchString(input):
		return "/channel/" + input, nil
	case strings.HasPrefix(input, "@") && len(input) > 1 && !strings.ContainsAny(input, "/ "):
		return "/" + url.PathEscape(input), nil
	}

	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err != nil {
		return "", ErrInvalid
	}

	host := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), "m.")
	if host != "youtube.com" {
		return "", ErrInvalid
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1:
		return "/" + url.PathEscape(parts[0]), nil
	case len(parts) >= 2 && parts[0] == "channel" && channelID.MatchString(parts[1]):
		return "/channel/" + parts[1], nil
	case len(parts) >= 2 && (parts[0] == "c" || parts[0] == "user") && parts[1] != "":
		return "/" + parts[0] + "/" + url.PathEscape(parts[1]), nil
	}

	return "", ErrInvalid
}

func (c *Client) Resolve(ctx context.Context, input string) (*Channel, error) {
	path, err := Path(input)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Skips the cookie consent page YouTube shows in some countries.
	req.Header.Set("Cookie", "CONSENT=YES+cb; SOCS=CAI")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("channel: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("channel: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("channel: %w", err)
	}

	return parse(body)
}

type initialData struct {
	Metadata struct {
		Channel *struct {
			Title            string `json:"title"`
			ExternalID       string `json:"externalId"`
			VanityChannelURL string `json:"vanityChannelUrl"`
			Avatar           struct {
				Thumbnails []struct {
					URL string `json:"url"`
				} `json:"thumbnails"`
			} `json:"avatar"`
		} `json:"channelMetadataRenderer"`
	} `json:"metadata"`
}

// Pages of channels that don't exist still load, with an alert and no
// channel metadata.
func parse(body []byte) (*Channel, error) {
	var data initialData
	found := false
	for _, marker := range []string{`var ytInitialData = `, `window["ytInitialData"] = `} {
		if i := bytes.Index(body, []byte(marker)); i >= 0 {
			if err := json.NewDecoder(bytes.NewReader(body[i+len(marker):])).Decode(&data); err != nil {
				return nil, fmt.Errorf("channel: decode page: %w", err)
			}
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("channel: page has no ytInitialData")
	}

	md := data.Metadata.Channel
	if md == nil || md.ExternalID == "" {
		return nil, ErrNotFound
	}

	ch := &Channel{ID: md.ExternalID, Name: md.Title}
	if i := strings.Index(md.VanityChannelURL, "/@"); i >= 0 {
		ch.Handle, _ = url.PathUnescape(md.VanityChannelURL[i+1:])
	}
	if len(md.Avatar.Thumbnails) > 0 {
		ch.AvatarURL = md.Avatar.Thumbnails[0].URL
	}

	return ch, nil
}
