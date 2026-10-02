package store

import (
	"context"
	"errors"
	"time"
)

var ErrStreamerNotFound = errors.New("streamer not found")

// How long PurgeStreamers keeps hidden streamers nothing subscribes to.
const StreamerRetention = 30 * 24 * time.Hour

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
	// Set while the streamer is hidden: dropped from the seed or removed by
	// the owner. The row stays so subscriptions to it survive.
	RemovedAt *time.Time
}

func (s *Streamer) Removed() bool { return s.RemovedAt != nil }

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

	// Replaces the groups and the seed streamers. Seed streamers missing from
	// the seed are hidden, not deleted. Owner streamers are left alone unless
	// the seed has an identical entry, which takes them back. User streamers
	// that appear in the seed become seed streamers.
	SyncSeed(ctx context.Context, groups []Group, streamers []Streamer) (SeedResult, error)

	SaveStreamer(ctx context.Context, s Streamer) error
	// Skips unknown channels and returns how many avatars changed.
	UpdateAvatars(ctx context.Context, avatars map[string]string) (int, error)
	// Hides the streamer. Saving it again brings it back.
	RemoveStreamer(ctx context.Context, channelID string) error
	// Deletes streamers hidden before the cutoff that no subscription
	// points at. Subscriptions of guilds the bot has left still count.
	PurgeStreamers(ctx context.Context, removedBefore time.Time) (int, error)
}
