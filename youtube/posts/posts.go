// Package posts reads a channel's community posts from its Posts tab,
// through the same browse API the YouTube website uses. No key needed.
package posts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://www.youtube.com"

const (
	// Any WEB client version is accepted, old ones included.
	clientVersion = "2.20240101.00.00"
	// Opens the Posts tab of the channel being browsed.
	postsTab = "EgVwb3N0c_IGBAoCSgA="
)

var ErrNotFound = errors.New("posts: channel not found")

type Post struct {
	ID        string
	ChannelID string
	Author    string
	AvatarURL string
	// Links are written out in full.
	Text    string
	Images  []string
	VideoID string
	Poll    []string
	// Posted in the last day. YouTube only says how long ago, like "3
	// hours ago".
	Recent bool
}

func (p *Post) URL() string { return "https://www.youtube.com/post/" + p.ID }

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

// Posts returns the newest posts first. Channels without a Posts tab have
// none.
func (c *Client) Posts(ctx context.Context, channelID string) ([]Post, error) {
	body, err := json.Marshal(map[string]any{
		"context": map[string]any{
			"client": map[string]any{"clientName": "WEB", "clientVersion": clientVersion, "hl": "en", "gl": "US"},
		},
		"browseId": channelID,
		"params":   postsTab,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/youtubei/v1/browse?prettyPrint=false", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("posts: %s", resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("posts: %w", err)
	}

	return parse(raw)
}

type text struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text               string `json:"text"`
		NavigationEndpoint struct {
			URLEndpoint *struct {
				URL string `json:"url"`
			} `json:"urlEndpoint"`
		} `json:"navigationEndpoint"`
	} `json:"runs"`
}

// Link runs show a shortened URL and point at a YouTube redirect whose q
// parameter has the real one.
func (t text) String() string {
	if t.SimpleText != "" {
		return t.SimpleText
	}

	var sb strings.Builder
	for _, r := range t.Runs {
		s := r.Text
		if ep := r.NavigationEndpoint.URLEndpoint; ep != nil {
			if u, err := url.Parse(ep.URL); err == nil {
				if q := u.Query().Get("q"); q != "" {
					s = q
				} else if u.Host != "www.youtube.com" {
					s = ep.URL
				}
			}
		}
		sb.WriteString(s)
	}
	return sb.String()
}

type thumbnails struct {
	Thumbnails []struct {
		URL string `json:"url"`
	} `json:"thumbnails"`
}

// The last thumbnail is the largest. Some URLs leave out the scheme.
func (t thumbnails) largest() string {
	if len(t.Thumbnails) == 0 {
		return ""
	}
	u := t.Thumbnails[len(t.Thumbnails)-1].URL
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	return u
}

type image struct {
	BackstageImageRenderer *struct {
		Image thumbnails `json:"image"`
	} `json:"backstageImageRenderer"`
}

type postRenderer struct {
	PostID         string     `json:"postId"`
	AuthorText     text       `json:"authorText"`
	AuthorThumb    thumbnails `json:"authorThumbnail"`
	AuthorEndpoint struct {
		BrowseEndpoint struct {
			BrowseID string `json:"browseId"`
		} `json:"browseEndpoint"`
	} `json:"authorEndpoint"`
	ContentText   text `json:"contentText"`
	PublishedTime text `json:"publishedTimeText"`
	Attachment    struct {
		image
		MultiImage *struct {
			Images []image `json:"images"`
		} `json:"postMultiImageRenderer"`
		Video *struct {
			VideoID string `json:"videoId"`
		} `json:"videoRenderer"`
		Poll *struct {
			Choices []struct {
				Text text `json:"text"`
			} `json:"choices"`
		} `json:"pollRenderer"`
	} `json:"backstageAttachment"`
}

type browseResponse struct {
	Contents struct {
		TwoColumn struct {
			Tabs []struct {
				Tab *struct {
					Title    string `json:"title"`
					Selected bool   `json:"selected"`
					Content  struct {
						SectionList struct {
							Contents []struct {
								ItemSection struct {
									Contents []struct {
										Thread *struct {
											Post struct {
												Backstage *postRenderer `json:"backstagePostRenderer"`
											} `json:"post"`
										} `json:"backstagePostThreadRenderer"`
									} `json:"contents"`
								} `json:"itemSectionRenderer"`
							} `json:"contents"`
						} `json:"sectionListRenderer"`
					} `json:"content"`
				} `json:"tabRenderer"`
			} `json:"tabs"`
		} `json:"twoColumnBrowseResultsRenderer"`
	} `json:"contents"`
}

// Unknown channels come back with an alert and no tabs.
func parse(raw []byte) ([]Post, error) {
	var resp browseResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("posts: decode: %w", err)
	}

	tabs := resp.Contents.TwoColumn.Tabs
	if len(tabs) == 0 {
		return nil, ErrNotFound
	}

	var out []Post
	for _, t := range tabs {
		if t.Tab == nil || !t.Tab.Selected || t.Tab.Title != "Posts" {
			continue
		}

		for _, section := range t.Tab.Content.SectionList.Contents {
			for _, item := range section.ItemSection.Contents {
				// Reposts of other channels' posts aren't backstage posts.
				if item.Thread == nil || item.Thread.Post.Backstage == nil {
					continue
				}
				out = append(out, toPost(item.Thread.Post.Backstage))
			}
		}
	}

	return out, nil
}

func toPost(r *postRenderer) Post {
	p := Post{
		ID:        r.PostID,
		ChannelID: r.AuthorEndpoint.BrowseEndpoint.BrowseID,
		Author:    r.AuthorText.String(),
		AvatarURL: r.AuthorThumb.largest(),
		Text:      strings.TrimSpace(r.ContentText.String()),
		Recent:    recent(r.PublishedTime.String()),
	}

	a := r.Attachment
	if a.BackstageImageRenderer != nil {
		p.Images = append(p.Images, a.BackstageImageRenderer.Image.largest())
	}
	if a.MultiImage != nil {
		for _, img := range a.MultiImage.Images {
			if img.BackstageImageRenderer != nil {
				p.Images = append(p.Images, img.BackstageImageRenderer.Image.largest())
			}
		}
	}
	if a.Video != nil {
		p.VideoID = a.Video.VideoID
	}
	if a.Poll != nil {
		for _, c := range a.Poll.Choices {
			p.Poll = append(p.Poll, c.Text.String())
		}
	}

	return p
}

// The browse request asks for English, so the time reads like "5 minutes
// ago" or "3 hours ago (edited)".
func recent(published string) bool {
	return strings.Contains(published, "second") || strings.Contains(published, "minute") ||
		strings.Contains(published, "hour")
}
