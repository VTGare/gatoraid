// Package subs keeps every active subscription in memory, indexed by
// target. Writes go to the store and swap in a new index, so readers never
// lock or query.
package subs

import (
	"context"
	"sync/atomic"

	"github.com/VTGare/gatoraid/store"
	"github.com/VTGare/gatoraid/streamers"
)

type Service struct {
	store   store.SubscriptionStore
	reg     *streamers.Registry
	idx     atomic.Pointer[index]
	changed chan struct{}
}

type index map[store.Feature]*targets

type targets struct {
	list    []*store.Subscription
	channel map[string][]*store.Subscription
	group   map[string][]*store.Subscription
	// Subscriptions whose target is all streamers.
	targetAll []*store.Subscription
	// Subscriptions per Discord channel.
	perChannel map[string]int
}

func New(st store.SubscriptionStore, reg *streamers.Registry) *Service {
	s := &Service{store: st, reg: reg, changed: make(chan struct{}, 1)}
	s.idx.Store(&index{})
	return s
}

// Changed receives after reloads. Several reloads in a row may arrive as
// one, so it suits a single consumer that rereads everything.
func (s *Service) Changed() <-chan struct{} { return s.changed }

// Reload rebuilds the index. Call it after guilds join or leave too, since
// subscriptions of guilds the bot left don't count.
func (s *Service) Reload(ctx context.Context) error {
	list, err := s.store.Subscriptions(ctx)
	if err != nil {
		return err
	}

	idx := index{}
	for i := range list {
		sub := &list[i]

		t := idx[sub.Feature]
		if t == nil {
			t = &targets{
				channel:    map[string][]*store.Subscription{},
				group:      map[string][]*store.Subscription{},
				perChannel: map[string]int{},
			}
			idx[sub.Feature] = t
		}

		t.list = append(t.list, sub)
		t.perChannel[sub.ChannelID]++
		switch sub.Target.Kind {
		case store.TargetChannel:
			t.channel[sub.Target.ID] = append(t.channel[sub.Target.ID], sub)
		case store.TargetGroup:
			t.group[sub.Target.ID] = append(t.group[sub.Target.ID], sub)
		case store.TargetAll:
			t.targetAll = append(t.targetAll, sub)
		}
	}

	s.idx.Store(&idx)
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return nil
}

// Match returns the feature's subscriptions that cover a YouTube channel:
// by the channel itself, one of its groups or parent groups, or all. Hidden
// streamers match nothing, and channels outside the curated registry only
// match by channel. A Discord channel gets at most one subscription, the
// most specific.
func (s *Service) Match(feature store.Feature, channelID string) []*store.Subscription {
	t := (*s.idx.Load())[feature]
	if t == nil {
		return nil
	}

	st, ok := s.reg.Streamer(channelID)
	if !ok {
		return nil
	}

	var (
		out  []*store.Subscription
		seen = map[string]bool{}
	)
	add := func(subs []*store.Subscription) {
		for _, sub := range subs {
			if !seen[sub.ChannelID] {
				seen[sub.ChannelID] = true
				out = append(out, sub)
			}
		}
	}

	add(t.channel[channelID])
	if st.Curated() {
		for _, g := range s.reg.Lineage(st.GroupID) {
			add(t.group[g.ID])
		}
		add(t.targetAll)
	}

	return out
}

// All returns every subscription to the feature. Gossip matches by what
// people say rather than by channel, so it needs the whole list.
func (s *Service) All(feature store.Feature) []*store.Subscription {
	if t := (*s.idx.Load())[feature]; t != nil {
		return t.list
	}
	return nil
}

// Count is how many of the feature's subscriptions post in a Discord
// channel.
func (s *Service) Count(feature store.Feature, discordChannelID string) int {
	if t := (*s.idx.Load())[feature]; t != nil {
		return t.perChannel[discordChannelID]
	}
	return 0
}

func (s *Service) Guild(ctx context.Context, guildID string, feature store.Feature) ([]store.Subscription, error) {
	return s.store.GuildSubscriptions(ctx, guildID, feature)
}

func (s *Service) Add(ctx context.Context, sub store.Subscription) (bool, error) {
	created, err := s.store.AddSubscription(ctx, sub)
	if err != nil {
		return false, err
	}
	return created, s.Reload(ctx)
}

func (s *Service) Remove(ctx context.Context, guildID string, feature store.Feature, target store.Target, channelID string) error {
	if err := s.store.RemoveSubscription(ctx, guildID, feature, target, channelID); err != nil {
		return err
	}
	return s.Reload(ctx)
}

func (s *Service) Clear(ctx context.Context, guildID string, feature store.Feature, channelID string) (int, error) {
	n, err := s.store.ClearSubscriptions(ctx, guildID, feature, channelID)
	if err != nil || n == 0 {
		return n, err
	}
	return n, s.Reload(ctx)
}
