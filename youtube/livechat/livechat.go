// Package livechat reads YouTube live chat through the internal API the
// YouTube website uses, so it needs no API key or quota.
package livechat

import (
	"bytes"
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

const DefaultBaseURL = "https://www.youtube.com"

// YouTube serves different pages to clients it doesn't recognize.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36"

var (
	ErrNotFound = errors.New("livechat: video not found")
	// Members-only streams look the same as private ones from outside.
	ErrUnavailable = errors.New("livechat: chat unavailable")
	// Ended streams report their chat as disabled too.
	ErrDisabled = errors.New("livechat: chat disabled")
	ErrEnded    = errors.New("livechat: chat ended")
)

type Message struct {
	ID              string
	AuthorChannelID string
	// YouTube shows @handles in chat now, not display names.
	AuthorName string
	Text       string
	Time       time.Time
	Owner      bool
	Moderator  bool
	Verified   bool
	Member     bool
	// The amount for super chats, like "$5.00". Empty for normal messages.
	SuperChat string
}

type Client struct {
	base string
	http *http.Client
}

type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.base = strings.TrimSuffix(u, "/") } }

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func New(opts ...Option) *Client {
	c := &Client{base: DefaultBaseURL, http: &http.Client{Timeout: 20 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Not safe for concurrent use.
type Chat struct {
	client        *Client
	videoID       string
	context       json.RawMessage
	clientVersion string
	continuation  string
	wait          time.Duration
	seen          *recentIDs
}

// Open starts following a chat. Messages already in the chat when it opens
// are skipped.
func (c *Client) Open(ctx context.Context, videoID string) (*Chat, error) {
	ch := &Chat{client: c, videoID: videoID, seen: newRecentIDs(2000)}
	if _, err := ch.bootstrap(ctx); err != nil {
		return nil, err
	}
	return ch, nil
}

func (ch *Chat) VideoID() string { return ch.videoID }

// How long YouTube asks clients to wait before the next Poll.
func (ch *Chat) Wait() time.Duration { return ch.wait }

func (ch *Chat) Poll(ctx context.Context) ([]Message, error) {
	resp, err := ch.client.getLiveChat(ctx, ch.context, ch.clientVersion, ch.continuation)
	if err != nil {
		return nil, err
	}
	return ch.take(resp)
}

// Reopen reconnects after an error. Messages posted in between are
// delivered, without repeating ones already returned.
func (ch *Chat) Reopen(ctx context.Context) ([]Message, error) {
	return ch.bootstrap(ctx)
}

func (ch *Chat) bootstrap(ctx context.Context) ([]Message, error) {
	page, err := ch.client.chatPage(ctx, ch.videoID)
	if err != nil {
		return nil, err
	}

	ch.context, ch.clientVersion = page.context, page.clientVersion
	resp, err := ch.client.getLiveChat(ctx, ch.context, ch.clientVersion, page.continuation)
	if err != nil {
		return nil, err
	}

	// Reload responses start with the chat's recent history. On the first
	// open that history is old news.
	first := ch.continuation == ""
	msgs, err := ch.take(resp)
	if first {
		return nil, err
	}
	return msgs, err
}

func (ch *Chat) take(resp *liveChatContinuation) ([]Message, error) {
	next, wait, ok := resp.next()
	if !ok {
		return nil, ErrEnded
	}
	ch.continuation, ch.wait = next, wait

	var out []Message
	for _, m := range resp.messages() {
		if ch.seen.add(m.ID) {
			out = append(out, m)
		}
	}
	return out, nil
}

type ytcfg struct {
	Context       json.RawMessage `json:"INNERTUBE_CONTEXT"`
	ClientVersion string          `json:"INNERTUBE_CLIENT_VERSION"`
}

type chatPage struct {
	context       json.RawMessage
	clientVersion string
	// The "Live chat" view. The default "Top chat" view filters messages.
	continuation string
}

func (c *Client) chatPage(ctx context.Context, videoID string) (*chatPage, error) {
	u := c.base + "/live_chat?" + url.Values{"v": {videoID}, "is_popout": {"1"}, "hl": {"en"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Skips the cookie consent page YouTube shows in some countries.
	req.Header.Set("Cookie", "CONSENT=YES+cb; SOCS=CAI")

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	// Pages call ytcfg.set several times with different parts of the config.
	var cfg ytcfg
	for rest := body; ; {
		i := bytes.Index(rest, []byte("ytcfg.set({"))
		if i < 0 {
			break
		}
		rest = rest[i+len("ytcfg.set("):]

		var part ytcfg
		if json.NewDecoder(bytes.NewReader(rest)).Decode(&part) == nil {
			if part.Context != nil {
				cfg.Context = part.Context
			}
			if part.ClientVersion != "" {
				cfg.ClientVersion = part.ClientVersion
			}
		}
	}

	var initial initialData
	if !decodeAfter(body, &initial, `window["ytInitialData"] = `, `var ytInitialData = `) || cfg.Context == nil {
		return nil, ErrNotFound
	}

	if m := initial.Contents.Message; m != nil {
		text := m.Text.String()
		if strings.Contains(strings.ToLower(text), "disabled") {
			return nil, fmt.Errorf("%w: %s", ErrDisabled, text)
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, text)
	}

	renderer := initial.Contents.LiveChat
	if renderer == nil {
		return nil, ErrNotFound
	}

	cont := renderer.allMessages()
	if cont == "" {
		return nil, fmt.Errorf("livechat: %s: no live chat view in the page", videoID)
	}

	return &chatPage{context: cfg.Context, clientVersion: cfg.ClientVersion, continuation: cont}, nil
}

func (c *Client) getLiveChat(ctx context.Context, apiContext json.RawMessage, clientVersion, continuation string) (*liveChatContinuation, error) {
	payload, err := json.Marshal(map[string]any{"context": apiContext, "continuation": continuation})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/youtubei/v1/live_chat/get_live_chat?prettyPrint=false", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Youtube-Client-Name", "1")
	req.Header.Set("X-Youtube-Client-Version", clientVersion)

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ContinuationContents struct {
			LiveChat *liveChatContinuation `json:"liveChatContinuation"`
		} `json:"continuationContents"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("livechat: decode: %w", err)
	}
	if resp.ContinuationContents.LiveChat == nil {
		return nil, ErrEnded
	}
	return resp.ContinuationContents.LiveChat, nil
}

type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("livechat: %d %s", e.Status, http.StatusText(e.Status))
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("livechat: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode}
	}

	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func decodeAfter(body []byte, into any, markers ...string) bool {
	for _, m := range markers {
		if i := bytes.Index(body, []byte(m)); i >= 0 {
			return json.NewDecoder(bytes.NewReader(body[i+len(m):])).Decode(into) == nil
		}
	}
	return false
}

func parseUsec(s string) time.Time {
	usec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(usec)
}
