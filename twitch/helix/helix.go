// Package helix asks the Twitch API which channels are live. It signs in as
// an app, so it needs a client ID and secret but no Twitch account.
package helix

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
	"sync"
	"time"

	"github.com/VTGare/gatoraid/twitch"
)

const (
	DefaultBaseURL  = "https://api.twitch.tv/helix"
	DefaultTokenURL = "https://id.twitch.tv/oauth2/token"
)

// /streams takes at most 100 user_login parameters.
const usernamesPerRequest = 100

// Tokens are renewed this long before Twitch says they expire.
const tokenMargin = time.Minute

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("helix: %d %s: %s", e.Status, http.StatusText(e.Status), e.Body)
}

type Stream struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Username    string `json:"user_login"`
	DisplayName string `json:"user_name"`
	// "live", or "" after an error on Twitch's side.
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	GameName  string    `json:"game_name"`
	StartedAt time.Time `json:"started_at"`
	// A template with {width} and {height} in it.
	ThumbnailURL string `json:"thumbnail_url"`
}

type Client struct {
	id, secret string
	base       string
	tokenURL   string
	http       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.base = strings.TrimSuffix(u, "/") } }

func WithTokenURL(u string) Option { return func(c *Client) { c.tokenURL = u } }

func New(clientID, clientSecret string, opts ...Option) *Client {
	c := &Client{
		id:       clientID,
		secret:   clientSecret,
		base:     DefaultBaseURL,
		tokenURL: DefaultTokenURL,
		http:     &http.Client{Timeout: 20 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Malformed usernames are skipped.
func (c *Client) Streams(ctx context.Context, usernames []string) ([]Stream, error) {
	valid := make([]string, 0, len(usernames))
	for _, l := range usernames {
		if twitch.ValidUsername(l) {
			valid = append(valid, l)
		}
	}

	var out []Stream
	for start := 0; start < len(valid); start += usernamesPerRequest {
		batch := valid[start:min(start+usernamesPerRequest, len(valid))]

		q := url.Values{"first": {strconv.Itoa(usernamesPerRequest)}, "user_login": batch}
		var page struct {
			Data []Stream `json:"data"`
		}
		if err := c.get(ctx, "/streams", q, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Data...)
	}

	return out, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, into any) error {
	err := c.getOnce(ctx, path, query, into)

	// The token can be revoked before it expires. One fresh token is worth a
	// retry.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		err = c.getOnce(ctx, path, query, into)
	}
	return err
}

func (c *Client) getOnce(ctx context.Context, path string, query url.Values, into any) error {
	token, err := c.appToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Client-Id", c.id)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("helix: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("helix: %s: decode: %w", path, err)
	}
	return nil
}

func (c *Client) appToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	form := url.Values{"client_id": {c.id}, "client_secret": {c.secret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("helix: token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("helix: token: %w", readError(resp))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("helix: token: decode: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("helix: token: no access token in the response")
	}

	c.token = tok.AccessToken
	c.expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - tokenMargin)
	return c.token, nil
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
}

func Thumbnail(template string, width, height int) string {
	return strings.NewReplacer("{width}", strconv.Itoa(width), "{height}", strconv.Itoa(height)).Replace(template)
}
