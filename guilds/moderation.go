package guilds

import (
	"context"
	"slices"
	"strings"

	"github.com/VTGare/gatoraid/store"
)

// Moderation is a guild's blacklist and filters.
type Moderation struct {
	// YouTube channel IDs.
	Blacklist map[string]bool
	// Lowercase. Lines containing one are dropped.
	Banned []string
	// Lowercase. Lines starting with one count as translations.
	Wanted []string
}

// Blocks reports whether the blacklist or a banned word drops a line. text
// must be lowercase.
func (m *Moderation) Blocks(authorChannelID, text string) bool {
	if m == nil {
		return false
	}
	if m.Blacklist[authorChannelID] {
		return true
	}
	return slices.ContainsFunc(m.Banned, func(b string) bool { return strings.Contains(text, b) })
}

// Wants reports whether a lowercase line starts with a wanted prefix.
func (m *Moderation) Wants(text string) bool {
	if m == nil {
		return false
	}

	text = strings.TrimSpace(text)
	return slices.ContainsFunc(m.Wanted, func(w string) bool { return strings.HasPrefix(text, w) })
}

type moderation struct {
	// Oldest first.
	blacklist []store.BlacklistEntry
	filters   []store.Filter
	rules     *Moderation
}

func (s *State) reloadModeration(ctx context.Context) error {
	blacklists, err := s.store.AllBlacklists(ctx)
	if err != nil {
		return err
	}
	filters, err := s.store.AllFilters(ctx)
	if err != nil {
		return err
	}

	idx := map[string]*moderation{}
	get := func(id string) *moderation {
		g, ok := idx[id]
		if !ok {
			g = &moderation{rules: &Moderation{Blacklist: map[string]bool{}}}
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

	s.mods.Store(&idx)
	return nil
}

func (s *State) moderation(guildID string) *moderation { return (*s.mods.Load())[guildID] }

// Moderation returns what lines are checked against. Nil when the guild
// has no blacklist or filters.
func (s *State) Moderation(guildID string) *Moderation {
	if g := s.moderation(guildID); g != nil {
		return g.rules
	}
	return nil
}

// Newest first.
func (s *State) Blacklist(guildID string) []store.BlacklistEntry {
	g := s.moderation(guildID)
	if g == nil {
		return nil
	}

	out := slices.Clone(g.blacklist)
	slices.Reverse(out)
	return out
}

func (s *State) Blacklisted(guildID, channelID string) bool {
	g := s.moderation(guildID)
	return g != nil && g.rules.Blacklist[channelID]
}

// Oldest first.
func (s *State) Filters(guildID string) []store.Filter {
	if g := s.moderation(guildID); g != nil {
		return g.filters
	}
	return nil
}

func (s *State) AddToBlacklist(ctx context.Context, e store.BlacklistEntry) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	created, err := s.store.AddToBlacklist(ctx, e)
	if err != nil || !created {
		return created, err
	}
	return true, s.reloadModeration(ctx)
}

func (s *State) RemoveFromBlacklist(ctx context.Context, guildID, channelID string) (*store.BlacklistEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := s.store.RemoveFromBlacklist(ctx, guildID, channelID)
	if err != nil {
		return nil, err
	}
	return e, s.reloadModeration(ctx)
}

func (s *State) AddFilter(ctx context.Context, f store.Filter) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	created, err := s.store.AddFilter(ctx, f)
	if err != nil || !created {
		return created, err
	}
	return true, s.reloadModeration(ctx)
}

func (s *State) RemoveFilter(ctx context.Context, guildID string, kind store.FilterKind, pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.store.RemoveFilter(ctx, guildID, kind, pattern); err != nil {
		return err
	}
	return s.reloadModeration(ctx)
}
