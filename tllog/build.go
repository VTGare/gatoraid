// Package tllog turns relayed lines into text logs of a stream and posts
// them when streams end.
package tllog

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
)

type Meta struct {
	VideoID string
	Title   string
	// Zero means times count from the first line.
	Start time.Time
}

func (m Meta) URL() string { return "https://youtu.be/" + m.VideoID }

// Build writes the log and reports whether any lines made it in. The
// blacklist and filters are applied as they are now, so authors blacklisted
// after they spoke are left out too. The streamer's own lines skip them,
// like in relays.
func Build(meta Meta, lines []store.Line, m *relay.Moderation) (string, bool) {
	type key struct {
		author string
		at     int64
		body   string
	}

	// A guild relaying a stream into two channels has every line twice.
	seen := map[key]bool{}
	var kept []store.Line
	for _, l := range lines {
		switch l.Kind {
		case store.LineOwner:
		case store.LineTL, store.LineVTuber, store.LineMod:
			if m.Blocks(l.AuthorChannelID, l.Body) {
				continue
			}
		default:
			continue
		}

		k := key{l.AuthorChannelID, l.SaidAt.UnixMilli(), l.Body}
		if !seen[k] {
			seen[k] = true
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return "", false
	}

	slices.SortStableFunc(kept, func(a, b store.Line) int { return cmp.Compare(a.SaidAt.UnixMilli(), b.SaidAt.UnixMilli()) })

	var sb strings.Builder
	if meta.Title != "" {
		sb.WriteString(meta.Title + "\n")
	}
	sb.WriteString(meta.URL() + "\n")

	start := meta.Start
	if start.IsZero() {
		start = kept[0].SaidAt
		sb.WriteString("Times count from the first line.\n\n")
	} else {
		sb.WriteString("Started " + start.UTC().Format("2006-01-02 15:04 MST") + "\n\n")
	}

	for _, l := range kept {
		fmt.Fprintf(&sb, "[%s] %s: %s\n", offset(l.SaidAt.Sub(start)), l.AuthorName, l.Body)
	}

	return sb.String(), true
}

// Lines from the waiting room come before the start, so they're negative.
func offset(d time.Duration) string {
	sign := ""
	if d < 0 {
		sign, d = "-", -d
	}
	d = d.Truncate(time.Second)

	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	return fmt.Sprintf("%s%d:%02d:%02d", sign, h, m, s)
}
