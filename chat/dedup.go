package chat

import (
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// YouTube is polled every few seconds and TLdex has its own delay, so the
// same line can arrive from both sources this far apart.
const dedupWindow = time.Minute

// deduper drops the second copy of a line that both sources delivered.
type deduper struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

func newDeduper(window time.Duration) *deduper {
	return &deduper{window: window, now: time.Now, seen: map[string]time.Time{}}
}

func (d *deduper) first(c *Comment) bool {
	if c.AuthorChannelID == "" {
		return true
	}

	key := c.AuthorChannelID + "\x00" + normalize(c.Text)
	now := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	for k, at := range d.seen {
		if now.Sub(at) > d.window {
			delete(d.seen, k)
		}
	}

	if _, dup := d.seen[key]; dup {
		return false
	}
	d.seen[key] = now
	return true
}

var emojiOrURL = regexp.MustCompile(`:[^\s:]+:|https?://\S+`)

// The sources write emojis differently, so emoji shortcodes and image URLs
// are dropped and only letters and digits count.
func normalize(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(emojiOrURL.ReplaceAllString(s, "")) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
