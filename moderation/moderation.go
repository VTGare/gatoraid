// Package moderation keeps every guild's blacklist and filters in memory.
// Writes go to the store and swap in a new index.
package moderation

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/VTGare/gatoraid/relay"
	"github.com/VTGare/gatoraid/store"
)

type Service struct {
	store store.ModerationStore
	idx   atomic.Pointer[map[string]*guild]
}

type guild struct {
	// Oldest first.
	blacklist []store.BlacklistEntry
	filters   []store.Filter
	rules     *relay.Moderation
}

func New(st store.ModerationStore) *Service {
	s := &Service{store: st}
	s.idx.Store(&map[string]*guild{})
	return s
}

func (s *Service) Reload(ctx context.Context) error {
	blacklists, err := s.store.AllBlacklists(ctx)
	if err != nil {
		return err
	}
	filters, err := s.store.AllFilters(ctx)
	if err != nil {
		return err
	}

	idx := map[string]*guild{}
	get := func(id string) *guild {
		g, ok := idx[id]
		if !ok {
			g = &guild{rules: &relay.Moderation{Blacklist: map[string]bool{}}}
			idx[id] = g
		}
		return g
	}

	for _, e := range blacklists {
		g := get(e.GuildID)
		g.blacklist = append(g.blacklist, e)
		g.rules.Blacklist[e.ChannelID] = true
	}
	for _, f := range filters {
		g := get(f.GuildID)
		g.filters = append(g.filters, f)
		switch f.Kind {
		case store.FilterBanned:
			g.rules.Banned = append(g.rules.Banned, f.Pattern)
		case store.FilterWanted:
			g.rules.Wanted = append(g.rules.Wanted, f.Pattern)
		}
	}

	s.idx.Store(&idx)
	return nil
}

func (s *Service) guild(id string) *guild { return (*s.idx.Load())[id] }

// For returns what the relay checks lines against. Nil when the guild has
// no blacklist or filters.
func (s *Service) For(guildID string) *relay.Moderation {
	if g := s.guild(guildID); g != nil {
		return g.rules
	}
	return nil
}

// Newest first.
func (s *Service) Blacklist(guildID string) []store.BlacklistEntry {
	g := s.guild(guildID)
	if g == nil {
		return nil
	}

	out := slices.Clone(g.blacklist)
	slices.Reverse(out)
	return out
}

func (s *Service) Blacklisted(guildID, channelID string) bool {
	g := s.guild(guildID)
	return g != nil && g.rules.Blacklist[channelID]
}

// Oldest first.
func (s *Service) Filters(guildID string) []store.Filter {
	if g := s.guild(guildID); g != nil {
		return g.filters
	}
	return nil
}

func (s *Service) AddToBlacklist(ctx context.Context, e store.BlacklistEntry) (bool, error) {
	created, err := s.store.AddToBlacklist(ctx, e)
	if err != nil || !created {
		return created, err
	}
	return true, s.Reload(ctx)
}

func (s *Service) RemoveFromBlacklist(ctx context.Context, guildID, channelID string) (*store.BlacklistEntry, error) {
	e, err := s.store.RemoveFromBlacklist(ctx, guildID, channelID)
	if err != nil {
		return nil, err
	}
	return e, s.Reload(ctx)
}

func (s *Service) AddFilter(ctx context.Context, f store.Filter) (bool, error) {
	created, err := s.store.AddFilter(ctx, f)
	if err != nil || !created {
		return created, err
	}
	return true, s.Reload(ctx)
}

func (s *Service) RemoveFilter(ctx context.Context, guildID string, kind store.FilterKind, pattern string) error {
	if err := s.store.RemoveFilter(ctx, guildID, kind, pattern); err != nil {
		return err
	}
	return s.Reload(ctx)
}
