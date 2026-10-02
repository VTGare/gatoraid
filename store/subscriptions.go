package store

import (
	"context"
	"errors"
	"time"
)

var ErrSubscriptionNotFound = errors.New("subscription not found")

type Feature string

const (
	FeatureRelay  Feature = "relay"
	FeatureCameos Feature = "cameos"
	FeatureGossip Feature = "gossip"
	// Live notifications and community posts.
	FeatureYouTube Feature = "youtube"
	FeaturePosts   Feature = "posts"
)

type TargetKind string

const (
	TargetChannel TargetKind = "channel"
	TargetGroup   TargetKind = "group"
	TargetAll     TargetKind = "all"
)

type Target struct {
	Kind TargetKind
	// A YouTube channel ID or a group ID. Empty for TargetAll.
	ID string
}

type Subscription struct {
	ID      int64
	GuildID string
	Feature Feature
	Target  Target
	// Where the lines or notifications go.
	ChannelID string
	// Pinged by notices. Empty for none.
	RoleID    string
	CreatedBy string
	CreatedAt time.Time
}

type SubscriptionStore interface {
	// Only guilds the bot is still in.
	Subscriptions(ctx context.Context) ([]Subscription, error)
	GuildSubscriptions(ctx context.Context, guildID string, feature Feature) ([]Subscription, error)

	// Updates the role if the subscription exists. Reports whether it was
	// new. ErrGuildNotFound if the guild was never recorded.
	AddSubscription(ctx context.Context, s Subscription) (bool, error)
	RemoveSubscription(ctx context.Context, guildID string, feature Feature, target Target, channelID string) error
	// Removes the feature's subscriptions in one Discord channel and
	// returns how many there were.
	ClearSubscriptions(ctx context.Context, guildID string, feature Feature, channelID string) (int, error)
}
