package store

import (
	"context"
	"errors"
	"time"
)

var ErrStreamerNotFound = errors.New("streamer not found")

type Group struct {
	ID                string
	Name              string
	ParentID          string
	SkipAutoTranslate bool
}

type StreamerSource string

const (
	// From the streamers/seed files. Synced on startup.
	SourceSeed StreamerSource = "seed"
	// Added or edited with owner commands. The seed never overwrites these.
	SourceOwner StreamerSource = "owner"
	// Any YouTube channel a guild subscribed to.
	SourceUser StreamerSource = "user"
)

type Streamer struct {
	ChannelID   string
	Name        string
	ChannelName string
	GroupID     string
	Twitter     string
	Aliases     []string
	AvatarURL   string
	// The channel streams in rooms titled "free chat", so those aren't
	// skipped as placeholder chats.
	FreeChatStreams bool
	Source          StreamerSource
	AddedByGuild    string
	UpdatedAt       time.Time
}

// Only curated streamers count as VTubers when they show up in other
// streamers' chats.
func (s *Streamer) Curated() bool { return s.Source != SourceUser }

type SeedResult struct {
	Added, Updated, Removed int
	// Seed entries skipped because an owner edited them.
	Kept int
	// Owner entries handed back to the seed because they matched it.
	Returned int
}

type StreamerStore interface {
	StreamerGroups(ctx context.Context) ([]Group, error)
	Streamers(ctx context.Context) ([]Streamer, error)
	Streamer(ctx context.Context, channelID string) (*Streamer, error)

	// Replaces the groups and the seed streamers. Owner streamers are left
	// alone unless the seed has an identical entry, which takes them back.
	// User streamers that appear in the seed become seed streamers.
	SyncSeed(ctx context.Context, groups []Group, streamers []Streamer) (SeedResult, error)

	SaveStreamer(ctx context.Context, s Streamer) error
	DeleteStreamer(ctx context.Context, channelID string) error
}
