package store

import (
	"context"
	"time"
)

// How long sent notices and posted logs are remembered. Far longer than
// any stream.
const NoticeRetention = 30 * 24 * time.Hour

type NoticeKind string

const (
	// A relay started in the waiting room.
	NoticePrechat NoticeKind = "prechat"
	// A relay started on a live stream.
	NoticeRelay NoticeKind = "relay"
	// A YouTube live notification.
	NoticeLive NoticeKind = "live"
)

type Notice struct {
	GuildID   string
	VideoID   string
	Kind      NoticeKind
	ChannelID string
}

type LogStore interface {
	// Records that a log is being posted and reports whether it's new.
	// Claim before posting, like notices.
	ClaimLog(ctx context.Context, guildID, videoID, channelID string) (bool, error)
	PruneLogs(ctx context.Context, before time.Time) (int, error)
}

type NoticeStore interface {
	// Records the notice and reports whether it's new. Claim before
	// sending: a crash in between loses a notice instead of pinging twice.
	ClaimNotice(ctx context.Context, n Notice) (bool, error)
	PruneNotices(ctx context.Context, before time.Time) (int, error)
}
