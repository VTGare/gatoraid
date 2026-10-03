package livechat

import (
	"encoding/json"
	"strings"
	"time"
)

type initialData struct {
	Contents struct {
		LiveChat *liveChatRenderer `json:"liveChatRenderer"`
		Message  *struct {
			Text text `json:"text"`
		} `json:"messageRenderer"`
	} `json:"contents"`
}

type liveChatRenderer struct {
	Header struct {
		Header struct {
			ViewSelector struct {
				Menu struct {
					Items []struct {
						Continuation struct {
							Reload continuationData `json:"reloadContinuationData"`
						} `json:"continuation"`
					} `json:"subMenuItems"`
				} `json:"sortFilterSubMenuRenderer"`
			} `json:"viewSelector"`
		} `json:"liveChatHeaderRenderer"`
	} `json:"header"`
}

// The view selector lists "Top chat" then "Live chat".
func (r *liveChatRenderer) allMessages() string {
	items := r.Header.Header.ViewSelector.Menu.Items
	if len(items) < 2 {
		return ""
	}
	return items[1].Continuation.Reload.Continuation
}

type continuationData struct {
	Continuation string `json:"continuation"`
	TimeoutMs    int    `json:"timeoutMs"`
}

type liveChatContinuation struct {
	Continuations []struct {
		Invalidation *continuationData `json:"invalidationContinuationData"`
		Timed        *continuationData `json:"timedContinuationData"`
		Reload       *continuationData `json:"reloadContinuationData"`
	} `json:"continuations"`
	Actions []struct {
		AddChatItem *struct {
			Item struct {
				Text *messageRenderer `json:"liveChatTextMessageRenderer"`
				Paid *messageRenderer `json:"liveChatPaidMessageRenderer"`
			} `json:"item"`
		} `json:"addChatItemAction"`
	} `json:"actions"`
}

// YouTube asks quiet chats to wait about 15 seconds. maxWait only guards
// against a broken timeoutMs.
const (
	minWait     = time.Second
	maxWait     = 30 * time.Second
	defaultWait = 5 * time.Second
)

func (l *liveChatContinuation) next() (string, time.Duration, bool) {
	for _, c := range l.Continuations {
		for _, d := range []*continuationData{c.Invalidation, c.Timed, c.Reload} {
			if d == nil || d.Continuation == "" {
				continue
			}

			wait := defaultWait
			if d.TimeoutMs > 0 {
				wait = min(max(time.Duration(d.TimeoutMs)*time.Millisecond, minWait), maxWait)
			}
			return d.Continuation, wait, true
		}
	}
	return "", 0, false
}

func (l *liveChatContinuation) messages() []Message {
	var out []Message
	for _, a := range l.Actions {
		if a.AddChatItem == nil {
			continue
		}

		item := a.AddChatItem.Item
		switch {
		case item.Text != nil:
			out = append(out, item.Text.message())
		case item.Paid != nil:
			m := item.Paid.message()
			m.SuperChat = item.Paid.PurchaseAmount.String()
			out = append(out, m)
		}
	}
	return out
}

type messageRenderer struct {
	ID              string `json:"id"`
	Message         text   `json:"message"`
	AuthorName      text   `json:"authorName"`
	AuthorChannelID string `json:"authorExternalChannelId"`
	TimestampUsec   string `json:"timestampUsec"`
	PurchaseAmount  text   `json:"purchaseAmountText"`
	Badges          []struct {
		Badge struct {
			Icon *struct {
				IconType string `json:"iconType"`
			} `json:"icon"`
			// Only member badges use a channel-specific image.
			CustomThumbnail json.RawMessage `json:"customThumbnail"`
		} `json:"liveChatAuthorBadgeRenderer"`
	} `json:"authorBadges"`
}

func (r *messageRenderer) message() Message {
	m := Message{
		ID:              r.ID,
		AuthorChannelID: r.AuthorChannelID,
		AuthorName:      r.AuthorName.String(),
		Text:            r.Message.String(),
		Time:            parseUsec(r.TimestampUsec),
	}

	for _, b := range r.Badges {
		if b.Badge.CustomThumbnail != nil {
			m.Member = true
		}
		if b.Badge.Icon == nil {
			continue
		}
		switch b.Badge.Icon.IconType {
		case "OWNER":
			m.Owner = true
		case "MODERATOR":
			m.Moderator = true
		case "VERIFIED":
			m.Verified = true
		}
	}

	return m
}

type text struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text  string `json:"text"`
		Emoji *struct {
			EmojiID   string   `json:"emojiId"`
			Shortcuts []string `json:"shortcuts"`
			IsCustom  bool     `json:"isCustomEmoji"`
		} `json:"emoji"`
	} `json:"runs"`
}

// Channel emojis become their shortcut, like :_MachiPat:, and standard
// emojis their Unicode character.
func (t text) String() string {
	if t.SimpleText != "" {
		return t.SimpleText
	}

	var sb strings.Builder
	for _, r := range t.Runs {
		switch {
		case r.Emoji == nil:
			sb.WriteString(r.Text)
		case r.Emoji.IsCustom && len(r.Emoji.Shortcuts) > 0:
			sb.WriteString(r.Emoji.Shortcuts[0])
		default:
			sb.WriteString(r.Emoji.EmojiID)
		}
	}
	return sb.String()
}

// recentIDs remembers the last n message IDs, so a reconnect doesn't
// deliver messages twice.
type recentIDs struct {
	set   map[string]struct{}
	order []string
	next  int
}

func newRecentIDs(n int) *recentIDs {
	return &recentIDs{set: make(map[string]struct{}, n), order: make([]string, n)}
}

// add reports whether id is new.
func (r *recentIDs) add(id string) bool {
	if _, ok := r.set[id]; ok {
		return false
	}

	if old := r.order[r.next]; old != "" {
		delete(r.set, old)
	}
	r.order[r.next] = id
	r.next = (r.next + 1) % len(r.order)
	r.set[id] = struct{}{}

	return true
}
